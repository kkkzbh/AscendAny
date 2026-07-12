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

var failureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

const maximumFailureDetailBytes = 4096

func (repository *PostgresRepository) ClaimTraining(
	ctx context.Context,
	owner string,
	attemptToken string,
	leaseDuration time.Duration,
) (claim *Claim, resultErr error) {
	leaseMilliseconds, err := validateLeaseArguments(owner, attemptToken, leaseDuration)
	if err != nil {
		return nil, err
	}
	resultErr = repository.transaction(ctx, "claim recommendation training", pgx.TxOptions{}, func(tx recommendationTx) error {
		candidate := Claim{}
		var manifest, status, previousStatus string
		err := tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT training_run_id, status
    FROM ascendany.recommendation_training_runs
    WHERE (
        status = 'queued'
        AND next_attempt_at <= clock_timestamp()
    ) OR (
        status = 'running'
        AND lease_expires_at <= clock_timestamp()
    )
    ORDER BY
        CASE WHEN status = 'queued' THEN next_attempt_at ELSE lease_expires_at END,
        training_run_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), claimed AS (
    UPDATE ascendany.recommendation_training_runs AS run
    SET status = 'running',
        attempt_count = run.attempt_count + 1,
        attempt_token = $2::uuid,
        lease_owner = $1,
        lease_expires_at = clock_timestamp() + ($3::bigint * interval '1 millisecond'),
        started_at = COALESCE(run.started_at, clock_timestamp()),
        updated_at = clock_timestamp()
    FROM candidate
    WHERE run.training_run_id = candidate.training_run_id
      AND NOT EXISTS (
          SELECT 1
          FROM ascendany.recommendation_trainer_attempt_receipts AS receipt
          WHERE receipt.training_run_id = candidate.training_run_id
            AND receipt.attempt_token = $2::uuid
      )
    RETURNING run.*
)
SELECT claimed.training_run_id,
       claimed.public_id::text,
       claimed.source_analytics_generation_id,
       claimed.source_analytics_head_revision,
       claimed.training_configuration_version_id,
       claimed.knowledge_catalog_version_id,
       claimed.bundle_protocol,
       claimed.input_manifest::text,
       claimed.input_manifest_sha256,
       claimed.status,
       claimed.attempt_count,
       claimed.created_at,
       claimed.started_at,
       claimed.finished_at,
       artifact.sha256,
       artifact.size_bytes,
       artifact.storage_key,
       claimed.attempt_token::text,
       claimed.lease_owner,
       claimed.lease_expires_at,
       candidate.status
FROM claimed
JOIN candidate ON candidate.training_run_id = claimed.training_run_id
JOIN ascendany.artifacts AS artifact
  ON artifact.artifact_id = claimed.input_bundle_artifact_id`, owner, attemptToken, leaseMilliseconds).Scan(
			&candidate.DatabaseID,
			&candidate.ID,
			&candidate.SourceAnalyticsGenerationID,
			&candidate.SourceAnalyticsHeadRevision,
			&candidate.TrainingConfigurationVersionID,
			&candidate.KnowledgeCatalogVersionID,
			&candidate.BundleProtocol,
			&manifest,
			&candidate.InputManifestSHA256,
			&status,
			&candidate.AttemptCount,
			&candidate.CreatedAt,
			&candidate.StartedAt,
			&candidate.FinishedAt,
			&candidate.InputArtifact.Hash,
			&candidate.InputArtifact.Size,
			&candidate.InputArtifact.StorageKey,
			&candidate.AttemptToken,
			&candidate.LeaseOwner,
			&candidate.LeaseExpiresAt,
			&previousStatus,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			claim = nil
			return nil
		}
		if err != nil {
			return databaseError("claim recommendation training", err)
		}
		canonicalManifest, err := canonicalStoredObject(manifest, candidate.InputManifestSHA256, maxManifestBytes, "scan claimed recommendation training run")
		if err != nil {
			return err
		}
		candidate.InputManifest = canonicalManifest
		candidate.Status = RunStatus(status)
		candidate.CreatedAt = candidate.CreatedAt.UTC()
		candidate.LeaseExpiresAt = candidate.LeaseExpiresAt.UTC()
		if candidate.StartedAt != nil {
			value := candidate.StartedAt.UTC()
			candidate.StartedAt = &value
		}
		candidate.Reclaimed = previousStatus == string(RunRunning)
		eventType := "claimed"
		if candidate.Reclaimed {
			eventType = "reclaimed"
		}
		if err := appendTrainingEvent(ctx, tx, candidate.DatabaseID, eventType, map[string]any{
			"attemptCount": candidate.AttemptCount,
			"leaseOwner":   candidate.LeaseOwner,
		}); err != nil {
			return err
		}
		claim = &candidate
		return nil
	})
	return claim, resultErr
}

func (repository *PostgresRepository) RenewTrainingLease(ctx context.Context, claim Claim, leaseDuration time.Duration) error {
	if err := validateClaim(claim); err != nil {
		return err
	}
	if leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return domainError(ErrorInvalidInput, true, "renew recommendation training lease", errors.New("positive lease duration is required"))
	}
	return repository.transaction(ctx, "renew recommendation training lease", pgx.TxOptions{}, func(tx recommendationTx) error {
		commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.recommendation_training_runs
SET lease_expires_at = clock_timestamp() + ($6::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE training_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`,
			claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner, leaseDuration.Milliseconds())
		if err != nil {
			return databaseError("renew recommendation training lease", err)
		}
		if commandTag.RowsAffected() != 1 {
			return domainError(ErrorLeaseLost, false, "renew recommendation training lease", errors.New("training attempt is no longer active"))
		}
		return appendTrainingEvent(ctx, tx, claim.DatabaseID, "lease_renewed", map[string]any{
			"attemptCount": claim.AttemptCount,
			"leaseOwner":   claim.LeaseOwner,
		})
	})
}

func (repository *PostgresRepository) RequeueTraining(
	ctx context.Context,
	claim Claim,
	retryDelay time.Duration,
	reason string,
) error {
	if err := validateClaim(claim); err != nil {
		return err
	}
	if retryDelay < time.Second || !failureCodePattern.MatchString(reason) {
		return domainError(ErrorInvalidInput, true, "requeue recommendation training", errors.New("retry delay and canonical reason are required"))
	}
	return repository.transitionTraining(ctx, claim, "requeue recommendation training", `
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
  AND lease_expires_at > clock_timestamp()`, []any{retryDelay.Milliseconds()}, "retry_scheduled", map[string]any{
		"attemptCount":      claim.AttemptCount,
		"delayMilliseconds": retryDelay.Milliseconds(),
		"reason":            reason,
	})
}

func (repository *PostgresRepository) FailTraining(ctx context.Context, claim Claim, code, detail string) error {
	if err := validateClaim(claim); err != nil {
		return err
	}
	if !failureCodePattern.MatchString(code) || detail == "" || strings.TrimSpace(detail) != detail ||
		len(detail) > maximumFailureDetailBytes || !utf8.ValidString(detail) {
		return domainError(ErrorInvalidInput, true, "fail recommendation training", errors.New("canonical failure code and bounded UTF-8 detail are required"))
	}
	return repository.transitionTraining(ctx, claim, "fail recommendation training", `
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
  AND lease_expires_at > clock_timestamp()`, []any{code, detail}, "failed", map[string]any{
		"attemptCount": claim.AttemptCount,
		"code":         code,
	})
}

func (repository *PostgresRepository) transitionTraining(
	ctx context.Context,
	claim Claim,
	operation string,
	query string,
	extraArguments []any,
	eventType string,
	payload map[string]any,
) error {
	return repository.transaction(ctx, operation, pgx.TxOptions{}, func(tx recommendationTx) error {
		arguments := []any{claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner}
		arguments = append(arguments, extraArguments...)
		commandTag, err := tx.Exec(ctx, query, arguments...)
		if err != nil {
			return databaseError(operation, err)
		}
		if commandTag.RowsAffected() != 1 {
			return domainError(ErrorLeaseLost, false, operation, errors.New("training attempt is no longer active"))
		}
		return appendTrainingEvent(ctx, tx, claim.DatabaseID, eventType, payload)
	})
}

func validateLeaseArguments(owner, attemptToken string, leaseDuration time.Duration) (int64, error) {
	if owner == "" || strings.TrimSpace(owner) != owner || len(owner) > 128 ||
		!canonicalUUIDv4Pattern.MatchString(attemptToken) || leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return 0, domainError(ErrorInvalidInput, true, "claim recommendation training", errors.New("bounded owner, canonical attempt token, and positive lease are required"))
	}
	return leaseDuration.Milliseconds(), nil
}

func validateClaim(claim Claim) error {
	if claim.DatabaseID <= 0 || !canonicalUUIDv4Pattern.MatchString(claim.ID) || claim.Status != RunRunning ||
		claim.AttemptCount <= 0 || !canonicalUUIDv4Pattern.MatchString(claim.AttemptToken) ||
		claim.LeaseOwner == "" || strings.TrimSpace(claim.LeaseOwner) != claim.LeaseOwner {
		return domainError(ErrorInvalidInput, true, "validate recommendation training claim", errors.New("canonical active training claim is required"))
	}
	return nil
}
