package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func RestoreVerify(ctx context.Context, config RestoreConfig, backupID string) (result RestoreResult, resultErr error) {
	if ctx == nil {
		return RestoreResult{}, errors.New("context is required")
	}
	if err := validateBundleID(backupID); err != nil {
		return RestoreResult{}, err
	}
	if err := validateRestoreRuntimeConfig(config, backupID); err != nil {
		return RestoreResult{}, err
	}
	runtimeRoot, err := openOwnedRuntimeRoot(config.RuntimeRoot)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore runtime root rejected: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, runtimeRoot.Close())
	}()
	if err := requireRuntimeFileAbsent(runtimeRoot, restorePGPassFilename); err != nil {
		return RestoreResult{}, err
	}
	verifyResult, err := Verify(ctx, VerifyConfig{
		BackupRoot:     config.BackupRoot,
		CommandTimeout: config.CommandTimeout,
		Tools:          config.Tools,
	}, backupID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup pre-restore verification failed: %w", err)
	}
	bundlePath := filepath.Join(config.BackupRoot, backupID)
	manifest, _, err := loadManifest(bundlePath, backupID)
	if err != nil {
		return RestoreResult{}, err
	}
	publicationIDs, err := parseKnowledgeCatalogPublicationIDs(manifest.Database.KnowledgeCatalogPublicationIDs)
	if err != nil {
		return RestoreResult{}, err
	}
	if _, err := os.Lstat(config.ArtifactRoot); err == nil {
		return RestoreResult{}, errors.New("restore artifact root must not already exist")
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreResult{}, errors.New("inspect restore artifact root")
	}
	artifactParent := filepath.Dir(config.ArtifactRoot)
	if err := validateExistingDirectory(artifactParent, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("restore artifact parent rejected: %w", err)
	}
	if _, err := os.Lstat(config.CatalogReceiptRoot); err == nil {
		return RestoreResult{}, errors.New("restore catalog publication receipt root must not already exist")
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreResult{}, errors.New("inspect restore catalog publication receipt root")
	}
	catalogReceiptParent := filepath.Dir(config.CatalogReceiptRoot)
	if err := validateExistingDirectory(catalogReceiptParent, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("restore catalog publication receipt parent rejected: %w", err)
	}
	connection, err := connectRestoreDatabase(ctx, config)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := requireFreshRestoreDatabase(ctx, connection); err != nil {
		connection.Close(context.Background())
		return RestoreResult{}, err
	}
	if err := connection.Close(ctx); err != nil {
		return RestoreResult{}, errors.New("close restore preflight connection")
	}

	operationContext, cancel := context.WithTimeout(ctx, config.CommandTimeout)
	defer cancel()
	stagingRoot := filepath.Join(artifactParent, ".restore-"+backupID)
	if err := os.Mkdir(stagingRoot, 0o750); err != nil {
		return RestoreResult{}, errors.New("create restore artifact staging root")
	}
	if err := os.Chmod(stagingRoot, 0o750); err != nil {
		_ = secureRemoveAll(stagingRoot, artifactParent)
		return RestoreResult{}, errors.New("set restore artifact staging mode")
	}
	publishedArtifacts := false
	defer func() {
		if !publishedArtifacts {
			resultErr = errors.Join(resultErr, secureRemoveAll(stagingRoot, artifactParent))
		}
	}()
	staging, err := os.OpenRoot(stagingRoot)
	if err != nil {
		return RestoreResult{}, errors.New("open restore artifact staging root")
	}
	extractErr := verifyOrExtractArtifactArchive(
		operationContext,
		config.Tools.Zstd,
		filepath.Join(bundlePath, ArtifactArchiveFilename),
		manifest.Artifacts,
		staging,
	)
	closeErr := staging.Close()
	if extractErr != nil || closeErr != nil {
		return RestoreResult{}, errors.Join(extractErr, closeErr)
	}
	if err := syncRestoredArtifactTree(stagingRoot, manifest.Artifacts.Entries); err != nil {
		return RestoreResult{}, err
	}
	catalogReceiptStagingRoot := filepath.Join(catalogReceiptParent, ".restore-catalog-receipts-"+backupID)
	if err := os.Mkdir(catalogReceiptStagingRoot, catalogReceiptDirectoryMode); err != nil {
		return RestoreResult{}, errors.New("create restore catalog publication receipt staging root")
	}
	if err := os.Chmod(catalogReceiptStagingRoot, catalogReceiptDirectoryMode); err != nil {
		_ = secureRemoveAll(catalogReceiptStagingRoot, catalogReceiptParent)
		return RestoreResult{}, errors.New("set restore catalog publication receipt staging mode")
	}
	publishedCatalogReceipts := false
	defer func() {
		if !publishedCatalogReceipts {
			resultErr = errors.Join(
				resultErr,
				secureRemoveAll(catalogReceiptStagingRoot, catalogReceiptParent),
			)
		}
	}()
	catalogReceiptStaging, err := os.OpenRoot(catalogReceiptStagingRoot)
	if err != nil {
		return RestoreResult{}, errors.New("open restore catalog publication receipt staging root")
	}
	extractErr = verifyOrExtractCatalogReceiptArchive(
		operationContext,
		config.Tools.Zstd,
		filepath.Join(bundlePath, CatalogReceiptArchiveFilename),
		manifest.CatalogPublicationReceipts,
		publicationIDs,
		catalogReceiptStaging,
	)
	closeErr = catalogReceiptStaging.Close()
	if extractErr != nil || closeErr != nil {
		return RestoreResult{}, errors.Join(extractErr, closeErr)
	}
	if err := verifyCatalogReceiptRoot(
		catalogReceiptStagingRoot,
		manifest.CatalogPublicationReceipts.Entries,
		publicationIDs,
	); err != nil {
		return RestoreResult{}, fmt.Errorf("restored catalog publication receipt staging root rejected: %w", err)
	}
	if err := syncDirectory(catalogReceiptStagingRoot); err != nil {
		return RestoreResult{}, errors.New("sync restored catalog publication receipt root")
	}
	if err := os.Rename(stagingRoot, config.ArtifactRoot); err != nil {
		return RestoreResult{}, errors.New("atomically publish restored artifact root")
	}
	publishedArtifacts = true
	rollbackArtifacts := true
	defer func() {
		if rollbackArtifacts {
			resultErr = errors.Join(resultErr, secureRemoveAll(config.ArtifactRoot, artifactParent))
		}
	}()
	if err := os.Rename(catalogReceiptStagingRoot, config.CatalogReceiptRoot); err != nil {
		return RestoreResult{}, errors.New("atomically publish restored catalog publication receipt root")
	}
	publishedCatalogReceipts = true
	rollbackCatalogReceipts := true
	defer func() {
		if rollbackCatalogReceipts {
			resultErr = errors.Join(
				resultErr,
				secureRemoveAll(config.CatalogReceiptRoot, catalogReceiptParent),
			)
		}
	}()
	if err := syncDirectory(artifactParent); err != nil {
		return RestoreResult{}, errors.New("sync restored artifact parent")
	}
	if err := syncDirectory(catalogReceiptParent); err != nil {
		return RestoreResult{}, errors.New("sync restored catalog publication receipt parent")
	}

	pgpassPath := filepath.Join(config.RuntimeRoot, restorePGPassFilename)
	if err := writePGPass(runtimeRoot, restorePGPassFilename, config.DatabaseURL, config.DatabasePassword); err != nil {
		return RestoreResult{}, err
	}
	pgpassPresent := true
	defer func() {
		if pgpassPresent {
			resultErr = errors.Join(resultErr, removePrivateRuntimeFile(runtimeRoot, restorePGPassFilename))
		}
	}()
	commands := systemCommandExecutor{}
	if err := commands.Restore(operationContext, config, filepath.Join(bundlePath, DatabaseDumpFilename), pgpassPath); err != nil {
		return RestoreResult{}, err
	}
	// pg_restore uses one transaction. Once it commits, keep both immutable
	// filesystem roots even if the subsequent evidence gate reports drift so
	// the operator never receives a restored database with deliberately removed
	// files.
	rollbackArtifacts = false
	rollbackCatalogReceipts = false
	err = removePrivateRuntimeFile(runtimeRoot, restorePGPassFilename)
	pgpassPresent = false
	if err != nil {
		return RestoreResult{}, err
	}
	connection, err = connectRestoreDatabase(operationContext, config)
	if err != nil {
		return RestoreResult{}, err
	}
	defer connection.Close(context.Background())
	if err := reconstructDumpOmittedRowTypeACLs(operationContext, connection); err != nil {
		return RestoreResult{}, fmt.Errorf("post-restore ACL reconstruction failed: %w", err)
	}
	if err := verifyRestoredDatabase(
		operationContext,
		connection,
		manifest,
		config.ArtifactRoot,
		config.CatalogReceiptRoot,
	); err != nil {
		return RestoreResult{}, fmt.Errorf("post-restore gate failed: %w", err)
	}
	return RestoreResult{
		BackupID:            backupID,
		ManifestSHA256:      verifyResult.ManifestSHA256,
		ArtifactCount:       manifest.Artifacts.Count,
		CatalogReceiptCount: manifest.CatalogPublicationReceipts.Count,
		DatabaseName:        RestoreDatabaseName,
		ArtifactRoot:        config.ArtifactRoot,
		CatalogReceiptRoot:  config.CatalogReceiptRoot,
		RecommendationModel: manifest.Database.RecommendationModel,
	}, nil
}

func validateRestoreRuntimeConfig(config RestoreConfig, backupID string) error {
	if err := validateDatabaseURL(config.DatabaseURL, RestoreDatabaseName, RestoreDatabaseLogin); err != nil {
		return fmt.Errorf("restore database URL rejected: %w", err)
	}
	if len(config.DatabasePassword) < minimumDatabasePassword {
		return errors.New("restore database password is too short")
	}
	if config.ConnectTimeout <= 0 || config.CommandTimeout <= 0 {
		return errors.New("restore timeouts must be positive")
	}
	if config.BackupRoot == "" || config.ArtifactRoot == "" || config.CatalogReceiptRoot == "" || config.RuntimeRoot == "" ||
		pathsOverlap(config.BackupRoot, config.ArtifactRoot) ||
		pathsOverlap(config.BackupRoot, config.CatalogReceiptRoot) ||
		pathsOverlap(config.ArtifactRoot, config.CatalogReceiptRoot) ||
		pathsOverlap(config.RuntimeRoot, config.BackupRoot) ||
		pathsOverlap(config.RuntimeRoot, config.ArtifactRoot) ||
		pathsOverlap(config.RuntimeRoot, config.CatalogReceiptRoot) {
		return errors.New("restore paths must be absolute and disjoint")
	}
	if !isCleanAbsoluteBelowRoot(config.BackupRoot) || !isCleanAbsoluteBelowRoot(config.ArtifactRoot) ||
		!isCleanAbsoluteBelowRoot(config.CatalogReceiptRoot) || !isCleanAbsoluteBelowRoot(config.RuntimeRoot) {
		return errors.New("restore paths must be clean absolute paths")
	}
	if filepath.Base(config.RuntimeRoot) != "ascendany-restore-verify-"+backupID {
		return errors.New("restore runtime root must match the backup instance")
	}
	if filepath.Base(config.ArtifactRoot) == ".restore-"+backupID ||
		filepath.Base(config.CatalogReceiptRoot) == ".restore-catalog-receipts-"+backupID {
		return errors.New("restore destination roots must not use reserved staging names")
	}
	if config.Tools.PGRestore == "" || config.Tools.Zstd == "" {
		return errors.New("restore tool paths are required")
	}
	if !isCleanAbsolute(config.Tools.PGRestore) || !isCleanAbsolute(config.Tools.Zstd) {
		return errors.New("restore tool paths must be clean absolute paths")
	}
	return nil
}

func syncRestoredArtifactTree(rootPath string, artifacts []ArtifactDescriptor) error {
	prefixes := make(map[string]struct{})
	for _, artifact := range artifacts {
		prefixes[artifact.SHA256[:2]] = struct{}{}
	}
	for prefix := range prefixes {
		if err := syncDirectory(filepath.Join(rootPath, "sha256", prefix)); err != nil {
			return errors.New("sync restored artifact prefix")
		}
	}
	if err := syncDirectory(filepath.Join(rootPath, "sha256")); err != nil {
		return errors.New("sync restored sha256 namespace")
	}
	if err := syncDirectory(rootPath); err != nil {
		return errors.New("sync restored artifact root")
	}
	return nil
}
