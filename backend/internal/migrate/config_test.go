package migrate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testPasswordPath = "/run/credentials/ascendany-migrate/db_password"

func TestLoadConfigReturnsStrictMigrationConfiguration(t *testing.T) {
	t.Parallel()

	environment := validMigrationEnvironment()
	configuration, err := LoadConfig(mapEnvironment(environment), func(path string) ([]byte, error) {
		if path != testPasswordPath {
			t.Fatalf("secret path = %q", path)
		}
		return []byte(strings.Repeat("p", minimumPasswordLen)), nil
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if configuration.DatabaseURL != environment["ASCENDANY_DATABASE_URL"] {
		t.Fatalf("database URL = %q", configuration.DatabaseURL)
	}
	if configuration.Password != strings.Repeat("p", minimumPasswordLen) {
		t.Fatal("database password was not loaded from the secret file")
	}
	if configuration.LockTimeout != 30*time.Second || configuration.ConnectTimeout != 4*time.Second {
		t.Fatalf("timeouts = %s/%s", configuration.LockTimeout, configuration.ConnectTimeout)
	}
}

func TestLoadConfigRequiresEveryMigrationBoundary(t *testing.T) {
	t.Parallel()

	orderedNames := []string{
		"ASCENDANY_DATABASE_URL",
		"ASCENDANY_DATABASE_PASSWORD_FILE",
		"ASCENDANY_DATABASE_ROLE",
		"ASCENDANY_DATABASE_SCHEMA",
		"ASCENDANY_MIGRATION_HISTORY_TABLE",
		"ASCENDANY_DATABASE_SCHEMA_VERSION",
		"ASCENDANY_MIGRATION_LOCK_TIMEOUT",
	}
	for _, missing := range orderedNames {
		missing := missing
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			environment := validMigrationEnvironment()
			delete(environment, missing)
			_, err := LoadConfig(mapEnvironment(environment), func(string) ([]byte, error) {
				return []byte(strings.Repeat("p", minimumPasswordLen)), nil
			})
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("LoadConfig() error = %v, want %s rejection", err, missing)
			}
		})
	}
}

func TestLoadConfigRejectsDatabasePasswordInURLWithoutLeakingIt(t *testing.T) {
	t.Parallel()

	for _, databaseURL := range []string{
		"postgresql://ascendany_migrator_login:supersecret@127.0.0.1:5432/ascendany_v2",
		"postgresql://ascendany_migrator_login@127.0.0.1:5432/ascendany_v2?password=supersecret",
	} {
		environment := validMigrationEnvironment()
		environment["ASCENDANY_DATABASE_URL"] = databaseURL
		_, err := LoadConfig(mapEnvironment(environment), testPasswordReader)
		if err == nil || !strings.Contains(err.Error(), "password") {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), databaseURL) {
			t.Fatalf("LoadConfig() leaked database secret: %v", err)
		}
	}
}

func TestLoadConfigRejectsNonDirectOrWrongDatabaseBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		databaseURL string
		want        string
	}{
		{name: "PgBouncer port", databaseURL: "postgresql://ascendany_migrator_login@127.0.0.1:6432/ascendany_v2", want: "direct PostgreSQL port 5432"},
		{name: "implicit port", databaseURL: "postgresql://ascendany_migrator_login@127.0.0.1/ascendany_v2", want: "direct PostgreSQL port 5432"},
		{name: "legacy database", databaseURL: "postgresql://ascendany_migrator_login@127.0.0.1:5432/ascendany", want: "database must be exactly ascendany_v2"},
		{name: "runtime login", databaseURL: "postgresql://ascendanyd_login@127.0.0.1:5432/ascendany_v2", want: "user must be ascendany_migrator_login"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			environment := validMigrationEnvironment()
			environment["ASCENDANY_DATABASE_URL"] = test.databaseURL
			_, err := LoadConfig(mapEnvironment(environment), testPasswordReader)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConfig() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadConfigRequiresExactOwnerSchemaHistoryAndVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "ASCENDANY_DATABASE_ROLE", value: "ascendany_migrator"},
		{name: "ASCENDANY_DATABASE_SCHEMA", value: "public"},
		{name: "ASCENDANY_MIGRATION_HISTORY_TABLE", value: "public.schema_migrations"},
		{name: "ASCENDANY_DATABASE_SCHEMA_VERSION", value: "1"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			environment := validMigrationEnvironment()
			environment[test.name] = test.value
			_, err := LoadConfig(mapEnvironment(environment), testPasswordReader)
			if err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("LoadConfig() error = %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsUnreadableOrMalformedPasswordFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		readFile ReadFile
		want     string
	}{
		{
			name: "unreadable",
			readFile: func(string) ([]byte, error) {
				return nil, errors.New("permission denied at private path")
			},
			want: "cannot be read",
		},
		{
			name:     "short",
			readFile: func(string) ([]byte, error) { return []byte("short"), nil },
			want:     "at least 16 bytes",
		},
		{
			name:     "newline",
			readFile: func(string) ([]byte, error) { return []byte(strings.Repeat("p", 16) + "\n"), nil },
			want:     "whitespace",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadConfig(mapEnvironment(validMigrationEnvironment()), test.readFile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConfig() error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), testPasswordPath) || strings.Contains(err.Error(), "private path") {
				t.Fatalf("LoadConfig() leaked secret path: %v", err)
			}
		})
	}
}

func validMigrationEnvironment() map[string]string {
	return map[string]string{
		"ASCENDANY_DATABASE_URL":             "postgresql://ascendany_migrator_login@127.0.0.1:5432/ascendany_v2?sslmode=disable",
		"ASCENDANY_DATABASE_PASSWORD_FILE":   testPasswordPath,
		"ASCENDANY_DATABASE_ROLE":            databaseRole,
		"ASCENDANY_DATABASE_SCHEMA":          databaseSchema,
		"ASCENDANY_DATABASE_SCHEMA_VERSION":  "10",
		"ASCENDANY_MIGRATION_HISTORY_TABLE":  historyTable,
		"ASCENDANY_MIGRATION_LOCK_TIMEOUT":   "30s",
		"ASCENDANY_DATABASE_CONNECT_TIMEOUT": "4s",
	}
}

func mapEnvironment(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func testPasswordReader(string) ([]byte, error) {
	return []byte(strings.Repeat("p", minimumPasswordLen)), nil
}
