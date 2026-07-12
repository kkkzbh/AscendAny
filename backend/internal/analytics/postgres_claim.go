package analytics

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (repository *PostgresRepository) Claim(
	ctx context.Context,
	owner string,
	leaseDuration time.Duration,
) (claim *Claim, resultErr error) {
	leaseMilliseconds, err := durationMilliseconds(leaseDuration, "claim analytics generation")
	if err != nil {
		return nil, err
	}
	resultErr = repository.transaction(ctx, "claim analytics generation", pgx.TxOptions{}, func(tx analyticsTx) error {
		var previousStatus string
		candidate := Claim{}
		err := tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT analytics_generation_id, status
    FROM ascendany.analytics_generations
    WHERE (
        status = 'queued'
        AND next_attempt_at <= clock_timestamp()
    ) OR (
        status = 'running'
        AND lease_expires_at <= clock_timestamp()
    )
    ORDER BY
        CASE WHEN status = 'queued' THEN next_attempt_at ELSE lease_expires_at END,
        analytics_generation_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE ascendany.analytics_generations AS generation
SET status = 'running',
    attempt_count = generation.attempt_count + 1,
    lease_owner = $1,
    lease_expires_at = clock_timestamp() + ($2::bigint * interval '1 millisecond'),
    started_at = COALESCE(generation.started_at, clock_timestamp())
FROM candidate
WHERE generation.analytics_generation_id = candidate.analytics_generation_id
RETURNING generation.analytics_generation_id,
          generation.lease_owner,
          generation.lease_expires_at,
          generation.attempt_count,
          generation.base_analytics_generation_id,
          generation.base_head_revision,
          generation.target_exam_id,
          generation.target_snapshot_id,
          generation.target_exam_head_revision,
          generation.input_manifest::text,
          generation.input_manifest_sha256,
          generation.algorithm_version,
          generation.config_sha256,
          candidate.status`, owner, leaseMilliseconds).Scan(
			&candidate.GenerationID,
			&candidate.LeaseOwner,
			&candidate.LeaseExpiresAt,
			&candidate.AttemptCount,
			&candidate.BaseAnalyticsGenerationID,
			&candidate.BaseHeadRevision,
			&candidate.TargetExamID,
			&candidate.TargetSnapshotID,
			&candidate.TargetExamHeadRevision,
			&candidate.ManifestJSON,
			&candidate.ManifestSHA256,
			&candidate.AlgorithmVersion,
			&candidate.ConfigSHA256,
			&previousStatus,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			claim = nil
			return nil
		}
		if err != nil {
			return databaseError("claim analytics generation", err)
		}
		candidate.Reclaimed = previousStatus == "running"
		if err := appendGenerationEvent(ctx, tx, candidate.GenerationID, "running", map[string]any{
			"attemptCount": candidate.AttemptCount,
			"reclaimed":    candidate.Reclaimed,
		}); err != nil {
			return err
		}
		claim = &candidate
		return nil
	})
	return claim, resultErr
}
