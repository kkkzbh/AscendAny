package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type migrationTestRow struct {
	count   int64
	version int64
	exact   bool
	err     error
}

func (row migrationTestRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 3 {
		return errors.New("unexpected destination count")
	}
	count, countOK := destinations[0].(*int64)
	version, versionOK := destinations[1].(*int64)
	exact, exactOK := destinations[2].(*bool)
	if !countOK || !versionOK || !exactOK {
		return errors.New("unexpected destination type")
	}
	*count = row.count
	*version = row.version
	*exact = row.exact
	return nil
}

type migrationTestQuerier struct {
	query string
	args  []any
	row   pgx.Row
}

func (querier *migrationTestQuerier) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	querier.query = query
	querier.args = append([]any(nil), args...)
	return querier.row
}

func TestMigrationStateReaderReadsExactMaximumV2Version(t *testing.T) {
	t.Parallel()

	expected := []ExpectedMigration{{Version: 1, Name: "fresh_schema", SHA256: strings.Repeat("a", 64)}}
	querier := &migrationTestQuerier{row: migrationTestRow{count: 1, version: 1, exact: true}}
	reader, err := NewMigrationStateReader(querier, expected)
	if err != nil {
		t.Fatalf("NewMigrationStateReader() error = %v", err)
	}
	state, err := reader.State(context.Background())
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Version != 1 {
		t.Fatalf("version = %d, want 1", state.Version)
	}
	if !strings.Contains(querier.query, "bool_and") || len(querier.args) != 3 {
		t.Fatalf("query/args do not verify the exact manifest: %q %#v", querier.query, querier.args)
	}
}

func TestMigrationStateReaderReturnsQueryFailure(t *testing.T) {
	t.Parallel()

	reader, err := NewMigrationStateReader(&migrationTestQuerier{
		row: migrationTestRow{err: errors.New("missing history")},
	}, []ExpectedMigration{{Version: 1, Name: "fresh_schema", SHA256: strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatalf("NewMigrationStateReader() error = %v", err)
	}
	_, err = reader.State(context.Background())
	if err == nil || err.Error() != "read database migration state: missing history" {
		t.Fatalf("State() error = %v", err)
	}
}

func TestNewMigrationStateReaderRejectsNilQuerier(t *testing.T) {
	t.Parallel()

	_, err := NewMigrationStateReader(nil, []ExpectedMigration{{Version: 1, Name: "fresh_schema", SHA256: strings.Repeat("a", 64)}})
	if err == nil {
		t.Fatal("NewMigrationStateReader(nil) error = nil")
	}
}

func TestMigrationStateReaderRejectsHistoryDrift(t *testing.T) {
	t.Parallel()

	expected := []ExpectedMigration{{Version: 1, Name: "fresh_schema", SHA256: strings.Repeat("a", 64)}}
	for _, row := range []migrationTestRow{
		{count: 0, version: 0, exact: false},
		{count: 1, version: 1, exact: false},
		{count: 2, version: 2, exact: true},
	} {
		reader, err := NewMigrationStateReader(&migrationTestQuerier{row: row}, expected)
		if err != nil {
			t.Fatalf("NewMigrationStateReader() error = %v", err)
		}
		if _, err := reader.State(context.Background()); err == nil {
			t.Fatalf("State() accepted drift row %#v", row)
		}
	}
}
