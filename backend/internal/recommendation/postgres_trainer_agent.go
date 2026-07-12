package recommendation

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	trainerAgentIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	trainerAgentErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

const maximumTrainerAgentErrorDetailBytes = 2048

func (repository *PostgresRepository) ResolveTrainerAgentClaim(
	ctx context.Context,
	attempt TrainerAgentAttempt,
) (claim Claim, actorIDs []int64, resultErr error) {
	if err := validateTrainerAgentAttempt(attempt); err != nil {
		return Claim{}, nil, err
	}
	resultErr = repository.transaction(ctx, "resolve recommendation trainer-agent claim", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		var manifest, status string
		err := tx.QueryRow(ctx, `
SELECT run.training_run_id,
       run.public_id::text,
       run.source_analytics_generation_id,
	       run.source_analytics_head_revision,
	       run.training_configuration_version_id,
	       run.knowledge_catalog_version_id,
	       run.bundle_protocol,
       run.input_manifest::text,
       run.input_manifest_sha256,
       run.status,
       run.attempt_count,
       run.created_at,
       run.started_at,
       run.finished_at,
       artifact.sha256,
       artifact.size_bytes,
       artifact.storage_key,
       run.attempt_token::text,
       run.lease_owner,
       run.lease_expires_at
FROM ascendany.recommendation_training_runs AS run
JOIN ascendany.artifacts AS artifact
  ON artifact.artifact_id = run.input_bundle_artifact_id
WHERE run.public_id = $1::uuid
  AND run.status = 'running'
  AND run.attempt_token = $2::uuid
  AND run.lease_owner = $3
  AND run.lease_expires_at > clock_timestamp()`, attempt.RunID, attempt.AttemptToken, attempt.AgentID).Scan(
			&claim.DatabaseID,
			&claim.ID,
			&claim.SourceAnalyticsGenerationID,
			&claim.SourceAnalyticsHeadRevision,
			&claim.TrainingConfigurationVersionID,
			&claim.KnowledgeCatalogVersionID,
			&claim.BundleProtocol,
			&manifest,
			&claim.InputManifestSHA256,
			&status,
			&claim.AttemptCount,
			&claim.CreatedAt,
			&claim.StartedAt,
			&claim.FinishedAt,
			&claim.InputArtifact.Hash,
			&claim.InputArtifact.Size,
			&claim.InputArtifact.StorageKey,
			&claim.AttemptToken,
			&claim.LeaseOwner,
			&claim.LeaseExpiresAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorLeaseLost, false, "resolve recommendation trainer-agent claim", errors.New("training attempt is no longer active"))
		}
		if err != nil {
			return databaseError("resolve recommendation trainer-agent claim", err)
		}
		claim.Status = RunStatus(status)
		claim.CreatedAt = claim.CreatedAt.UTC()
		claim.LeaseExpiresAt = claim.LeaseExpiresAt.UTC()
		if claim.StartedAt != nil {
			value := claim.StartedAt.UTC()
			claim.StartedAt = &value
		}
		canonicalManifest, err := canonicalStoredObject(
			manifest, claim.InputManifestSHA256, maxManifestBytes, "resolve recommendation trainer-agent claim",
		)
		if err != nil {
			return err
		}
		claim.InputManifest = canonicalManifest
		if err := validateClaim(claim); err != nil {
			return err
		}
		actorIDs, err = loadSourceActorIDs(ctx, tx, claim.SourceAnalyticsGenerationID)
		return err
	})
	return claim, actorIDs, resultErr
}

func loadSourceActorIDs(ctx context.Context, tx recommendationTx, generationID int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `
SELECT actor_id
FROM ascendany.student_analytics
WHERE analytics_generation_id = $1
ORDER BY actor_id`, generationID)
	if err != nil {
		return nil, databaseError("load recommendation source actors", err)
	}
	defer rows.Close()
	actorIDs := make([]int64, 0)
	for rows.Next() {
		var actorID int64
		if err := rows.Scan(&actorID); err != nil {
			return nil, databaseError("scan recommendation source actor", err)
		}
		actorIDs = append(actorIDs, actorID)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate recommendation source actors", err)
	}
	if len(actorIDs) == 0 {
		return nil, domainError(ErrorStoredDataInvalid, true, "load recommendation source actors", errors.New("training source generation has no actors"))
	}
	return actorIDs, nil
}

func (repository *PostgresRepository) RenewTrainerAgentLease(
	ctx context.Context,
	attempt TrainerAgentAttempt,
	leaseDuration time.Duration,
) (expiresAt time.Time, resultErr error) {
	if err := validateTrainerAgentAttempt(attempt); err != nil {
		return time.Time{}, err
	}
	if leaseDuration <= 0 || leaseDuration%time.Millisecond != 0 {
		return time.Time{}, domainError(ErrorInvalidInput, true, "renew recommendation trainer-agent lease", errors.New("positive whole-millisecond lease duration is required"))
	}
	resultErr = repository.transaction(ctx, "renew recommendation trainer-agent lease", pgx.TxOptions{}, func(tx recommendationTx) error {
		var databaseID int64
		var attemptCount int
		err := tx.QueryRow(ctx, `
UPDATE ascendany.recommendation_training_runs
SET lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE public_id = $1::uuid
  AND status = 'running'
  AND attempt_token = $2::uuid
  AND lease_owner = $3
  AND lease_expires_at > clock_timestamp()
RETURNING training_run_id, attempt_count, lease_expires_at`,
			attempt.RunID, attempt.AttemptToken, attempt.AgentID, leaseDuration.Milliseconds(),
		).Scan(&databaseID, &attemptCount, &expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorLeaseLost, false, "renew recommendation trainer-agent lease", errors.New("training attempt is no longer active"))
		}
		if err != nil {
			return databaseError("renew recommendation trainer-agent lease", err)
		}
		expiresAt = expiresAt.UTC()
		return appendTrainingEvent(ctx, tx, databaseID, "lease_renewed", map[string]any{
			"attemptCount": attemptCount,
			"leaseOwner":   attempt.AgentID,
		})
	})
	return expiresAt, resultErr
}

func (repository *PostgresRepository) LookupTrainerAgentTerminalReceipt(
	ctx context.Context,
	attempt TrainerAgentAttempt,
	operation TrainerAgentTerminalOperation,
	requestSHA256 string,
) (receipt *TrainerAgentTerminalReceipt, resultErr error) {
	if err := validateTrainerAgentReceiptReference(attempt, operation, requestSHA256); err != nil {
		return nil, err
	}
	resultErr = repository.transaction(ctx, "read recommendation trainer-agent terminal receipt", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		var err error
		receipt, err = lookupTrainerAgentTerminalReceipt(ctx, tx, attempt, operation, requestSHA256)
		return err
	})
	return receipt, resultErr
}

func (repository *PostgresRepository) ReportTrainerAgentFailure(
	ctx context.Context,
	command TrainerAgentFailureCommand,
) (receipt TrainerAgentTerminalReceipt, resultErr error) {
	if err := validateTrainerAgentFailureCommand(command); err != nil {
		return TrainerAgentTerminalReceipt{}, err
	}
	attempt := trainerAgentAttemptFromClaim(command.Claim)
	resultErr = repository.transaction(ctx, "report recommendation trainer-agent failure", pgx.TxOptions{}, func(tx recommendationTx) error {
		replayed, err := lookupTrainerAgentTerminalReceipt(
			ctx, tx, attempt, TrainerAgentFailureOperation, command.RequestSHA256,
		)
		if err != nil {
			return err
		}
		if replayed != nil {
			receipt = *replayed
			return nil
		}
		result := TrainerAgentFailed
		query := `
UPDATE ascendany.recommendation_training_runs
SET status = 'failed',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $6,
    error_detail = $7,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE training_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`
		arguments := []any{
			command.Claim.DatabaseID, command.Claim.ID, command.Claim.AttemptCount,
			command.Claim.AttemptToken, command.Claim.LeaseOwner, command.Code, command.Detail,
		}
		eventType := "failed"
		eventPayload := map[string]any{"attemptCount": command.Claim.AttemptCount, "code": command.Code}
		if command.Retryable {
			result = TrainerAgentRequeued
			query = `
UPDATE ascendany.recommendation_training_runs
SET status = 'queued',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = clock_timestamp() + ($6::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE training_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`
			arguments = []any{
				command.Claim.DatabaseID, command.Claim.ID, command.Claim.AttemptCount,
				command.Claim.AttemptToken, command.Claim.LeaseOwner, command.RetryDelay.Milliseconds(),
			}
			eventType = "retry_scheduled"
			eventPayload = map[string]any{
				"attemptCount":      command.Claim.AttemptCount,
				"delayMilliseconds": command.RetryDelay.Milliseconds(),
				"reason":            command.Code,
			}
		}
		commandTag, err := tx.Exec(ctx, query, arguments...)
		if err != nil {
			return databaseError("report recommendation trainer-agent failure", err)
		}
		if commandTag.RowsAffected() != 1 {
			replayed, replayErr := lookupTrainerAgentTerminalReceipt(
				ctx, tx, attempt, TrainerAgentFailureOperation, command.RequestSHA256,
			)
			if replayErr != nil {
				return replayErr
			}
			if replayed != nil {
				receipt = *replayed
				return nil
			}
			return domainError(ErrorLeaseLost, false, "report recommendation trainer-agent failure", errors.New("training attempt is no longer active"))
		}
		if err := appendTrainingEvent(ctx, tx, command.Claim.DatabaseID, eventType, eventPayload); err != nil {
			return err
		}
		receipt = TrainerAgentTerminalReceipt{
			Operation: TrainerAgentFailureOperation, RequestSHA256: command.RequestSHA256, Result: result,
		}
		return insertTrainerAgentTerminalReceipt(ctx, tx, command.Claim.DatabaseID, attempt, receipt)
	})
	return receipt, resultErr
}

func (repository *PostgresRepository) RejectTrainerAgentOutput(
	ctx context.Context,
	command TrainerAgentOutputRejectionCommand,
) (receipt TrainerAgentTerminalReceipt, resultErr error) {
	if err := validateTrainerAgentOutputRejectionCommand(command); err != nil {
		return TrainerAgentTerminalReceipt{}, err
	}
	attempt := trainerAgentAttemptFromClaim(command.Claim)
	resultErr = repository.transaction(ctx, "reject recommendation trainer-agent output", pgx.TxOptions{}, func(tx recommendationTx) error {
		replayed, err := lookupTrainerAgentTerminalReceipt(
			ctx, tx, attempt, TrainerAgentOutputOperation, command.RequestSHA256,
		)
		if err != nil {
			return err
		}
		if replayed != nil {
			receipt = *replayed
			return nil
		}
		commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.recommendation_training_runs
SET status = 'failed',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $6,
    error_detail = $7,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE training_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, command.Claim.DatabaseID, command.Claim.ID, command.Claim.AttemptCount,
			command.Claim.AttemptToken, command.Claim.LeaseOwner, command.FailureCode, command.FailureDetail)
		if err != nil {
			return databaseError("reject recommendation trainer-agent output", err)
		}
		if commandTag.RowsAffected() != 1 {
			replayed, replayErr := lookupTrainerAgentTerminalReceipt(
				ctx, tx, attempt, TrainerAgentOutputOperation, command.RequestSHA256,
			)
			if replayErr != nil {
				return replayErr
			}
			if replayed != nil {
				receipt = *replayed
				return nil
			}
			return domainError(ErrorLeaseLost, false, "reject recommendation trainer-agent output", errors.New("training attempt is no longer active"))
		}
		if err := appendTrainingEvent(ctx, tx, command.Claim.DatabaseID, "failed", map[string]any{
			"attemptCount": command.Claim.AttemptCount,
			"code":         command.FailureCode,
		}); err != nil {
			return err
		}
		retryable := false
		errorCode, errorDetail := command.ErrorCode, command.ErrorDetail
		receipt = TrainerAgentTerminalReceipt{
			Operation: TrainerAgentOutputOperation, RequestSHA256: command.RequestSHA256,
			Result: TrainerAgentOutputRejected, ErrorCode: &errorCode, ErrorDetail: &errorDetail,
			ErrorRetryable: &retryable,
		}
		return insertTrainerAgentTerminalReceipt(ctx, tx, command.Claim.DatabaseID, attempt, receipt)
	})
	return receipt, resultErr
}

func lookupTrainerAgentTerminalReceipt(
	ctx context.Context,
	tx recommendationTx,
	attempt TrainerAgentAttempt,
	operation TrainerAgentTerminalOperation,
	requestSHA256 string,
) (*TrainerAgentTerminalReceipt, error) {
	var storedOperation, storedRequestSHA256, storedResult string
	var modelID, runtimeConstructionSHA256, runtimeProvenanceSHA256, runtimeTreeSHA256 *string
	var hostCapabilitySHA256, runtimeAttestationSHA256, errorCode, errorDetail *string
	var errorRetryable *bool
	err := tx.QueryRow(ctx, `
SELECT receipt.operation,
       receipt.request_sha256,
       receipt.result,
       receipt.model_public_id::text,
	   receipt.runtime_construction_sha256,
	   receipt.runtime_provenance_sha256,
	   receipt.runtime_tree_sha256,
	   receipt.host_capability_sha256,
	   receipt.runtime_attestation_sha256,
       receipt.error_code,
       receipt.error_detail,
       receipt.error_retryable
FROM ascendany.recommendation_trainer_attempt_receipts AS receipt
JOIN ascendany.recommendation_training_runs AS run
  ON run.training_run_id = receipt.training_run_id
WHERE run.public_id = $1::uuid
  AND receipt.attempt_token = $2::uuid
  AND receipt.agent_id = $3`, attempt.RunID, attempt.AttemptToken, attempt.AgentID).Scan(
		&storedOperation, &storedRequestSHA256, &storedResult, &modelID,
		&runtimeConstructionSHA256, &runtimeProvenanceSHA256, &runtimeTreeSHA256,
		&hostCapabilitySHA256, &runtimeAttestationSHA256,
		&errorCode, &errorDetail, &errorRetryable,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, databaseError("read recommendation trainer-agent terminal receipt", err)
	}
	if storedOperation != string(operation) || storedRequestSHA256 != requestSHA256 {
		return nil, domainError(ErrorLeaseLost, false, "replay recommendation trainer-agent terminal request", errors.New("attempt already has a different terminal request"))
	}
	receipt := &TrainerAgentTerminalReceipt{
		Operation: operation, RequestSHA256: storedRequestSHA256, Result: TrainerAgentTerminalResult(storedResult),
		ModelID: modelID, RuntimeConstructionSHA256: runtimeConstructionSHA256,
		RuntimeProvenanceSHA256: runtimeProvenanceSHA256, RuntimeTreeSHA256: runtimeTreeSHA256,
		HostCapabilitySHA256: hostCapabilitySHA256, RuntimeAttestationSHA256: runtimeAttestationSHA256,
		ErrorCode: errorCode, ErrorDetail: errorDetail, ErrorRetryable: errorRetryable,
	}
	if err := validateTrainerAgentTerminalReceipt(*receipt); err != nil {
		return nil, domainError(ErrorStoredDataInvalid, true, "read recommendation trainer-agent terminal receipt", err)
	}
	return receipt, nil
}

func insertTrainerAgentTerminalReceipt(
	ctx context.Context,
	tx recommendationTx,
	trainingRunID int64,
	attempt TrainerAgentAttempt,
	receipt TrainerAgentTerminalReceipt,
) error {
	if trainingRunID <= 0 {
		return domainError(ErrorInvalidInput, true, "insert recommendation trainer-agent terminal receipt", errors.New("training run database ID is required"))
	}
	if err := validateTrainerAgentTerminalReceipt(receipt); err != nil {
		return domainError(ErrorInvalidInput, true, "insert recommendation trainer-agent terminal receipt", err)
	}
	_, err := tx.Exec(ctx, `
INSERT INTO ascendany.recommendation_trainer_attempt_receipts (
    training_run_id,
    attempt_token,
    agent_id,
    operation,
    request_sha256,
    result,
    model_public_id,
	runtime_construction_sha256,
	runtime_provenance_sha256,
	runtime_tree_sha256,
	host_capability_sha256,
	runtime_attestation_sha256,
    error_code,
    error_detail,
    error_retryable
)
VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, $9, $10, $11, $12, $13, $14, $15)`,
		trainingRunID, attempt.AttemptToken, attempt.AgentID, receipt.Operation, receipt.RequestSHA256,
		receipt.Result, receipt.ModelID, receipt.RuntimeConstructionSHA256, receipt.RuntimeProvenanceSHA256,
		receipt.RuntimeTreeSHA256, receipt.HostCapabilitySHA256, receipt.RuntimeAttestationSHA256,
		receipt.ErrorCode, receipt.ErrorDetail, receipt.ErrorRetryable,
	)
	if err != nil {
		return databaseError("insert recommendation trainer-agent terminal receipt", err)
	}
	return nil
}

func trainerAgentAttemptFromClaim(claim Claim) TrainerAgentAttempt {
	return TrainerAgentAttempt{RunID: claim.ID, AgentID: claim.LeaseOwner, AttemptToken: claim.AttemptToken}
}

func validateTrainerAgentAttempt(attempt TrainerAgentAttempt) error {
	if !canonicalUUIDv4Pattern.MatchString(attempt.RunID) || !canonicalUUIDv4Pattern.MatchString(attempt.AttemptToken) ||
		!trainerAgentIDPattern.MatchString(attempt.AgentID) {
		return domainError(ErrorInvalidInput, true, "validate recommendation trainer-agent attempt", errors.New("canonical run, agent, and attempt IDs are required"))
	}
	return nil
}

func validateTrainerAgentReceiptReference(
	attempt TrainerAgentAttempt,
	operation TrainerAgentTerminalOperation,
	requestSHA256 string,
) error {
	if err := validateTrainerAgentAttempt(attempt); err != nil {
		return err
	}
	if operation != TrainerAgentOutputOperation && operation != TrainerAgentFailureOperation ||
		!lowercaseSHA256Pattern.MatchString(requestSHA256) {
		return domainError(ErrorInvalidInput, true, "validate recommendation trainer-agent receipt reference", errors.New("terminal operation and request digest are invalid"))
	}
	return nil
}

func validateTrainerAgentFailureCommand(command TrainerAgentFailureCommand) error {
	if err := validateClaim(command.Claim); err != nil {
		return err
	}
	if err := validateTrainerAgentReceiptReference(
		trainerAgentAttemptFromClaim(command.Claim), TrainerAgentFailureOperation, command.RequestSHA256,
	); err != nil {
		return err
	}
	if !failureCodePattern.MatchString(command.Code) || command.Detail == "" || strings.TrimSpace(command.Detail) != command.Detail ||
		len(command.Detail) > maximumFailureDetailBytes || !utf8.ValidString(command.Detail) || strings.ContainsRune(command.Detail, 0) {
		return domainError(ErrorInvalidInput, true, "validate recommendation trainer-agent failure", errors.New("canonical failure code and bounded detail are required"))
	}
	if command.Retryable && command.RetryDelay < time.Second {
		return domainError(ErrorInvalidInput, true, "validate recommendation trainer-agent failure", errors.New("retryable failure requires at least one second of retry delay"))
	}
	return nil
}

func validateTrainerAgentOutputRejectionCommand(command TrainerAgentOutputRejectionCommand) error {
	if err := validateClaim(command.Claim); err != nil {
		return err
	}
	if err := validateTrainerAgentReceiptReference(
		trainerAgentAttemptFromClaim(command.Claim), TrainerAgentOutputOperation, command.RequestSHA256,
	); err != nil {
		return err
	}
	if !failureCodePattern.MatchString(command.FailureCode) || command.FailureDetail == "" ||
		strings.TrimSpace(command.FailureDetail) != command.FailureDetail || len(command.FailureDetail) > maximumFailureDetailBytes ||
		!utf8.ValidString(command.FailureDetail) || strings.ContainsRune(command.FailureDetail, 0) ||
		!trainerAgentErrorCodePattern.MatchString(command.ErrorCode) || command.ErrorDetail == "" ||
		strings.TrimSpace(command.ErrorDetail) != command.ErrorDetail || len(command.ErrorDetail) > maximumTrainerAgentErrorDetailBytes ||
		!utf8.ValidString(command.ErrorDetail) || strings.ContainsRune(command.ErrorDetail, 0) {
		return domainError(ErrorInvalidInput, true, "validate recommendation trainer-agent output rejection", errors.New("output rejection fields are invalid"))
	}
	return nil
}

func validateTrainerAgentTerminalReceipt(receipt TrainerAgentTerminalReceipt) error {
	if !lowercaseSHA256Pattern.MatchString(receipt.RequestSHA256) {
		return errors.New("terminal receipt request digest is invalid")
	}
	switch receipt.Result {
	case TrainerAgentActivated, TrainerAgentSuperseded:
		if receipt.Operation != TrainerAgentOutputOperation || receipt.ModelID == nil ||
			!canonicalUUIDv4Pattern.MatchString(*receipt.ModelID) || receipt.ErrorCode != nil ||
			receipt.ErrorDetail != nil || receipt.ErrorRetryable != nil ||
			receipt.RuntimeConstructionSHA256 == nil || !lowercaseSHA256Pattern.MatchString(*receipt.RuntimeConstructionSHA256) ||
			receipt.RuntimeProvenanceSHA256 == nil || !lowercaseSHA256Pattern.MatchString(*receipt.RuntimeProvenanceSHA256) ||
			receipt.RuntimeTreeSHA256 == nil || !lowercaseSHA256Pattern.MatchString(*receipt.RuntimeTreeSHA256) ||
			receipt.HostCapabilitySHA256 == nil || !lowercaseSHA256Pattern.MatchString(*receipt.HostCapabilitySHA256) ||
			receipt.RuntimeAttestationSHA256 == nil || !lowercaseSHA256Pattern.MatchString(*receipt.RuntimeAttestationSHA256) {
			return errors.New("output publication receipt shape is invalid")
		}
	case TrainerAgentFailed, TrainerAgentRequeued:
		if receipt.Operation != TrainerAgentFailureOperation || receipt.ModelID != nil || receipt.ErrorCode != nil ||
			receipt.ErrorDetail != nil || receipt.ErrorRetryable != nil || receiptHasRuntimeIdentity(receipt) {
			return errors.New("failure receipt shape is invalid")
		}
	case TrainerAgentOutputRejected:
		if receipt.Operation != TrainerAgentOutputOperation || receipt.ModelID != nil || receipt.ErrorCode == nil ||
			receipt.ErrorDetail == nil || receipt.ErrorRetryable == nil || *receipt.ErrorRetryable ||
			receiptHasRuntimeIdentity(receipt) ||
			!trainerAgentErrorCodePattern.MatchString(*receipt.ErrorCode) ||
			*receipt.ErrorDetail == "" || strings.TrimSpace(*receipt.ErrorDetail) != *receipt.ErrorDetail ||
			len(*receipt.ErrorDetail) > maximumTrainerAgentErrorDetailBytes || !utf8.ValidString(*receipt.ErrorDetail) ||
			strings.ContainsRune(*receipt.ErrorDetail, 0) {
			return errors.New("output rejection receipt shape is invalid")
		}
	default:
		return errors.New("terminal receipt result is invalid")
	}
	return nil
}

func receiptHasRuntimeIdentity(receipt TrainerAgentTerminalReceipt) bool {
	return receipt.RuntimeConstructionSHA256 != nil || receipt.RuntimeProvenanceSHA256 != nil ||
		receipt.RuntimeTreeSHA256 != nil || receipt.HostCapabilitySHA256 != nil ||
		receipt.RuntimeAttestationSHA256 != nil
}
