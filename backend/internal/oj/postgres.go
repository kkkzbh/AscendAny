package oj

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	run func(postgresTx) error,
) error {
	return repository.transactionWithOptions(ctx, operation, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, run)
}

func (repository *PostgresRepository) readTransaction(
	ctx context.Context,
	operation string,
	run func(postgresTx) error,
) error {
	return repository.transactionWithOptions(ctx, operation, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, run)
}

func (repository *PostgresRepository) transactionWithOptions(
	ctx context.Context,
	operation string,
	options pgx.TxOptions,
	run func(postgresTx) error,
) (resultErr error) {
	if ctx == nil {
		return ojError(ErrorInvalidInput, true, operation, errors.New("context is required"))
	}
	tx, err := repository.begin(ctx, options)
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
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

func mapPrincipalError(operation string, err error) error {
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorInvalidPrincipal:
		return ojError(ErrorInvalidInput, true, operation, err)
	case principalguard.ErrorRejected:
		return ojError(ErrorPrincipalRejected, true, operation, err)
	case principalguard.ErrorStoredData:
		return ojError(ErrorStoredDataInvalid, true, operation, err)
	case principalguard.ErrorCanceled:
		return ojError(ErrorCanceled, false, operation, err)
	case principalguard.ErrorDatabase:
		return ojError(ErrorDatabase, false, operation, err)
	default:
		return ojError(ErrorDatabase, false, operation, err)
	}
}

func registerArtifact(ctx context.Context, tx postgresTx, value Artifact) (int64, error) {
	var databaseID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (sha256) DO NOTHING
RETURNING artifact_id`, value.SHA256, value.SizeBytes, value.MediaType, value.StorageKey).Scan(&databaseID)
	if err == nil {
		return databaseID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, databaseFailure("register OJ artifact", err)
	}
	var stored Artifact
	if err := tx.QueryRow(ctx, `
SELECT artifact_id, sha256, size_bytes, media_type, storage_key
FROM ascendany.artifacts
WHERE sha256 = $1`, value.SHA256).Scan(&databaseID, &stored.SHA256, &stored.SizeBytes, &stored.MediaType, &stored.StorageKey); err != nil {
		return 0, databaseFailure("load existing OJ artifact", err)
	}
	if stored != value {
		return 0, ojError(ErrorArtifactConflict, true, "register OJ artifact", errors.New("artifact digest already owns different immutable metadata"))
	}
	return databaseID, nil
}
