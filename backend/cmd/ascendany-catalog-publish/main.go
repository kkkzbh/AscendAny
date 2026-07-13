package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/catalogartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/catalogpublication"
	"github.com/kkkzbh/AscendAny/backend/internal/config"
	configurationdomain "github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/database"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/logging"
	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

const publicationAddress = "127.0.0.1:18000"

type accessTokenVerifier struct {
	verifier *auth.AccessTokenVerifier
}

func (verifier accessTokenVerifier) VerifyAccessToken(serialized string) (auth.AccessPrincipal, error) {
	return verifier.verifier.VerifyCatalogPublicationCapability(serialized)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(
		ctx,
		os.Args[1:],
		os.LookupEnv,
		os.ReadFile,
		os.Getenv("CREDENTIALS_DIRECTORY"),
		os.Stdout,
		os.Stderr,
		net.Listen,
	))
}

func run(
	ctx context.Context,
	args []string,
	lookup config.LookupEnv,
	readFile config.ReadFile,
	credentialsDirectory string,
	stdout io.Writer,
	stderr io.Writer,
	listen func(string, string) (net.Listener, error),
) int {
	bootstrapLogger, _ := logging.New(stderr, "info")
	if err := validateCommand(args); err != nil {
		bootstrapLogger.Error("command rejected", "usage", "ascendany-catalog-publish publish")
		return 2
	}
	if ctx == nil || stdout == nil || stderr == nil || listen == nil {
		bootstrapLogger.Error("catalog publication runtime dependencies are invalid")
		return 1
	}
	configuration, err := config.LoadCatalogPublication(lookup, readFile)
	if err != nil {
		bootstrapLogger.Error("catalog publication configuration rejected", "error", err)
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

	isolationListener, err := listen("tcp", publicationAddress)
	if err != nil {
		logger.Error("catalog publication requires exclusive ownership of the production HTTP address", "error", err)
		return 1
	}
	defer isolationListener.Close()

	loadedModel, err := modelartifact.Load(
		configuration.Recommendation.ModelPath,
		configuration.Recommendation.ModelSHA256,
	)
	if err != nil {
		logger.Error("recommendation model artifact rejected", "error", err)
		return 1
	}
	if err := recommendation.ValidateInferenceModel(loadedModel.Model, configuration.Recommendation.ModelPurpose); err != nil {
		logger.Error("recommendation model feature contract rejected", "error", err)
		return 1
	}
	loadedCatalog, err := catalogartifact.Load(
		configuration.Recommendation.CatalogPath,
		configuration.Recommendation.CatalogSHA256,
		loadedModel.Model.Manifest(),
	)
	if err != nil {
		logger.Error("knowledge catalog artifact rejected", "error", err)
		return 1
	}
	inputs, err := catalogpublication.ReadInputs(
		filepath.Join(credentialsDirectory, catalogpublication.RequestInputName),
		filepath.Join(credentialsDirectory, catalogpublication.AccessTokenInputName),
	)
	if err != nil {
		logger.Error("catalog publication inputs rejected", "error", err)
		return 1
	}
	jwtVerifier, err := auth.NewAccessTokenVerifier(
		configuration.Authentication.Issuer,
		configuration.Authentication.Audience,
		configuration.Authentication.JWTVerificationPublicKey,
	)
	if err != nil {
		logger.Error("catalog publication access-token verifier initialization failed", "code", auth.ErrorCodeOf(err))
		return 1
	}

	operationContext, cancelOperation := context.WithTimeout(ctx, 45*time.Second)
	defer cancelOperation()
	pool, err := database.Open(operationContext, database.PoolOptions{
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
	if report := readiness.Check(operationContext); report.Status != health.StatusReady {
		logger.Error("database readiness rejected before catalog publication", "report", report)
		return 1
	}

	publicationContract := recommendation.ConfigurationPublicationContract{}
	repository, err := configurationdomain.NewPostgresRepository(pool, publicationContract)
	if err != nil {
		logger.Error("configuration repository initialization failed", "code", configurationdomain.CodeOf(err))
		return 1
	}
	service, err := configurationdomain.NewService(repository, publicationContract)
	if err != nil {
		logger.Error("configuration service initialization failed", "code", configurationdomain.CodeOf(err))
		return 1
	}
	application, err := configurationdomain.NewApplicationService(accessTokenVerifier{verifier: jwtVerifier}, service)
	if err != nil {
		logger.Error("configuration application initialization failed", "code", configurationdomain.CodeOf(err))
		return 1
	}
	applicationIdentity := version.Current()
	receipt, err := catalogpublication.Publish(
		operationContext,
		application,
		inputs.AccessToken,
		inputs.Request,
		loadedCatalog,
		loadedModel,
		modelrelease.ApplicationIdentity{
			Version:   applicationIdentity.Version,
			Commit:    applicationIdentity.Commit,
			BuildTime: applicationIdentity.BuildTime,
		},
	)
	if err != nil {
		logger.Error(
			"knowledge catalog publication failed",
			"configurationCode", configurationdomain.CodeOf(err),
			"authenticationCode", auth.ErrorCodeOf(err),
		)
		return 1
	}
	encodedReceipt, err := catalogpublication.CanonicalReceipt(receipt)
	if err != nil {
		logger.Error("knowledge catalog receipt encoding failed", "error", err)
		return 1
	}
	if err := catalogpublication.WriteReceipt(
		catalogpublication.ProductionReceiptDirectory,
		receipt.KnowledgeCatalogPublicationID,
		encodedReceipt,
	); err != nil {
		logger.Error("knowledge catalog receipt publication failed", "error", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "%s\n", encodedReceipt); err != nil {
		logger.Error("knowledge catalog publication succeeded but receipt output failed", "error", err)
		return 1
	}
	return 0
}

func validateCommand(args []string) error {
	if len(args) != 1 || args[0] != "publish" {
		return errors.New("usage: ascendany-catalog-publish publish")
	}
	return nil
}
