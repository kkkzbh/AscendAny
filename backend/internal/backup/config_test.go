package backup

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testCredentialPath = "/run/credentials/backup-password"
const testRestoreBackupID = "backup-20260711T010203Z-0123456789abcdef"

func TestLoadCreateConfigAcceptsExactProductionContract(t *testing.T) {
	t.Parallel()
	config, err := LoadCreateConfig(mapLookup(validCreateEnvironment()), func(path string) ([]byte, error) {
		if path != testCredentialPath {
			t.Fatalf("credential path = %q", path)
		}
		return []byte(strings.Repeat("p", 24)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.DatabaseURL != "postgresql://ascendany_backup_login@127.0.0.1:5432/ascendany_v2?sslmode=disable" ||
		config.ArtifactRoot != "/var/lib/ascendany/artifacts" ||
		config.BackupRoot != "/var/backups/ascendany" ||
		config.RuntimeRoot != BackupRuntimeRoot ||
		config.RetainDaily != 14 || config.RetainWeekly != 8 ||
		config.ConnectTimeout != 4*time.Second || config.CommandTimeout != 90*time.Minute {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadCreateConfigRejectsCapabilityAndPathDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"password in URL", func(values map[string]string) {
			values["ASCENDANY_DATABASE_URL"] = "postgresql://ascendany_backup_login:secret@127.0.0.1:5432/ascendany_v2"
		}, "password"},
		{"pgbouncer port", func(values map[string]string) {
			values["ASCENDANY_DATABASE_URL"] = "postgresql://ascendany_backup_login@127.0.0.1:6432/ascendany_v2"
		}, "5432"},
		{"runtime login", func(values map[string]string) {
			values["ASCENDANY_DATABASE_URL"] = "postgresql://ascendanyd_login@127.0.0.1:5432/ascendany_v2"
		}, SourceDatabaseLogin},
		{"legacy database", func(values map[string]string) {
			values["ASCENDANY_DATABASE_URL"] = "postgresql://ascendany_backup_login@127.0.0.1:5432/ascendany"
		}, SourceDatabaseName},
		{"nested backup root", func(values map[string]string) {
			values["ASCENDANY_BACKUP_ROOT"] = "/var/lib/ascendany/artifacts/backups"
		}, "disjoint"},
		{"missing runtime root", func(values map[string]string) {
			delete(values, "ASCENDANY_BACKUP_RUNTIME_ROOT")
		}, "ASCENDANY_BACKUP_RUNTIME_ROOT is required"},
		{"runtime root drift", func(values map[string]string) {
			values["ASCENDANY_BACKUP_RUNTIME_ROOT"] = "/var/backups/ascendany/runtime"
		}, BackupRuntimeRoot},
		{"format drift", func(values map[string]string) { values["ASCENDANY_BACKUP_FORMAT"] = "tar" }, BackupFormat},
		{"hash drift", func(values map[string]string) { values["ASCENDANY_BACKUP_MANIFEST_HASH"] = "sha512" }, ManifestHashAlgorithm},
		{"no retention", func(values map[string]string) {
			values["ASCENDANY_BACKUP_RETAIN_DAILY"], values["ASCENDANY_BACKUP_RETAIN_WEEKLY"] = "0", "0"
		}, "retention"},
		{"relative tool", func(values map[string]string) { values["ASCENDANY_PG_DUMP_PATH"] = "pg_dump" }, "absolute"},
		{"missing connect timeout", func(values map[string]string) { delete(values, "ASCENDANY_DATABASE_CONNECT_TIMEOUT") }, "required"},
		{"missing command timeout", func(values map[string]string) { delete(values, "ASCENDANY_BACKUP_COMMAND_TIMEOUT") }, "required"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validCreateEnvironment()
			test.mutate(values)
			_, err := LoadCreateConfig(mapLookup(values), func(string) ([]byte, error) {
				return []byte(strings.Repeat("p", 24)), nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadCreateConfig() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadConfigSanitizesCredentialReadFailures(t *testing.T) {
	t.Parallel()
	_, err := LoadCreateConfig(mapLookup(validCreateEnvironment()), func(string) ([]byte, error) {
		return nil, errors.New("permission denied at /private/actual-secret-location")
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "actual-secret") || strings.Contains(err.Error(), testCredentialPath) {
		t.Fatalf("credential path leaked: %v", err)
	}
}

func TestLoadRestoreConfigRequiresDedicatedScratchDatabase(t *testing.T) {
	t.Parallel()
	values := validCreateEnvironment()
	values["ASCENDANY_RESTORE_ARTIFACT_ROOT"] = "/var/lib/ascendany-restore/artifacts"
	values["ASCENDANY_RESTORE_DATABASE_URL"] = "postgresql://" + RestoreDatabaseLogin + "@127.0.0.1:5432/" + RestoreDatabaseName
	values["ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE"] = "/run/credentials/restore-password"
	values["ASCENDANY_RESTORE_RUNTIME_ROOT"] = RestoreRuntimeRootPrefix + testRestoreBackupID
	config, err := LoadRestoreConfig(mapLookup(values), func(string) ([]byte, error) {
		return []byte(strings.Repeat("r", 24)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.DatabaseURL != values["ASCENDANY_RESTORE_DATABASE_URL"] ||
		config.RuntimeRoot != RestoreRuntimeRootPrefix+testRestoreBackupID {
		t.Fatalf("config = %#v", config)
	}

	values["ASCENDANY_RESTORE_DATABASE_URL"] = "postgresql://ascendany_migrator_login@127.0.0.1:5432/" + RestoreDatabaseName
	_, err = LoadRestoreConfig(mapLookup(values), func(string) ([]byte, error) {
		return []byte(strings.Repeat("r", 24)), nil
	})
	if err == nil || !strings.Contains(err.Error(), RestoreDatabaseLogin) {
		t.Fatalf("wrong restore login error = %v", err)
	}

	values["ASCENDANY_RESTORE_DATABASE_URL"] = "postgresql://" + RestoreDatabaseLogin + "@127.0.0.1:5432/ascendany_v2"
	_, err = LoadRestoreConfig(mapLookup(values), func(string) ([]byte, error) {
		return []byte(strings.Repeat("r", 24)), nil
	})
	if err == nil || !strings.Contains(err.Error(), RestoreDatabaseName) {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRestoreConfigRequiresCanonicalPerInstanceRuntimeRoot(t *testing.T) {
	t.Parallel()
	for _, runtimeRoot := range []string{
		"/run/ascendany-restore-verify-%i",
		"/run/ascendany-restore-verify-invalid",
		"/var/lib/ascendany-restore/ascendany-restore-verify-" + testRestoreBackupID,
	} {
		values := validCreateEnvironment()
		values["ASCENDANY_RESTORE_ARTIFACT_ROOT"] = "/var/lib/ascendany-restore/artifacts"
		values["ASCENDANY_RESTORE_DATABASE_URL"] = "postgresql://" + RestoreDatabaseLogin + "@127.0.0.1:5432/" + RestoreDatabaseName
		values["ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE"] = "/run/credentials/restore-password"
		values["ASCENDANY_RESTORE_RUNTIME_ROOT"] = runtimeRoot
		_, err := LoadRestoreConfig(mapLookup(values), func(string) ([]byte, error) {
			return []byte(strings.Repeat("r", 24)), nil
		})
		if err == nil || !strings.Contains(err.Error(), "ASCENDANY_RESTORE_RUNTIME_ROOT") {
			t.Fatalf("runtime root %q error = %v", runtimeRoot, err)
		}
	}
}

func validCreateEnvironment() map[string]string {
	return map[string]string{
		"ASCENDANY_DATABASE_URL":             "postgresql://ascendany_backup_login@127.0.0.1:5432/ascendany_v2?sslmode=disable",
		"ASCENDANY_DATABASE_PASSWORD_FILE":   testCredentialPath,
		"ASCENDANY_ARTIFACT_ROOT":            "/var/lib/ascendany/artifacts",
		"ASCENDANY_BACKUP_ROOT":              "/var/backups/ascendany",
		"ASCENDANY_BACKUP_RUNTIME_ROOT":      BackupRuntimeRoot,
		"ASCENDANY_BACKUP_FORMAT":            BackupFormat,
		"ASCENDANY_BACKUP_MANIFEST_HASH":     ManifestHashAlgorithm,
		"ASCENDANY_BACKUP_RETAIN_DAILY":      "14",
		"ASCENDANY_BACKUP_RETAIN_WEEKLY":     "8",
		"ASCENDANY_DATABASE_CONNECT_TIMEOUT": "4s",
		"ASCENDANY_BACKUP_COMMAND_TIMEOUT":   "90m",
		"ASCENDANY_PG_DUMP_PATH":             "/usr/bin/pg_dump",
		"ASCENDANY_PG_RESTORE_PATH":          "/usr/bin/pg_restore",
		"ASCENDANY_ZSTD_PATH":                "/usr/bin/zstd",
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
