package analytics

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (repository *PostgresRepository) RenewLease(
	ctx context.Context,
	claim Claim,
	leaseDuration time.Duration,
) error {
	leaseMilliseconds, err := durationMilliseconds(leaseDuration, "renew analytics lease")
	if err != nil {
		return err
	}
	if claim.GenerationID <= 0 || claim.LeaseOwner == "" || claim.AttemptCount <= 0 {
		return analyticsError(ErrorStateConflict, false, "renew analytics lease", errors.New("claim attempt identity is invalid"))
	}
	return repository.transaction(ctx, "renew analytics lease", pgx.TxOptions{}, func(tx analyticsTx) error {
		var renewedUntil time.Time
		err := tx.QueryRow(ctx, `
UPDATE ascendany.analytics_generations
SET lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond')
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND lease_owner = $2
  AND attempt_count = $3
  AND lease_expires_at > clock_timestamp()
RETURNING lease_expires_at`, claim.GenerationID, claim.LeaseOwner, claim.AttemptCount, leaseMilliseconds).Scan(&renewedUntil)
		if errors.Is(err, pgx.ErrNoRows) {
			return analyticsError(ErrorLeaseLost, false, "renew analytics lease", errors.New("claim attempt is no longer active"))
		}
		if err != nil {
			return databaseError("renew analytics lease", err)
		}
		return nil
	})
}
