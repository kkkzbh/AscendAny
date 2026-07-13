package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

// ModelActivationConfig contains only the database, model, and logging
// settings consumed by the one-shot model activation command.
type ModelActivationConfig struct {
	Database       DatabaseConfig
	Recommendation RecommendationConfig
	Log            LogConfig
}

func LoadModelActivation(lookup LookupEnv, readFile ReadFile) (ModelActivationConfig, error) {
	if lookup == nil {
		return ModelActivationConfig{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return ModelActivationConfig{}, errors.New("secret file reader is required")
	}

	databaseURL, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_URL")
	if err != nil {
		return ModelActivationConfig{}, err
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return ModelActivationConfig{}, fmt.Errorf("ASCENDANY_DATABASE_URL: %w", err)
	}
	poolMode, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_POOL_MODE")
	if err != nil {
		return ModelActivationConfig{}, err
	}
	if poolMode != "transaction" {
		return ModelActivationConfig{}, errors.New("ASCENDANY_DATABASE_POOL_MODE must be transaction")
	}
	databasePassword, err := loadSecret(
		lookup,
		readFile,
		"ASCENDANY_DATABASE_PASSWORD_FILE",
		minimumDatabasePasswordBytes,
	)
	if err != nil {
		return ModelActivationConfig{}, err
	}
	expectedSchemaVersion, err := requiredPositiveInt64(lookup, "ASCENDANY_DATABASE_SCHEMA_VERSION")
	if err != nil {
		return ModelActivationConfig{}, err
	}
	connectTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return ModelActivationConfig{}, err
	}
	healthTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_HEALTH_TIMEOUT", 3*time.Second)
	if err != nil {
		return ModelActivationConfig{}, err
	}

	modelPath, err := requiredTrimmed(lookup, "ASCENDANY_RECOMMENDATION_MODEL_PATH")
	if err != nil {
		return ModelActivationConfig{}, err
	}
	if err := validateAbsoluteFilePath(modelPath); err != nil {
		return ModelActivationConfig{}, fmt.Errorf("ASCENDANY_RECOMMENDATION_MODEL_PATH: %w", err)
	}
	modelSHA256, err := requiredLowercaseSHA256(lookup, "ASCENDANY_RECOMMENDATION_MODEL_SHA256")
	if err != nil {
		return ModelActivationConfig{}, err
	}
	modelPurposeValue, err := requiredTrimmed(lookup, "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE")
	if err != nil {
		return ModelActivationConfig{}, err
	}
	modelPurpose, err := inferencemodel.ParsePurpose(modelPurposeValue)
	if err != nil {
		return ModelActivationConfig{}, fmt.Errorf("ASCENDANY_RECOMMENDATION_MODEL_PURPOSE: %w", err)
	}

	logLevel := strings.ToLower(optionalTrimmed(lookup, "ASCENDANY_LOG_LEVEL", "info"))
	if !isValidLogLevel(logLevel) {
		return ModelActivationConfig{}, errors.New("ASCENDANY_LOG_LEVEL must be one of debug, info, warn, error")
	}

	return ModelActivationConfig{
		Database: DatabaseConfig{
			URL:                   databaseURL,
			Password:              databasePassword,
			ExpectedSchemaVersion: expectedSchemaVersion,
			MaxConnections:        1,
			MinConnections:        0,
			ConnectTimeout:        connectTimeout,
			MaxConnectionLifetime: 5 * time.Minute,
			MaxConnectionIdleTime: time.Minute,
			HealthTimeout:         healthTimeout,
		},
		Recommendation: RecommendationConfig{
			ModelPath:    modelPath,
			ModelSHA256:  modelSHA256,
			ModelPurpose: modelPurpose,
		},
		Log: LogConfig{Level: logLevel},
	}, nil
}
