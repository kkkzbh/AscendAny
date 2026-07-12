package migrate

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	databaseName       = "ascendany_v2"
	databaseLogin      = "ascendany_migrator_login"
	databaseRole       = "ascendany_owner"
	databaseSchema     = "ascendany"
	historyTable       = "ascendany.schema_migrations_v2"
	directDatabasePort = "5432"
	minimumPasswordLen = 16
)

type LookupEnv func(string) (string, bool)
type ReadFile func(string) ([]byte, error)

type Config struct {
	DatabaseURL    string
	Password       string
	LockTimeout    time.Duration
	ConnectTimeout time.Duration
}

func LoadConfig(lookup LookupEnv, readFile ReadFile) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return Config{}, errors.New("secret file reader is required")
	}

	databaseURL, err := required(lookup, "ASCENDANY_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	if err := validateMigrationDatabaseURL(databaseURL); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_DATABASE_URL: %w", err)
	}

	passwordPath, err := required(lookup, "ASCENDANY_DATABASE_PASSWORD_FILE")
	if err != nil {
		return Config{}, err
	}
	passwordBytes, err := readFile(passwordPath)
	if err != nil {
		return Config{}, errors.New("ASCENDANY_DATABASE_PASSWORD_FILE cannot be read")
	}
	if len(passwordBytes) < minimumPasswordLen {
		return Config{}, fmt.Errorf("ASCENDANY_DATABASE_PASSWORD_FILE must reference at least %d bytes", minimumPasswordLen)
	}
	if bytes.IndexByte(passwordBytes, 0) >= 0 {
		return Config{}, errors.New("ASCENDANY_DATABASE_PASSWORD_FILE must not contain NUL bytes")
	}
	if !bytes.Equal(passwordBytes, bytes.TrimSpace(passwordBytes)) {
		return Config{}, errors.New("ASCENDANY_DATABASE_PASSWORD_FILE content must not start or end with whitespace")
	}

	if err := requireExact(lookup, "ASCENDANY_DATABASE_ROLE", databaseRole); err != nil {
		return Config{}, err
	}
	if err := requireExact(lookup, "ASCENDANY_DATABASE_SCHEMA", databaseSchema); err != nil {
		return Config{}, err
	}
	if err := requireExact(lookup, "ASCENDANY_MIGRATION_HISTORY_TABLE", historyTable); err != nil {
		return Config{}, err
	}

	expectedVersion, err := required(lookup, "ASCENDANY_DATABASE_SCHEMA_VERSION")
	if err != nil {
		return Config{}, err
	}
	parsedVersion, err := strconv.ParseUint(expectedVersion, 10, 64)
	if err != nil || parsedVersion != uint64(CurrentVersion()) {
		return Config{}, fmt.Errorf("ASCENDANY_DATABASE_SCHEMA_VERSION must equal embedded version %d", CurrentVersion())
	}

	lockTimeout, err := requiredDuration(lookup, "ASCENDANY_MIGRATION_LOCK_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	if lockTimeout < time.Millisecond {
		return Config{}, errors.New("ASCENDANY_MIGRATION_LOCK_TIMEOUT must be at least 1ms")
	}
	connectTimeout, err := optionalDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseURL:    databaseURL,
		Password:       string(passwordBytes),
		LockTimeout:    lockTimeout,
		ConnectTimeout: connectTimeout,
	}, nil
}

func validateMigrationDatabaseURL(raw string) error {
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
	if parsed.User == nil || parsed.User.Username() != databaseLogin {
		return fmt.Errorf("user must be %s", databaseLogin)
	}
	if _, present := parsed.User.Password(); present {
		return errors.New("password must be supplied through ASCENDANY_DATABASE_PASSWORD_FILE")
	}
	if parsed.Query().Has("password") {
		return errors.New("password query parameter is forbidden")
	}
	if parsed.Path != "/"+databaseName {
		return fmt.Errorf("database must be exactly %s", databaseName)
	}
	if parsed.RawQuery != "" {
		for key := range parsed.Query() {
			switch key {
			case "sslmode", "sslrootcert", "sslcert", "sslkey":
			default:
				return fmt.Errorf("unsupported database URL query parameter %q", key)
			}
		}
	}
	if parsed.Fragment != "" {
		return errors.New("fragment is forbidden")
	}
	return nil
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

func requiredDuration(lookup LookupEnv, name string) (time.Duration, error) {
	value, err := required(lookup, name)
	if err != nil {
		return 0, err
	}
	return parsePositiveDuration(name, value)
}

func optionalDuration(lookup LookupEnv, name string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return parsePositiveDuration(name, strings.TrimSpace(value))
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return parsed, nil
}
