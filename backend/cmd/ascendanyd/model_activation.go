package main

import (
	"context"
	"io"
	"log/slog"
	"net"

	"github.com/kkkzbh/AscendAny/backend/internal/config"
	"github.com/kkkzbh/AscendAny/backend/internal/database"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/logging"
	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

func runModelActivation(lookup config.LookupEnv, readFile config.ReadFile, stderr io.Writer) int {
	bootstrapLogger, _ := logging.New(stderr, "info")
	configuration, err := config.LoadModelActivation(lookup, readFile)
	if err != nil {
		bootstrapLogger.Error("model activation configuration rejected", "error", err)
		return 1
	}
	if configuration.Database.ExpectedSchemaVersion != migrate.CurrentVersion() {
		bootstrapLogger.Error(
			"schema version configuration rejected",
			"configured", configuration.Database.ExpectedSchemaVersion,
			"embedded", migrate.CurrentVersion(),
		)
		return 1
	}
	logger, err := logging.New(stderr, configuration.Log.Level)
	if err != nil {
		bootstrapLogger.Error("logger configuration rejected", "error", err)
		return 1
	}
	slog.SetDefault(logger)
	isolationListener, err := net.Listen("tcp", "127.0.0.1:18000")
	if err != nil {
		logger.Error("model activation requires exclusive ownership of the production HTTP address", "error", err)
		return 1
	}
	defer isolationListener.Close()

	loaded, err := modelartifact.Load(
		configuration.Recommendation.ModelPath,
		configuration.Recommendation.ModelSHA256,
	)
	if err != nil {
		logger.Error("recommendation model artifact rejected", "error", err)
		return 1
	}
	if err := recommendation.ValidateInferenceModel(loaded.Model, configuration.Recommendation.ModelPurpose); err != nil {
		logger.Error("recommendation model feature contract rejected", "error", err)
		return 1
	}

	operationTimeout := configuration.Database.ConnectTimeout + configuration.Database.HealthTimeout
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	pool, err := database.Open(ctx, database.PoolOptions{
		URL:                   configuration.Database.URL,
		Password:              configuration.Database.Password,
		MaxConnections:        configuration.Database.MaxConnections,
		MinConnections:        configuration.Database.MinConnections,
		ConnectTimeout:        configuration.Database.ConnectTimeout,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
		MaxConnectionIdleTime: configuration.Database.MaxConnectionIdleTime,
	})
	if err != nil {
		logger.Error("database pool configuration rejected", "error", err)
		return 1
	}
	defer pool.Close()

	manifest := migrate.Manifest()
	expectedMigrations := make([]database.ExpectedMigration, 0, len(manifest))
	for _, entry := range manifest {
		expectedMigrations = append(expectedMigrations, database.ExpectedMigration{
			Version: entry.Version,
			Name:    entry.Name,
			SHA256:  entry.SHA256,
		})
	}
	migrationReader, err := database.NewMigrationStateReader(pool, expectedMigrations)
	if err != nil {
		logger.Error("migration readiness initialization failed", "error", err)
		return 1
	}
	readiness := health.NewReadiness(
		pool,
		migrationReader,
		configuration.Database.ExpectedSchemaVersion,
		configuration.Database.HealthTimeout,
	)
	if report := readiness.Check(ctx); report.Status != health.StatusReady {
		logger.Error("database readiness rejected before model activation", "report", report)
		return 1
	}

	repository, err := modelrelease.NewRepository(pool)
	if err != nil {
		logger.Error("recommendation model release repository initialization failed", "error", err)
		return 1
	}
	application := version.Current()
	binding, err := repository.Bind(ctx, loaded, modelrelease.ApplicationIdentity{
		Version:   application.Version,
		Commit:    application.Commit,
		BuildTime: application.BuildTime,
	})
	if err != nil {
		logger.Error("recommendation model activation failed", "error", err)
		return 1
	}
	logger.Info(
		"recommendation model activation complete",
		"releaseId", binding.ReleaseID,
		"headRevision", binding.HeadRevision,
		"activated", binding.Activated,
		"artifactSha256", configuration.Recommendation.ModelSHA256,
	)
	return 0
}
