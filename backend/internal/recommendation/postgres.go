package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func canonicalStoredObject(raw, expectedSHA256 string, maximumBytes int, operation string) (json.RawMessage, error) {
	canonical, digest, err := canonicaljson.Object(json.RawMessage(raw), maximumBytes)
	if err != nil || (expectedSHA256 != "" && digest != expectedSHA256) {
		return nil, domainError(ErrorStoredDataInvalid, true, operation, errors.New("stored JSON object or its SHA-256 is invalid"))
	}
	return canonical, nil
}

type recommendationTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context, pgx.TxOptions) (recommendationTx, error)

type PostgresRepository struct {
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (recommendationTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	options pgx.TxOptions,
	run func(recommendationTx) error,
) (resultErr error) {
	if ctx == nil {
		return domainError(ErrorInvalidInput, true, operation, errors.New("context is required"))
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
