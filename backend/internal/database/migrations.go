package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type RowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type ExpectedMigration struct {
	Version int64
	Name    string
	SHA256  string
}

type MigrationState struct {
	Version int64
}

type MigrationStateReader struct {
	querier RowQuerier
	query   string
	args    []any
	count   int64
}

func NewMigrationStateReader(querier RowQuerier, expected []ExpectedMigration) (*MigrationStateReader, error) {
	if querier == nil {
		return nil, fmt.Errorf("migration row querier is required")
	}
	if len(expected) == 0 {
		return nil, fmt.Errorf("expected migration manifest is required")
	}

	placeholders := make([]string, 0, len(expected))
	args := make([]any, 0, len(expected)*3)
	for index, entry := range expected {
		wantVersion := int64(index + 1)
		if entry.Version != wantVersion || entry.Name == "" || len(entry.SHA256) != 64 {
			return nil, fmt.Errorf("expected migration manifest is invalid at version %d", wantVersion)
		}
		base := index*3 + 1
		placeholders = append(placeholders, fmt.Sprintf("($%d::bigint, $%d::text, $%d::text)", base, base+1, base+2))
		args = append(args, entry.Version, entry.Name, entry.SHA256)
	}
	query := fmt.Sprintf(`
SELECT
    count(*)::bigint,
    COALESCE(MAX(version), 0)::bigint,
    COALESCE(bool_and((version, name, sha256) IN (VALUES %s)), false)
FROM ascendany.schema_migrations_v2`, strings.Join(placeholders, ", "))
	return &MigrationStateReader{
		querier: querier,
		query:   query,
		args:    args,
		count:   int64(len(expected)),
	}, nil
}

func (reader *MigrationStateReader) State(ctx context.Context) (MigrationState, error) {
	var count int64
	var state MigrationState
	var exact bool
	if err := reader.querier.QueryRow(ctx, reader.query, reader.args...).Scan(&count, &state.Version, &exact); err != nil {
		return MigrationState{}, fmt.Errorf("read database migration state: %w", err)
	}
	if count != reader.count || !exact {
		return MigrationState{}, fmt.Errorf("database migration history does not match the binary manifest")
	}
	return state, nil
}
