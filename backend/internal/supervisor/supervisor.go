package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
)

type ImportWorker interface {
	ClaimAndProcess(context.Context, string) (*importing.ImportOutcome, error)
}

type AnalyticsWorker interface {
	RunOne(context.Context) (*analytics.RunOutcome, error)
}

type ArtifactReconciler interface {
	Reconcile(context.Context) error
}

type FeedbackWorker interface {
	RunOne(context.Context) (*feedback.DeliveryOutcome, error)
}

type Config struct {
	ImportOwner               string
	ImportPollInterval        time.Duration
	AnalyticsPollInterval     time.Duration
	FeedbackPollInterval      time.Duration
	ArtifactReconcileInterval time.Duration
}

type Supervisor struct {
	imports   ImportWorker
	analytics AnalyticsWorker
	feedback  FeedbackWorker
	artifacts ArtifactReconciler
	config    Config
	logger    *slog.Logger
}

func New(
	imports ImportWorker,
	analyticsWorker AnalyticsWorker,
	feedbackWorker FeedbackWorker,
	artifacts ArtifactReconciler,
	config Config,
	logger *slog.Logger,
) (*Supervisor, error) {
	if imports == nil {
		return nil, errors.New("import worker is required")
	}
	if analyticsWorker == nil {
		return nil, errors.New("analytics worker is required")
	}
	if feedbackWorker == nil {
		return nil, errors.New("feedback worker is required")
	}
	if artifacts == nil {
		return nil, errors.New("artifact reconciler is required")
	}
	if config.ImportOwner == "" {
		return nil, errors.New("import worker owner is required")
	}
	if config.ImportPollInterval <= 0 {
		return nil, errors.New("import poll interval must be positive")
	}
	if config.AnalyticsPollInterval <= 0 {
		return nil, errors.New("analytics poll interval must be positive")
	}
	if config.FeedbackPollInterval <= 0 {
		return nil, errors.New("feedback poll interval must be positive")
	}
	if config.ArtifactReconcileInterval <= 0 {
		return nil, errors.New("artifact reconcile interval must be positive")
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	return &Supervisor{
		imports: imports, analytics: analyticsWorker, feedback: feedbackWorker,
		artifacts: artifacts, config: config, logger: logger,
	}, nil
}

func (supervisor *Supervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("supervisor context is required")
	}
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	fatalErrors := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(4)
	go func() {
		defer workers.Done()
		supervisor.runImports(workerContext)
	}()
	go func() {
		defer workers.Done()
		supervisor.runAnalytics(workerContext)
	}()
	go func() {
		defer workers.Done()
		supervisor.runFeedback(workerContext)
	}()
	go func() {
		defer workers.Done()
		if err := supervisor.runArtifactReconciliation(workerContext); err != nil {
			fatalErrors <- err
		}
	}()
	var result error
	select {
	case <-ctx.Done():
	case err := <-fatalErrors:
		result = fmt.Errorf("artifact reconciliation failed: %w", err)
	}
	cancel()
	workers.Wait()
	return result
}

func (supervisor *Supervisor) runArtifactReconciliation(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := supervisor.artifacts.Reconcile(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if !wait(ctx, supervisor.config.ArtifactReconcileInterval) {
			return nil
		}
	}
	return nil
}

func (supervisor *Supervisor) runImports(ctx context.Context) {
	for ctx.Err() == nil {
		outcome, err := supervisor.imports.ClaimAndProcess(ctx, supervisor.config.ImportOwner)
		if err != nil && !errors.Is(err, context.Canceled) {
			supervisor.logger.ErrorContext(ctx, "import worker iteration failed", "error", err)
		}
		if outcome != nil {
			supervisor.logger.InfoContext(ctx, "import worker iteration completed", "disposition", outcome.Disposition)
		}
		if err != nil || outcome == nil {
			if !wait(ctx, supervisor.config.ImportPollInterval) {
				return
			}
		}
	}
}

func (supervisor *Supervisor) runAnalytics(ctx context.Context) {
	for ctx.Err() == nil {
		outcome, err := supervisor.analytics.RunOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			supervisor.logger.ErrorContext(ctx, "analytics worker iteration failed", "error", err)
		}
		if outcome != nil {
			supervisor.logger.InfoContext(ctx, "analytics worker iteration completed", "disposition", outcome.Disposition)
		}
		if err != nil || outcome == nil {
			if !wait(ctx, supervisor.config.AnalyticsPollInterval) {
				return
			}
		}
	}
}

func (supervisor *Supervisor) runFeedback(ctx context.Context) {
	for ctx.Err() == nil {
		outcome, err := supervisor.feedback.RunOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			supervisor.logger.ErrorContext(ctx, "feedback delivery worker iteration failed", "error", err)
		}
		if outcome != nil {
			supervisor.logger.InfoContext(ctx, "feedback delivery worker iteration completed", "disposition", outcome.Disposition)
		}
		if err != nil || outcome == nil {
			if !wait(ctx, supervisor.config.FeedbackPollInterval) {
				return
			}
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
