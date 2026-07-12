package importing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PgxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type dbTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type txBegin func(context.Context) (dbTx, error)

type PostgresRepository struct {
	begin txBegin
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{
		begin: func(ctx context.Context) (dbTx, error) {
			return pool.Begin(ctx)
		},
	}, nil
}

func newPostgresRepository(begin txBegin) (*PostgresRepository, error) {
	if begin == nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func NewService(pool PgxBeginner) (*Service, error) {
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		return nil, err
	}
	return newService(repository, randomUUIDv4)
}

func (r *PostgresRepository) transaction(ctx context.Context, operation string, run func(dbTx) error) (resultErr error) {
	if ctx == nil {
		return importError(ErrorDatabase, false, operation, errors.New("context is required"))
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return importError(ErrorDatabase, false, operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(rollbackContext); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			rollbackErr := importError(ErrorDatabase, false, "rollback "+operation, err)
			if resultErr == nil {
				resultErr = rollbackErr
			} else {
				resultErr = errors.Join(resultErr, rollbackErr)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return importError(ErrorDatabase, false, "commit "+operation, err)
	}
	finished = true
	return nil
}

func databaseError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return importError(ErrorCanceled, false, operation, err)
	}
	return importError(ErrorDatabase, false, operation, err)
}

func appendEvent(ctx context.Context, tx dbTx, jobID int64, eventType string, payload any) (int64, error) {
	payloadJSON, err := canonicalEventPayload(payload)
	if err != nil {
		return 0, err
	}
	var sequence int64
	err = tx.QueryRow(ctx, `
INSERT INTO ascendany.import_job_events (
    import_job_id,
    event_sequence,
    event_type,
    payload
)
SELECT
    $1,
    COALESCE(MAX(event_sequence), 0) + 1,
    $2,
    $3::jsonb
FROM ascendany.import_job_events
WHERE import_job_id = $1
RETURNING event_sequence`, jobID, eventType, payloadJSON).Scan(&sequence)
	if err != nil {
		return 0, databaseError("append import event", err)
	}
	return sequence, nil
}

func scanJob(row pgx.Row, job *Job, extra ...any) error {
	targets := []any{
		&job.ID,
		&job.PublicID,
		&job.ArtifactID,
		&job.Status,
		&job.Stage,
		&job.AttemptCount,
		&job.LeaseOwner,
		&job.LeaseExpiresAt,
		&job.SnapshotID,
		&job.CreatedAt,
		&job.StartedAt,
		&job.FinishedAt,
		&job.UpdatedAt,
	}
	targets = append(targets, extra...)
	return row.Scan(targets...)
}

const jobReturningColumns = `
    import_job_id,
    public_id::text,
    artifact_id,
    status,
    stage,
    attempt_count,
    lease_owner,
    lease_expires_at,
    snapshot_id,
    created_at,
    started_at,
    finished_at,
    updated_at`

const qualifiedJobReturningColumns = `
    job.import_job_id,
    job.public_id::text,
    job.artifact_id,
    job.status,
    job.stage,
    job.attempt_count,
    job.lease_owner,
    job.lease_expires_at,
    job.snapshot_id,
    job.created_at,
    job.started_at,
    job.finished_at,
    job.updated_at`

func requirePositiveDuration(value time.Duration, operation string) (int64, error) {
	if value <= 0 || value.Milliseconds() <= 0 {
		return 0, importError(ErrorInvalidConfiguration, false, operation, fmt.Errorf("duration must be at least one millisecond"))
	}
	return value.Milliseconds(), nil
}
