package config

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

// CatalogPublicationConfig contains only settings owned by the stopped-runtime
// knowledge-catalog publication operator.
type CatalogPublicationConfig struct {
	Database       DatabaseConfig
	Authentication CatalogPublicationAuthenticationConfig
	Recommendation RecommendationConfig
	Log            LogConfig
}

// CatalogPublicationAuthenticationConfig contains the exact verifier inputs
// required to authenticate the administrator access token. The publisher has
// no password, refresh-token, or token-issuing capability.
type CatalogPublicationAuthenticationConfig struct {
	JWTVerificationPublicKey ed25519.PublicKey
	Issuer                   string
	Audience                 string
}

func LoadCatalogPublication(lookup LookupEnv, readFile ReadFile) (CatalogPublicationConfig, error) {
	if lookup == nil {
		return CatalogPublicationConfig{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return CatalogPublicationConfig{}, errors.New("secret file reader is required")
	}

	databaseURL, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_URL")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return CatalogPublicationConfig{}, fmt.Errorf("ASCENDANY_DATABASE_URL: %w", err)
	}
	poolMode, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_POOL_MODE")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	if poolMode != "transaction" {
		return CatalogPublicationConfig{}, errors.New("ASCENDANY_DATABASE_POOL_MODE must be transaction")
	}
	databasePassword, err := loadSecret(lookup, readFile, "ASCENDANY_DATABASE_PASSWORD_FILE", minimumDatabasePasswordBytes)
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	jwtVerificationPublicKey, err := loadJWTVerificationPublicKey(lookup, readFile)
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	issuer, err := requiredTrimmed(lookup, "ASCENDANY_AUTH_ISSUER")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	audience, err := requiredTrimmed(lookup, "ASCENDANY_AUTH_AUDIENCE")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	expectedSchemaVersion, err := requiredPositiveInt64(lookup, "ASCENDANY_DATABASE_SCHEMA_VERSION")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	connectTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	healthTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_HEALTH_TIMEOUT", 3*time.Second)
	if err != nil {
		return CatalogPublicationConfig{}, err
	}

	modelPath, err := requiredTrimmed(lookup, "ASCENDANY_RECOMMENDATION_MODEL_PATH")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	if err := validateAbsoluteFilePath(modelPath); err != nil {
		return CatalogPublicationConfig{}, fmt.Errorf("ASCENDANY_RECOMMENDATION_MODEL_PATH: %w", err)
	}
	modelSHA256, err := requiredLowercaseSHA256(lookup, "ASCENDANY_RECOMMENDATION_MODEL_SHA256")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	modelPurposeValue, err := requiredTrimmed(lookup, "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	modelPurpose, err := inferencemodel.ParsePurpose(modelPurposeValue)
	if err != nil {
		return CatalogPublicationConfig{}, fmt.Errorf("ASCENDANY_RECOMMENDATION_MODEL_PURPOSE: %w", err)
	}
	catalogPath, err := requiredTrimmed(lookup, "ASCENDANY_KNOWLEDGE_CATALOG_PATH")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}
	if err := validateAbsoluteFilePath(catalogPath); err != nil {
		return CatalogPublicationConfig{}, fmt.Errorf("ASCENDANY_KNOWLEDGE_CATALOG_PATH: %w", err)
	}
	catalogSHA256, err := requiredLowercaseSHA256(lookup, "ASCENDANY_KNOWLEDGE_CATALOG_SHA256")
	if err != nil {
		return CatalogPublicationConfig{}, err
	}

	logLevel := strings.ToLower(optionalTrimmed(lookup, "ASCENDANY_LOG_LEVEL", "info"))
	if !isValidLogLevel(logLevel) {
		return CatalogPublicationConfig{}, errors.New("ASCENDANY_LOG_LEVEL must be one of debug, info, warn, error")
	}
	return CatalogPublicationConfig{
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
		Authentication: CatalogPublicationAuthenticationConfig{
			JWTVerificationPublicKey: jwtVerificationPublicKey,
			Issuer:                   issuer,
			Audience:                 audience,
		},
		Recommendation: RecommendationConfig{
			ModelPath:     modelPath,
			ModelSHA256:   modelSHA256,
			ModelPurpose:  modelPurpose,
			CatalogPath:   catalogPath,
			CatalogSHA256: catalogSHA256,
		},
		Log: LogConfig{Level: logLevel},
	}, nil
}
