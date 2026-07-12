package oj

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

type fakeJudgeRepository struct {
	claim       *JudgeClaim
	request     judgecontract.ExecutionRequest
	completed   *CompleteJudgeCommand
	requeueCode string
	failedCode  string
}

func (repository *fakeJudgeRepository) ClaimJudge(context.Context, string, string, time.Duration) (*JudgeClaim, error) {
	return repository.claim, nil
}

func (*fakeJudgeRepository) RenewJudgeLease(context.Context, JudgeClaim, time.Duration) error {
	return nil
}

func (repository *fakeJudgeRepository) LoadExecution(context.Context, JudgeClaim) (judgecontract.ExecutionRequest, error) {
	return repository.request, nil
}

func (repository *fakeJudgeRepository) CompleteJudge(_ context.Context, command CompleteJudgeCommand) (JudgeResult, error) {
	repository.completed = &command
	return JudgeResult{Verdict: command.Verdict, ResultSHA256: command.ResultSHA256}, nil
}

func (repository *fakeJudgeRepository) RequeueJudge(_ context.Context, _ JudgeClaim, _ time.Duration, code string) error {
	repository.requeueCode = code
	return nil
}

func (repository *fakeJudgeRepository) FailJudge(_ context.Context, _ JudgeClaim, code, _ string) error {
	repository.failedCode = code
	return nil
}

type executorFunc func(context.Context, judgecontract.ExecutionRequest) (judgecontract.ExecutorResult, error)

func (executor executorFunc) Execute(ctx context.Context, request judgecontract.ExecutionRequest) (judgecontract.ExecutorResult, error) {
	return executor(ctx, request)
}

type fakeOutputPublisher struct {
	released bool
}

func (publisher *fakeOutputPublisher) PublishJudgeOutput(_ context.Context, output []byte) (*PublishedOutput, error) {
	artifact := testArtifact("d", JudgeOutputMediaType, int64(len(output)))
	return &PublishedOutput{Artifact: artifact, Release: func() error { publisher.released = true; return nil }}, nil
}

func TestWorkerCompletesWithPublishedOutput(t *testing.T) {
	claim := testJudgeClaim(1)
	repository := &fakeJudgeRepository{claim: &claim, request: testExecutionRequest()}
	publisher := &fakeOutputPublisher{}
	worker, err := newWorker(repository, executorFunc(func(context.Context, judgecontract.ExecutionRequest) (judgecontract.ExecutorResult, error) {
		return judgecontract.ExecutorResult{
			Verdict: judgecontract.VerdictAccepted, ScoreFraction: 1, PassedCaseCount: 2, TotalCaseCount: 2,
			MaxTimeMS: 8, MaxMemoryBytes: 4096, Output: []byte("accepted"),
			ResultManifest: []byte(` {"cases":2} `),
		}, nil
	}), publisher, WorkerConfig{Owner: "judge-test", LeaseDuration: time.Minute, RetryDelay: time.Second, MaximumAttempts: 3},
		func() (string, error) { return claim.AttemptToken, nil })
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.RunOne(context.Background())
	if err != nil || outcome == nil || outcome.Disposition != "completed" || repository.completed == nil || !publisher.released {
		t.Fatalf("outcome=%#v completed=%#v released=%t error=%v", outcome, repository.completed, publisher.released, err)
	}
	if string(repository.completed.ResultManifest) != `{"cases":2}` || repository.completed.Output == nil {
		t.Fatalf("completion=%#v", repository.completed)
	}
}

func TestWorkerClassifiesRetryAndTerminalFailure(t *testing.T) {
	for name, test := range map[string]struct {
		attempt     int32
		failure     *judgecontract.ExecutionFailure
		wantRetry   string
		wantFailure string
	}{
		"transient": {attempt: 1, failure: &judgecontract.ExecutionFailure{Code: "runner_busy", Cause: errors.New("busy")}, wantRetry: "runner_busy"},
		"permanent": {attempt: 1, failure: &judgecontract.ExecutionFailure{Code: "sandbox_contract", Permanent: true, Cause: errors.New("invalid")}, wantFailure: "sandbox_contract"},
		"exhausted": {attempt: 3, failure: &judgecontract.ExecutionFailure{Code: "runner_busy", Cause: errors.New("busy")}, wantFailure: "attempts_exhausted"},
	} {
		t.Run(name, func(t *testing.T) {
			claim := testJudgeClaim(test.attempt)
			repository := &fakeJudgeRepository{claim: &claim, request: testExecutionRequest()}
			worker, err := newWorker(repository, executorFunc(func(context.Context, judgecontract.ExecutionRequest) (judgecontract.ExecutorResult, error) {
				return judgecontract.ExecutorResult{}, test.failure
			}), &fakeOutputPublisher{}, WorkerConfig{Owner: "judge-test", LeaseDuration: time.Minute, RetryDelay: time.Second, MaximumAttempts: 3},
				func() (string, error) { return claim.AttemptToken, nil })
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := worker.RunOne(context.Background())
			if err != nil || outcome == nil || repository.requeueCode != test.wantRetry || repository.failedCode != test.wantFailure {
				t.Fatalf("outcome=%#v retry=%q failure=%q error=%v", outcome, repository.requeueCode, repository.failedCode, err)
			}
		})
	}
}

func TestWorkerRejectsUnclassifiedExecutorFailure(t *testing.T) {
	claim := testJudgeClaim(1)
	repository := &fakeJudgeRepository{claim: &claim, request: testExecutionRequest()}
	worker, err := newWorker(repository, executorFunc(func(context.Context, judgecontract.ExecutionRequest) (judgecontract.ExecutorResult, error) {
		return judgecontract.ExecutorResult{}, errors.New("raw failure")
	}), &fakeOutputPublisher{}, WorkerConfig{Owner: "judge-test", LeaseDuration: time.Minute, RetryDelay: time.Second, MaximumAttempts: 3},
		func() (string, error) { return claim.AttemptToken, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOne(context.Background()); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
}

func testJudgeClaim(attempt int32) JudgeClaim {
	return JudgeClaim{DatabaseID: 7, ID: testJobID, AttemptCount: attempt,
		AttemptToken: "88888888-8888-4888-8888-888888888888", LeaseOwner: "judge-test", LeaseExpiresAt: time.Now().Add(time.Minute)}
}

func testExecutionRequest() judgecontract.ExecutionRequest {
	return judgecontract.ExecutionRequest{JudgeJobID: testJobID, SubmissionID: "99999999-9999-4999-8999-999999999999",
		ProblemID: testProblemID, ProblemVersion: 1, Mode: judgecontract.SubmissionSubmit, LanguageID: judgecontract.LanguageCPP20,
		Source: testExecutionArtifact("a", judgecontract.CPP20SourceMediaType, 10), TestBundle: testExecutionArtifact("b", judgecontract.TestBundleMediaType, 10),
		ProblemSchema: judgecontract.ProblemSchemaV1, ProblemSpec: []byte(`{}`), TimeLimitMS: 1000,
		MemoryLimitBytes: 1 << 20, OutputLimitBytes: int64(len(strings.Repeat("x", 1024))),
	}
}

func testExecutionArtifact(prefix, mediaType string, size int64) judgecontract.Artifact {
	value := testArtifact(prefix, mediaType, size)
	return judgecontract.Artifact{
		SHA256: value.SHA256, SizeBytes: value.SizeBytes, MediaType: value.MediaType, StorageKey: value.StorageKey,
	}
}
