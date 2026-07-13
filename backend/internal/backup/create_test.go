package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/catalogpublication"
)

type fakeSnapshot struct {
	data   databaseSnapshot
	closed bool
}

func (snapshot *fakeSnapshot) Data() databaseSnapshot { return snapshot.data }
func (snapshot *fakeSnapshot) Close(context.Context) error {
	snapshot.closed = true
	return nil
}

type fakeCommands struct {
	dumpError  error
	dumpBytes  []byte
	listed     bool
	pgpassPath string
}

func (commands *fakeCommands) Dump(_ context.Context, config CreateConfig, snapshotID, dumpPath, pgpassPath string) error {
	if snapshotID == "" {
		return errors.New("snapshot id missing")
	}
	info, err := os.Lstat(pgpassPath)
	if err != nil {
		return errors.New("pgpass missing during dump")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		filepath.Dir(pgpassPath) != config.RuntimeRoot || filepath.Base(pgpassPath) != backupPGPassFilename {
		return errors.New("pgpass escaped the private runtime root")
	}
	commands.pgpassPath = pgpassPath
	if commands.dumpError != nil {
		return commands.dumpError
	}
	return os.WriteFile(dumpPath, commands.dumpBytes, 0o600)
}

func (commands *fakeCommands) ListDump(context.Context, ToolConfig, string) error {
	commands.listed = true
	return nil
}

func (*fakeCommands) Restore(context.Context, RestoreConfig, string, string) error { return nil }

func TestCreatePublishesOneBoundAndVerifiableBundle(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	snapshot := &fakeSnapshot{data: databaseSnapshot{
		ID:                             "00000003-0000001B-1",
		Artifacts:                      []ArtifactDescriptor{artifact},
		Migrations:                     testMigrations(),
		KnowledgeCatalogPublicationIDs: []int64{1},
		KnowledgeCatalogPublications:   []catalogpublication.Receipt{testCatalogPublication(t, 1)},
		RecommendationModel:            testRecommendationModelDescriptor(t),
	}}
	commands := &fakeCommands{dumpBytes: []byte("fake custom dump fixture")}
	result, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  func() time.Time { return time.Date(2026, 7, 11, 1, 2, 3, 987, time.FixedZone("test", 8*60*60)) },
		random: bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7}),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			return snapshot, nil
		},
		commands: commands,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupID != "backup-20260710T170203Z-0001020304050607" {
		t.Fatalf("backup id = %q", result.BackupID)
	}
	if !snapshot.closed || !commands.listed || result.ArtifactCount != 1 || result.CatalogReceiptCount != 1 ||
		!sha256Pattern.MatchString(result.ManifestSHA256) {
		t.Fatalf("result=%#v snapshot.closed=%v commands.listed=%v", result, snapshot.closed, commands.listed)
	}
	if commands.pgpassPath != filepath.Join(config.RuntimeRoot, backupPGPassFilename) {
		t.Fatalf("pgpass path = %q", commands.pgpassPath)
	}
	runtimeEntries, err := os.ReadDir(config.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeEntries) != 0 {
		t.Fatalf("runtime root retained entries: %#v", runtimeEntries)
	}
	entries, err := os.ReadDir(config.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != result.BackupID {
		t.Fatalf("backup root entries = %#v", entries)
	}
	bundleInfo, err := os.Lstat(result.BundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bundleInfo.IsDir() || bundleInfo.Mode().Perm() != backupBundleMode {
		t.Fatalf("published bundle mode = %04o", bundleInfo.Mode().Perm())
	}
	for _, name := range []string{
		ArtifactArchiveFilename,
		CatalogReceiptArchiveFilename,
		DatabaseDumpFilename,
		ManifestDigestFilename,
		ManifestFilename,
	} {
		info, err := os.Lstat(filepath.Join(result.BundlePath, name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != backupBundleFileMode {
			t.Fatalf("published %s mode = %04o", name, info.Mode().Perm())
		}
	}
	verified, err := verifyWithExecutor(context.Background(), VerifyConfig{
		BackupRoot:     config.BackupRoot,
		CommandTimeout: time.Minute,
		Tools:          config.Tools,
	}, result.BackupID, commands)
	if err != nil {
		t.Fatal(err)
	}
	if verified.ManifestSHA256 != result.ManifestSHA256 || verified.ArtifactCount != 1 || verified.CatalogReceiptCount != 1 {
		t.Fatalf("verified = %#v", verified)
	}
	manifest, _, err := loadManifest(result.BundlePath, result.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.CreatedAt.Equal(time.Date(2026, 7, 10, 17, 2, 3, 0, time.UTC)) || manifest.Artifacts.TotalBytes != artifact.SizeBytes {
		t.Fatalf("manifest = %#v", manifest)
	}
	if len(manifest.Database.KnowledgeCatalogPublicationIDs) != 1 ||
		manifest.Database.KnowledgeCatalogPublicationIDs[0] != "1" ||
		manifest.CatalogPublicationReceipts.Count != 1 ||
		manifest.CatalogPublicationReceipts.Entries[0].PublicationID != "1" {
		t.Fatalf("catalog receipt manifest = %#v", manifest.CatalogPublicationReceipts)
	}
}

func TestCreateFailureRemovesStagingAndNeverPublishes(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	snapshot := &fakeSnapshot{data: testDatabaseSnapshot(t, "snapshot", artifact)}
	_, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  func() time.Time { return time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC) },
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			return snapshot, nil
		},
		commands: &fakeCommands{dumpError: errors.New("injected pg_dump failure")},
	})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("error = %v", err)
	}
	entries, readErr := os.ReadDir(config.BackupRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 || !snapshot.closed {
		t.Fatalf("entries=%v snapshot.closed=%v", entries, snapshot.closed)
	}
	runtimeEntries, readErr := os.ReadDir(config.RuntimeRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(runtimeEntries) != 0 {
		t.Fatalf("failed backup retained runtime entries: %#v", runtimeEntries)
	}
}

func TestCreateRejectsSnapshotWithoutCatalogPublicationBeforeDump(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	snapshotData := testDatabaseSnapshot(t, "snapshot", artifact)
	snapshotData.KnowledgeCatalogPublicationIDs = nil
	commands := &fakeCommands{dumpBytes: []byte("dump must not run")}
	_, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  time.Now,
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			return &fakeSnapshot{data: snapshotData}, nil
		},
		commands: commands,
	})
	if err == nil || !strings.Contains(err.Error(), "at least one immutable knowledge catalog publication") {
		t.Fatalf("error = %v", err)
	}
	if commands.pgpassPath != "" {
		t.Fatalf("database dump ran with pgpass %q", commands.pgpassPath)
	}
}

func TestCreateRemovesExactCrashStagingBeforeOpeningSnapshot(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	stale := filepath.Join(config.BackupRoot, ".incoming-backup-20260709T010203Z-1111111111111111")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "partial.dump"), []byte("crash residue"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  func() time.Time { return time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC) },
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale staging still exists when snapshot opens: %v", err)
			}
			return &fakeSnapshot{data: testDatabaseSnapshot(t, "snapshot", artifact)}, nil
		},
		commands: &fakeCommands{dumpBytes: []byte("dump")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateRejectsUnsafeCrashStagingWithoutRemovingIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		create func(*testing.T, string, string)
	}{
		{"malformed name", func(t *testing.T, root, _ string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, ".incoming-backup-invalid"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"regular file", func(t *testing.T, root, name string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, name), []byte("unsafe"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"symbolic link", func(t *testing.T, root, name string) {
			t.Helper()
			target := t.TempDir()
			if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong mode", func(t *testing.T, root, name string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			zstd := requireZstd(t)
			config, _ := testCreateConfig(t, zstd)
			name := ".incoming-backup-20260709T010203Z-1111111111111111"
			test.create(t, config.BackupRoot, name)
			safeStale := filepath.Join(config.BackupRoot, ".incoming-backup-20260708T010203Z-2222222222222222")
			if err := os.Mkdir(safeStale, 0o700); err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(config.BackupRoot, name)
			if test.name == "malformed name" {
				candidate = filepath.Join(config.BackupRoot, ".incoming-backup-invalid")
			}
			_, err := createWithDependencies(context.Background(), config, createDependencies{
				clock:  time.Now,
				random: bytes.NewReader(make([]byte, 8)),
				openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
					t.Fatal("snapshot opened after unsafe staging drift")
					return nil, nil
				},
				commands: &fakeCommands{},
			})
			if err == nil || !strings.Contains(err.Error(), "unsafe backup staging") {
				t.Fatalf("error = %v", err)
			}
			if _, err := os.Lstat(candidate); err != nil {
				t.Fatalf("unsafe staging entry was removed: %v", err)
			}
			if _, err := os.Lstat(safeStale); err != nil {
				t.Fatalf("safe staging was removed before unsafe drift rejection: %v", err)
			}
		})
	}
}

func TestCreateRejectsPreexistingRuntimePGPass(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, _ := testCreateConfig(t, zstd)
	pgpass := filepath.Join(config.RuntimeRoot, backupPGPassFilename)
	if err := os.WriteFile(pgpass, []byte("crash residue"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  time.Now,
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			t.Fatal("snapshot opened with a stale runtime credential")
			return nil, nil
		},
		commands: &fakeCommands{},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Lstat(pgpass); err != nil {
		t.Fatalf("stale runtime pgpass was silently removed: %v", err)
	}
}

func TestVerifyRejectsPayloadTamperingBeforeArchiveDecode(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	commands := &fakeCommands{dumpBytes: []byte("dump")}
	result, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  func() time.Time { return time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC) },
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			return &fakeSnapshot{data: testDatabaseSnapshot(t, "snapshot", artifact)}, nil
		},
		commands: commands,
	})
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(result.BundlePath, ArtifactArchiveFilename)
	archive, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write([]byte("tamper")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = verifyWithExecutor(context.Background(), VerifyConfig{BackupRoot: config.BackupRoot, CommandTimeout: time.Minute, Tools: config.Tools}, result.BackupID, commands)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyRejectsPrivateModeAfterBundlePublication(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	commands := &fakeCommands{dumpBytes: []byte("dump")}
	result, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  func() time.Time { return time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC) },
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			return &fakeSnapshot{data: testDatabaseSnapshot(t, "snapshot", artifact)}, nil
		},
		commands: commands,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(result.BundlePath, ManifestFilename), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = verifyWithExecutor(context.Background(), VerifyConfig{
		BackupRoot: config.BackupRoot, CommandTimeout: time.Minute, Tools: config.Tools,
	}, result.BackupID, commands)
	if err == nil || !strings.Contains(err.Error(), "0640") {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateRejectsBackupRootWithoutReaderGroupTraversal(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	config, artifact := testCreateConfig(t, zstd)
	if err := os.Chmod(config.BackupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := createWithDependencies(context.Background(), config, createDependencies{
		clock:  func() time.Time { return time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC) },
		random: bytes.NewReader(make([]byte, 8)),
		openSnapshot: func(context.Context, CreateConfig) (snapshotHandle, error) {
			return &fakeSnapshot{data: testDatabaseSnapshot(t, "snapshot", artifact)}, nil
		},
		commands: &fakeCommands{dumpBytes: []byte("dump")},
	})
	if err == nil || !strings.Contains(err.Error(), "0750") {
		t.Fatalf("error = %v", err)
	}
}

func testCreateConfig(t *testing.T, zstd string) (CreateConfig, ArtifactDescriptor) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(root, "backups")
	artifactRoot := filepath.Join(root, "artifacts")
	catalogReceiptRoot := filepath.Join(root, "catalog-receipts")
	runtimeRoot := filepath.Join(root, "runtime")
	for _, directory := range []string{backupRoot, artifactRoot, catalogReceiptRoot, runtimeRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(backupRoot, backupRootMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(artifactRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(catalogReceiptRoot, catalogReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	receipt := testCatalogReceiptBytes(t, 1)
	if err := os.WriteFile(filepath.Join(catalogReceiptRoot, "1.json"), receipt, catalogReceiptFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(catalogReceiptRoot, "1.json"), catalogReceiptFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(artifactRoot, "sha256"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(artifactRoot, "sha256"), 0o750); err != nil {
		t.Fatal(err)
	}
	contents := []byte("immutable artifact fixture")
	digest := sha256.Sum256(contents)
	digestHex := hex.EncodeToString(digest[:])
	prefix := filepath.Join(artifactRoot, "sha256", digestHex[:2])
	if err := os.Mkdir(prefix, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(prefix, 0o750); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(prefix, digestHex)
	if err := os.WriteFile(artifactPath, contents, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(artifactPath, 0o640); err != nil {
		t.Fatal(err)
	}
	return CreateConfig{
		DatabaseURL:        "postgresql://ascendany_backup_login@127.0.0.1:5432/ascendany_v2?sslmode=disable",
		DatabasePassword:   strings.Repeat("p", 24),
		ArtifactRoot:       artifactRoot,
		CatalogReceiptRoot: catalogReceiptRoot,
		BackupRoot:         backupRoot,
		RuntimeRoot:        runtimeRoot,
		RetainDaily:        14,
		RetainWeekly:       8,
		ConnectTimeout:     time.Second,
		CommandTimeout:     time.Minute,
		Tools: ToolConfig{
			PGDump:    "/usr/bin/pg_dump",
			PGRestore: "/usr/bin/pg_restore",
			Zstd:      zstd,
		},
	}, ArtifactDescriptor{SHA256: digestHex, SizeBytes: int64(len(contents)), StorageKey: "sha256/" + digestHex[:2] + "/" + digestHex}
}

func testDatabaseSnapshot(t *testing.T, id string, artifact ArtifactDescriptor) databaseSnapshot {
	t.Helper()
	return databaseSnapshot{
		ID:                             id,
		Artifacts:                      []ArtifactDescriptor{artifact},
		Migrations:                     testMigrations(),
		KnowledgeCatalogPublicationIDs: []int64{1},
		KnowledgeCatalogPublications:   []catalogpublication.Receipt{testCatalogPublication(t, 1)},
		RecommendationModel:            testRecommendationModelDescriptor(t),
	}
}

func testMigrations() []MigrationDescriptor {
	return []MigrationDescriptor{{Version: 1, Name: "fresh_schema", SHA256: strings.Repeat("a", 64)}}
}

func requireZstd(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("zstd")
	if err != nil {
		t.Skip("zstd is not installed")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
