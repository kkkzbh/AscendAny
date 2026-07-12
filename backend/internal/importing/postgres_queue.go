package importing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

func (r *PostgresRepository) QueueArtifact(
	ctx context.Context,
	published artifact.Artifact,
	mediaType string,
	jobPublicID string,
) (result QueueResult, resultErr error) {
	resultErr = r.transaction(ctx, "queue artifact", func(tx dbTx) error {
		var artifactID int64
		err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (sha256) DO NOTHING
RETURNING artifact_id`, published.Hash, published.Size, mediaType, published.StorageKey).Scan(&artifactID)
		if errors.Is(err, pgx.ErrNoRows) {
			var existingSize int64
			var existingMediaType string
			var existingStorageKey string
			if err := tx.QueryRow(ctx, `
SELECT artifact_id, size_bytes, media_type, storage_key
FROM ascendany.artifacts
WHERE sha256 = $1`, published.Hash).Scan(
				&artifactID,
				&existingSize,
				&existingMediaType,
				&existingStorageKey,
			); err != nil {
				return databaseError("load existing artifact", err)
			}
			if existingSize != published.Size || existingMediaType != mediaType || existingStorageKey != published.StorageKey {
				return importError(
					ErrorArtifactMetadata,
					true,
					"queue artifact",
					fmt.Errorf("existing artifact metadata differs for SHA-256 %s", published.Hash),
				)
			}
		} else if err != nil {
			return databaseError("insert artifact", err)
		}

		job := Job{}
		inserted := true
		row := tx.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (
    public_id,
    artifact_id,
    job_kind,
    status,
    stage
)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
ON CONFLICT (artifact_id, job_kind) DO NOTHING
RETURNING `+jobReturningColumns, jobPublicID, artifactID)
		if err := scanJob(row, &job); errors.Is(err, pgx.ErrNoRows) {
			inserted = false
			if err := scanJob(tx.QueryRow(ctx, `
SELECT `+jobReturningColumns+`
FROM ascendany.import_jobs
WHERE artifact_id = $1
  AND job_kind = 'pintia_snapshot_v2'`, artifactID), &job); err != nil {
				return databaseError("load idempotent import job", err)
			}
		} else if err != nil {
			return databaseError("insert import job", err)
		}

		if inserted {
			if _, err := appendEvent(ctx, tx, job.ID, "received", struct {
				ArtifactSHA256 string `json:"artifactSha256"`
				SizeBytes      int64  `json:"sizeBytes"`
			}{published.Hash, published.Size}); err != nil {
				return err
			}
		}
		result = QueueResult{Job: job, Created: inserted}
		return nil
	})
	return result, resultErr
}

func (r *PostgresRepository) Claim(ctx context.Context, owner string, leaseDuration time.Duration) (claim *Claim, resultErr error) {
	leaseMilliseconds, err := requirePositiveDuration(leaseDuration, "claim job")
	if err != nil {
		return nil, err
	}
	resultErr = r.transaction(ctx, "claim job", func(tx dbTx) error {
		job := Job{}
		var previousStatus JobStatus
		row := tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT import_job_id, status, stage
    FROM ascendany.import_jobs
    WHERE (
        status = 'queued'
        AND next_attempt_at <= clock_timestamp()
    ) OR (
        status = 'running'
        AND lease_expires_at IS NOT NULL
        AND lease_expires_at <= clock_timestamp()
    )
    ORDER BY
        CASE WHEN status = 'queued' THEN next_attempt_at ELSE lease_expires_at END,
        import_job_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE ascendany.import_jobs AS job
SET status = 'running',
    stage = CASE WHEN candidate.status = 'queued' THEN 'validating' ELSE candidate.stage END,
    attempt_count = job.attempt_count + 1,
    lease_owner = $1,
    lease_expires_at = clock_timestamp() + ($2::bigint * interval '1 millisecond'),
    started_at = COALESCE(job.started_at, clock_timestamp()),
    updated_at = clock_timestamp()
FROM candidate
WHERE job.import_job_id = candidate.import_job_id
RETURNING `+qualifiedJobReturningColumns+`, candidate.status`, owner, leaseMilliseconds)
		if err := scanJob(row, &job, &previousStatus); errors.Is(err, pgx.ErrNoRows) {
			claim = nil
			return nil
		} else if err != nil {
			return databaseError("claim import job", err)
		}
		reclaimed := previousStatus == JobRunning
		eventType := "claimed"
		if reclaimed {
			eventType = "reclaimed"
		}
		if _, err := appendEvent(ctx, tx, job.ID, eventType, struct {
			AttemptCount int32    `json:"attemptCount"`
			LeaseOwner   string   `json:"leaseOwner"`
			Stage        JobStage `json:"stage"`
		}{job.AttemptCount, owner, job.Stage}); err != nil {
			return err
		}
		claim = &Claim{Job: job, Reclaimed: reclaimed}
		return nil
	})
	return claim, resultErr
}
