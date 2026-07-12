package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/config"
	configurationdomain "github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/credential"
	"github.com/kkkzbh/AscendAny/backend/internal/database"
	"github.com/kkkzbh/AscendAny/backend/internal/examcatalog"
	"github.com/kkkzbh/AscendAny/backend/internal/examgeneration"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/httpapi"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/judgeexecutor"
	"github.com/kkkzbh/AscendAny/backend/internal/logging"
	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspexecutor"
	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
	"github.com/kkkzbh/AscendAny/backend/internal/modelprobe"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
	"github.com/kkkzbh/AscendAny/backend/internal/publicdelivery"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/runtimeapp"
	"github.com/kkkzbh/AscendAny/backend/internal/server"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentserver"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	bootstrapLogger, _ := logging.New(os.Stderr, "info")
	if err := validateCommand(args); err != nil {
		bootstrapLogger.Error("command rejected", "usage", "ascendanyd serve")
		return 2
	}

	configuration, err := config.Load(os.LookupEnv, os.ReadFile)
	if err != nil {
		bootstrapLogger.Error("configuration rejected", "error", err)
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

	logger, err := logging.New(os.Stderr, configuration.Log.Level)
	if err != nil {
		bootstrapLogger.Error("logger configuration rejected", "error", err)
		return 1
	}
	slog.SetDefault(logger)

	pool, err := database.Open(context.Background(), database.PoolOptions{
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
	authRepository, err := auth.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("authentication repository initialization failed", "code", auth.ErrorCodeOf(err))
		return 1
	}
	authService, err := auth.NewService(authRepository, auth.ProductionConfig(
		configuration.Auth.Issuer,
		configuration.Auth.Audience,
		[]byte(configuration.Auth.JWTSigningKey),
		[]byte(configuration.Auth.PasswordPepper),
		configuration.Auth.AccessTTL,
		configuration.Auth.RefreshTTL,
	))
	if err != nil {
		logger.Error("authentication service initialization failed", "code", auth.ErrorCodeOf(err))
		return 1
	}
	accountManager, err := auth.NewProductionAccountManager(authRepository)
	if err != nil {
		logger.Error("account manager initialization failed", "code", auth.ErrorCodeOf(err))
		return 1
	}
	accountApplicationService, err := auth.NewAccountApplicationService(authService, accountManager)
	if err != nil {
		logger.Error("account application service initialization failed", "code", auth.ErrorCodeOf(err))
		return 1
	}
	agentNotesRepository, err := agentnotes.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("agent notes repository initialization failed", "code", agentnotes.CodeOf(err))
		return 1
	}
	agentNotesService, err := agentnotes.NewService(agentNotesRepository)
	if err != nil {
		logger.Error("agent notes service initialization failed", "code", agentnotes.CodeOf(err))
		return 1
	}
	agentNotesApplication, err := agentnotes.NewApplicationService(authService, agentNotesService)
	if err != nil {
		logger.Error("agent notes application initialization failed", "code", agentnotes.CodeOf(err))
		return 1
	}
	chatAgentRepository, err := chatagent.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("chat agent repository initialization failed", "code", chatagent.CodeOf(err))
		return 1
	}
	chatAgentService, err := chatagent.NewService(chatAgentRepository)
	if err != nil {
		logger.Error("chat agent service initialization failed", "code", chatagent.CodeOf(err))
		return 1
	}
	chatAgentApplication, err := chatagent.NewApplicationService(authService, chatAgentService)
	if err != nil {
		logger.Error("chat agent application initialization failed", "code", chatagent.CodeOf(err))
		return 1
	}
	importReader, err := importing.NewPostgresReader(pool)
	if err != nil {
		logger.Error("import reader initialization failed", "code", importing.ErrorInvalidConfiguration)
		return 1
	}
	studentAnalyticsRepository, err := studentanalytics.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("student analytics repository initialization failed", "code", studentanalytics.CodeOf(err))
		return 1
	}
	studentAnalyticsService, err := studentanalytics.NewService(studentAnalyticsRepository)
	if err != nil {
		logger.Error("student analytics service initialization failed", "code", studentanalytics.CodeOf(err))
		return 1
	}
	studentLeaderboardService, err := studentanalytics.NewLeaderboardService(studentAnalyticsRepository)
	if err != nil {
		logger.Error("student leaderboard service initialization failed", "code", studentanalytics.CodeOf(err))
		return 1
	}
	studentApplicationService, err := studentanalytics.NewApplicationService(
		authService,
		studentAnalyticsService,
		studentLeaderboardService,
	)
	if err != nil {
		logger.Error("student application service initialization failed", "code", studentanalytics.CodeOf(err))
		return 1
	}
	achievementRepository, err := achievement.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("achievement repository initialization failed", "code", achievement.CodeOf(err))
		return 1
	}
	achievementService, err := achievement.NewService(achievementRepository)
	if err != nil {
		logger.Error("achievement service initialization failed", "code", achievement.CodeOf(err))
		return 1
	}
	achievementApplication, err := achievement.NewApplicationService(authService, achievementService)
	if err != nil {
		logger.Error("achievement application initialization failed", "code", achievement.CodeOf(err))
		return 1
	}
	examCatalogRepository, err := examcatalog.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("exam catalog repository initialization failed", "code", examcatalog.CodeOf(err))
		return 1
	}
	examCatalogService, err := examcatalog.NewService(examCatalogRepository)
	if err != nil {
		logger.Error("exam catalog service initialization failed", "code", examcatalog.CodeOf(err))
		return 1
	}
	examCatalogApplication, err := examcatalog.NewApplicationService(authService, examCatalogService)
	if err != nil {
		logger.Error("exam catalog application initialization failed", "code", examcatalog.CodeOf(err))
		return 1
	}
	examGenerationRepository, err := examgeneration.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("exam generation repository initialization failed", "code", examgeneration.CodeOf(err))
		return 1
	}
	examGenerationService, err := examgeneration.NewService(examGenerationRepository)
	if err != nil {
		logger.Error("exam generation service initialization failed", "code", examgeneration.CodeOf(err))
		return 1
	}
	examGenerationApplication, err := examgeneration.NewApplicationService(authService, examGenerationService)
	if err != nil {
		logger.Error("exam generation application initialization failed", "code", examgeneration.CodeOf(err))
		return 1
	}
	administrationRepository, err := administration.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("administration repository initialization failed", "code", administration.CodeOf(err))
		return 1
	}
	administrationService, err := administration.NewProductionService(administrationRepository)
	if err != nil {
		logger.Error("administration service initialization failed", "code", administration.CodeOf(err))
		return 1
	}
	administrationApplication, err := administration.NewApplicationService(authService, administrationService)
	if err != nil {
		logger.Error("administration application initialization failed", "code", administration.CodeOf(err))
		return 1
	}
	configurationRepository, err := configurationdomain.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("configuration repository initialization failed", "code", configurationdomain.CodeOf(err))
		return 1
	}
	configurationService, err := configurationdomain.NewService(configurationRepository, recommendation.ConfigurationDocumentValidator{})
	if err != nil {
		logger.Error("configuration service initialization failed", "code", configurationdomain.CodeOf(err))
		return 1
	}
	configurationApplication, err := configurationdomain.NewApplicationService(authService, configurationService)
	if err != nil {
		logger.Error("configuration application initialization failed", "code", configurationdomain.CodeOf(err))
		return 1
	}
	feedbackRepository, err := feedback.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("feedback repository initialization failed", "code", feedback.CodeOf(err))
		return 1
	}
	feedbackService, err := feedback.NewService(feedbackRepository, feedback.Policy{
		Window:                   configuration.Feedback.RateWindow,
		MaximumSubmissions:       configuration.Feedback.RateMaximum,
		DeliveryConfigurationKey: configuration.Feedback.DeliveryConfigurationKey,
	})
	if err != nil {
		logger.Error("feedback service initialization failed", "code", feedback.CodeOf(err))
		return 1
	}
	feedbackApplication, err := feedback.NewApplicationService(authService, feedbackService)
	if err != nil {
		logger.Error("feedback application initialization failed", "code", feedback.CodeOf(err))
		return 1
	}
	ojPolicy := productionOJPolicy(configuration)
	ojRepository, err := oj.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("OJ repository initialization failed", "code", oj.CodeOf(err))
		return 1
	}
	ojService, err := oj.NewService(ojRepository, ojPolicy)
	if err != nil {
		logger.Error("OJ service initialization failed", "code", oj.CodeOf(err))
		return 1
	}
	recommendationRepository, err := recommendation.NewPostgresRepository(pool)
	if err != nil {
		logger.Error("recommendation repository initialization failed", "code", recommendation.CodeOf(err))
		return 1
	}
	recommendationReaderService, err := recommendation.NewReaderService(recommendationRepository)
	if err != nil {
		logger.Error("recommendation reader service initialization failed", "code", recommendation.CodeOf(err))
		return 1
	}
	recommendationReaderApplication, err := recommendation.NewReaderApplicationService(authService, recommendationReaderService)
	if err != nil {
		logger.Error("recommendation reader application initialization failed", "code", recommendation.CodeOf(err))
		return 1
	}
	recommendationAdminReaderService, err := recommendation.NewAdminReaderService(recommendationRepository)
	if err != nil {
		logger.Error("recommendation admin reader service initialization failed", "code", recommendation.CodeOf(err))
		return 1
	}
	recommendationAdminReaderApplication, err := recommendation.NewAdminReaderApplicationService(authService, recommendationAdminReaderService)
	if err != nil {
		logger.Error("recommendation admin reader application initialization failed", "code", recommendation.CodeOf(err))
		return 1
	}
	var lspManager *lspexecutor.Manager
	var lspPolicy lsp.Policy
	var writeRuntime *runtimeapp.Components
	var judgeRuntime *judgeexecutor.Runtime
	var chatRuntime *chatagent.RuntimeComponents
	var trainerAgentVerifier *traineragentserver.ScopedBearerVerifier
	var trainerAgentTransport *traineragentserver.Service
	var recommendationQueueApplication *recommendation.QueueApplicationService
	var modelProbeApplication *modelprobe.Service
	if configuration.Write.Enabled {
		lspWorkerUID, lookupErr := resolveSystemUserUID(configuration.LSP.WorkerUser, user.Lookup)
		if lookupErr != nil {
			logger.Error("LSP worker identity resolution failed", "error", lookupErr)
			return 1
		}
		lspLauncher, launcherErr := lspexecutor.NewSystemdLauncher(configuration.LSP.SystemctlPath)
		if launcherErr != nil {
			logger.Error("LSP systemd launcher initialization failed", "error", launcherErr)
			return 1
		}
		lspPolicy = lsp.DefaultPolicy()
		lspManager, err = lspexecutor.NewManager(lspLauncher, lspexecutor.Config{
			SocketPath:               configuration.LSP.ControlSocket,
			ExpectedWorkerUID:        lspWorkerUID,
			MaximumSessions:          configuration.LSP.MaximumSessions,
			MaximumPendingHandshakes: configuration.LSP.MaximumPendingHandshakes,
			HandshakeTimeout:         configuration.LSP.HandshakeTimeout,
			StartupTimeout:           configuration.LSP.StartupTimeout,
			StopTimeout:              configuration.LSP.StopTimeout,
			Random:                   rand.Reader,
			Policy:                   lspPolicy,
		})
		if err != nil {
			logger.Error("LSP session manager initialization failed", "error", err)
			return 1
		}
		credentialResolver, resolverErr := credential.NewEnvironmentFileResolver(os.LookupEnv, os.ReadFile)
		if resolverErr != nil {
			logger.Error("credential resolver initialization failed", "error", resolverErr)
			return 1
		}
		feedbackProvider, providerErr := feedback.NewWebhookDeliveryProvider(credentialResolver)
		if providerErr != nil {
			logger.Error("feedback delivery provider initialization failed", "code", feedback.CodeOf(providerErr))
			return 1
		}
		modelProvider, providerErr := chatagent.NewOpenAICompatibleProvider(credentialResolver)
		if providerErr != nil {
			logger.Error("model connection provider initialization failed", "code", chatagent.CodeOf(providerErr))
			return 1
		}
		modelProbeApplication, err = modelprobe.NewService(configurationApplication, modelProvider)
		if err != nil {
			logger.Error("model connection probe initialization failed", "code", modelprobe.CodeOf(err))
			return 1
		}
		writeRuntime, err = runtimeapp.New(pool, configuration, feedbackProvider, logger)
		if err != nil {
			logger.Error("write runtime initialization failed", "error", err)
			return 1
		}
		recommendationQueueService, queueErr := recommendation.NewQueueService(
			recommendationRepository,
			writeRuntime.Artifacts,
			recommendation.ServiceConfig{
				MaximumInputBundleBytes: configuration.Recommendation.MaximumInputBundleBytes,
			},
		)
		if queueErr != nil {
			logger.Error("recommendation queue service initialization failed", "code", recommendation.CodeOf(queueErr))
			return 1
		}
		recommendationQueueApplication, queueErr = recommendation.NewQueueApplicationService(authService, recommendationQueueService)
		if queueErr != nil {
			logger.Error("recommendation queue application initialization failed", "code", recommendation.CodeOf(queueErr))
			return 1
		}
		trainerTokenResolver, resolverErr := traineragentserver.NewEnvironmentFileTokenResolver(os.LookupEnv, os.ReadFile)
		if resolverErr != nil {
			logger.Error("trainer-agent token resolver initialization failed", "error", resolverErr)
			return 1
		}
		if _, resolveErr := trainerTokenResolver.Resolve(
			context.Background(),
			configuration.Recommendation.TrainerAgentID,
		); resolveErr != nil {
			logger.Error("trainer-agent token credential validation failed", "error", resolveErr)
			return 1
		}
		trainerAgentVerifier, err = traineragentserver.NewScopedBearerVerifier(
			configuration.Recommendation.TrainerAgentID,
			trainerTokenResolver,
		)
		if err != nil {
			logger.Error("trainer-agent bearer verifier initialization failed", "error", err)
			return 1
		}
		trainerAgentTransport, err = traineragentserver.NewService(
			recommendationRepository,
			writeRuntime.Artifacts,
			traineragentserver.ServiceConfig{
				LeaseDuration:            configuration.Recommendation.TrainerLeaseDuration,
				RetryDelay:               configuration.Recommendation.TrainerRetryDelay,
				MaximumInputBundleBytes:  configuration.Recommendation.MaximumInputBundleBytes,
				MaximumOutputBundleBytes: configuration.Recommendation.MaximumOutputBundleBytes,
			},
		)
		if err != nil {
			logger.Error("trainer-agent transport initialization failed", "error", err)
			return 1
		}
		chatRuntime, err = chatagent.NewRuntime(
			chatAgentRepository,
			credentialResolver,
			chatagent.RuntimeConfig{
				WorkerOwner:         configuration.ChatAgent.WorkerOwner,
				LeaseDuration:       configuration.ChatAgent.LeaseDuration,
				PollInterval:        configuration.ChatAgent.PollInterval,
				MaximumContextItems: configuration.ChatAgent.MaximumContextItems,
				MaximumToolRounds:   configuration.ChatAgent.MaximumToolRounds,
			},
			logger,
		)
		if err != nil {
			logger.Error("chat agent runtime initialization failed", "error", err)
			return 1
		}
		judgeWorkerUID, resolveErr := resolveSystemUserUID(configuration.Judge.WorkerUser, user.Lookup)
		if resolveErr != nil {
			logger.Error("OJ judge worker identity resolution failed", "error", resolveErr)
			return 1
		}
		judgeRuntime, err = judgeexecutor.NewProductionRuntime(
			ojRepository,
			writeRuntime.Artifacts,
			judgeexecutor.RuntimeConfig{
				SystemctlPath: configuration.Judge.SystemctlPath,
				Executor: judgeexecutor.Config{
					SocketDirectory:  configuration.Judge.SocketDirectory,
					ExpectedJudgeUID: judgeWorkerUID,
					StartupTimeout:   configuration.Judge.StartupTimeout,
					SessionTimeout:   configuration.Judge.SessionTimeout,
					StopTimeout:      configuration.Judge.StopTimeout,
					Policy:           ojPolicy,
				},
				Worker: oj.WorkerConfig{
					Owner:           configuration.Judge.WorkerOwner,
					LeaseDuration:   configuration.Judge.LeaseDuration,
					RetryDelay:      configuration.Judge.RetryDelay,
					MaximumAttempts: configuration.Judge.MaximumAttempts,
				},
				Supervisor: judgeexecutor.SupervisorConfig{
					PollInterval: configuration.Judge.PollInterval,
				},
			},
			logger,
		)
		if err != nil {
			logger.Error("OJ judge runtime initialization failed", "error", err)
			return 1
		}
	}
	rateLimiter, err := httpapi.NewDefaultRateLimiter()
	if err != nil {
		logger.Error("API rate limiter initialization failed")
		return 1
	}
	var ojApplication *oj.ApplicationService
	if writeRuntime != nil {
		ojApplication, err = oj.NewApplicationService(authService, ojService, writeRuntime.Artifacts, ojPolicy)
	} else {
		ojApplication, err = oj.NewReadOnlyApplicationService(authService, ojService, ojPolicy)
	}
	if err != nil {
		logger.Error("OJ application initialization failed", "code", oj.CodeOf(err))
		return 1
	}
	httpDependencies := httpRuntimeDependencies{
		readiness:                 readiness,
		logger:                    logger,
		auth:                      authService,
		enrollment:                authService,
		accountManagement:         accountApplicationService,
		rateLimiter:               rateLimiter,
		requestIDRandom:           rand.Reader,
		importReader:              importReader,
		studentAnalytics:          studentApplicationService,
		achievement:               achievementApplication,
		examCatalog:               examCatalogApplication,
		examGeneration:            examGenerationApplication,
		administration:            administrationApplication,
		configuration:             configurationApplication,
		feedback:                  feedbackApplication,
		agentNotes:                agentNotesApplication,
		chatAgent:                 chatAgentApplication,
		oj:                        ojApplication,
		ojPolicy:                  ojPolicy,
		recommendationReader:      recommendationReaderApplication,
		recommendationAdminReader: recommendationAdminReaderApplication,
	}
	httpDependencies, err = bindWriteHTTPRuntimeDependencies(
		httpDependencies,
		configuration.Write.Enabled,
		writeHTTPRuntime{
			components:          writeRuntime,
			lspManager:          lspManager,
			lspPolicy:           lspPolicy,
			recommendationQueue: recommendationQueueApplication,
			modelProbe:          modelProbeApplication,
			trainerVerifier:     trainerAgentVerifier,
			trainerAgent:        trainerAgentTransport,
		},
	)
	if err != nil {
		logger.Error("HTTP write dependency binding failed", "error", err)
		return 1
	}
	apiHandler, err := httpapi.New(buildHTTPHandlerOptions(configuration, httpDependencies))
	if err != nil {
		logger.Error("HTTP API initialization failed", "error", err)
		return 1
	}
	handler, err := publicdelivery.New(apiHandler)
	if err != nil {
		logger.Error("public delivery initialization failed", "error", err)
		return 1
	}
	httpServer := server.New(server.Options{
		Address:           configuration.HTTP.Address,
		ReadHeaderTimeout: configuration.HTTP.ReadHeaderTimeout,
		ReadTimeout:       configuration.HTTP.ReadTimeout,
		IdleTimeout:       configuration.HTTP.IdleTimeout,
	}, handler)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	group, runContext := errgroup.WithContext(ctx)
	if lspManager != nil {
		group.Go(func() error {
			return lspManager.Serve(runContext)
		})
		select {
		case <-lspManager.Ready():
		case <-runContext.Done():
			if err := group.Wait(); err != nil {
				logger.Error("LSP control manager startup failed", "error", err)
				return 1
			}
			return 0
		}
	}
	group.Go(func() error {
		return server.Run(runContext, httpServer, configuration.HTTP.ShutdownTimeout, logger)
	})
	if writeRuntime != nil {
		group.Go(func() error {
			return writeRuntime.Workers.Run(runContext)
		})
	}
	if judgeRuntime != nil {
		group.Go(func() error {
			return judgeRuntime.Run(runContext)
		})
	}
	if chatRuntime != nil {
		group.Go(func() error {
			return chatRuntime.Supervisor.Run(runContext)
		})
	}
	if err := group.Wait(); err != nil {
		logger.Error("HTTP server stopped with an error", "error", err)
		return 1
	}
	return 0
}

type httpRuntimeDependencies struct {
	readiness                    httpapi.ReadinessChecker
	logger                       *slog.Logger
	auth                         httpapi.AuthService
	enrollment                   httpapi.EnrollmentService
	accountManagement            httpapi.AccountManagementService
	rateLimiter                  httpapi.RequestRateLimiter
	requestIDRandom              io.Reader
	artifacts                    httpapi.ArtifactPublisher
	imports                      httpapi.ImportQueue
	importReader                 httpapi.ImportReader
	studentAnalytics             httpapi.StudentAnalyticsService
	achievement                  httpapi.AchievementService
	examCatalog                  httpapi.ExamCatalogService
	examGeneration               httpapi.ExamGenerationService
	administration               httpapi.AdministrationService
	configuration                httpapi.ConfigurationService
	feedback                     httpapi.FeedbackService
	agentNotes                   httpapi.AgentNotesService
	chatAgent                    httpapi.ChatAgentService
	oj                           httpapi.OJService
	ojPolicy                     oj.Policy
	recommendationReader         httpapi.RecommendationReader
	recommendationAdminReader    httpapi.RecommendationAdminReader
	recommendationQueue          httpapi.RecommendationQueue
	modelProbe                   httpapi.ModelProbeService
	lsp                          httpapi.LSPService
	lspPolicy                    lsp.Policy
	trainerAgentTransportEnabled bool
	trainerAgentVerifier         httpapi.TrainerAgentBearerVerifier
	trainerAgent                 httpapi.TrainerAgentService
}

type writeHTTPRuntime struct {
	components          *runtimeapp.Components
	lspManager          *lspexecutor.Manager
	lspPolicy           lsp.Policy
	recommendationQueue *recommendation.QueueApplicationService
	modelProbe          *modelprobe.Service
	trainerVerifier     *traineragentserver.ScopedBearerVerifier
	trainerAgent        *traineragentserver.Service
}

func bindWriteHTTPRuntimeDependencies(
	dependencies httpRuntimeDependencies,
	writesEnabled bool,
	writeRuntime writeHTTPRuntime,
) (httpRuntimeDependencies, error) {
	if !writesEnabled {
		if writeRuntime != (writeHTTPRuntime{}) {
			return httpRuntimeDependencies{}, errors.New("disabled writes cannot bind write runtime dependencies")
		}
		return dependencies, nil
	}
	if writeRuntime.components == nil || writeRuntime.components.Artifacts == nil || writeRuntime.components.Imports == nil ||
		writeRuntime.lspManager == nil || !lsp.ValidPolicy(writeRuntime.lspPolicy) ||
		writeRuntime.recommendationQueue == nil || writeRuntime.modelProbe == nil ||
		writeRuntime.trainerVerifier == nil || writeRuntime.trainerAgent == nil {
		return httpRuntimeDependencies{}, errors.New("enabled writes require the complete write runtime dependency set")
	}
	dependencies.artifacts = writeRuntime.components.Artifacts
	dependencies.imports = writeRuntime.components.Imports
	dependencies.recommendationQueue = writeRuntime.recommendationQueue
	dependencies.modelProbe = writeRuntime.modelProbe
	dependencies.lsp = writeRuntime.lspManager
	dependencies.lspPolicy = writeRuntime.lspPolicy
	dependencies.trainerAgentTransportEnabled = true
	dependencies.trainerAgentVerifier = writeRuntime.trainerVerifier
	dependencies.trainerAgent = writeRuntime.trainerAgent
	return dependencies, nil
}

func buildHTTPHandlerOptions(configuration config.Config, dependencies httpRuntimeDependencies) httpapi.Options {
	return httpapi.Options{
		Readiness:                    dependencies.readiness,
		Version:                      version.Current(),
		Logger:                       dependencies.logger,
		Auth:                         dependencies.auth,
		Enrollment:                   dependencies.enrollment,
		AccountManagement:            dependencies.accountManagement,
		AllowedOrigins:               configuration.Auth.AllowedOrigins,
		RateLimiter:                  dependencies.rateLimiter,
		RequestIDRandom:              dependencies.requestIDRandom,
		TrustedProxyCIDRs:            configuration.HTTP.TrustedProxyCIDRs,
		ClientIPHeader:               configuration.HTTP.ClientIPHeader,
		Artifacts:                    dependencies.artifacts,
		Imports:                      dependencies.imports,
		ImportReader:                 dependencies.importReader,
		StudentAnalytics:             dependencies.studentAnalytics,
		Achievement:                  dependencies.achievement,
		ExamCatalog:                  dependencies.examCatalog,
		ExamGeneration:               dependencies.examGeneration,
		Administration:               dependencies.administration,
		Configuration:                dependencies.configuration,
		Feedback:                     dependencies.feedback,
		AgentNotes:                   dependencies.agentNotes,
		ChatAgent:                    dependencies.chatAgent,
		OJ:                           dependencies.oj,
		OJPolicy:                     dependencies.ojPolicy,
		RecommendationReader:         dependencies.recommendationReader,
		RecommendationAdminReader:    dependencies.recommendationAdminReader,
		RecommendationQueue:          dependencies.recommendationQueue,
		ModelProbe:                   dependencies.modelProbe,
		LSP:                          dependencies.lsp,
		LSPPolicy:                    dependencies.lspPolicy,
		TrainerAgentTransportEnabled: dependencies.trainerAgentTransportEnabled,
		TrainerAgentVerifier:         dependencies.trainerAgentVerifier,
		TrainerAgent:                 dependencies.trainerAgent,
		AuthBodyTimeout:              configuration.HTTP.AuthBodyTimeout,
		UploadBodyTimeout:            configuration.HTTP.UploadBodyTimeout,
		SSEMaxDuration:               configuration.HTTP.SSEMaxDuration,
		SSEReauthInterval:            configuration.HTTP.SSEReauthInterval,
		SSEWriteTimeout:              configuration.HTTP.SSEWriteTimeout,
		MaxActiveSSE:                 configuration.HTTP.MaxActiveSSE,
		Capabilities: httpapi.Capabilities{
			PintiaSnapshotSchema: pintia.SchemaV2,
			PintiaSchemaSHA256:   pintia.ExpectedSchemaSHA256,
			MaxUploadBytes:       configuration.Artifact.MaxBytes,
			MaxProblems:          configuration.Pintia.MaxProblems,
			MaxParticipants:      configuration.Pintia.MaxParticipants,
			MaxSubmissions:       configuration.Pintia.MaxSubmissions,
			MaxCodeBytes:         configuration.Pintia.MaxCodeBytes,
			WritesEnabled:        configuration.Write.Enabled,
		},
	}
}

func productionOJPolicy(configuration config.Config) oj.Policy {
	policy := oj.DefaultPolicy()
	policy.MaximumTestBundleBytes = min(policy.MaximumTestBundleBytes, configuration.Artifact.MaxBytes)
	policy.MaximumSourceBytes = min(policy.MaximumSourceBytes, configuration.Artifact.MaxBytes)
	policy.MaximumStdinBytes = min(policy.MaximumStdinBytes, configuration.Artifact.MaxBytes)
	return policy
}

func validateCommand(args []string) error {
	if len(args) != 1 || args[0] != "serve" {
		return errors.New("usage: ascendanyd serve")
	}
	return nil
}

func resolveSystemUserUID(name string, lookup func(string) (*user.User, error)) (uint32, error) {
	if name == "" || lookup == nil {
		return 0, errors.New("system user name and lookup are required")
	}
	account, err := lookup(name)
	if err != nil || account == nil || account.Username != name {
		return 0, fmt.Errorf("system user %q could not be resolved", name)
	}
	value, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("system user %q must resolve to a non-root uint32 UID", name)
	}
	return uint32(value), nil
}
