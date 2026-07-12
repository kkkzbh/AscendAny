package importing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) RenewLease(
	ctx context.Context,
	claim Claim,
	leaseDuration time.Duration,
) (resultErr error) {
	leaseMilliseconds, err := requirePositiveDuration(leaseDuration, "renew import lease")
	if err != nil {
		return err
	}
	attempt, err := requireClaimAttempt(claim, "renew import lease")
	if err != nil {
		return err
	}
	return r.transaction(ctx, "renew import lease", func(tx dbTx) error {
		var renewedUntil time.Time
		err := tx.QueryRow(ctx, `
UPDATE ascendany.import_jobs
SET lease_expires_at = clock_timestamp() + ($5::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND lease_owner = $3
  AND attempt_count = $4
  AND lease_expires_at > clock_timestamp()
RETURNING lease_expires_at`, claim.ID, claim.PublicID, attempt.owner, attempt.count, leaseMilliseconds).Scan(&renewedUntil)
		if errors.Is(err, pgx.ErrNoRows) {
			return importError(ErrorLeaseLost, false, "renew import lease", errors.New("claim attempt is no longer active"))
		}
		if err != nil {
			return databaseError("renew import lease", err)
		}
		return nil
	})
}

func (r *PostgresRepository) LoadArtifact(ctx context.Context, claim Claim) (metadata ArtifactMetadata, resultErr error) {
	attempt, err := requireClaimAttempt(claim, "load claimed artifact")
	if err != nil {
		return ArtifactMetadata{}, err
	}
	resultErr = r.transaction(ctx, "load claimed artifact", func(tx dbTx) error {
		err := tx.QueryRow(ctx, `
SELECT artifact.artifact_id,
       artifact.sha256,
       artifact.size_bytes,
       artifact.media_type,
       artifact.storage_key
FROM ascendany.import_jobs AS job
JOIN ascendany.artifacts AS artifact ON artifact.artifact_id = job.artifact_id
WHERE job.import_job_id = $1
  AND job.public_id = $2::uuid
  AND job.status = 'running'
  AND job.lease_owner = $3
  AND job.attempt_count = $4
  AND job.lease_expires_at > clock_timestamp()
FOR SHARE OF job`, claim.ID, claim.PublicID, attempt.owner, attempt.count).Scan(
			&metadata.ID,
			&metadata.Hash,
			&metadata.Size,
			&metadata.MediaType,
			&metadata.StorageKey,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return importError(ErrorLeaseLost, false, "load claimed artifact", errors.New("claim is no longer active"))
		}
		if err != nil {
			return databaseError("load claimed artifact", err)
		}
		if metadata.ID != claim.ArtifactID || metadata.MediaType != PintiaSnapshotV2MediaType {
			return importError(ErrorArtifactMetadata, true, "load claimed artifact", errors.New("claimed artifact metadata is inconsistent"))
		}
		return nil
	})
	return metadata, resultErr
}

func (r *PostgresRepository) MarkImporting(
	ctx context.Context,
	claim Claim,
	leaseDuration time.Duration,
) (updated Claim, resultErr error) {
	leaseMilliseconds, err := requirePositiveDuration(leaseDuration, "mark job importing")
	if err != nil {
		return Claim{}, err
	}
	attempt, err := requireClaimAttempt(claim, "mark job importing")
	if err != nil {
		return Claim{}, err
	}
	resultErr = r.transaction(ctx, "mark job importing", func(tx dbTx) error {
		job := Job{}
		err := scanJob(tx.QueryRow(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'importing',
    lease_expires_at = clock_timestamp() + ($5::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND stage = 'validating'
  AND lease_owner = $3
  AND attempt_count = $4
  AND lease_expires_at > clock_timestamp()
RETURNING `+jobReturningColumns, claim.ID, claim.PublicID, attempt.owner, attempt.count, leaseMilliseconds), &job)
		if errors.Is(err, pgx.ErrNoRows) {
			return importError(ErrorLeaseLost, false, "mark job importing", errors.New("validating claim is no longer active"))
		}
		if err != nil {
			return databaseError("mark job importing", err)
		}
		if _, err := appendEvent(ctx, tx, job.ID, "validation_completed", struct {
			Stage JobStage `json:"stage"`
		}{StageImporting}); err != nil {
			return err
		}
		updated = Claim{Job: job, Reclaimed: claim.Reclaimed}
		return nil
	})
	return updated, resultErr
}

func (r *PostgresRepository) Requeue(
	ctx context.Context,
	claim Claim,
	retryDelay time.Duration,
	reason ErrorCode,
) (resultErr error) {
	retryMilliseconds, err := requirePositiveDuration(retryDelay, "requeue job")
	if err != nil {
		return err
	}
	attempt, err := requireClaimAttempt(claim, "requeue job")
	if err != nil {
		return err
	}
	return r.transaction(ctx, "requeue job", func(tx dbTx) error {
		var jobID int64
		err := tx.QueryRow(ctx, `
UPDATE ascendany.import_jobs
SET status = 'queued',
    stage = 'received',
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = clock_timestamp() + ($5::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND stage IN ('validating', 'importing')
  AND lease_owner = $3
  AND attempt_count = $4
  AND lease_expires_at > clock_timestamp()
RETURNING import_job_id`, claim.ID, claim.PublicID, attempt.owner, attempt.count, retryMilliseconds).Scan(&jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			return importError(ErrorLeaseLost, false, "requeue job", errors.New("claim attempt changed"))
		}
		if err != nil {
			return databaseError("requeue job", err)
		}
		_, err = appendEvent(ctx, tx, jobID, "retry_scheduled", struct {
			DelayMilliseconds int64     `json:"delayMilliseconds"`
			Reason            ErrorCode `json:"reason"`
		}{retryMilliseconds, reason})
		return err
	})
}

func (r *PostgresRepository) FailPermanent(
	ctx context.Context,
	claim Claim,
	code ErrorCode,
	detail string,
) error {
	attempt, err := requireClaimAttempt(claim, "fail job permanently")
	if err != nil {
		return err
	}
	return r.transaction(ctx, "fail job permanently", func(tx dbTx) error {
		var jobID int64
		err := tx.QueryRow(ctx, `
UPDATE ascendany.import_jobs
SET status = 'failed',
    stage = 'failed',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $5,
    error_detail = $6,
    error_permanent = true,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND lease_owner = $3
  AND attempt_count = $4
  AND lease_expires_at > clock_timestamp()
RETURNING import_job_id`, claim.ID, claim.PublicID, attempt.owner, attempt.count, code, detail).Scan(&jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			return importError(ErrorLeaseLost, false, "fail job permanently", errors.New("claim attempt changed"))
		}
		if err != nil {
			return databaseError("fail job permanently", err)
		}
		_, err = appendEvent(ctx, tx, jobID, "failed", struct {
			Code      ErrorCode `json:"code"`
			Permanent bool      `json:"permanent"`
		}{code, true})
		return err
	})
}
