package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

const maxProviderReceiptBytes = 64 << 10

type DeliveryWorkerConfig struct {
	Owner         string
	LeaseDuration time.Duration
	RetryDelay    time.Duration
}

type DeliveryWorker struct {
	repository    DeliveryRepository
	provider      DeliveryProvider
	owner         string
	leaseDuration time.Duration
	retryDelay    time.Duration
	uuid          UUIDGenerator
}

func NewDeliveryWorker(repository DeliveryRepository, provider DeliveryProvider, config DeliveryWorkerConfig) (*DeliveryWorker, error) {
	return newDeliveryWorker(repository, provider, config, randomUUIDv4)
}

func newDeliveryWorker(
	repository DeliveryRepository,
	provider DeliveryProvider,
	config DeliveryWorkerConfig,
	uuid UUIDGenerator,
) (*DeliveryWorker, error) {
	if repository == nil || provider == nil || uuid == nil || strings.TrimSpace(config.Owner) != config.Owner || config.Owner == "" || len(config.Owner) > 128 {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback delivery worker", errors.New("repository, provider, UUID generator, and bounded owner are required"))
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback delivery worker", err)
	}
	if config.RetryDelay < time.Second {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback delivery worker", errors.New("retry delay must be at least one second"))
	}
	return &DeliveryWorker{
		repository:    repository,
		provider:      provider,
		owner:         config.Owner,
		leaseDuration: config.LeaseDuration,
		retryDelay:    config.RetryDelay,
		uuid:          uuid,
	}, nil
}

func (worker *DeliveryWorker) RunOne(ctx context.Context) (*DeliveryOutcome, error) {
	if ctx == nil {
		return nil, feedbackError(ErrorInvalidInput, true, "run feedback delivery worker", errors.New("context is required"))
	}
	attemptToken, err := worker.uuid()
	if err != nil {
		return nil, feedbackError(ErrorInvalidConfiguration, false, "generate delivery attempt token", err)
	}
	claim, err := worker.repository.ClaimDelivery(ctx, worker.owner, attemptToken, worker.leaseDuration)
	if err != nil || claim == nil {
		return nil, err
	}
	outcome, err := worker.Process(ctx, *claim)
	return &outcome, err
}

func (worker *DeliveryWorker) Process(ctx context.Context, claim DeliveryClaim) (DeliveryOutcome, error) {
	if ctx == nil {
		return DeliveryOutcome{}, feedbackError(ErrorInvalidInput, true, "process feedback delivery", errors.New("context is required"))
	}
	renewer, err := workerlease.Start(ctx, worker.leaseDuration, func(renewContext context.Context) error {
		return worker.repository.RenewDeliveryLease(renewContext, claim, worker.leaseDuration)
	})
	if err != nil {
		return DeliveryOutcome{}, err
	}
	outcome, resultErr := worker.processWithLease(renewer.Context(), claim)
	renewer.Stop()
	if renewalErr := renewer.Failure(); renewalErr != nil {
		if resultErr == nil && outcome.Disposition != "" && CodeOf(renewalErr) == ErrorLeaseLost {
			return outcome, nil
		}
		return DeliveryOutcome{}, renewalErr
	}
	return outcome, resultErr
}

func (worker *DeliveryWorker) processWithLease(ctx context.Context, claim DeliveryClaim) (DeliveryOutcome, error) {
	request, err := worker.repository.LoadDelivery(ctx, claim)
	if err != nil {
		return DeliveryOutcome{}, err
	}
	receipt, err := worker.provider.Deliver(ctx, request)
	if contextErr := context.Cause(ctx); contextErr != nil {
		return DeliveryOutcome{}, feedbackError(ErrorCanceled, false, "deliver feedback", contextErr)
	}
	if err != nil {
		var providerFailure *ProviderFailure
		if !errors.As(err, &providerFailure) || !providerFailureCodePattern.MatchString(providerFailure.Code) {
			return DeliveryOutcome{}, feedbackError(ErrorProvider, false, "deliver feedback", err)
		}
		if providerFailure.Permanent {
			detail := truncateProviderDetail(providerFailure.Error())
			if failErr := worker.repository.FailDelivery(ctx, claim, providerFailure.Code, detail); failErr != nil {
				return DeliveryOutcome{}, errors.Join(err, failErr)
			}
			code := providerFailure.Code
			return DeliveryOutcome{JobID: claim.ID, Disposition: DeliveryFailed, FailureCode: &code}, nil
		}
		if requeueErr := worker.repository.RequeueDelivery(ctx, claim, worker.retryDelay, providerFailure.Code); requeueErr != nil {
			return DeliveryOutcome{}, errors.Join(err, requeueErr)
		}
		code := providerFailure.Code
		return DeliveryOutcome{JobID: claim.ID, Disposition: DeliveryRetry, FailureCode: &code}, nil
	}
	if len(receipt) == 0 || len(receipt) > maxProviderReceiptBytes {
		return DeliveryOutcome{}, feedbackError(ErrorProvider, false, "validate feedback provider receipt", errors.New("provider receipt must be non-empty and bounded"))
	}
	digest := sha256.Sum256(receipt)
	receiptHash := hex.EncodeToString(digest[:])
	if err := worker.repository.CompleteDelivery(ctx, claim, receiptHash); err != nil {
		return DeliveryOutcome{}, err
	}
	return DeliveryOutcome{JobID: claim.ID, Disposition: DeliverySucceeded, ReceiptSHA256: &receiptHash}, nil
}

func truncateProviderDetail(value string) string {
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
