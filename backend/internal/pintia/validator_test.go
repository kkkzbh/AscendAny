package pintia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestValidatorConsumesAllContractFixtures(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	root := testContractRoot()

	validFixtures, err := filepath.Glob(filepath.Join(root, "fixtures", "valid", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(validFixtures) == 0 {
		t.Fatal("no valid contract fixtures found")
	}
	for _, path := range validFixtures {
		path := path
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			payload := mustReadFile(t, path)
			snapshot, err := validator.Validate(payload)
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if _, err := DomainHash(context.Background(), snapshot); err != nil {
				t.Fatalf("DomainHash() error = %v", err)
			}
		})
	}

	invalidGroups := []struct {
		directory string
		code      ErrorCode
	}{
		{directory: "invalid-structural", code: ErrorSchemaViolation},
		{directory: "invalid-semantic", code: ErrorSemanticViolation},
	}
	for _, group := range invalidGroups {
		fixtures, err := filepath.Glob(filepath.Join(root, "fixtures", group.directory, "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		if len(fixtures) == 0 {
			t.Fatalf("no %s contract fixtures found", group.directory)
		}
		for _, path := range fixtures {
			path := path
			t.Run(group.directory+"/"+filepath.Base(path), func(t *testing.T) {
				_, err := validator.Validate(mustReadFile(t, path))
				assertValidationCode(t, err, group.code)
			})
		}
	}
}

func TestValidateReaderStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	validator := testValidator(t, DefaultLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := validator.ValidateReader(ctx, bytes.NewReader(mustReadFile(t, testValidFixturePath())))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateReader() error = %v", err)
	}
}

func TestValidatorRejectsWrongSchemaDigestAndSchemaVersion(t *testing.T) {
	validator := testValidator(t, DefaultLimits())

	wrongDigest := testFixtureDocument(t)
	wrongDigest["schemaSha256"] = strings.Repeat("0", 64)
	_, err := validator.Validate(mustMarshal(t, wrongDigest))
	assertValidationCode(t, err, ErrorSchemaDigestMismatch)

	v1 := testFixtureDocument(t)
	v1["schema"] = "ascendany.pintia.snapshot.v1"
	_, err = validator.Validate(mustMarshal(t, v1))
	assertValidationCode(t, err, ErrorSchemaViolation)
}

func TestValidatorRejectsSourceURLForAnotherProblemSet(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	testCases := map[string]string{
		"different ID":     "https://pintia.cn/problem-sets/problem-set-200/submissions",
		"prefix collision": "https://pintia.cn/problem-sets/problem-set-1000/problems",
		"nested route":     "https://pintia.cn/archive/problem-sets/problem-set-100/problems",
	}
	for name, sourceURL := range testCases {
		t.Run(name, func(t *testing.T) {
			document := testFixtureDocument(t)
			exam, ok := document["exam"].(map[string]any)
			if !ok {
				t.Fatal("valid fixture exam is not an object")
			}
			exam["sourceUrl"] = sourceURL
			_, err := validator.Validate(mustMarshal(t, document))
			assertValidationCode(t, err, ErrorSemanticViolation)
			if !strings.Contains(err.Error(), "$.exam.sourceUrl") {
				t.Fatalf("Validate() error = %v, want sourceUrl path", err)
			}
		})
	}
}

func TestValidatorRejectsStrictJSONAndClosedObjectViolations(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	valid := mustReadFile(t, testValidFixturePath())

	unknown := testFixtureDocument(t)
	unknown["raw"] = map[string]any{}
	_, err := validator.Validate(mustMarshal(t, unknown))
	assertValidationCode(t, err, ErrorSchemaViolation)

	missing := testFixtureDocument(t)
	delete(missing, "exam")
	_, err = validator.Validate(mustMarshal(t, missing))
	assertValidationCode(t, err, ErrorSchemaViolation)

	_, err = validator.Validate([]byte(`{"schema":"a","schema":"b"}`))
	assertValidationCode(t, err, ErrorMalformedJSON)

	_, err = validator.Validate([]byte(`{"exam":{"title":"a","title":"b"}}`))
	assertValidationCode(t, err, ErrorMalformedJSON)

	_, err = validator.Validate(append(append([]byte(nil), valid...), []byte(` {}`)...))
	assertValidationCode(t, err, ErrorMalformedJSON)

	invalidUTF8 := append(append([]byte(nil), valid[:len(valid)-2]...), 0xff, '}', '\n')
	_, err = validator.Validate(invalidUTF8)
	assertValidationCode(t, err, ErrorMalformedJSON)
}

func TestStreamingPreflightRejectsUnknownFieldBeforeHugeValue(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxTotalNodes = 5
	limits.MaxProblems = 1
	limits.MaxParticipants = 1
	limits.MaxProblemResultsPerRanking = 1
	limits.MaxSubmissions = 1
	limits.MaxCaseResultsPerSubmission = 1
	validator := testValidator(t, limits)
	payload := []byte(`{"unknown":[` + strings.Repeat(`{"nested":"value"},`, 20_000) + `null]}`)

	_, err := validator.Validate(payload)
	assertValidationCode(t, err, ErrorSchemaViolation)
	if !strings.Contains(err.Error(), "$.unknown") {
		t.Fatalf("Validate() error = %v, want immediate unknown-field path", err)
	}
}

func TestStreamingPreflightEnforcesNestedArrayLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		limit  func(*Limits)
		path   string
	}{
		{
			name: "ranking problem results",
			mutate: func(root map[string]any) {
				participant := objectValue(arrayAt(root, "participants")[0])
				ranking := objectValue(participant["ranking"])
				results := arrayAt(ranking, "problemResults")
				ranking["problemResults"] = append(results, cloneObject(t, objectValue(results[0])))
			},
			limit: func(limits *Limits) { limits.MaxProblemResultsPerRanking = 1 },
			path:  "$.participants[0].ranking.problemResults",
		},
		{
			name: "submission case results",
			mutate: func(root map[string]any) {
				submission := objectValue(arrayAt(root, "submissions")[0])
				results := arrayAt(submission, "caseResults")
				submission["caseResults"] = append(results, cloneObject(t, objectValue(results[0])))
			},
			limit: func(limits *Limits) { limits.MaxCaseResultsPerSubmission = 1 },
			path:  "$.submissions[0].caseResults",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := testFixtureDocument(t)
			test.mutate(document)
			limits := DefaultLimits()
			test.limit(&limits)
			validator := testValidator(t, limits)
			_, err := validator.Validate(mustMarshal(t, document))
			assertValidationCode(t, err, ErrorLimitExceeded)
			if !strings.Contains(err.Error(), test.path) {
				t.Fatalf("Validate() error = %v, want path %s", err, test.path)
			}
		})
	}
}

func TestStreamingPreflightEnforcesStringCodeAndAggregateBoundaries(t *testing.T) {
	payload := mustReadFile(t, testValidFixturePath())
	limits := DefaultLimits()
	limits.MaxStringBytes = 64
	limits.MaxCodeBytes = 64
	validator := testValidator(t, limits)
	if _, err := validator.Validate(payload); err != nil {
		t.Fatalf("Validate(exact max string) error = %v", err)
	}

	document := testFixtureDocument(t)
	objectValue(document["exam"])["title"] = strings.Repeat("x", 65)
	_, err := validator.Validate(mustMarshal(t, document))
	assertValidationCode(t, err, ErrorLimitExceeded)

	codeDocument := testFixtureDocument(t)
	code := "界"
	digest := sha256.Sum256([]byte(code))
	for _, value := range arrayAt(codeDocument, "submissions") {
		submission := objectValue(value)
		submission["code"] = code
		submission["codeSha256"] = hex.EncodeToString(digest[:])
	}
	codePayload := mustMarshal(t, codeDocument)
	codeLimits := DefaultLimits()
	codeLimits.MaxCodeBytes = len([]byte(code))
	if _, err := testValidator(t, codeLimits).Validate(codePayload); err != nil {
		t.Fatalf("Validate(exact max code bytes) error = %v", err)
	}
	codeLimits.MaxCodeBytes--
	_, err = testValidator(t, codeLimits).Validate(codePayload)
	assertValidationCode(t, err, ErrorLimitExceeded)

	minimal := []byte(`{"schema":"x"}`)
	aggregateLimits := DefaultLimits()
	aggregateLimits.MaxTotalStringBytes = 7
	aggregateLimits.MaxStringBytes = 6
	aggregateLimits.MaxCodeBytes = 6
	if err := validateStreamingPreflight(
		context.Background(),
		minimal,
		validator.preflightSchema,
		aggregateLimits,
		validator.arrayLimits,
	); err != nil {
		t.Fatalf("preflight(exact total string bytes) error = %v", err)
	}
	aggregateLimits.MaxTotalStringBytes--
	err = validateStreamingPreflight(
		context.Background(),
		minimal,
		validator.preflightSchema,
		aggregateLimits,
		validator.arrayLimits,
	)
	assertValidationCode(t, err, ErrorLimitExceeded)
}

func TestStreamingPreflightEnforcesNodeAndDepthBoundaries(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	minimal := []byte(`{"schema":"x"}`)
	limits := DefaultLimits()
	limits.MaxTotalNodes = 3
	if err := validateStreamingPreflight(
		context.Background(),
		minimal,
		validator.preflightSchema,
		limits,
		validator.arrayLimits,
	); err != nil {
		t.Fatalf("preflight(exact total nodes) error = %v", err)
	}
	limits.MaxTotalNodes--
	err := validateStreamingPreflight(
		context.Background(),
		minimal,
		validator.preflightSchema,
		limits,
		validator.arrayLimits,
	)
	assertValidationCode(t, err, ErrorLimitExceeded)

	limits = DefaultLimits()
	limits.MaxJSONDepth = 4
	exactDepth := []byte(`{"schema":[[["x"]]]}`)
	if err := validateStreamingPreflight(
		context.Background(),
		exactDepth,
		validator.preflightSchema,
		limits,
		validator.arrayLimits,
	); err != nil {
		t.Fatalf("preflight(exact JSON depth) error = %v", err)
	}
	overDepth := []byte(`{"schema":[[[["x"]]]]}`)
	err = validateStreamingPreflight(
		context.Background(),
		overDepth,
		validator.preflightSchema,
		limits,
		validator.arrayLimits,
	)
	assertValidationCode(t, err, ErrorLimitExceeded)
}

func TestStreamingPreflightHonorsCancellationDuringTokenScan(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	payload := mustReadFile(t, testValidFixturePath())
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingChunkReader{payload: payload, chunkBytes: 8, cancelAtRead: 4, cancel: cancel}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	scanner := preflightScanner{
		ctx:         ctx,
		decoder:     decoder,
		limits:      DefaultLimits(),
		arrayLimits: validator.arrayLimits,
	}

	err := scanner.scanValue(validator.preflightSchema, "$", "$", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scanValue() error = %v, want context.Canceled", err)
	}
	if reader.reads < reader.cancelAtRead {
		t.Fatalf("reader canceled before token scan: reads=%d", reader.reads)
	}
}

func TestValidatorEnforcesAllSemanticInvariants(t *testing.T) {
	validator := testValidator(t, DefaultLimits())

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "duplicate problemId",
			mutate: func(root map[string]any) {
				problems := arrayAt(root, "problems")
				duplicate := cloneObject(t, objectValue(problems[0]))
				duplicate["problemSetProblemId"] = "psp-200"
				duplicate["title"] = "Second title"
				root["problems"] = append(problems, duplicate)
				setCollectionCounts(root, "problems", 2, 2, 2)
			},
		},
		{
			name: "duplicate participant userId",
			mutate: func(root map[string]any) {
				participants := arrayAt(root, "participants")
				duplicate := cloneObject(t, objectValue(participants[0]))
				duplicate["displayName"] = "Different display name"
				duplicate["ranking"] = nil
				root["participants"] = append(participants, duplicate)
				completenessAt(root, "participants")["exportedCount"] = json.Number("3")
			},
		},
		{
			name: "duplicate submissionId",
			mutate: func(root map[string]any) {
				submissions := arrayAt(root, "submissions")
				duplicate := cloneObject(t, objectValue(submissions[1]))
				duplicate["verdict"] = "RUNTIME_ERROR"
				root["submissions"] = append(submissions, duplicate)
				setCollectionCounts(root, "submissions", 3, 3, 3)
			},
		},
		{
			name: "duplicate caseId",
			mutate: func(root map[string]any) {
				submission := objectValue(arrayAt(root, "submissions")[0])
				cases := arrayAt(submission, "caseResults")
				duplicate := cloneObject(t, objectValue(cases[0]))
				duplicate["message"] = "duplicate"
				submission["caseResults"] = append(cases, duplicate)
			},
		},
		{
			name: "duplicate ranking problem reference",
			mutate: func(root map[string]any) {
				participant := objectValue(arrayAt(root, "participants")[0])
				ranking := objectValue(participant["ranking"])
				results := arrayAt(ranking, "problemResults")
				duplicate := cloneObject(t, objectValue(results[0]))
				duplicate["score"] = json.Number("99")
				ranking["problemResults"] = append(results, duplicate)
			},
		},
		{
			name: "dangling ranking problem reference",
			mutate: func(root map[string]any) {
				participant := objectValue(arrayAt(root, "participants")[0])
				ranking := objectValue(participant["ranking"])
				objectValue(arrayAt(ranking, "problemResults")[0])["problemSetProblemId"] = "psp-missing"
			},
		},
		{
			name: "extra participant outside union",
			mutate: func(root map[string]any) {
				participants := arrayAt(root, "participants")
				extra := cloneObject(t, objectValue(participants[1]))
				extra["userId"] = "user-300"
				root["participants"] = append(participants, extra)
				completenessAt(root, "participants")["exportedCount"] = json.Number("3")
			},
		},
		{
			name: "ranking exported count mismatch",
			mutate: func(root map[string]any) {
				completenessAt(root, "rankings")["exportedCount"] = json.Number("0")
			},
		},
		{
			name: "participant exported count mismatch",
			mutate: func(root map[string]any) {
				completenessAt(root, "participants")["exportedCount"] = json.Number("1")
			},
		},
		{
			name: "observed count below exported count",
			mutate: func(root map[string]any) {
				setCollectionCounts(root, "problems", 0, 0, 1)
			},
		},
		{
			name: "source reported count differs after pagination exhaustion",
			mutate: func(root map[string]any) {
				completenessAt(root, "problems")["sourceReportedCount"] = json.Number("2")
			},
		},
		{
			name: "exam starts after it ends",
			mutate: func(root map[string]any) {
				exam := objectValue(root["exam"])
				exam["startsAt"] = "2026-07-09T04:00:00Z"
			},
		},
		{
			name: "whitespace exam title",
			mutate: func(root map[string]any) {
				objectValue(root["exam"])["title"] = " \t "
			},
		},
		{
			name: "whitespace problem title",
			mutate: func(root map[string]any) {
				objectValue(arrayAt(root, "problems")[0])["title"] = " \n "
			},
		},
		{
			name: "whitespace student number",
			mutate: func(root map[string]any) {
				objectValue(arrayAt(root, "participants")[0])["studentNumber"] = " \t "
			},
		},
		{
			name: "whitespace submission verdict",
			mutate: func(root map[string]any) {
				objectValue(arrayAt(root, "submissions")[0])["verdict"] = " \n "
			},
		},
		{
			name: "empty programming code",
			mutate: func(root map[string]any) {
				submission := objectValue(arrayAt(root, "submissions")[0])
				submission["code"] = ""
				submission["codeSha256"] = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := testFixtureDocument(t)
			test.mutate(document)
			_, err := validator.Validate(mustMarshal(t, document))
			assertValidationCode(t, err, ErrorSemanticViolation)
		})
	}
}

func TestValidatorAllowsWhitespaceOnlyProgrammingCode(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	document := testFixtureDocument(t)
	submission := objectValue(arrayAt(document, "submissions")[0])
	code := " \n\t"
	digest := sha256.Sum256([]byte(code))
	submission["code"] = code
	submission["codeSha256"] = hex.EncodeToString(digest[:])

	if _, err := validator.Validate(mustMarshal(t, document)); err != nil {
		t.Fatalf("Validate(whitespace code) error = %v", err)
	}
}

func TestDefaultLimitsCapSubmissionsAtTwentyThousand(t *testing.T) {
	if got := DefaultLimits().MaxSubmissions; got != 20_000 {
		t.Fatalf("DefaultLimits().MaxSubmissions = %d, want 20000", got)
	}
}

func TestValidatorRequiresUTCOffsetZero(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	document := testFixtureDocument(t)
	objectValue(arrayAt(document, "submissions")[0])["submittedAt"] = "2026-07-09T09:01:00+08:00"
	_, err := validator.Validate(mustMarshal(t, document))
	assertValidationCode(t, err, ErrorSchemaViolation)
}

func TestValidatorRequiresNonNegativeIntegerAcceptTimeSeconds(t *testing.T) {
	validator := testValidator(t, DefaultLimits())

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "missing",
			mutate: func(result map[string]any) {
				delete(result, "acceptTimeSeconds")
			},
		},
		{
			name: "fractional",
			mutate: func(result map[string]any) {
				result["acceptTimeSeconds"] = json.Number("1.5")
			},
		},
		{
			name: "negative",
			mutate: func(result map[string]any) {
				result["acceptTimeSeconds"] = json.Number("-1")
			},
		},
		{
			name: "legacy absolute timestamp",
			mutate: func(result map[string]any) {
				delete(result, "acceptTimeSeconds")
				result["acceptedAt"] = "2026-07-09T01:01:00Z"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := testFixtureDocument(t)
			result := objectValue(arrayAt(objectValue(objectValue(arrayAt(document, "participants")[0])["ranking"]), "problemResults")[0])
			test.mutate(result)
			_, err := validator.Validate(mustMarshal(t, document))
			assertValidationCode(t, err, ErrorSchemaViolation)
		})
	}
}

func TestValidatorEnforcesRankingPassedMeaning(t *testing.T) {
	validator := testValidator(t, DefaultLimits())

	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{
			name: "positive partial score is not passed",
			mutate: func(_ map[string]any, result map[string]any) {
				result["score"] = json.Number("79")
				result["passed"] = true
			},
		},
		{
			name: "full score is passed",
			mutate: func(_ map[string]any, result map[string]any) {
				result["passed"] = false
			},
		},
		{
			name: "missing score makes passed unknown",
			mutate: func(_ map[string]any, result map[string]any) {
				result["score"] = nil
				result["passed"] = false
			},
		},
		{
			name: "missing max score makes passed unknown",
			mutate: func(problem map[string]any, result map[string]any) {
				problem["maxScore"] = nil
				result["passed"] = true
			},
		},
		{
			name: "known scores require passed",
			mutate: func(_ map[string]any, result map[string]any) {
				result["passed"] = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := testFixtureDocument(t)
			problem := objectValue(arrayAt(document, "problems")[0])
			participant := objectValue(arrayAt(document, "participants")[0])
			ranking := objectValue(participant["ranking"])
			result := objectValue(arrayAt(ranking, "problemResults")[0])
			test.mutate(problem, result)
			_, err := validator.Validate(mustMarshal(t, document))
			assertValidationCode(t, err, ErrorSemanticViolation)
		})
	}
}

func TestValidatorEnforcesConfiguredLimits(t *testing.T) {
	payload := mustReadFile(t, testValidFixturePath())
	tests := []struct {
		name   string
		mutate func(*Limits)
		code   ErrorCode
	}{
		{
			name: "total bytes",
			mutate: func(limits *Limits) {
				limits.MaxTotalBytes = int64(len(payload) - 1)
				limits.MaxTotalStringBytes = limits.MaxTotalBytes
				limits.MaxStringBytes = len(payload) - 1
				limits.MaxCodeBytes = len(payload) - 1
			},
			code: ErrorPayloadTooLarge,
		},
		{
			name:   "participants",
			mutate: func(limits *Limits) { limits.MaxParticipants = 1 },
			code:   ErrorLimitExceeded,
		},
		{
			name:   "submissions",
			mutate: func(limits *Limits) { limits.MaxSubmissions = 1 },
			code:   ErrorLimitExceeded,
		},
		{
			name:   "code bytes",
			mutate: func(limits *Limits) { limits.MaxCodeBytes = 1 },
			code:   ErrorLimitExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			test.mutate(&limits)
			validator := testValidator(t, limits)
			_, err := validator.Validate(payload)
			assertValidationCode(t, err, test.code)
		})
	}

	problemsDocument := testFixtureDocument(t)
	problems := arrayAt(problemsDocument, "problems")
	second := cloneObject(t, objectValue(problems[0]))
	second["problemSetProblemId"] = "psp-200"
	second["problemId"] = "problem-200"
	problemsDocument["problems"] = append(problems, second)
	limits := DefaultLimits()
	limits.MaxProblems = 1
	validator := testValidator(t, limits)
	_, err := validator.Validate(mustMarshal(t, problemsDocument))
	assertValidationCode(t, err, ErrorLimitExceeded)
}

func TestValidatorRejectsDecimalExpansionBeforeSchemaValidation(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	document := testFixtureDocument(t)
	objectValue(document["exam"])["totalScore"] = json.Number("1e2000000")
	_, err := validator.Validate(mustMarshal(t, document))
	assertValidationCode(t, err, ErrorLimitExceeded)
}

func TestValidatorRejectsIntegersOutsidePostgreSQLRepresentability(t *testing.T) {
	validator := testValidator(t, DefaultLimits())

	integerDocument := testFixtureDocument(t)
	objectValue(arrayAt(integerDocument, "problems")[0])["memoryLimitBytes"] = json.Number("9223372036854775808")
	_, err := validator.Validate(mustMarshal(t, integerDocument))
	assertValidationCode(t, err, ErrorSemanticViolation)

	acceptTimeDocument := testFixtureDocument(t)
	participant := objectValue(arrayAt(acceptTimeDocument, "participants")[0])
	ranking := objectValue(participant["ranking"])
	objectValue(arrayAt(ranking, "problemResults")[0])["acceptTimeSeconds"] = json.Number("9223372036854775808")
	_, err = validator.Validate(mustMarshal(t, acceptTimeDocument))
	assertValidationCode(t, err, ErrorSemanticViolation)
}

func TestValidatorEnforcesAnalyticsDecimalContractOnEveryPath(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	paths := []struct {
		name   string
		mutate func(map[string]any, json.Number)
	}{
		{
			name: "exam totalScore",
			mutate: func(root map[string]any, value json.Number) {
				objectValue(root["exam"])["totalScore"] = value
			},
		},
		{
			name: "problem maxScore",
			mutate: func(root map[string]any, value json.Number) {
				objectValue(arrayAt(root, "problems")[0])["maxScore"] = value
				participant := objectValue(arrayAt(root, "participants")[0])
				result := objectValue(arrayAt(objectValue(participant["ranking"]), "problemResults")[0])
				result["score"] = nil
				result["passed"] = nil
			},
		},
		{
			name: "ranking totalScore",
			mutate: func(root map[string]any, value json.Number) {
				participant := objectValue(arrayAt(root, "participants")[0])
				objectValue(participant["ranking"])["totalScore"] = value
			},
		},
		{
			name: "ranking problem score",
			mutate: func(root map[string]any, value json.Number) {
				objectValue(arrayAt(root, "problems")[0])["maxScore"] = nil
				participant := objectValue(arrayAt(root, "participants")[0])
				result := objectValue(arrayAt(objectValue(participant["ranking"]), "problemResults")[0])
				result["score"] = value
				result["passed"] = nil
			},
		},
		{
			name: "submission score",
			mutate: func(root map[string]any, value json.Number) {
				objectValue(arrayAt(root, "submissions")[0])["score"] = value
			},
		},
		{
			name: "submission case score",
			mutate: func(root map[string]any, value json.Number) {
				submission := objectValue(arrayAt(root, "submissions")[0])
				objectValue(arrayAt(submission, "caseResults")[0])["score"] = value
			},
		},
	}
	values := []struct {
		name     string
		value    json.Number
		wantCode ErrorCode
	}{
		{name: "exact positive minimum", value: json.Number(AnalyticsDecimalMinimumPositive)},
		{name: "exact maximum", value: json.Number(AnalyticsDecimalMaximum)},
		{name: "exact comparison over maximum", value: json.Number("1.0000000000000000000000000001e100"), wantCode: ErrorSchemaViolation},
		{name: "large exponent", value: json.Number("1e1000"), wantCode: ErrorSchemaViolation},
		{name: "positive underflow exponent", value: json.Number("1e-1000"), wantCode: ErrorSchemaViolation},
	}
	for _, path := range paths {
		for _, value := range values {
			t.Run(path.name+"/"+value.name, func(t *testing.T) {
				document := testFixtureDocument(t)
				path.mutate(document, value.value)
				_, err := validator.Validate(mustMarshal(t, document))
				if value.wantCode == "" {
					if err != nil {
						t.Fatalf("Validate() error = %v", err)
					}
					return
				}
				assertValidationCode(t, err, value.wantCode)
			})
		}
	}
}

func TestValidateReaderHonorsTotalByteLimit(t *testing.T) {
	payload := mustReadFile(t, testValidFixturePath())
	limits := DefaultLimits()
	limits.MaxTotalBytes = int64(len(payload) - 1)
	limits.MaxTotalStringBytes = limits.MaxTotalBytes
	limits.MaxStringBytes = len(payload) - 1
	limits.MaxCodeBytes = len(payload) - 1
	validator := testValidator(t, limits)
	_, err := validator.ValidateReader(context.Background(), bytes.NewReader(payload))
	assertValidationCode(t, err, ErrorPayloadTooLarge)
}

func TestNewValidatorRejectsInvalidLimits(t *testing.T) {
	schema := mustReadFile(t, filepath.Join(testContractRoot(), "ascendany.pintia.snapshot.v2.schema.json"))
	limits := DefaultLimits()
	limits.MaxCodeBytes = 0
	_, err := NewValidator(schema, limits)
	assertValidationCode(t, err, ErrorInvalidLimits)
}

func TestValidatePreflightArrayCoverageRejectsSchemaAndLimitDrift(t *testing.T) {
	t.Run("schema adds array without configured cap", func(t *testing.T) {
		document := testSchemaDocument(t)
		properties := objectValue(objectValue(document)["properties"])
		properties["uncappedCollection"] = map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "string",
			},
		}

		preflight, err := compilePreflightSchema(document)
		if err != nil {
			t.Fatalf("compilePreflightSchema() error = %v", err)
		}
		err = validatePreflightArrayCoverage(preflight, DefaultLimits().arrayLimits())
		if err == nil || !strings.Contains(err.Error(), "authoritative schema array $.uncappedCollection has no configured limit") {
			t.Fatalf("validatePreflightArrayCoverage() error = %v", err)
		}
	})

	t.Run("configured cap remains after schema deletes array", func(t *testing.T) {
		document := testSchemaDocument(t)
		definitions := objectValue(objectValue(document)["$defs"])
		submission := objectValue(definitions["submission"])
		delete(objectValue(submission["properties"]), "caseResults")
		required := submission["required"].([]any)
		filteredRequired := make([]any, 0, len(required)-1)
		for _, field := range required {
			if field != "caseResults" {
				filteredRequired = append(filteredRequired, field)
			}
		}
		submission["required"] = filteredRequired

		preflight, err := compilePreflightSchema(document)
		if err != nil {
			t.Fatalf("compilePreflightSchema() error = %v", err)
		}
		err = validatePreflightArrayCoverage(preflight, DefaultLimits().arrayLimits())
		if err == nil || !strings.Contains(err.Error(), "configured array limit $.submissions[].caseResults does not exist in the authoritative schema") {
			t.Fatalf("validatePreflightArrayCoverage() error = %v", err)
		}
	})
}

func TestNewValidatorRejectsSchemaByteDrift(t *testing.T) {
	schema := mustReadFile(t, filepath.Join(testContractRoot(), "ascendany.pintia.snapshot.v2.schema.json"))
	schema = append(schema, '\n')

	_, err := NewValidator(schema, DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "schema digest mismatch") {
		t.Fatalf("NewValidator() error = %v, want schema digest mismatch", err)
	}
}

func TestEmbeddedSchemaMatchesRootContractByteForByte(t *testing.T) {
	rootSchema := mustReadFile(t, filepath.Join(testContractRoot(), "ascendany.pintia.snapshot.v2.schema.json"))
	if !bytes.Equal(EmbeddedSchemaV2(), rootSchema) {
		t.Fatal("embedded Pintia schema differs from root contract bytes")
	}
	validator, err := NewEmbeddedValidator(DefaultLimits())
	if err != nil {
		t.Fatalf("NewEmbeddedValidator() error = %v", err)
	}
	if validator.SchemaSHA256() != ExpectedSchemaSHA256 {
		t.Fatalf("embedded schema digest = %q, want %q", validator.SchemaSHA256(), ExpectedSchemaSHA256)
	}
}

func testValidator(t *testing.T, limits Limits) *Validator {
	t.Helper()
	schema := mustReadFile(t, filepath.Join(testContractRoot(), "ascendany.pintia.snapshot.v2.schema.json"))
	validator, err := NewValidator(schema, limits)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}

func testContractRoot() string {
	return filepath.Join("..", "..", "..", "contracts", "pintia")
}

func testValidFixturePath() string {
	return filepath.Join(testContractRoot(), "fixtures", "valid", "complete.json")
}

func testFixtureDocument(t *testing.T) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(mustReadFile(t, testValidFixturePath())))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func testSchemaDocument(t *testing.T) any {
	t.Helper()
	document, err := parseStrictJSON(mustReadFile(
		t,
		filepath.Join(testContractRoot(), "ascendany.pintia.snapshot.v2.schema.json"),
	))
	if err != nil {
		t.Fatalf("parseStrictJSON(authoritative schema) error = %v", err)
	}
	return document
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertValidationCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("Validate() error = nil, want code %q", code)
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T (%v), want *ValidationError", err, err)
	}
	if validationErr.Code != code {
		t.Fatalf("error code = %q, want %q; error = %v", validationErr.Code, code, err)
	}
}

func arrayAt(object map[string]any, key string) []any {
	return object[key].([]any)
}

func objectValue(value any) map[string]any {
	return value.(map[string]any)
}

func completenessAt(root map[string]any, name string) map[string]any {
	return objectValue(objectValue(root["completeness"])[name])
}

func setCollectionCounts(root map[string]any, name string, source, observed, exported uint64) {
	collection := completenessAt(root, name)
	collection["sourceReportedCount"] = json.Number(strconv.FormatUint(source, 10))
	collection["observedCount"] = json.Number(strconv.FormatUint(observed, 10))
	collection["exportedCount"] = json.Number(strconv.FormatUint(exported, 10))
}

func cloneObject(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	payload := mustMarshal(t, source)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var clone map[string]any
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

type cancelingChunkReader struct {
	payload      []byte
	offset       int
	chunkBytes   int
	reads        int
	cancelAtRead int
	cancel       context.CancelFunc
}

func (reader *cancelingChunkReader) Read(destination []byte) (int, error) {
	if reader.offset >= len(reader.payload) {
		return 0, io.EOF
	}
	count := min(reader.chunkBytes, len(destination), len(reader.payload)-reader.offset)
	copy(destination, reader.payload[reader.offset:reader.offset+count])
	reader.offset += count
	reader.reads++
	if reader.reads == reader.cancelAtRead {
		reader.cancel()
	}
	return count, nil
}
