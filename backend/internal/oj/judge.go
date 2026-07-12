package oj

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

const maxJudgeResultManifestBytes = 256 << 10

var executionFailureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

type JudgeRepository interface {
	ClaimJudge(context.Context, string, string, time.Duration) (*JudgeClaim, error)
	RenewJudgeLease(context.Context, JudgeClaim, time.Duration) error
	LoadExecution(context.Context, JudgeClaim) (judgecontract.ExecutionRequest, error)
	CompleteJudge(context.Context, CompleteJudgeCommand) (JudgeResult, error)
	RequeueJudge(context.Context, JudgeClaim, time.Duration, string) error
	FailJudge(context.Context, JudgeClaim, string, string) error
}

type OutputPublisher interface {
	PublishJudgeOutput(context.Context, []byte) (*PublishedOutput, error)
}

type WorkerConfig struct {
	Owner           string
	LeaseDuration   time.Duration
	RetryDelay      time.Duration
	MaximumAttempts int32
}

type Worker struct {
	repository      JudgeRepository
	executor        judgecontract.Executor
	outputs         OutputPublisher
	owner           string
	leaseDuration   time.Duration
	retryDelay      time.Duration
	maximumAttempts int32
	uuid            UUIDGenerator
}

func NewWorker(repository JudgeRepository, executor judgecontract.Executor, outputs OutputPublisher, config WorkerConfig) (*Worker, error) {
	return newWorker(repository, executor, outputs, config, randomUUIDv4)
}

func newWorker(repository JudgeRepository, executor judgecontract.Executor, outputs OutputPublisher, config WorkerConfig, uuid UUIDGenerator) (*Worker, error) {
	if repository == nil || executor == nil || outputs == nil || uuid == nil || strings.TrimSpace(config.Owner) != config.Owner ||
		config.Owner == "" || len(config.Owner) > 128 || config.RetryDelay < time.Second || config.MaximumAttempts < 1 || config.MaximumAttempts > 100 {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ judge worker", errors.New("repository, executor, output publisher, UUID generator, and bounded worker policy are required"))
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ judge worker", err)
	}
	return &Worker{
		repository: repository, executor: executor, outputs: outputs, owner: config.Owner,
		leaseDuration: config.LeaseDuration, retryDelay: config.RetryDelay,
		maximumAttempts: config.MaximumAttempts, uuid: uuid,
	}, nil
}

func (worker *Worker) RunOne(ctx context.Context) (*JudgeOutcome, error) {
	if ctx == nil {
		return nil, ojError(ErrorInvalidInput, true, "run OJ judge worker", errors.New("context is required"))
	}
	attemptToken, err := worker.uuid()
	if err != nil {
		return nil, ojError(ErrorInvalidConfiguration, false, "generate OJ judge attempt token", err)
	}
	claim, err := worker.repository.ClaimJudge(ctx, worker.owner, attemptToken, worker.leaseDuration)
	if err != nil || claim == nil {
		return nil, err
	}
	outcome, err := worker.Process(ctx, *claim)
	return &outcome, err
}

func (worker *Worker) Process(ctx context.Context, claim JudgeClaim) (JudgeOutcome, error) {
	if ctx == nil {
		return JudgeOutcome{}, ojError(ErrorInvalidInput, true, "process OJ judge job", errors.New("context is required"))
	}
	renewer, err := workerlease.Start(ctx, worker.leaseDuration, func(renewContext context.Context) error {
		return worker.repository.RenewJudgeLease(renewContext, claim, worker.leaseDuration)
	})
	if err != nil {
		return JudgeOutcome{}, err
	}
	outcome, resultErr := worker.processWithLease(renewer.Context(), claim)
	renewer.Stop()
	if renewalErr := renewer.Failure(); renewalErr != nil {
		if resultErr == nil && outcome.Disposition != "" && CodeOf(renewalErr) == ErrorLeaseLost {
			return outcome, nil
		}
		return JudgeOutcome{}, renewalErr
	}
	return outcome, resultErr
}

func (worker *Worker) processWithLease(ctx context.Context, claim JudgeClaim) (outcome JudgeOutcome, resultErr error) {
	request, err := worker.repository.LoadExecution(ctx, claim)
	if err != nil {
		return JudgeOutcome{}, err
	}
	executed, err := worker.executor.Execute(ctx, request)
	if contextErr := context.Cause(ctx); contextErr != nil {
		return JudgeOutcome{}, ojError(ErrorCanceled, false, "execute OJ submission", contextErr)
	}
	if err != nil {
		return worker.handleExecutionFailure(ctx, claim, err)
	}
	if int64(len(executed.Output)) > request.OutputLimitBytes {
		return worker.handleExecutionFailure(ctx, claim, &judgecontract.ExecutionFailure{
			Code: "executor_output_limit_contract", Permanent: true,
			Cause: errors.New("executor returned output beyond the problem hard limit"),
		})
	}
	var publication *PublishedOutput
	if len(executed.Output) > 0 {
		publication, err = worker.outputs.PublishJudgeOutput(ctx, executed.Output)
		if err != nil {
			return worker.handleExecutionFailure(ctx, claim, &judgecontract.ExecutionFailure{Code: "output_publish_failed", Cause: err})
		}
		if publication == nil || publication.Release == nil {
			return JudgeOutcome{}, ojError(ErrorStoredDataInvalid, true, "publish OJ judge output", errors.New("output publisher returned an incomplete publication"))
		}
		defer func() {
			if releaseErr := publication.Release(); releaseErr != nil {
				wrapped := ojError(ErrorDatabase, false, "release OJ output publication", releaseErr)
				if resultErr == nil {
					resultErr = wrapped
				} else {
					resultErr = errors.Join(resultErr, wrapped)
				}
			}
		}()
		if err := validateArtifact(publication.Artifact, JudgeOutputMediaType, request.OutputLimitBytes); err != nil {
			return JudgeOutcome{}, ojError(ErrorStoredDataInvalid, true, "validate published OJ output", err)
		}
	}
	input := JudgeResultInput{
		Verdict: Verdict(executed.Verdict), ScoreFraction: executed.ScoreFraction,
		PassedCaseCount: executed.PassedCaseCount, TotalCaseCount: executed.TotalCaseCount,
		MaxTimeMS: executed.MaxTimeMS, MaxMemoryBytes: executed.MaxMemoryBytes,
		ResultManifest: executed.ResultManifest,
	}
	if publication != nil {
		input.Output = &publication.Artifact
	}
	manifest, manifestHash, err := validateJudgeResult(input, maxJudgeResultManifestBytes)
	if err != nil {
		return worker.handleExecutionFailure(ctx, claim, &judgecontract.ExecutionFailure{Code: "executor_result_contract", Permanent: true, Cause: err})
	}
	input.ResultManifest = manifest
	resultHash := judgeResultHash(input, manifestHash)
	result, err := worker.repository.CompleteJudge(ctx, CompleteJudgeCommand{
		Claim: claim, JudgeResultInput: input,
		ResultSchema: JudgeResultSchemaV1, ResultSHA256: resultHash,
	})
	if err != nil {
		return JudgeOutcome{}, err
	}
	return JudgeOutcome{JobID: claim.ID, Disposition: "completed", Result: &result}, nil
}

func (worker *Worker) handleExecutionFailure(ctx context.Context, claim JudgeClaim, err error) (JudgeOutcome, error) {
	var failure *judgecontract.ExecutionFailure
	if !errors.As(err, &failure) || !executionFailureCodePattern.MatchString(failure.Code) {
		return JudgeOutcome{}, ojError(ErrorStoredDataInvalid, true, "execute OJ submission", errors.New("executor returned an unclassified failure"))
	}
	code := failure.Code
	if failure.Permanent || claim.AttemptCount >= worker.maximumAttempts {
		if claim.AttemptCount >= worker.maximumAttempts && !failure.Permanent {
			code = "attempts_exhausted"
		}
		detail := failure.Error()
		if len(detail) > 4096 {
			detail = detail[:4096]
		}
		if failErr := worker.repository.FailJudge(ctx, claim, code, detail); failErr != nil {
			return JudgeOutcome{}, errors.Join(err, failErr)
		}
		return JudgeOutcome{JobID: claim.ID, Disposition: "system_error", FailureCode: &code}, nil
	}
	if requeueErr := worker.repository.RequeueJudge(ctx, claim, worker.retryDelay, code); requeueErr != nil {
		return JudgeOutcome{}, errors.Join(err, requeueErr)
	}
	return JudgeOutcome{JobID: claim.ID, Disposition: "retry", FailureCode: &code}, nil
}
