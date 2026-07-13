package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maximumManifestBytes = 64 << 20

func Verify(ctx context.Context, config VerifyConfig, backupID string) (VerifyResult, error) {
	return verifyWithExecutor(ctx, config, backupID, systemCommandExecutor{})
}

func verifyWithExecutor(ctx context.Context, config VerifyConfig, backupID string, commands commandExecutor) (VerifyResult, error) {
	if ctx == nil {
		return VerifyResult{}, errors.New("context is required")
	}
	if commands == nil {
		return VerifyResult{}, errors.New("command executor is required")
	}
	if err := validateBundleID(backupID); err != nil {
		return VerifyResult{}, err
	}
	if err := validateExistingDirectory(config.BackupRoot, backupRootMode); err != nil {
		return VerifyResult{}, fmt.Errorf("backup root rejected: %w", err)
	}
	if config.CommandTimeout <= 0 || config.Tools.PGRestore == "" || config.Tools.Zstd == "" {
		return VerifyResult{}, errors.New("verification configuration is incomplete")
	}
	if !isCleanAbsoluteBelowRoot(config.BackupRoot) || !isCleanAbsolute(config.Tools.PGRestore) || !isCleanAbsolute(config.Tools.Zstd) {
		return VerifyResult{}, errors.New("verification paths must be clean and absolute")
	}
	bundlePath := filepath.Join(config.BackupRoot, backupID)
	manifest, manifestSHA, err := loadManifest(bundlePath, backupID)
	if err != nil {
		return VerifyResult{}, err
	}
	operationContext, cancel := context.WithTimeout(ctx, config.CommandTimeout)
	defer cancel()
	if err := verifyBundlePayload(operationContext, config.Tools, bundlePath, manifest, commands); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{
		BackupID:            backupID,
		BundlePath:          bundlePath,
		ManifestSHA256:      manifestSHA,
		ArtifactCount:       manifest.Artifacts.Count,
		CatalogReceiptCount: manifest.CatalogPublicationReceipts.Count,
	}, nil
}

func loadManifest(bundlePath, expectedID string) (Manifest, string, error) {
	if err := validateExistingDirectory(bundlePath, backupBundleMode); err != nil {
		return Manifest{}, "", fmt.Errorf("backup bundle rejected: %w", err)
	}
	root, err := os.OpenRoot(bundlePath)
	if err != nil {
		return Manifest{}, "", errors.New("open backup bundle")
	}
	defer root.Close()
	if err := ensureExactBundleEntries(root); err != nil {
		return Manifest{}, "", err
	}
	manifestBytes, err := readExactBundleFile(root, ManifestFilename, maximumManifestBytes)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("read manifest: %w", err)
	}
	digestBytes, err := readExactBundleFile(root, ManifestDigestFilename, 256)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("read manifest digest: %w", err)
	}
	digest := sha256.Sum256(manifestBytes)
	digestHex := hex.EncodeToString(digest[:])
	recordedDigest, err := parseManifestDigestDocument(digestBytes)
	if err != nil || recordedDigest != digestHex {
		return Manifest{}, "", errors.New("manifest SHA-256 document does not match manifest bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, "", fmt.Errorf("decode backup manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Manifest{}, "", err
	}
	if err := validateManifest(manifest, expectedID); err != nil {
		return Manifest{}, "", fmt.Errorf("backup manifest rejected: %w", err)
	}
	return manifest, digestHex, nil
}

func verifyBundlePayload(ctx context.Context, tools ToolConfig, bundlePath string, manifest Manifest, commands commandExecutor) error {
	publicationIDs, err := parseKnowledgeCatalogPublicationIDs(manifest.Database.KnowledgeCatalogPublicationIDs)
	if err != nil {
		return err
	}
	if err := validateKnowledgeCatalogPublications(manifest.Database.KnowledgeCatalogPublications); err != nil ||
		!equalPublicationIDsAndDescriptors(publicationIDs, manifest.Database.KnowledgeCatalogPublications) {
		return errors.New("knowledge catalog publication descriptors differ from their database identities")
	}
	for _, descriptor := range []FileDescriptor{
		manifest.Database.File,
		manifest.Artifacts.File,
		manifest.CatalogPublicationReceipts.File,
	} {
		path := filepath.Join(bundlePath, descriptor.Filename)
		if _, err := validateRegularFile(path, backupBundleFileMode); err != nil {
			return fmt.Errorf("backup payload %s rejected: %w", descriptor.Filename, err)
		}
		digest, size, err := fileDigest(path)
		if err != nil {
			return fmt.Errorf("hash backup payload %s: %w", descriptor.Filename, err)
		}
		if digest != descriptor.SHA256 || size != descriptor.SizeBytes {
			return fmt.Errorf("backup payload %s does not match its manifest", descriptor.Filename)
		}
	}
	dumpPath := filepath.Join(bundlePath, DatabaseDumpFilename)
	if err := commands.ListDump(ctx, tools, dumpPath); err != nil {
		return err
	}
	if err := verifyOrExtractArtifactArchive(ctx, tools.Zstd, filepath.Join(bundlePath, ArtifactArchiveFilename), manifest.Artifacts, nil); err != nil {
		return fmt.Errorf("verify artifact archive: %w", err)
	}
	if err := verifyOrExtractCatalogReceiptArchive(
		ctx,
		tools.Zstd,
		filepath.Join(bundlePath, CatalogReceiptArchiveFilename),
		manifest.CatalogPublicationReceipts,
		publicationIDs,
		nil,
	); err != nil {
		return fmt.Errorf("verify catalog publication receipt archive: %w", err)
	}
	return nil
}

func validateManifest(manifest Manifest, expectedID string) error {
	if manifest.Schema != BundleSchema {
		return fmt.Errorf("schema must be %s", BundleSchema)
	}
	if err := validateBundleID(manifest.BackupID); err != nil {
		return err
	}
	if manifest.BackupID != expectedID {
		return errors.New("backup id does not match its directory")
	}
	if manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC || manifest.CreatedAt.Nanosecond() != 0 {
		return errors.New("createdAt must be a whole-second UTC timestamp")
	}
	if !strings.HasPrefix(manifest.BackupID, "backup-"+manifest.CreatedAt.Format("20060102T150405Z")+"-") {
		return errors.New("backup id timestamp does not match createdAt")
	}
	if manifest.Database.DatabaseName != SourceDatabaseName {
		return fmt.Errorf("database name must be %s", SourceDatabaseName)
	}
	if err := validateFileDescriptor(manifest.Database.File, DatabaseDumpFilename, "postgresql-custom"); err != nil {
		return fmt.Errorf("database dump: %w", err)
	}
	if err := validateMigrations(manifest.Database.Migrations); err != nil {
		return err
	}
	publicationIDs, err := parseKnowledgeCatalogPublicationIDs(manifest.Database.KnowledgeCatalogPublicationIDs)
	if err != nil {
		return err
	}
	if err := validateRecommendationModelDescriptor(manifest.Database.RecommendationModel); err != nil {
		return fmt.Errorf("recommendation model: %w", err)
	}
	if err := validateFileDescriptor(manifest.Artifacts.File, ArtifactArchiveFilename, "tar+zstd"); err != nil {
		return fmt.Errorf("artifact archive: %w", err)
	}
	if err := validateArtifactList(manifest.Artifacts.Entries); err != nil {
		return err
	}
	if manifest.Artifacts.Count != len(manifest.Artifacts.Entries) {
		return errors.New("artifact count does not match entries")
	}
	totalBytes := int64(0)
	for _, artifact := range manifest.Artifacts.Entries {
		if artifact.SizeBytes > int64(^uint64(0)>>1)-totalBytes {
			return errors.New("artifact total size overflow")
		}
		totalBytes += artifact.SizeBytes
	}
	if totalBytes != manifest.Artifacts.TotalBytes {
		return errors.New("artifact total bytes does not match entries")
	}
	if err := validateFileDescriptor(
		manifest.CatalogPublicationReceipts.File,
		CatalogReceiptArchiveFilename,
		"tar+zstd",
	); err != nil {
		return fmt.Errorf("catalog publication receipt archive: %w", err)
	}
	if err := validateCatalogReceiptSnapshot(
		manifest.CatalogPublicationReceipts,
		publicationIDs,
	); err != nil {
		return err
	}
	if err := validateCatalogReceiptDatabaseBinding(
		manifest.Database.KnowledgeCatalogPublications,
		manifest.CatalogPublicationReceipts,
	); err != nil {
		return err
	}
	return nil
}

func validateFileDescriptor(descriptor FileDescriptor, filename, format string) error {
	if descriptor.Filename != filename || descriptor.Format != format {
		return errors.New("filename or format is invalid")
	}
	if !sha256Pattern.MatchString(descriptor.SHA256) || descriptor.SizeBytes <= 0 {
		return errors.New("digest or size is invalid")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("backup manifest contains trailing JSON")
		}
		return fmt.Errorf("decode backup manifest trailer: %w", err)
	}
	return nil
}

func parseManifestDigestDocument(contents []byte) (string, error) {
	text := string(contents)
	if len(text) != 64+2+len(ManifestFilename)+1 || !strings.HasSuffix(text, "  "+ManifestFilename+"\n") {
		return "", errors.New("manifest digest document is invalid")
	}
	digest := text[:64]
	if !sha256Pattern.MatchString(digest) {
		return "", errors.New("manifest digest is invalid")
	}
	return digest, nil
}
