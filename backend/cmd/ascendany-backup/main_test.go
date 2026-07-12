package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/backup"
)

const validBackupID = "backup-20260711T010203Z-0123456789abcdef"

func TestRunCreateLoadsSecretAndReportsSafeIdentity(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	called := false
	exitCode := run(
		context.Background(),
		[]string{"create"},
		mapLookup(validEnvironment()),
		func(path string) ([]byte, error) {
			if path != "/run/credentials/backup-password" {
				t.Fatalf("secret path = %q", path)
			}
			return []byte(strings.Repeat("s", 24)), nil
		},
		&output,
		operations{create: func(_ context.Context, config backup.CreateConfig) (backup.CreateResult, error) {
			called = true
			if config.DatabasePassword != strings.Repeat("s", 24) || config.RuntimeRoot != backup.BackupRuntimeRoot {
				t.Fatalf("create config = %#v", config)
			}
			return backup.CreateResult{BackupID: validBackupID, ManifestSHA256: strings.Repeat("a", 64), ArtifactCount: 7}, nil
		}},
	)
	if exitCode != 0 || !called {
		t.Fatalf("exit=%d called=%v output=%s", exitCode, called, output.String())
	}
	if !strings.Contains(output.String(), `"backupId":"`+validBackupID+`"`) || strings.Contains(output.String(), strings.Repeat("s", 24)) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestRunVerifyDoesNotReadAnyCredential(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"verify", validBackupID},
		mapLookup(validEnvironment()),
		func(string) ([]byte, error) { t.Fatal("credential reader called"); return nil, nil },
		&output,
		operations{verify: func(_ context.Context, _ backup.VerifyConfig, backupID string) (backup.VerifyResult, error) {
			if backupID != validBackupID {
				t.Fatalf("backup id = %q", backupID)
			}
			return backup.VerifyResult{BackupID: backupID, ManifestSHA256: strings.Repeat("b", 64), ArtifactCount: 2}, nil
		}},
	)
	if exitCode != 0 || !strings.Contains(output.String(), "backup verified") {
		t.Fatalf("exit=%d output=%s", exitCode, output.String())
	}
}

func TestRunRestoreVerifyUsesDedicatedCredential(t *testing.T) {
	t.Parallel()
	values := validEnvironment()
	values["ASCENDANY_RESTORE_ARTIFACT_ROOT"] = "/var/lib/ascendany-restore/artifacts"
	values["ASCENDANY_RESTORE_DATABASE_URL"] = "postgresql://ascendany_restore_login@127.0.0.1:5432/ascendany_v2_restore_verify"
	values["ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE"] = "/run/credentials/restore-password"
	values["ASCENDANY_RESTORE_RUNTIME_ROOT"] = backup.RestoreRuntimeRootPrefix + validBackupID
	var output bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"restore-verify", validBackupID},
		mapLookup(values),
		func(path string) ([]byte, error) {
			if path != "/run/credentials/restore-password" {
				t.Fatalf("secret path = %q", path)
			}
			return []byte(strings.Repeat("r", 24)), nil
		},
		&output,
		operations{restore: func(_ context.Context, config backup.RestoreConfig, backupID string) (backup.RestoreResult, error) {
			if config.DatabasePassword != strings.Repeat("r", 24) ||
				config.RuntimeRoot != backup.RestoreRuntimeRootPrefix+validBackupID {
				t.Fatalf("restore config = %#v", config)
			}
			return backup.RestoreResult{BackupID: backupID, ManifestSHA256: strings.Repeat("c", 64), ArtifactCount: 2, DatabaseName: backup.RestoreDatabaseName}, nil
		}},
	)
	if exitCode != 0 || !strings.Contains(output.String(), "backup restore verified") {
		t.Fatalf("exit=%d output=%s", exitCode, output.String())
	}
}

func TestRunRejectsMalformedCommandsBeforeConfiguration(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {}, {"create", "extra"}, {"verify"}, {"verify", "../escape"}, {"restore"}, {"delete", validBackupID}} {
		var output bytes.Buffer
		exitCode := run(
			context.Background(),
			args,
			func(string) (string, bool) { t.Fatal("environment lookup called"); return "", false },
			func(string) ([]byte, error) { t.Fatal("credential reader called"); return nil, nil },
			&output,
			operations{},
		)
		if exitCode != 2 {
			t.Fatalf("run(%v) = %d, output=%s", args, exitCode, output.String())
		}
	}
}

func TestRunSanitizesOperationalFailure(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"create"},
		mapLookup(validEnvironment()),
		func(string) ([]byte, error) { return []byte(strings.Repeat("s", 24)), nil },
		&output,
		operations{create: func(context.Context, backup.CreateConfig) (backup.CreateResult, error) {
			return backup.CreateResult{}, errors.New("pg_dump exited")
		}},
	)
	if exitCode != 1 || !strings.Contains(output.String(), "backup creation failed") {
		t.Fatalf("exit=%d output=%s", exitCode, output.String())
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"ASCENDANY_DATABASE_URL":             "postgresql://ascendany_backup_login@127.0.0.1:5432/ascendany_v2",
		"ASCENDANY_DATABASE_PASSWORD_FILE":   "/run/credentials/backup-password",
		"ASCENDANY_ARTIFACT_ROOT":            "/var/lib/ascendany/artifacts",
		"ASCENDANY_BACKUP_ROOT":              "/var/backups/ascendany",
		"ASCENDANY_BACKUP_RUNTIME_ROOT":      backup.BackupRuntimeRoot,
		"ASCENDANY_BACKUP_FORMAT":            backup.BackupFormat,
		"ASCENDANY_BACKUP_MANIFEST_HASH":     backup.ManifestHashAlgorithm,
		"ASCENDANY_BACKUP_RETAIN_DAILY":      "14",
		"ASCENDANY_BACKUP_RETAIN_WEEKLY":     "8",
		"ASCENDANY_DATABASE_CONNECT_TIMEOUT": "5s",
		"ASCENDANY_BACKUP_COMMAND_TIMEOUT":   "1h",
		"ASCENDANY_PG_DUMP_PATH":             "/usr/bin/pg_dump",
		"ASCENDANY_PG_RESTORE_PATH":          "/usr/bin/pg_restore",
		"ASCENDANY_ZSTD_PATH":                "/usr/bin/zstd",
	}
}

func mapLookup(values map[string]string) backup.LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
