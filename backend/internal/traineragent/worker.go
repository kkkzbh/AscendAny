package traineragent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/recommendationprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

type WorkerConfig struct {
	LeaseDuration time.Duration
}

type WorkerDisposition string

const (
	WorkerActivated  WorkerDisposition = "activated"
	WorkerSuperseded WorkerDisposition = "superseded"
	WorkerFailed     WorkerDisposition = "failed"
	WorkerRequeued   WorkerDisposition = "requeued"
)

type WorkerOutcome struct {
	RunID                     string
	AttemptToken              string
	InputManifestSHA256       string
	OutputBundleSHA256        string
	RequestSHA256             string
	RuntimeConstructionSHA256 string
	RuntimeProvenanceSHA256   string
	RuntimeTreeSHA256         string
	HostCapabilitySHA256      string
	RuntimeAttestationSHA256  string
	Disposition               WorkerDisposition
	ModelID                   *string
	ClaimedAt                 time.Time
	HeartbeatAt               time.Time
	UploadedAt                time.Time
	FailureCode               *string
}

type Worker struct {
	transport Transport
	trainer   recommendationprotocol.Trainer
	config    WorkerConfig
}

func NewWorker(transport Transport, trainer recommendationprotocol.Trainer, config WorkerConfig) (*Worker, error) {
	if transport == nil || trainer == nil {
		return nil, errors.New("trainer-agent transport and trainer are required")
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, fmt.Errorf("trainer-agent worker lease duration: %w", err)
	}
	return &Worker{transport: transport, trainer: trainer, config: config}, nil
}

func (worker *Worker) RunOne(ctx context.Context) (*WorkerOutcome, error) {
	if ctx == nil {
		return nil, errors.New("trainer-agent worker context is required")
	}
	claim, err := worker.transport.Claim(ctx)
	if err != nil || claim == nil {
		return nil, err
	}
	if claim.LeaseDuration != worker.config.LeaseDuration {
		return nil, errors.New("trainer-agent claim lease duration differs from worker configuration")
	}
	var heartbeatMutex sync.Mutex
	lastHeartbeatAt := time.Time{}
	renewer, err := workerlease.Start(ctx, worker.config.LeaseDuration, func(renewContext context.Context) error {
		heartbeat, heartbeatErr := worker.transport.Heartbeat(renewContext, claim.LeaseReference)
		if heartbeatErr != nil {
			return heartbeatErr
		}
		if heartbeat.SucceededAt.IsZero() || heartbeat.LeaseExpiresAt.Sub(heartbeat.SucceededAt) != worker.config.LeaseDuration {
			return errors.New("trainer-agent heartbeat timestamps differ from the lease contract")
		}
		heartbeatMutex.Lock()
		lastHeartbeatAt = heartbeat.SucceededAt
		heartbeatMutex.Unlock()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("start trainer-agent lease renewal: %w", err)
	}
	outcome, operationErr := worker.processClaim(renewer.Context(), *claim)
	renewer.Stop()
	if operationErr == nil {
		if outcome.Disposition == WorkerActivated || outcome.Disposition == WorkerSuperseded {
			heartbeatMutex.Lock()
			outcome.HeartbeatAt = lastHeartbeatAt
			heartbeatMutex.Unlock()
		}
		return &outcome, nil
	}
	if renewalErr := renewer.Failure(); renewalErr != nil {
		return nil, fmt.Errorf("renew trainer-agent lease: %w", renewalErr)
	}
	if contextErr := context.Cause(ctx); contextErr != nil {
		return nil, contextErr
	}
	return nil, operationErr
}

func (worker *Worker) processClaim(ctx context.Context, claim Claim) (WorkerOutcome, error) {
	output, err := worker.trainer.Train(ctx, recommendationprotocol.TrainingRequest{
		RunID:               claim.RunID,
		InputBundle:         claim.InputBundle,
		InputManifestSHA256: claim.InputManifestSHA256,
	})
	if err != nil {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return WorkerOutcome{}, contextErr
		}
		failure := normalizeTrainerFailure(err)
		disposition, reportErr := worker.transport.ReportFailure(ctx, claim, failure)
		if reportErr != nil {
			return WorkerOutcome{}, errors.Join(err, reportErr)
		}
		failureCode := failure.Code
		switch disposition {
		case FailureRecorded:
			return WorkerOutcome{RunID: claim.RunID, Disposition: WorkerFailed, FailureCode: &failureCode}, nil
		case FailureRequeued:
			return WorkerOutcome{RunID: claim.RunID, Disposition: WorkerRequeued, FailureCode: &failureCode}, nil
		default:
			return WorkerOutcome{}, errors.New("trainer-agent transport returned an invalid failure disposition")
		}
	}
	publication, err := worker.transport.Publish(ctx, claim, output)
	if err != nil {
		return WorkerOutcome{}, err
	}
	if !sha256Pattern.MatchString(publication.RuntimeConstructionSHA256) ||
		!sha256Pattern.MatchString(publication.RuntimeProvenanceSHA256) ||
		!sha256Pattern.MatchString(publication.RuntimeTreeSHA256) ||
		!sha256Pattern.MatchString(publication.HostCapabilitySHA256) ||
		!sha256Pattern.MatchString(publication.RuntimeAttestationSHA256) {
		return WorkerOutcome{}, errors.New("trainer-agent publication runtime identity is invalid")
	}
	modelID := publication.ModelID
	switch publication.Disposition {
	case PublicationActivated:
		return successfulWorkerOutcome(claim, publication, WorkerActivated, modelID), nil
	case PublicationSuperseded:
		return successfulWorkerOutcome(claim, publication, WorkerSuperseded, modelID), nil
	default:
		return WorkerOutcome{}, errors.New("trainer-agent transport returned an invalid publication disposition")
	}
}

func successfulWorkerOutcome(
	claim Claim,
	publication Publication,
	disposition WorkerDisposition,
	modelID string,
) WorkerOutcome {
	return WorkerOutcome{
		RunID: claim.RunID, AttemptToken: claim.AttemptToken, InputManifestSHA256: claim.InputManifestSHA256,
		OutputBundleSHA256: publication.OutputBundleSHA256, RequestSHA256: publication.RequestSHA256,
		RuntimeConstructionSHA256: publication.RuntimeConstructionSHA256,
		RuntimeProvenanceSHA256:   publication.RuntimeProvenanceSHA256,
		RuntimeTreeSHA256:         publication.RuntimeTreeSHA256,
		HostCapabilitySHA256:      publication.HostCapabilitySHA256,
		RuntimeAttestationSHA256:  publication.RuntimeAttestationSHA256,
		Disposition:               disposition, ModelID: &modelID, ClaimedAt: claim.ClaimedAt, UploadedAt: publication.UploadedAt,
	}
}

func normalizeTrainerFailure(cause error) FailureReport {
	var failure *recommendationprotocol.TrainerFailure
	if errors.As(cause, &failure) && validTrainerFailure(failure) {
		return FailureReport{Code: failure.Code, Detail: failure.Detail, Retryable: failure.Retryable}
	}
	return FailureReport{Code: "trainer_unclassified", Detail: boundedFailureDetail(cause), Retryable: false}
}

func validTrainerFailure(failure *recommendationprotocol.TrainerFailure) bool {
	return failure != nil && remoteCodePattern.MatchString(failure.Code) && failure.Detail != "" &&
		strings.TrimSpace(failure.Detail) == failure.Detail && len(failure.Detail) <= maximumFailureDetailBytes &&
		utf8.ValidString(failure.Detail) && !strings.ContainsRune(failure.Detail, 0)
}

func boundedFailureDetail(cause error) string {
	const generic = "trainer-agent execution failed"
	value := generic
	if cause != nil {
		value = strings.TrimSpace(cause.Error())
		if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			value = generic
		}
	}
	if len(value) > maximumFailureDetailBytes {
		value = value[:maximumFailureDetailBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
