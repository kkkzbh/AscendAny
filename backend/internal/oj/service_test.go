package oj

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID = "11111111-1111-4111-8111-111111111111"
	testSessionID = "22222222-2222-4222-8222-222222222222"
	testJWTID     = "33333333-3333-4333-8333-333333333333"
	testProblemID = "44444444-4444-4444-8444-444444444444"
	testRequestID = "55555555-5555-4555-8555-555555555555"
	testJobID     = "66666666-6666-4666-8666-666666666666"
)

type capturingRepository struct {
	problemCommand    CreateProblemVersionCommand
	submissionCommand CreateSubmissionCommand
}

func (repository *capturingRepository) CreateProblemVersion(_ context.Context, command CreateProblemVersionCommand) (CreateProblemVersionResult, error) {
	repository.problemCommand = command
	return CreateProblemVersionResult{Problem: Problem{ID: command.ProblemPublicID, Slug: command.Slug}}, nil
}

func (*capturingRepository) GetProblem(context.Context, ProblemQuery) (Problem, bool, error) {
	return Problem{}, false, nil
}

func (*capturingRepository) ListProblems(context.Context, ProblemListQuery) (ProblemPage, error) {
	return ProblemPage{}, nil
}

func (repository *capturingRepository) CreateSubmission(_ context.Context, command CreateSubmissionCommand) (CreateSubmissionResult, error) {
	repository.submissionCommand = command
	return CreateSubmissionResult{Created: true}, nil
}

func (*capturingRepository) GetSubmission(context.Context, SubmissionQuery) (SubmissionDetail, bool, error) {
	return SubmissionDetail{}, false, nil
}

func (*capturingRepository) ReadJudgeEvents(context.Context, JudgeEventQuery) (JudgeEventBatch, bool, error) {
	return JudgeEventBatch{}, false, nil
}

func TestServiceCanonicalizesProblemAndBindsSubmission(t *testing.T) {
	repository := &capturingRepository{}
	ids := []string{testProblemID, "77777777-7777-4777-8777-777777777777", testJobID}
	service, err := newService(repository, DefaultPolicy(), func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := testPrincipal(auth.RoleAdmin)
	problemInput := validProblemInput(principal)
	result, err := service.CreateProblemVersion(context.Background(), problemInput)
	if err != nil || result.Problem.ID != testProblemID {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if string(repository.problemCommand.ProblemSpec) != `{"a":1,"z":2}` ||
		repository.problemCommand.ProblemSpecSHA256 == "" || repository.problemCommand.ContentSHA256 == "" ||
		repository.problemCommand.ProblemSchema != ProblemSchemaV1 {
		t.Fatalf("problem command=%#v", repository.problemCommand)
	}

	source := testArtifact("a", CPP20SourceMediaType, 100)
	stdin := testArtifact("b", PlainTextMediaType, 10)
	_, err = service.CreateSubmission(context.Background(), CreateSubmissionInput{
		Principal: principal, ClientRequestID: testRequestID, ProblemID: testProblemID,
		ExpectedProblemHeadRevision: 1, Mode: SubmissionRun, LanguageID: LanguageCPP20,
		Source: source, Stdin: &stdin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.submissionCommand.SubmissionPublicID != "77777777-7777-4777-8777-777777777777" ||
		repository.submissionCommand.JudgeJobPublicID != testJobID {
		t.Fatalf("submission command=%#v", repository.submissionCommand)
	}
}

func TestServiceRejectsNonCanonicalAndOverLimitInputs(t *testing.T) {
	repository := &capturingRepository{}
	service, err := newService(repository, DefaultPolicy(), func() (string, error) { return testProblemID, nil })
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CreateProblemVersionInput){
		"student admin mutation": func(input *CreateProblemVersionInput) { input.Principal.Role = auth.RoleStudent },
		"unsorted tags":          func(input *CreateProblemVersionInput) { input.KnowledgeTags = []string{"tree", "array"} },
		"duplicate JSON key":     func(input *CreateProblemVersionInput) { input.ProblemSpec = json.RawMessage(`{"a":1,"a":2}`) },
		"null tags":              func(input *CreateProblemVersionInput) { input.KnowledgeTags = nil },
		"NUL title":              func(input *CreateProblemVersionInput) { input.Title = "bad\x00title" },
		"NUL statement":          func(input *CreateProblemVersionInput) { input.StatementMarkdown = "bad\x00statement" },
		"invalid UTF-8 title":    func(input *CreateProblemVersionInput) { input.Title = string([]byte{0xff}) },
		"invalid UTF-8 statement": func(input *CreateProblemVersionInput) {
			input.StatementMarkdown = string([]byte{0xff})
		},
		"NUL solution": func(input *CreateProblemVersionInput) {
			value := "bad\x00solution"
			input.SolutionMarkdown = &value
		},
		"NUL problem spec": func(input *CreateProblemVersionInput) {
			input.ProblemSpec = json.RawMessage(`{"value":"\u0000"}`)
		},
		"oversized bundle": func(input *CreateProblemVersionInput) {
			input.TestBundle.SizeBytes = DefaultPolicy().MaximumTestBundleBytes + 1
		},
		"untrimmed title": func(input *CreateProblemVersionInput) { input.Title = " title " },
	} {
		t.Run(name, func(t *testing.T) {
			input := validProblemInput(testPrincipal(auth.RoleAdmin))
			mutate(&input)
			if _, err := service.CreateProblemVersion(context.Background(), input); CodeOf(err) != ErrorInvalidInput {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
		})
	}

	input := CreateSubmissionInput{
		Principal: testPrincipal(auth.RoleStudent), ClientRequestID: testRequestID,
		ProblemID: testProblemID, ExpectedProblemHeadRevision: 1,
		Mode: SubmissionSubmit, LanguageID: LanguageCPP20,
		Source: testArtifact("a", CPP20SourceMediaType, 10),
		Stdin:  ptrArtifact(testArtifact("b", PlainTextMediaType, 1)),
	}
	if _, err := service.CreateSubmission(context.Background(), input); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("submit with stdin error=%v", err)
	}
	input.Mode = SubmissionRun
	input.Stdin = nil
	if _, err := service.CreateSubmission(context.Background(), input); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("run without stdin error=%v", err)
	}
}

func validProblemInput(principal auth.AccessPrincipal) CreateProblemVersionInput {
	return CreateProblemVersionInput{
		Principal: principal, Slug: "sum-array", ExpectedHeadRevision: 0, Lifecycle: LifecycleActive,
		Title: "Sum array", StatementMarkdown: "Compute the sum.",
		KnowledgeTags: []string{"array", "prefix-sum"}, TimeLimitMS: 1000,
		MemoryLimitBytes: 128 << 20, OutputLimitBytes: 1 << 20,
		ProblemSpec: json.RawMessage(` {"z":2.0,"a":1e0} `),
		TestBundle:  testArtifact("c", TestBundleMediaType, 1024),
	}
}

func testPrincipal(role auth.Role) auth.AccessPrincipal {
	return auth.AccessPrincipal{AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID, Role: role, AuthRevision: 1}
}

func testArtifact(character, mediaType string, size int64) Artifact {
	hash := strings.Repeat(character, 64)
	return Artifact{SHA256: hash, SizeBytes: size, MediaType: mediaType, StorageKey: "sha256/" + hash[:2] + "/" + hash}
}

func ptrArtifact(value Artifact) *Artifact { return &value }
