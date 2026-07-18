package runtimeapp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/config"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
	"github.com/kkkzbh/AscendAny/backend/internal/supervisor"
)

// Database is the exact PostgreSQL capability required by the online import
// runtime. pgxpool.Pool satisfies it directly.
type Database interface {
	importing.PgxBeginner
	importing.PgxReader
	analytics.PgxBeginner
	feedback.PgxBeginner
}

type Components struct {
	Artifacts *artifact.Store
	Imports   *importing.Service
	Workers   *supervisor.Supervisor
}

func New(
	database Database,
	configuration config.Config,
	analyticsConfiguration analytics.ParsedConfig,
	feedbackProvider feedback.DeliveryProvider,
	logger *slog.Logger,
) (*Components, error) {
	if database == nil {
		return nil, errors.New("runtime database is required")
	}
	if !configuration.Write.Enabled {
		return nil, errors.New("write runtime cannot start while writes are disabled")
	}
	if logger == nil {
		return nil, errors.New("runtime logger is required")
	}
	if feedbackProvider == nil {
		return nil, errors.New("feedback delivery provider is required")
	}
	parsedAnalytics, err := analytics.VerifyParsedConfig(analyticsConfiguration)
	if err != nil {
		return nil, fmt.Errorf("verify analytics runtime configuration: %w", err)
	}
	artifacts, err := artifact.NewStore(configuration.Artifact.Root, configuration.Artifact.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("initialize artifact store: %w", err)
	}
	imports, err := importing.NewService(database)
	if err != nil {
		return nil, fmt.Errorf("initialize import service: %w", err)
	}
	importWorker, err := importing.NewWorker(database, artifacts, importing.WorkerConfig{
		LeaseDuration: configuration.Import.LeaseDuration,
		RetryDelay:    configuration.Import.RetryDelay,
		PintiaLimits: pintia.Limits{
			MaxTotalBytes:               configuration.Artifact.MaxBytes,
			MaxTotalNodes:               configuration.Pintia.MaxTotalNodes,
			MaxTotalStringBytes:         configuration.Pintia.MaxTotalStringBytes,
			MaxJSONDepth:                configuration.Pintia.MaxJSONDepth,
			MaxStringBytes:              configuration.Pintia.MaxStringBytes,
			MaxProblems:                 configuration.Pintia.MaxProblems,
			MaxParticipants:             configuration.Pintia.MaxParticipants,
			MaxProblemResultsPerRanking: configuration.Pintia.MaxProblemResultsPerRanking,
			MaxSubmissions:              configuration.Pintia.MaxSubmissions,
			MaxCaseResultsPerSubmission: configuration.Pintia.MaxCaseResultsPerSubmission,
			MaxCodeBytes:                configuration.Pintia.MaxCodeBytes,
		},
		Analytics: importing.AnalyticsConfig{
			AlgorithmVersion: parsedAnalytics.Value.AlgorithmVersion,
			ConfigSHA256:     parsedAnalytics.SHA256,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize import worker: %w", err)
	}
	analyticsWorker, err := analytics.NewWorker(database, analytics.WorkerConfig{
		Owner:         configuration.Analytics.WorkerOwner,
		LeaseDuration: configuration.Analytics.LeaseDuration,
		AnalyticsJSON: parsedAnalytics.Canonical,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize analytics worker: %w", err)
	}
	feedbackRepository, err := feedback.NewPostgresRepository(database)
	if err != nil {
		return nil, fmt.Errorf("initialize feedback repository: %w", err)
	}
	feedbackWorker, err := feedback.NewDeliveryWorker(feedbackRepository, artifacts, feedbackProvider, feedback.DeliveryWorkerConfig{
		Owner:         configuration.Feedback.WorkerOwner,
		LeaseDuration: configuration.Feedback.LeaseDuration,
		RetryDelay:    configuration.Feedback.RetryDelay,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize feedback delivery worker: %w", err)
	}
	reconciler := &artifactReconciler{
		store:    artifacts,
		database: database,
		minAge:   configuration.Artifact.OrphanMinAge,
	}
	workers, err := supervisor.New(importWorker, analyticsWorker, feedbackWorker, reconciler, supervisor.Config{
		ImportOwner:               configuration.Import.WorkerOwner,
		ImportPollInterval:        configuration.Import.PollInterval,
		AnalyticsPollInterval:     configuration.Analytics.PollInterval,
		FeedbackPollInterval:      configuration.Feedback.PollInterval,
		ArtifactReconcileInterval: configuration.Artifact.ReconcileInterval,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize worker supervisor: %w", err)
	}
	return &Components{
		Artifacts: artifacts,
		Imports:   imports,
		Workers:   workers,
	}, nil
}

func LoadAnalyticsConfig(path string) (analytics.ParsedConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return analytics.ParsedConfig{}, fmt.Errorf("open analytics configuration: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, analytics.MaxConfigBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return analytics.ParsedConfig{}, fmt.Errorf("read analytics configuration: %w", readErr)
	}
	if closeErr != nil {
		return analytics.ParsedConfig{}, fmt.Errorf("close analytics configuration: %w", closeErr)
	}
	if len(data) > analytics.MaxConfigBytes {
		return analytics.ParsedConfig{}, fmt.Errorf("analytics configuration exceeds %d bytes", analytics.MaxConfigBytes)
	}
	parsed, err := analytics.ParseConfig(data)
	if err != nil {
		return analytics.ParsedConfig{}, fmt.Errorf("parse analytics runtime configuration: %w", err)
	}
	return parsed, nil
}
