package pintia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"
)

func TestDomainHashGolden(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	snapshot, err := validator.Validate(mustReadFile(t, testValidFixturePath()))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DomainHash(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const want = "d4310900fde625c06e896d5e6900a794d8e9fcfe7f5e3c35efb807128dfa7058"
	if digest != want {
		t.Fatalf("DomainHash() = %q, want %q", digest, want)
	}
}

func TestDomainHashStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	validator := testValidator(t, DefaultLimits())
	snapshot, err := validator.Validate(mustReadFile(t, testValidFixturePath()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = DomainHash(ctx, snapshot)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DomainHash() error = %v", err)
	}
}

func TestCanonicalDomainV1UsesPortableJSONStringEscaping(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	snapshot, err := validator.Validate(mustReadFile(t, testValidFixturePath()))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := CanonicalDomainV1(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`\u003c`)) || !bytes.Contains(encoded, []byte(`<p>Print ok.</p>`)) {
		t.Fatalf("canonical encoding uses implementation-specific HTML escaping: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(`"acceptTimeSeconds":60`)) || bytes.Contains(encoded, []byte(`acceptedAt`)) {
		t.Fatalf("canonical ranking result uses the wrong time field: %s", encoded)
	}
}

func TestDomainHashIgnoresJSONKeyAndEntityOrder(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	document := makeRichFixtureDocument(t)
	originalPayload := mustMarshal(t, document)
	original, err := validator.Validate(originalPayload)
	if err != nil {
		t.Fatalf("Validate(original) error = %v", err)
	}
	originalHash, err := DomainHash(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}

	reordered := cloneDocument(t, document)
	reverseAnySlice(arrayAt(reordered, "problems"))
	reverseAnySlice(arrayAt(reordered, "participants"))
	reverseAnySlice(arrayAt(reordered, "submissions"))
	firstParticipant := objectValue(arrayAt(reordered, "participants")[1])
	firstRanking := objectValue(firstParticipant["ranking"])
	reverseAnySlice(arrayAt(firstRanking, "problemResults"))
	firstSubmission := objectValue(arrayAt(reordered, "submissions")[1])
	reverseAnySlice(arrayAt(firstSubmission, "caseResults"))
	reorderedPayload := marshalReverseKeyOrder(t, reordered)
	reorderedSnapshot, err := validator.Validate(reorderedPayload)
	if err != nil {
		t.Fatalf("Validate(reordered) error = %v", err)
	}
	reorderedHash, err := DomainHash(context.Background(), reorderedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if reorderedHash != originalHash {
		t.Fatalf("reordered hash = %q, want %q", reorderedHash, originalHash)
	}
}

func TestDomainHashNormalizesDecimalAndUTCInstantRepresentations(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	base, err := validator.Validate(mustReadFile(t, testValidFixturePath()))
	if err != nil {
		t.Fatal(err)
	}
	baseHash, err := DomainHash(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}

	document := testFixtureDocument(t)
	objectValue(document["exam"])["totalScore"] = json.Number("1e2")
	objectValue(arrayAt(document, "problems")[0])["maxScore"] = json.Number("100.000")
	firstParticipant := objectValue(arrayAt(document, "participants")[0])
	firstRanking := objectValue(firstParticipant["ranking"])
	firstRanking["totalScore"] = json.Number("10e1")
	objectValue(arrayAt(firstRanking, "problemResults")[0])["score"] = json.Number("100.0")
	firstSubmission := objectValue(arrayAt(document, "submissions")[0])
	firstSubmission["submittedAt"] = "2026-07-09T01:01:00+00:00"
	firstSubmission["score"] = json.Number("1.00e2")
	variant, err := validator.Validate(mustMarshal(t, document))
	if err != nil {
		t.Fatalf("Validate(variant) error = %v", err)
	}
	variantHash, err := DomainHash(context.Background(), variant)
	if err != nil {
		t.Fatal(err)
	}
	if variantHash != baseHash {
		t.Fatalf("normalized hash = %q, want %q", variantHash, baseHash)
	}

	millisecondDocument := testFixtureDocument(t)
	objectValue(arrayAt(millisecondDocument, "submissions")[0])["submittedAt"] = "2026-07-09T01:01:00.000999Z"
	millisecondVariant, err := validator.Validate(mustMarshal(t, millisecondDocument))
	if err != nil {
		t.Fatal(err)
	}
	millisecondHash, err := DomainHash(context.Background(), millisecondVariant)
	if err != nil {
		t.Fatal(err)
	}
	if millisecondHash != baseHash {
		t.Fatalf("sub-millisecond normalized hash = %q, want %q", millisecondHash, baseHash)
	}
}

func TestDomainHashExcludesTransportMetadataAndCompleteness(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	snapshot, err := validator.Validate(mustReadFile(t, testValidFixturePath()))
	if err != nil {
		t.Fatal(err)
	}
	baseHash, err := DomainHash(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}

	variant := cloneSnapshot(t, snapshot)
	variant.Exporter.Version = "9.9.9"
	variant.Exporter.ExportedAt.Time = variant.Exporter.ExportedAt.Add(24 * time.Hour)
	variant.Exam.SourceURL = "https://pintia.cn/problem-sets/problem-set-100/problems"
	variant.Completeness.Problems.SourceReportedCount = nil
	variantHash, err := DomainHash(context.Background(), variant)
	if err != nil {
		t.Fatal(err)
	}
	if variantHash != baseHash {
		t.Fatalf("metadata-only hash = %q, want %q", variantHash, baseHash)
	}
}

func TestDomainHashChangesForIncludedFieldsAndNullPresence(t *testing.T) {
	validator := testValidator(t, DefaultLimits())
	snapshot, err := validator.Validate(mustReadFile(t, testValidFixturePath()))
	if err != nil {
		t.Fatal(err)
	}
	baseHash, err := DomainHash(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{name: "exam", mutate: func(value *Snapshot) { value.Exam.Title += " changed" }},
		{name: "problem", mutate: func(value *Snapshot) { value.Problems[0].Title += " changed" }},
		{name: "participant", mutate: func(value *Snapshot) {
			changed := "Changed"
			value.Participants[0].DisplayName = &changed
		}},
		{name: "ranking accept time", mutate: func(value *Snapshot) {
			value.Participants[0].Ranking.ProblemResults[0].AcceptTimeSeconds = NewNonNegativeInteger(61)
		}},
		{name: "submission", mutate: func(value *Snapshot) { value.Submissions[0].Verdict = "PARTIALLY_CORRECT" }},
		{name: "case result", mutate: func(value *Snapshot) {
			changed := "changed"
			value.Submissions[0].CaseResults[0].Message = &changed
		}},
		{name: "explicit null", mutate: func(value *Snapshot) { value.Exam.TotalScore = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variant := cloneSnapshot(t, snapshot)
			test.mutate(variant)
			variantHash, err := DomainHash(context.Background(), variant)
			if err != nil {
				t.Fatal(err)
			}
			if variantHash == baseHash {
				t.Fatalf("DomainHash() remained %q after included-field change", baseHash)
			}
		})
	}
}

func makeRichFixtureDocument(t *testing.T) map[string]any {
	t.Helper()
	document := testFixtureDocument(t)
	problems := arrayAt(document, "problems")
	secondProblem := cloneObject(t, objectValue(problems[0]))
	secondProblem["problemSetProblemId"] = "psp-200"
	secondProblem["problemId"] = "problem-200"
	secondProblem["label"] = "7-2"
	secondProblem["title"] = "Second problem"
	document["problems"] = append(problems, secondProblem)
	setCollectionCounts(document, "problems", 2, 2, 2)

	participant := objectValue(arrayAt(document, "participants")[0])
	ranking := objectValue(participant["ranking"])
	results := arrayAt(ranking, "problemResults")
	secondResult := cloneObject(t, objectValue(results[0]))
	secondResult["problemSetProblemId"] = "psp-200"
	secondResult["score"] = json.Number("0")
	secondResult["passed"] = false
	ranking["problemResults"] = append(results, secondResult)

	submission := objectValue(arrayAt(document, "submissions")[0])
	cases := arrayAt(submission, "caseResults")
	secondCase := cloneObject(t, objectValue(cases[0]))
	secondCase["caseId"] = "case-2"
	secondCase["message"] = "second case"
	submission["caseResults"] = append(cases, secondCase)
	return document
}

func cloneDocument(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	return cloneObject(t, document)
}

func cloneSnapshot(t *testing.T, snapshot *Snapshot) *Snapshot {
	t.Helper()
	payload := mustMarshal(t, snapshot)
	var clone Snapshot
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func reverseAnySlice(values []any) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func marshalReverseKeyOrder(t *testing.T, value any) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := writeReverseKeyOrderJSON(&buffer, value); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeReverseKeyOrderJSON(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buffer.Write(encodedKey)
			buffer.WriteByte(':')
			if err := writeReverseKeyOrderJSON(buffer, typed[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
		return nil
	case []any:
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeReverseKeyOrderJSON(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
		return nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
		return nil
	}
}
