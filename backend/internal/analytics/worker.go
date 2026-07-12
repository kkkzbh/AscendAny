package analytics

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

type WorkerConfig struct {
	Owner         string
	LeaseDuration time.Duration
	AnalyticsJSON []byte
}

type analyticsRepository interface {
	Claim(context.Context, string, time.Duration) (*Claim, error)
	RenewLease(context.Context, Claim, time.Duration) error
	Load(context.Context, Claim, ParsedConfig) (WorkItem, error)
	Publish(context.Context, Claim, Result) (PublishResult, error)
	FailPermanent(context.Context, Claim, ErrorCode, string) error
}

type Worker struct {
	repository    analyticsRepository
	configuration ParsedConfig
	owner         string
	leaseDuration time.Duration
}

func NewWorker(pool PgxBeginner, configuration WorkerConfig) (*Worker, error) {
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseConfig(configuration.AnalyticsJSON)
	if err != nil {
		return nil, err
	}
	return newWorker(repository, parsed, configuration.Owner, configuration.LeaseDuration)
}

func newWorker(
	repository analyticsRepository,
	configuration ParsedConfig,
	owner string,
	leaseDuration time.Duration,
) (*Worker, error) {
	if repository == nil {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct analytics worker", errors.New("repository is required"))
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(owner) != owner {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct analytics worker", errors.New("worker owner must be a non-empty trimmed string"))
	}
	if len(owner) > 128 {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct analytics worker", errors.New("worker owner must not exceed 128 bytes"))
	}
	if _, err := workerlease.ValidateDuration(leaseDuration); err != nil {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct analytics worker", err)
	}
	validated, err := ParseConfig(configuration.Canonical)
	if err != nil {
		return nil, err
	}
	if validated.SHA256 != configuration.SHA256 {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct analytics worker", errors.New("parsed configuration hash is inconsistent"))
	}
	return &Worker{repository: repository, configuration: validated, owner: owner, leaseDuration: leaseDuration}, nil
}

func (worker *Worker) RunOne(ctx context.Context) (*RunOutcome, error) {
	if ctx == nil {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "run analytics worker", errors.New("context is required"))
	}
	claim, err := worker.repository.Claim(ctx, worker.owner, worker.leaseDuration)
	if err != nil || claim == nil {
		return nil, err
	}
	outcome, err := worker.Process(ctx, *claim)
	return &outcome, err
}

func (worker *Worker) Process(ctx context.Context, claim Claim) (RunOutcome, error) {
	if ctx == nil {
		return RunOutcome{}, analyticsError(ErrorInvalidConfiguration, true, "process analytics generation", errors.New("context is required"))
	}
	renewer, err := workerlease.Start(ctx, worker.leaseDuration, func(renewalContext context.Context) error {
		return worker.repository.RenewLease(renewalContext, claim, worker.leaseDuration)
	})
	if err != nil {
		return RunOutcome{}, err
	}
	outcome, resultErr := worker.processWithLease(renewer.Context(), claim)
	renewer.Stop()
	if renewalErr := renewer.Failure(); renewalErr != nil {
		if resultErr == nil && completedAnalyticsAttempt(outcome) {
			// Publish and permanent-failure transactions fence the same active
			// attempt before clearing its lease. A later heartbeat can therefore
			// report lease loss only because that terminal transition won first.
			if code, ok := CodeOf(renewalErr); ok && code == ErrorLeaseLost {
				return outcome, nil
			}
		}
		return RunOutcome{}, renewalErr
	}
	return outcome, resultErr
}

func completedAnalyticsAttempt(outcome RunOutcome) bool {
	switch outcome.Disposition {
	case RunSucceeded, RunSuperseded, RunFailed:
		return true
	case "":
		return false
	default:
		return false
	}
}

func (worker *Worker) processWithLease(ctx context.Context, claim Claim) (RunOutcome, error) {
	item, err := worker.repository.Load(ctx, claim, worker.configuration)
	if err != nil {
		if contextErr := analyticsProcessingContextError(ctx, "load analytics work"); contextErr != nil {
			return RunOutcome{}, contextErr
		}
		return worker.handleFailure(ctx, claim, err)
	}
	result, err := Compute(ctx, worker.configuration.Value, item.Dataset)
	if contextErr := analyticsProcessingContextError(ctx, "compute analytics"); contextErr != nil {
		return RunOutcome{}, contextErr
	}
	if err != nil {
		return worker.handleFailure(ctx, claim, err)
	}
	if contextErr := analyticsProcessingContextError(ctx, "publish analytics"); contextErr != nil {
		return RunOutcome{}, contextErr
	}
	published, err := worker.repository.Publish(ctx, claim, result)
	if err != nil {
		if contextErr := analyticsProcessingContextError(ctx, "publish analytics"); contextErr != nil {
			return RunOutcome{}, contextErr
		}
		return worker.handleFailure(ctx, claim, err)
	}
	if published.Disposition == PublishSucceeded {
		return RunOutcome{GenerationID: claim.GenerationID, Disposition: RunSucceeded}, nil
	}
	if published.Disposition != PublishSuperseded || published.ReplacementGenerationID == nil {
		return RunOutcome{}, analyticsError(ErrorStateConflict, false, "process analytics generation", errors.New("repository returned an invalid publish disposition"))
	}
	return RunOutcome{
		GenerationID:            claim.GenerationID,
		Disposition:             RunSuperseded,
		ReplacementGenerationID: published.ReplacementGenerationID,
	}, nil
}

func (worker *Worker) handleFailure(ctx context.Context, claim Claim, failure error) (RunOutcome, error) {
	if contextErr := analyticsProcessingContextError(ctx, "handle analytics failure"); contextErr != nil {
		return RunOutcome{}, contextErr
	}
	if !IsPermanent(failure) {
		return RunOutcome{}, failure
	}
	code, ok := CodeOf(failure)
	if !ok || !permanentFailureCode(code) {
		return RunOutcome{}, failure
	}
	if err := worker.repository.FailPermanent(ctx, claim, code, truncateErrorDetail(failure.Error())); err != nil {
		return RunOutcome{}, errors.Join(failure, err)
	}
	return RunOutcome{GenerationID: claim.GenerationID, Disposition: RunFailed, FailureCode: &code}, nil
}

func analyticsProcessingContextError(ctx context.Context, operation string) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return nil
	}
	if _, ok := CodeOf(cause); ok {
		return cause
	}
	return analyticsError(ErrorCanceled, false, operation, cause)
}
