package importing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

const maxPersistedErrorDetailBytes = 4096

type WorkerConfig struct {
	LeaseDuration time.Duration
	RetryDelay    time.Duration
	PintiaLimits  pintia.Limits
	Analytics     AnalyticsConfig
}

type Worker struct {
	repository workerStore
	artifacts  artifactVerifier
	validator  *pintia.Validator
	uuid       uuidGenerator
	config     WorkerConfig
}

func NewWorker(pool PgxBeginner, artifacts artifactVerifier, config WorkerConfig) (*Worker, error) {
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		return nil, err
	}
	validator, err := pintia.NewEmbeddedValidator(config.PintiaLimits)
	if err != nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import worker", err)
	}
	return newWorker(repository, artifacts, validator, randomUUIDv4, config)
}

func newWorker(
	repository workerStore,
	artifacts artifactVerifier,
	validator *pintia.Validator,
	uuid uuidGenerator,
	config WorkerConfig,
) (*Worker, error) {
	if repository == nil || artifacts == nil || validator == nil || uuid == nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import worker", errors.New("repository, artifact verifier, validator, and UUID generator are required"))
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import worker", err)
	}
	if config.RetryDelay <= 0 {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import worker", errors.New("retry duration must be positive"))
	}
	if strings.TrimSpace(config.Analytics.AlgorithmVersion) == "" {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import worker", errors.New("analytics algorithm version is required"))
	}
	if !lowercaseSHA256Pattern.MatchString(config.Analytics.ConfigSHA256) {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import worker", errors.New("analytics config SHA-256 is invalid"))
	}
	return &Worker{
		repository: repository,
		artifacts:  artifacts,
		validator:  validator,
		uuid:       uuid,
		config:     config,
	}, nil
}

func (w *Worker) ClaimAndProcess(ctx context.Context, owner string) (*ImportOutcome, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, importError(ErrorInvalidConfiguration, false, "claim and process", errors.New("worker owner is required"))
	}
	claim, err := w.repository.Claim(ctx, owner, w.config.LeaseDuration)
	if err != nil || claim == nil {
		return nil, err
	}
	outcome, err := w.Process(ctx, *claim)
	return &outcome, err
}

func (w *Worker) Process(ctx context.Context, claim Claim) (ImportOutcome, error) {
	if ctx == nil {
		return ImportOutcome{}, importError(ErrorInvalidConfiguration, false, "process import", errors.New("context is required"))
	}
	renewer, err := workerlease.Start(ctx, w.config.LeaseDuration, func(renewalContext context.Context) error {
		return w.repository.RenewLease(renewalContext, claim, w.config.LeaseDuration)
	})
	if err != nil {
		return ImportOutcome{}, err
	}
	outcome, resultErr := w.processWithLease(renewer.Context(), claim)
	renewer.Stop()
	if renewalErr := renewer.Failure(); renewalErr != nil {
		if resultErr == nil && completedImportAttempt(outcome) {
			// A completed repository operation can win the row lock just before a
			// heartbeat. Its exact active-attempt fence proves that the terminal
			// transition owned the lease; the following heartbeat then observes
			// the intentionally cleared lease.
			if code, ok := CodeOf(renewalErr); ok && code == ErrorLeaseLost {
				return outcome, nil
			}
		}
		return ImportOutcome{}, renewalErr
	}
	return outcome, resultErr
}

func completedImportAttempt(outcome ImportOutcome) bool {
	switch outcome.Disposition {
	case ImportCreated, ImportDuplicate, ImportFailed, ImportRetry:
		return true
	case ImportAnalyzing, "":
		return false
	default:
		return false
	}
}

func (w *Worker) processWithLease(ctx context.Context, claim Claim) (ImportOutcome, error) {
	if claim.Stage == StageAnalyzing && claim.Status == JobRunning {
		return ImportOutcome{Disposition: ImportAnalyzing}, nil
	}
	if claim.Stage != StageValidating && claim.Stage != StageImporting {
		return ImportOutcome{}, importError(ErrorStateConflict, false, "process import", fmt.Errorf("unsupported claimed stage %q", claim.Stage))
	}

	metadata, err := w.repository.LoadArtifact(ctx, claim)
	if err != nil {
		if contextErr := importProcessingContextError(ctx, "load artifact"); contextErr != nil {
			return ImportOutcome{}, contextErr
		}
		return w.handleClaimFailure(ctx, claim, err)
	}
	verified, err := w.artifacts.Verify(ctx, metadata.Hash, metadata.Size)
	if err != nil {
		if contextErr := importProcessingContextError(ctx, "verify artifact"); contextErr != nil {
			return ImportOutcome{}, contextErr
		}
		if artifactVerificationIsPermanent(err) {
			return w.failPermanent(
				ctx,
				claim,
				importError(ErrorArtifactVerification, true, "verify artifact", err),
			)
		}
		return w.retry(ctx, claim, ErrorArtifactVerification, importError(ErrorArtifactVerification, false, "verify artifact", err))
	}
	if verified.Hash != metadata.Hash || verified.Size != metadata.Size || verified.StorageKey != metadata.StorageKey {
		mismatch := importError(ErrorArtifactMetadata, true, "verify artifact", errors.New("filesystem descriptor differs from database metadata"))
		return w.failPermanent(ctx, claim, mismatch)
	}

	file, err := os.Open(verified.Path)
	if err != nil {
		return w.retry(ctx, claim, ErrorArtifactVerification, importError(ErrorArtifactVerification, false, "open verified artifact", err))
	}
	snapshot, validationErr := w.validator.ValidateReader(ctx, file)
	closeErr := file.Close()
	if contextErr := importProcessingContextError(ctx, "validate Pintia snapshot"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	if validationErr != nil {
		var typedValidation *pintia.ValidationError
		if errors.As(validationErr, &typedValidation) {
			permanent := importError(ErrorValidation, true, "validate Pintia snapshot", validationErr)
			return w.failPermanent(ctx, claim, permanent)
		}
		return w.retry(ctx, claim, ErrorArtifactVerification, importError(ErrorArtifactVerification, false, "read verified artifact", validationErr))
	}
	if closeErr != nil {
		return w.retry(ctx, claim, ErrorArtifactVerification, importError(ErrorArtifactVerification, false, "close verified artifact", closeErr))
	}
	domainHash, err := pintia.DomainHash(ctx, snapshot)
	if contextErr := importProcessingContextError(ctx, "hash Pintia domain"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	if err != nil {
		return w.failPermanent(ctx, claim, importError(ErrorValidation, true, "hash Pintia domain", err))
	}

	activeClaim := claim
	if claim.Stage == StageValidating {
		activeClaim, err = w.repository.MarkImporting(ctx, claim, w.config.LeaseDuration)
		if err != nil {
			if contextErr := importProcessingContextError(ctx, "mark importing"); contextErr != nil {
				return ImportOutcome{}, contextErr
			}
			return w.handleClaimFailure(ctx, claim, err)
		}
	}
	logicalExamID, err := w.uuid()
	if contextErr := importProcessingContextError(ctx, "generate logical exam UUID"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	if err != nil {
		return w.retry(ctx, activeClaim, ErrorUUIDGeneration, err)
	}
	snapshotID, err := w.uuid()
	if contextErr := importProcessingContextError(ctx, "generate snapshot UUID"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	if err != nil {
		return w.retry(ctx, activeClaim, ErrorUUIDGeneration, err)
	}
	if contextErr := importProcessingContextError(ctx, "import snapshot"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	outcome, err := w.repository.ImportSnapshot(ctx, ImportRequest{
		Claim:      activeClaim,
		Snapshot:   snapshot,
		DomainHash: domainHash,
		PublicIDs:  PublicIDs{LogicalExam: logicalExamID, Snapshot: snapshotID},
		Analytics:  w.config.Analytics,
	})
	if err == nil {
		return outcome, nil
	}
	if contextErr := importProcessingContextError(ctx, "import snapshot"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	if IsPermanent(err) {
		return w.failPermanent(ctx, activeClaim, err)
	}
	if code, ok := CodeOf(err); ok && (code == ErrorLeaseLost || code == ErrorStateConflict) {
		return ImportOutcome{}, err
	}
	return w.retry(ctx, activeClaim, errorCodeOrDatabase(err), err)
}

func (w *Worker) handleClaimFailure(ctx context.Context, claim Claim, failure error) (ImportOutcome, error) {
	if contextErr := importProcessingContextError(ctx, "handle import failure"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	if IsPermanent(failure) {
		return w.failPermanent(ctx, claim, failure)
	}
	if code, ok := CodeOf(failure); ok && (code == ErrorLeaseLost || code == ErrorStateConflict) {
		return ImportOutcome{}, failure
	}
	return w.retry(ctx, claim, errorCodeOrDatabase(failure), failure)
}

func (w *Worker) failPermanent(ctx context.Context, claim Claim, failure error) (ImportOutcome, error) {
	if contextErr := importProcessingContextError(ctx, "fail import permanently"); contextErr != nil {
		return ImportOutcome{}, contextErr
	}
	code := errorCodeOrDatabase(failure)
	if err := w.repository.FailPermanent(ctx, claim, code, truncateErrorDetail(failure.Error())); err != nil {
		return ImportOutcome{}, errors.Join(failure, err)
	}
	return ImportOutcome{Disposition: ImportFailed, FailureCode: &code}, nil
}

func (w *Worker) retry(ctx context.Context, claim Claim, reason ErrorCode, failure error) (ImportOutcome, error) {
	if contextErr := importProcessingContextError(ctx, "retry import"); contextErr != nil {
		return ImportOutcome{}, errors.Join(failure, contextErr)
	}
	if err := w.repository.Requeue(ctx, claim, w.config.RetryDelay, reason); err != nil {
		return ImportOutcome{}, errors.Join(failure, err)
	}
	return ImportOutcome{Disposition: ImportRetry, FailureCode: &reason}, nil
}

func importProcessingContextError(ctx context.Context, operation string) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return nil
	}
	if _, ok := CodeOf(cause); ok {
		return cause
	}
	return importError(ErrorCanceled, false, operation, cause)
}

func errorCodeOrDatabase(err error) ErrorCode {
	if code, ok := CodeOf(err); ok {
		return code
	}
	return ErrorDatabase
}

func truncateErrorDetail(detail string) string {
	if len(detail) <= maxPersistedErrorDetailBytes {
		return detail
	}
	truncated := detail[:maxPersistedErrorDetailBytes]
	for !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func artifactVerificationIsPermanent(err error) bool {
	code, ok := artifact.CodeOf(err)
	if !ok {
		return false
	}
	switch code {
	case artifact.ErrorNotFound,
		artifact.ErrorCorrupt,
		artifact.ErrorInvalidHash,
		artifact.ErrorEmptyArtifact,
		artifact.ErrorInvalidArgument,
		artifact.ErrorInvalidConfiguration:
		return true
	case artifact.ErrorIO,
		artifact.ErrorCanceled,
		artifact.ErrorPayloadTooLarge,
		artifact.ErrorReferenceCheck:
		return false
	default:
		return false
	}
}
