package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type analyticsTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type analyticsBegin func(context.Context, pgx.TxOptions) (analyticsTx, error)

type PostgresRepository struct {
	begin analyticsBegin
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{
		begin: func(ctx context.Context, options pgx.TxOptions) (analyticsTx, error) {
			return pool.BeginTx(ctx, options)
		},
	}, nil
}

func newPostgresRepository(begin analyticsBegin) (*PostgresRepository, error) {
	if begin == nil {
		return nil, analyticsError(ErrorInvalidConfiguration, true, "construct PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	options pgx.TxOptions,
	run func(analyticsTx) error,
) (resultErr error) {
	if ctx == nil {
		return analyticsError(ErrorInvalidConfiguration, true, operation, errors.New("context is required"))
	}
	tx, err := repository.begin(ctx, options)
	if err != nil {
		return databaseError(operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(rollbackContext); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			rollbackErr := databaseError("rollback "+operation, err)
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
		return databaseError("commit "+operation, err)
	}
	finished = true
	return nil
}

func databaseError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return analyticsError(ErrorCanceled, false, operation, err)
	}
	return analyticsError(ErrorDatabase, false, operation, err)
}

func durationMilliseconds(value time.Duration, operation string) (int64, error) {
	if value <= 0 || value.Milliseconds() <= 0 {
		return 0, analyticsError(ErrorInvalidConfiguration, true, operation, fmt.Errorf("duration must be at least one millisecond"))
	}
	return value.Milliseconds(), nil
}

func appendGenerationEvent(
	ctx context.Context,
	tx analyticsTx,
	generationID int64,
	eventType string,
	payload map[string]any,
) error {
	if generationID <= 0 || eventType == "" || payload == nil {
		return analyticsError(ErrorStateConflict, false, "append analytics generation event", errors.New("generation, event type, and payload are required"))
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return analyticsError(ErrorStateConflict, false, "encode analytics generation event", err)
	}
	canonical, _, err := canonicaljson.Object(encoded, 32<<10)
	if err != nil {
		return analyticsError(ErrorStateConflict, false, "canonicalize analytics generation event", err)
	}
	commandTag, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_events (
    analytics_generation_id,
    event_sequence,
    event_type,
    payload
)
SELECT $1,
       COALESCE(MAX(event_sequence), 0) + 1,
       $2,
       $3::jsonb
FROM ascendany.analytics_generation_events
WHERE analytics_generation_id = $1`, generationID, eventType, string(canonical))
	if err != nil {
		return databaseError("append analytics generation event", err)
	}
	if commandTag.RowsAffected() != 1 {
		return analyticsError(ErrorStateConflict, false, "append analytics generation event", errors.New("event insert did not affect one row"))
	}
	return nil
}
