package config

import (
	"errors"
	"fmt"
	"time"
)

// AdminBootstrapConfig contains only the database and password material used
// by the one-shot administrator bootstrap command.
type AdminBootstrapConfig struct {
	Database       DatabaseConfig
	PasswordPepper string
}

func LoadAdminBootstrap(lookup LookupEnv, readFile ReadFile) (AdminBootstrapConfig, error) {
	if lookup == nil {
		return AdminBootstrapConfig{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return AdminBootstrapConfig{}, errors.New("secret file reader is required")
	}
	databaseURL, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_URL")
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return AdminBootstrapConfig{}, fmt.Errorf("ASCENDANY_DATABASE_URL: %w", err)
	}
	poolMode, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_POOL_MODE")
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	if poolMode != "transaction" {
		return AdminBootstrapConfig{}, errors.New("ASCENDANY_DATABASE_POOL_MODE must be transaction")
	}
	databasePassword, err := loadSecret(
		lookup,
		readFile,
		"ASCENDANY_DATABASE_PASSWORD_FILE",
		minimumDatabasePasswordBytes,
	)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	expectedSchemaVersion, err := requiredPositiveInt64(lookup, "ASCENDANY_DATABASE_SCHEMA_VERSION")
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	maxConnections, err := optionalPositiveInt32(lookup, "ASCENDANY_DATABASE_MAX_CONNECTIONS", 20)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	minConnections, err := optionalNonNegativeInt32(lookup, "ASCENDANY_DATABASE_MIN_CONNECTIONS", 2)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	if minConnections > maxConnections {
		return AdminBootstrapConfig{}, errors.New("ASCENDANY_DATABASE_MIN_CONNECTIONS must not exceed ASCENDANY_DATABASE_MAX_CONNECTIONS")
	}
	connectTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	maxConnectionLifetime, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_MAX_CONNECTION_LIFETIME", 30*time.Minute)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	maxConnectionIdleTime, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_MAX_CONNECTION_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	healthTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_HEALTH_TIMEOUT", 3*time.Second)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	passwordPepper, err := loadSecret(
		lookup,
		readFile,
		"ASCENDANY_PASSWORD_PEPPER_FILE",
		minimumPasswordPepperBytes,
	)
	if err != nil {
		return AdminBootstrapConfig{}, err
	}
	return AdminBootstrapConfig{
		Database: DatabaseConfig{
			URL:                   databaseURL,
			Password:              databasePassword,
			ExpectedSchemaVersion: expectedSchemaVersion,
			MaxConnections:        maxConnections,
			MinConnections:        minConnections,
			ConnectTimeout:        connectTimeout,
			MaxConnectionLifetime: maxConnectionLifetime,
			MaxConnectionIdleTime: maxConnectionIdleTime,
			HealthTimeout:         healthTimeout,
		},
		PasswordPepper: passwordPepper,
	}, nil
}
