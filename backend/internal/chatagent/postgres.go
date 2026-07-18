package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type postgresTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context, pgx.TxOptions) (postgresTx, error)

type PostgresRepository struct {
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	options pgx.TxOptions,
	run func(postgresTx) error,
) (resultErr error) {
	if ctx == nil {
		return domainError(ErrorInvalidInput, true, operation, errors.New("context is required"))
	}
	tx, err := repository.begin(ctx, options)
	if err != nil {
		return databaseFailure(operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(rollbackContext); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, err)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}

func readOnlyOptions() pgx.TxOptions {
	return pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
}

func mapPrincipalError(operation string, err error) error {
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorInvalidPrincipal:
		return domainError(ErrorInvalidInput, true, operation, err)
	case principalguard.ErrorRejected:
		return domainError(ErrorPrincipalRejected, true, operation, err)
	case principalguard.ErrorStoredData:
		return domainError(ErrorStoredDataInvalid, true, operation, err)
	case principalguard.ErrorCanceled:
		return domainError(ErrorCanceled, false, operation, err)
	case principalguard.ErrorDatabase:
		return domainError(ErrorDatabase, false, operation, err)
	default:
		return domainError(ErrorDatabase, false, operation, err)
	}
}

func resolveStudent(ctx context.Context, tx postgresTx, principal auth.AccessPrincipal, lock bool) (principalguard.Resolved, error) {
	var (
		resolved principalguard.Resolved
		err      error
	)
	if lock {
		resolved, err = principalguard.ResolveForUpdate(ctx, tx, principal, principalguard.Roles(auth.RoleStudent))
	} else {
		resolved, err = principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleStudent))
	}
	if err != nil {
		return principalguard.Resolved{}, mapPrincipalError("authorize chat agent operation", err)
	}
	return resolved, nil
}

func appendRunEvent(ctx context.Context, tx postgresTx, runDatabaseID int64, eventType string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return domainError(ErrorInvalidInput, true, "encode agent run event", err)
	}
	canonical, _, err := canonicaljson.Object(encoded, MaxRunEventDocumentBytes)
	if err != nil {
		return domainError(ErrorInvalidInput, true, "canonicalize agent run event", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO ascendany.agent_run_events (
    agent_run_id,
    event_sequence,
    event_type,
    payload
)
SELECT $1,
       COALESCE(max(event_sequence), 0) + 1,
       $2,
       $3::jsonb
FROM ascendany.agent_run_events
WHERE agent_run_id = $1`, runDatabaseID, eventType, string(canonical))
	if err != nil {
		return databaseFailure("append agent run event", err)
	}
	return nil
}

func utcOptional(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func normalizeRunTimes(run *Run) {
	run.CreatedAt = run.CreatedAt.UTC()
	run.UpdatedAt = run.UpdatedAt.UTC()
	run.StartedAt = utcOptional(run.StartedAt)
	run.FinishedAt = utcOptional(run.FinishedAt)
}
