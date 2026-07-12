package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type snapshotHandle interface {
	Data() databaseSnapshot
	Close(context.Context) error
}

type createDependencies struct {
	clock        func() time.Time
	random       io.Reader
	openSnapshot func(context.Context, CreateConfig) (snapshotHandle, error)
	commands     commandExecutor
}

func Create(ctx context.Context, config CreateConfig) (CreateResult, error) {
	return createWithDependencies(ctx, config, createDependencies{
		clock:  time.Now,
		random: defaultRandomReader(),
		openSnapshot: func(ctx context.Context, config CreateConfig) (snapshotHandle, error) {
			return openDatabaseSnapshot(ctx, config)
		},
		commands: systemCommandExecutor{},
	})
}

func createWithDependencies(ctx context.Context, config CreateConfig, dependencies createDependencies) (result CreateResult, resultErr error) {
	if ctx == nil {
		return CreateResult{}, errors.New("context is required")
	}
	if dependencies.clock == nil || dependencies.random == nil || dependencies.openSnapshot == nil || dependencies.commands == nil {
		return CreateResult{}, errors.New("backup dependencies are incomplete")
	}
	if err := validateCreateRuntimeConfig(config); err != nil {
		return CreateResult{}, err
	}
	if err := validateExistingDirectory(config.BackupRoot, backupRootMode); err != nil {
		return CreateResult{}, fmt.Errorf("backup root rejected: %w", err)
	}
	runtimeRoot, err := openOwnedRuntimeRoot(config.RuntimeRoot)
	if err != nil {
		return CreateResult{}, fmt.Errorf("backup runtime root rejected: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, runtimeRoot.Close())
	}()
	if err := requireRuntimeFileAbsent(runtimeRoot, backupPGPassFilename); err != nil {
		return CreateResult{}, err
	}
	if err := cleanupStaleBackupStaging(config.BackupRoot); err != nil {
		return CreateResult{}, err
	}
	now := dependencies.clock().UTC().Truncate(time.Second)
	backupID, err := newBundleID(now, dependencies.random)
	if err != nil {
		return CreateResult{}, err
	}
	stagingPath := filepath.Join(config.BackupRoot, ".incoming-"+backupID)
	bundlePath := filepath.Join(config.BackupRoot, backupID)
	if _, err := os.Lstat(bundlePath); err == nil {
		return CreateResult{}, errors.New("generated backup id already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return CreateResult{}, errors.New("inspect target backup bundle")
	}
	if err := os.Mkdir(stagingPath, 0o700); err != nil {
		return CreateResult{}, errors.New("create backup staging directory")
	}
	if err := os.Chmod(stagingPath, 0o700); err != nil {
		_ = secureRemoveAll(stagingPath, config.BackupRoot)
		return CreateResult{}, errors.New("set backup staging directory mode")
	}
	published := false
	defer func() {
		if !published {
			resultErr = errors.Join(resultErr, secureRemoveAll(stagingPath, config.BackupRoot))
		}
	}()

	operationContext, cancel := context.WithTimeout(ctx, config.CommandTimeout)
	defer cancel()
	snapshot, err := dependencies.openSnapshot(operationContext, config)
	if err != nil {
		return CreateResult{}, err
	}
	snapshotOpen := true
	defer func() {
		if snapshotOpen {
			resultErr = errors.Join(resultErr, snapshot.Close(context.Background()))
		}
	}()
	snapshotData := snapshot.Data()
	if snapshotData.ID == "" {
		return CreateResult{}, errors.New("database returned an empty exported snapshot id")
	}
	if err := validateArtifactList(snapshotData.Artifacts); err != nil {
		return CreateResult{}, fmt.Errorf("artifact snapshot rejected: %w", err)
	}
	if err := validateMigrations(snapshotData.Migrations); err != nil {
		return CreateResult{}, fmt.Errorf("migration snapshot rejected: %w", err)
	}

	pgpassPath := filepath.Join(config.RuntimeRoot, backupPGPassFilename)
	if err := writePGPass(runtimeRoot, backupPGPassFilename, config.DatabaseURL, config.DatabasePassword); err != nil {
		return CreateResult{}, err
	}
	pgpassPresent := true
	defer func() {
		if pgpassPresent {
			resultErr = errors.Join(resultErr, removePrivateRuntimeFile(runtimeRoot, backupPGPassFilename))
		}
	}()
	dumpPath := filepath.Join(stagingPath, DatabaseDumpFilename)
	dumpFile, err := createPrivateFile(dumpPath)
	if err != nil {
		return CreateResult{}, errors.New("create database dump file")
	}
	if err := syncAndClose(dumpFile); err != nil {
		return CreateResult{}, errors.New("sync empty database dump file")
	}
	if err := dependencies.commands.Dump(operationContext, config, snapshotData.ID, dumpPath, pgpassPath); err != nil {
		return CreateResult{}, err
	}
	if err := syncExistingFile(dumpPath); err != nil {
		return CreateResult{}, errors.New("sync database dump")
	}
	err = removePrivateRuntimeFile(runtimeRoot, backupPGPassFilename)
	pgpassPresent = false
	if err != nil {
		return CreateResult{}, err
	}
	if err := dependencies.commands.ListDump(operationContext, config.Tools, dumpPath); err != nil {
		return CreateResult{}, err
	}
	dumpSHA, dumpSize, err := fileDigest(dumpPath)
	if err != nil {
		return CreateResult{}, errors.New("hash database dump")
	}
	if dumpSize <= 0 {
		return CreateResult{}, errors.New("database dump is empty")
	}
	if _, err := validateRegularFile(dumpPath, 0o600); err != nil {
		return CreateResult{}, fmt.Errorf("database dump rejected: %w", err)
	}

	archiveDescriptor, totalArtifactBytes, err := createArtifactArchive(
		operationContext,
		config.Tools.Zstd,
		config.ArtifactRoot,
		filepath.Join(stagingPath, ArtifactArchiveFilename),
		snapshotData.Artifacts,
	)
	if err != nil {
		return CreateResult{}, err
	}
	if err := snapshot.Close(operationContext); err != nil {
		return CreateResult{}, errors.New("close database snapshot")
	}
	snapshotOpen = false

	manifest := Manifest{
		Schema:    BundleSchema,
		BackupID:  backupID,
		CreatedAt: now,
		Database: DatabaseSnapshotDescriptor{
			DatabaseName: SourceDatabaseName,
			File: FileDescriptor{
				Filename:  DatabaseDumpFilename,
				Format:    "postgresql-custom",
				SHA256:    dumpSHA,
				SizeBytes: dumpSize,
			},
			Migrations: append([]MigrationDescriptor(nil), snapshotData.Migrations...),
		},
		Artifacts: ArtifactSnapshotDescriptor{
			File:       archiveDescriptor,
			Count:      len(snapshotData.Artifacts),
			TotalBytes: totalArtifactBytes,
			Entries:    append([]ArtifactDescriptor(nil), snapshotData.Artifacts...),
		},
	}
	if err := validateManifest(manifest, backupID); err != nil {
		return CreateResult{}, fmt.Errorf("generated manifest rejected: %w", err)
	}
	manifestSHA, err := writeManifest(stagingPath, manifest)
	if err != nil {
		return CreateResult{}, err
	}
	if err := prepareBundleForPublication(stagingPath); err != nil {
		return CreateResult{}, fmt.Errorf("prepare backup bundle for publication: %w", err)
	}
	if err := syncDirectory(stagingPath); err != nil {
		return CreateResult{}, errors.New("sync backup staging directory")
	}
	if err := os.Rename(stagingPath, bundlePath); err != nil {
		return CreateResult{}, errors.New("atomically publish backup bundle")
	}
	published = true
	if err := syncDirectory(config.BackupRoot); err != nil {
		return CreateResult{}, errors.New("sync backup root after publish")
	}
	if err := applyRetention(config.BackupRoot, config.RetainDaily, config.RetainWeekly, backupID); err != nil {
		return CreateResult{}, fmt.Errorf("apply backup retention: %w", err)
	}
	return CreateResult{
		BackupID:       backupID,
		BundlePath:     bundlePath,
		ManifestSHA256: manifestSHA,
		ArtifactCount:  len(snapshotData.Artifacts),
	}, nil
}

func writeManifest(directory string, manifest Manifest) (string, error) {
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", errors.New("encode backup manifest")
	}
	contents = append(contents, '\n')
	digest := sha256.Sum256(contents)
	digestHex := hex.EncodeToString(digest[:])
	manifestFile, err := createPrivateFile(filepath.Join(directory, ManifestFilename))
	if err != nil {
		return "", errors.New("create backup manifest")
	}
	if _, err := manifestFile.Write(contents); err != nil {
		_ = manifestFile.Close()
		return "", errors.New("write backup manifest")
	}
	if err := syncAndClose(manifestFile); err != nil {
		return "", errors.New("sync backup manifest")
	}
	digestFile, err := createPrivateFile(filepath.Join(directory, ManifestDigestFilename))
	if err != nil {
		return "", errors.New("create manifest digest")
	}
	if _, err := digestFile.WriteString(digestHex + "  " + ManifestFilename + "\n"); err != nil {
		_ = digestFile.Close()
		return "", errors.New("write manifest digest")
	}
	if err := syncAndClose(digestFile); err != nil {
		return "", errors.New("sync manifest digest")
	}
	return digestHex, nil
}

func validateCreateRuntimeConfig(config CreateConfig) error {
	if err := validateDatabaseURL(config.DatabaseURL, SourceDatabaseName, SourceDatabaseLogin); err != nil {
		return fmt.Errorf("database URL rejected: %w", err)
	}
	if len(config.DatabasePassword) < minimumDatabasePassword {
		return errors.New("database password is too short")
	}
	if config.ConnectTimeout <= 0 || config.CommandTimeout <= 0 {
		return errors.New("backup timeouts must be positive")
	}
	if config.RetainDaily < 0 || config.RetainWeekly < 0 || config.RetainDaily+config.RetainWeekly == 0 {
		return errors.New("backup retention is invalid")
	}
	if pathsOverlap(config.ArtifactRoot, config.BackupRoot) ||
		pathsOverlap(config.RuntimeRoot, config.ArtifactRoot) ||
		pathsOverlap(config.RuntimeRoot, config.BackupRoot) {
		return errors.New("runtime, artifact, and backup roots must be disjoint")
	}
	if !isCleanAbsoluteBelowRoot(config.ArtifactRoot) || !isCleanAbsoluteBelowRoot(config.BackupRoot) ||
		!isCleanAbsoluteBelowRoot(config.RuntimeRoot) {
		return errors.New("runtime, artifact, and backup roots must be clean absolute paths")
	}
	if config.Tools.PGDump == "" || config.Tools.PGRestore == "" || config.Tools.Zstd == "" {
		return errors.New("backup tool paths are required")
	}
	if !isCleanAbsolute(config.Tools.PGDump) || !isCleanAbsolute(config.Tools.PGRestore) || !isCleanAbsolute(config.Tools.Zstd) {
		return errors.New("backup tool paths must be clean absolute paths")
	}
	return nil
}
