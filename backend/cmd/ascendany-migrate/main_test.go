package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
)

func TestRunAcceptsOnlyUpAndPassesValidatedConfig(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	called := false
	exitCode := run(
		context.Background(),
		[]string{"up"},
		mapLookup(validEnvironment()),
		func(string) ([]byte, error) { return []byte(strings.Repeat("p", 32)), nil },
		&output,
		func(_ context.Context, configuration migrate.Config) error {
			called = true
			if configuration.LockTimeout.String() != "30s" {
				t.Fatalf("lock timeout = %s", configuration.LockTimeout)
			}
			return nil
		},
	)
	if exitCode != 0 {
		t.Fatalf("run() = %d, output = %s", exitCode, output.String())
	}
	if !called {
		t.Fatal("migration runner was not called")
	}
	if !strings.Contains(output.String(), `"schemaVersion":10`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestRunRejectsEveryOtherCommandBeforeLoadingConfig(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {}, {"down"}, {"up", "extra"}} {
		var output bytes.Buffer
		exitCode := run(
			context.Background(),
			args,
			func(string) (string, bool) { t.Fatal("configuration lookup called"); return "", false },
			func(string) ([]byte, error) { t.Fatal("secret reader called"); return nil, nil },
			&output,
			func(context.Context, migrate.Config) error { t.Fatal("migration runner called"); return nil },
		)
		if exitCode != 2 {
			t.Fatalf("run(%v) = %d, output = %s", args, exitCode, output.String())
		}
	}
}

func TestRunReportsMigrationFailure(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"up"},
		mapLookup(validEnvironment()),
		func(string) ([]byte, error) { return []byte(strings.Repeat("p", 32)), nil },
		&output,
		func(context.Context, migrate.Config) error { return errors.New("database offline") },
	)
	if exitCode != 1 || !strings.Contains(output.String(), "migration failed") {
		t.Fatalf("run() = %d, output = %s", exitCode, output.String())
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"ASCENDANY_DATABASE_URL":             "postgresql://ascendany_migrator_login@127.0.0.1:5432/ascendany_v2",
		"ASCENDANY_DATABASE_PASSWORD_FILE":   "/run/credentials/db_password",
		"ASCENDANY_DATABASE_ROLE":            "ascendany_owner",
		"ASCENDANY_DATABASE_SCHEMA":          "ascendany",
		"ASCENDANY_DATABASE_SCHEMA_VERSION":  "10",
		"ASCENDANY_MIGRATION_HISTORY_TABLE":  "ascendany.schema_migrations_v2",
		"ASCENDANY_MIGRATION_LOCK_TIMEOUT":   "30s",
		"ASCENDANY_DATABASE_CONNECT_TIMEOUT": "5s",
	}
}

func mapLookup(values map[string]string) migrate.LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
