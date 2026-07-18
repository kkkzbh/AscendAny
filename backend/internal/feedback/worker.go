package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
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
	artifacts     artifactVerifier
	provider      DeliveryProvider
	owner         string
	leaseDuration time.Duration
	retryDelay    time.Duration
	uuid          UUIDGenerator
}

type artifactVerifier interface {
	Verify(context.Context, string, int64) (artifactstore.Artifact, error)
}

func NewDeliveryWorker(repository DeliveryRepository, artifacts artifactVerifier, provider DeliveryProvider, config DeliveryWorkerConfig) (*DeliveryWorker, error) {
	return newDeliveryWorker(repository, artifacts, provider, config, randomUUIDv4)
}

func newDeliveryWorker(
	repository DeliveryRepository,
	artifacts artifactVerifier,
	provider DeliveryProvider,
	config DeliveryWorkerConfig,
	uuid UUIDGenerator,
) (*DeliveryWorker, error) {
	if repository == nil || artifacts == nil || provider == nil || uuid == nil || strings.TrimSpace(config.Owner) != config.Owner || config.Owner == "" || len(config.Owner) > 128 {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback delivery worker", errors.New("repository, artifact verifier, provider, UUID generator, and bounded owner are required"))
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback delivery worker", err)
	}
	if config.RetryDelay < time.Second {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback delivery worker", errors.New("retry delay must be at least one second"))
	}
	return &DeliveryWorker{
		repository:    repository,
		artifacts:     artifacts,
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
	request, artifactFailure := worker.hydrateAttachments(ctx, request)
	if artifactFailure != nil {
		if errors.Is(artifactFailure.cause, context.Canceled) || errors.Is(artifactFailure.cause, context.DeadlineExceeded) {
			return DeliveryOutcome{}, feedbackError(ErrorCanceled, false, "hydrate feedback attachments", artifactFailure.cause)
		}
		if artifactFailure.permanent {
			if failErr := worker.repository.FailDelivery(ctx, claim, artifactFailure.code, truncateProviderDetail(artifactFailure.cause.Error())); failErr != nil {
				return DeliveryOutcome{}, errors.Join(artifactFailure.cause, failErr)
			}
			code := artifactFailure.code
			return DeliveryOutcome{JobID: claim.ID, Disposition: DeliveryFailed, FailureCode: &code}, nil
		}
		if requeueErr := worker.repository.RequeueDelivery(ctx, claim, worker.retryDelay, artifactFailure.code); requeueErr != nil {
			return DeliveryOutcome{}, errors.Join(artifactFailure.cause, requeueErr)
		}
		code := artifactFailure.code
		return DeliveryOutcome{JobID: claim.ID, Disposition: DeliveryRetry, FailureCode: &code}, nil
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

type attachmentArtifactFailure struct {
	code      string
	permanent bool
	cause     error
}

func (worker *DeliveryWorker) hydrateAttachments(ctx context.Context, request DeliveryRequest) (DeliveryRequest, *attachmentArtifactFailure) {
	if err := validateDeliveryAttachmentManifest(request.Attachments); err != nil {
		return DeliveryRequest{}, &attachmentArtifactFailure{code: "attachment_artifact_invalid", permanent: true, cause: err}
	}
	hydrated := append([]DeliveryAttachment(nil), request.Attachments...)
	for index := range hydrated {
		attachment := &hydrated[index]
		verified, err := worker.artifacts.Verify(ctx, attachment.SHA256, attachment.SizeBytes)
		if err != nil {
			return DeliveryRequest{}, classifyAttachmentArtifactFailure(err)
		}
		if verified.Hash != attachment.SHA256 || verified.Size != attachment.SizeBytes ||
			verified.StorageKey != attachment.StorageKey || verified.Path == "" {
			return DeliveryRequest{}, &attachmentArtifactFailure{
				code: "attachment_artifact_invalid", permanent: true,
				cause: errors.New("verified feedback attachment metadata differs from the durable manifest"),
			}
		}
		file, err := os.Open(verified.Path)
		if err != nil {
			return DeliveryRequest{}, classifyAttachmentFileFailure("open verified feedback attachment", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(file, attachment.SizeBytes+1))
		closeErr := file.Close()
		if contextErr := context.Cause(ctx); contextErr != nil {
			return DeliveryRequest{}, &attachmentArtifactFailure{code: "attachment_artifact_unavailable", cause: contextErr}
		}
		if readErr != nil {
			return DeliveryRequest{}, classifyAttachmentFileFailure("read verified feedback attachment", readErr)
		}
		if closeErr != nil {
			return DeliveryRequest{}, classifyAttachmentFileFailure("close verified feedback attachment", closeErr)
		}
		digest := sha256.Sum256(content)
		if int64(len(content)) != attachment.SizeBytes || hex.EncodeToString(digest[:]) != attachment.SHA256 {
			return DeliveryRequest{}, &attachmentArtifactFailure{
				code: "attachment_artifact_invalid", permanent: true,
				cause: errors.New("verified feedback attachment content differs from the durable manifest"),
			}
		}
		attachment.Content = content
	}
	request.Attachments = hydrated
	return request, nil
}

func classifyAttachmentArtifactFailure(err error) *attachmentArtifactFailure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &attachmentArtifactFailure{code: "attachment_artifact_unavailable", cause: err}
	}
	if code, owned := artifactstore.CodeOf(err); owned {
		switch code {
		case artifactstore.ErrorCanceled:
			return &attachmentArtifactFailure{code: "attachment_artifact_unavailable", cause: err}
		case artifactstore.ErrorIO, artifactstore.ErrorReferenceCheck:
			return &attachmentArtifactFailure{code: "attachment_artifact_unavailable", cause: err}
		default:
			return &attachmentArtifactFailure{code: "attachment_artifact_invalid", permanent: true, cause: err}
		}
	}
	return &attachmentArtifactFailure{code: "attachment_artifact_unavailable", cause: err}
}

func classifyAttachmentFileFailure(operation string, err error) *attachmentArtifactFailure {
	failure := &attachmentArtifactFailure{code: "attachment_artifact_unavailable", cause: fmt.Errorf("%s: %w", operation, err)}
	if errors.Is(err, os.ErrNotExist) {
		failure.code = "attachment_artifact_invalid"
		failure.permanent = true
	}
	return failure
}

func truncateProviderDetail(value string) string {
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
