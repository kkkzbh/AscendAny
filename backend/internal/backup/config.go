package backup

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	BundleSchema             = "ascendany.backup.bundle.v2"
	BackupFormat             = "pg_custom_plus_artifact_and_catalog_receipt_tar_zstd"
	ManifestHashAlgorithm    = "sha256"
	SourceDatabaseName       = "ascendany_v2"
	SourceDatabaseLogin      = "ascendany_backup_login"
	RestoreDatabaseName      = "ascendany_v2_restore_verify"
	RestoreDatabaseLogin     = "ascendany_restore_login"
	RestoreDatabaseRole      = "ascendany_owner"
	BackupRuntimeRoot        = "/run/ascendany-backup"
	RestoreRuntimeRootPrefix = "/run/ascendany-restore-verify-"
	directDatabasePort       = "5432"
	minimumDatabasePassword  = 16
	maximumRetentionCount    = 3660
)

type LookupEnv func(string) (string, bool)
type ReadFile func(string) ([]byte, error)

type ToolConfig struct {
	PGDump    string
	PGRestore string
	Zstd      string
}

type CreateConfig struct {
	DatabaseURL        string
	DatabasePassword   string
	ArtifactRoot       string
	CatalogReceiptRoot string
	BackupRoot         string
	RuntimeRoot        string
	RetainDaily        int
	RetainWeekly       int
	ConnectTimeout     time.Duration
	CommandTimeout     time.Duration
	Tools              ToolConfig
}

type VerifyConfig struct {
	BackupRoot     string
	CommandTimeout time.Duration
	Tools          ToolConfig
}

type RestoreConfig struct {
	BackupRoot         string
	ArtifactRoot       string
	CatalogReceiptRoot string
	RuntimeRoot        string
	DatabaseURL        string
	DatabasePassword   string
	ConnectTimeout     time.Duration
	CommandTimeout     time.Duration
	Tools              ToolConfig
}

func LoadCreateConfig(lookup LookupEnv, readFile ReadFile) (CreateConfig, error) {
	if lookup == nil {
		return CreateConfig{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return CreateConfig{}, errors.New("secret file reader is required")
	}
	databaseURL, err := required(lookup, "ASCENDANY_DATABASE_URL")
	if err != nil {
		return CreateConfig{}, err
	}
	if err := validateDatabaseURL(databaseURL, SourceDatabaseName, SourceDatabaseLogin); err != nil {
		return CreateConfig{}, fmt.Errorf("ASCENDANY_DATABASE_URL: %w", err)
	}
	password, err := loadPassword(lookup, readFile, "ASCENDANY_DATABASE_PASSWORD_FILE")
	if err != nil {
		return CreateConfig{}, err
	}
	artifactRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_ARTIFACT_ROOT")
	if err != nil {
		return CreateConfig{}, err
	}
	catalogReceiptRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_CATALOG_RECEIPT_ROOT")
	if err != nil {
		return CreateConfig{}, err
	}
	backupRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_BACKUP_ROOT")
	if err != nil {
		return CreateConfig{}, err
	}
	if pathsOverlap(artifactRoot, catalogReceiptRoot) || pathsOverlap(artifactRoot, backupRoot) ||
		pathsOverlap(catalogReceiptRoot, backupRoot) {
		return CreateConfig{}, errors.New("ASCENDANY_ARTIFACT_ROOT, ASCENDANY_CATALOG_RECEIPT_ROOT, and ASCENDANY_BACKUP_ROOT must be disjoint")
	}
	runtimeRoot, err := required(lookup, "ASCENDANY_BACKUP_RUNTIME_ROOT")
	if err != nil {
		return CreateConfig{}, err
	}
	if runtimeRoot != BackupRuntimeRoot {
		return CreateConfig{}, fmt.Errorf("ASCENDANY_BACKUP_RUNTIME_ROOT must be exactly %s", BackupRuntimeRoot)
	}
	if pathsOverlap(runtimeRoot, artifactRoot) || pathsOverlap(runtimeRoot, catalogReceiptRoot) || pathsOverlap(runtimeRoot, backupRoot) {
		return CreateConfig{}, errors.New("ASCENDANY_BACKUP_RUNTIME_ROOT must be disjoint from durable roots")
	}
	if err := requireExact(lookup, "ASCENDANY_BACKUP_FORMAT", BackupFormat); err != nil {
		return CreateConfig{}, err
	}
	if err := requireExact(lookup, "ASCENDANY_BACKUP_MANIFEST_HASH", ManifestHashAlgorithm); err != nil {
		return CreateConfig{}, err
	}
	retainDaily, err := requiredBoundedNonNegativeInt(lookup, "ASCENDANY_BACKUP_RETAIN_DAILY")
	if err != nil {
		return CreateConfig{}, err
	}
	retainWeekly, err := requiredBoundedNonNegativeInt(lookup, "ASCENDANY_BACKUP_RETAIN_WEEKLY")
	if err != nil {
		return CreateConfig{}, err
	}
	if retainDaily == 0 && retainWeekly == 0 {
		return CreateConfig{}, errors.New("at least one backup retention window must be positive")
	}
	connectTimeout, err := requiredPositiveDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT")
	if err != nil {
		return CreateConfig{}, err
	}
	commandTimeout, err := requiredPositiveDuration(lookup, "ASCENDANY_BACKUP_COMMAND_TIMEOUT")
	if err != nil {
		return CreateConfig{}, err
	}
	tools, err := loadTools(lookup)
	if err != nil {
		return CreateConfig{}, err
	}
	return CreateConfig{
		DatabaseURL:        databaseURL,
		DatabasePassword:   password,
		ArtifactRoot:       artifactRoot,
		CatalogReceiptRoot: catalogReceiptRoot,
		BackupRoot:         backupRoot,
		RuntimeRoot:        runtimeRoot,
		RetainDaily:        retainDaily,
		RetainWeekly:       retainWeekly,
		ConnectTimeout:     connectTimeout,
		CommandTimeout:     commandTimeout,
		Tools:              tools,
	}, nil
}

func LoadVerifyConfig(lookup LookupEnv) (VerifyConfig, error) {
	if lookup == nil {
		return VerifyConfig{}, errors.New("environment lookup is required")
	}
	backupRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_BACKUP_ROOT")
	if err != nil {
		return VerifyConfig{}, err
	}
	if err := requireExact(lookup, "ASCENDANY_BACKUP_FORMAT", BackupFormat); err != nil {
		return VerifyConfig{}, err
	}
	if err := requireExact(lookup, "ASCENDANY_BACKUP_MANIFEST_HASH", ManifestHashAlgorithm); err != nil {
		return VerifyConfig{}, err
	}
	commandTimeout, err := requiredPositiveDuration(lookup, "ASCENDANY_BACKUP_COMMAND_TIMEOUT")
	if err != nil {
		return VerifyConfig{}, err
	}
	tools, err := loadTools(lookup)
	if err != nil {
		return VerifyConfig{}, err
	}
	return VerifyConfig{BackupRoot: backupRoot, CommandTimeout: commandTimeout, Tools: tools}, nil
}

func LoadRestoreConfig(lookup LookupEnv, readFile ReadFile) (RestoreConfig, error) {
	if lookup == nil {
		return RestoreConfig{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return RestoreConfig{}, errors.New("secret file reader is required")
	}
	verify, err := LoadVerifyConfig(lookup)
	if err != nil {
		return RestoreConfig{}, err
	}
	artifactRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_RESTORE_ARTIFACT_ROOT")
	if err != nil {
		return RestoreConfig{}, err
	}
	if pathsOverlap(verify.BackupRoot, artifactRoot) {
		return RestoreConfig{}, errors.New("ASCENDANY_BACKUP_ROOT and ASCENDANY_RESTORE_ARTIFACT_ROOT must be disjoint")
	}
	catalogReceiptRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_RESTORE_CATALOG_RECEIPT_ROOT")
	if err != nil {
		return RestoreConfig{}, err
	}
	if pathsOverlap(verify.BackupRoot, catalogReceiptRoot) || pathsOverlap(artifactRoot, catalogReceiptRoot) {
		return RestoreConfig{}, errors.New("backup, restore artifact, and restore catalog receipt roots must be disjoint")
	}
	runtimeRoot, err := requiredAbsoluteDirectoryPath(lookup, "ASCENDANY_RESTORE_RUNTIME_ROOT")
	if err != nil {
		return RestoreConfig{}, err
	}
	if !strings.HasPrefix(runtimeRoot, RestoreRuntimeRootPrefix) ||
		validateBundleID(strings.TrimPrefix(runtimeRoot, RestoreRuntimeRootPrefix)) != nil {
		return RestoreConfig{}, fmt.Errorf(
			"ASCENDANY_RESTORE_RUNTIME_ROOT must be %s followed by one canonical backup id",
			RestoreRuntimeRootPrefix,
		)
	}
	if pathsOverlap(runtimeRoot, verify.BackupRoot) || pathsOverlap(runtimeRoot, artifactRoot) ||
		pathsOverlap(runtimeRoot, catalogReceiptRoot) {
		return RestoreConfig{}, errors.New("ASCENDANY_RESTORE_RUNTIME_ROOT must be disjoint from durable roots")
	}
	databaseURL, err := required(lookup, "ASCENDANY_RESTORE_DATABASE_URL")
	if err != nil {
		return RestoreConfig{}, err
	}
	if err := validateDatabaseURL(databaseURL, RestoreDatabaseName, RestoreDatabaseLogin); err != nil {
		return RestoreConfig{}, fmt.Errorf("ASCENDANY_RESTORE_DATABASE_URL: %w", err)
	}
	password, err := loadPassword(lookup, readFile, "ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE")
	if err != nil {
		return RestoreConfig{}, err
	}
	connectTimeout, err := requiredPositiveDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT")
	if err != nil {
		return RestoreConfig{}, err
	}
	return RestoreConfig{
		BackupRoot:         verify.BackupRoot,
		ArtifactRoot:       artifactRoot,
		CatalogReceiptRoot: catalogReceiptRoot,
		RuntimeRoot:        runtimeRoot,
		DatabaseURL:        databaseURL,
		DatabasePassword:   password,
		ConnectTimeout:     connectTimeout,
		CommandTimeout:     verify.CommandTimeout,
		Tools:              verify.Tools,
	}, nil
}

func loadTools(lookup LookupEnv) (ToolConfig, error) {
	pgDump, err := requiredAbsoluteExecutablePath(lookup, "ASCENDANY_PG_DUMP_PATH")
	if err != nil {
		return ToolConfig{}, err
	}
	pgRestore, err := requiredAbsoluteExecutablePath(lookup, "ASCENDANY_PG_RESTORE_PATH")
	if err != nil {
		return ToolConfig{}, err
	}
	zstd, err := requiredAbsoluteExecutablePath(lookup, "ASCENDANY_ZSTD_PATH")
	if err != nil {
		return ToolConfig{}, err
	}
	return ToolConfig{PGDump: pgDump, PGRestore: pgRestore, Zstd: zstd}, nil
}

func validateDatabaseURL(raw, databaseName, requiredLogin string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("must be a valid PostgreSQL URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return errors.New("scheme must be postgres or postgresql")
	}
	if parsed.Hostname() == "" {
		return errors.New("host is required")
	}
	if parsed.Port() != directDatabasePort {
		return fmt.Errorf("must use direct PostgreSQL port %s", directDatabasePort)
	}
	if parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return errors.New("database user is required")
	}
	if requiredLogin != "" && parsed.User.Username() != requiredLogin {
		return fmt.Errorf("user must be %s", requiredLogin)
	}
	if _, present := parsed.User.Password(); present {
		return errors.New("password must be supplied through a credential file")
	}
	if parsed.Query().Has("password") {
		return errors.New("password query parameter is forbidden")
	}
	if parsed.Path != "/"+databaseName {
		return fmt.Errorf("database must be exactly %s", databaseName)
	}
	for key := range parsed.Query() {
		switch key {
		case "sslmode", "sslrootcert", "sslcert", "sslkey":
		default:
			return fmt.Errorf("unsupported database URL query parameter %q", key)
		}
	}
	if parsed.Fragment != "" {
		return errors.New("fragment is forbidden")
	}
	return nil
}

func loadPassword(lookup LookupEnv, readFile ReadFile, variable string) (string, error) {
	path, err := required(lookup, variable)
	if err != nil {
		return "", err
	}
	contents, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("%s cannot be read", variable)
	}
	if len(contents) < minimumDatabasePassword {
		return "", fmt.Errorf("%s must reference at least %d bytes", variable, minimumDatabasePassword)
	}
	if bytes.IndexByte(contents, 0) >= 0 {
		return "", fmt.Errorf("%s must not contain NUL bytes", variable)
	}
	if !bytes.Equal(contents, bytes.TrimSpace(contents)) {
		return "", fmt.Errorf("%s content must not start or end with whitespace", variable)
	}
	return string(contents), nil
}

func required(lookup LookupEnv, name string) (string, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return strings.TrimSpace(value), nil
}

func requireExact(lookup LookupEnv, name, expected string) error {
	value, err := required(lookup, name)
	if err != nil {
		return err
	}
	if value != expected {
		return fmt.Errorf("%s must be exactly %s", name, expected)
	}
	return nil
}

func requiredAbsoluteDirectoryPath(lookup LookupEnv, name string) (string, error) {
	value, err := required(lookup, name)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) {
		return "", fmt.Errorf("%s must be a clean absolute path below the filesystem root", name)
	}
	return value, nil
}

func requiredAbsoluteExecutablePath(lookup LookupEnv, name string) (string, error) {
	value, err := required(lookup, name)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", fmt.Errorf("%s must be a clean absolute executable path", name)
	}
	return value, nil
}

func requiredBoundedNonNegativeInt(lookup LookupEnv, name string) (int, error) {
	value, err := required(lookup, name)
	if err != nil {
		return 0, err
	}
	parsed, parseErr := strconv.Atoi(value)
	if parseErr != nil || parsed < 0 || parsed > maximumRetentionCount {
		return 0, fmt.Errorf("%s must be an integer between 0 and %d", name, maximumRetentionCount)
	}
	return parsed, nil
}

func requiredPositiveDuration(lookup LookupEnv, name string) (time.Duration, error) {
	value, err := required(lookup, name)
	if err != nil {
		return 0, err
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return parsed, nil
}

func pathsOverlap(left, right string) bool {
	leftWithSeparator := left + string(filepath.Separator)
	rightWithSeparator := right + string(filepath.Separator)
	return left == right || strings.HasPrefix(leftWithSeparator, rightWithSeparator) || strings.HasPrefix(rightWithSeparator, leftWithSeparator)
}

func isCleanAbsolute(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value
}

func isCleanAbsoluteBelowRoot(value string) bool {
	return isCleanAbsolute(value) && value != string(filepath.Separator)
}
