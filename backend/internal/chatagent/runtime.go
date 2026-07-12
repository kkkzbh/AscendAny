package chatagent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	credentialdomain "github.com/kkkzbh/AscendAny/backend/internal/credential"
)

type RuntimeConfig struct {
	WorkerOwner         string
	LeaseDuration       time.Duration
	PollInterval        time.Duration
	MaximumContextItems int
	MaximumToolRounds   int
}

type RuntimeComponents struct {
	Repository *PostgresRepository
	Provider   *OpenAICompatibleProvider
	Tools      *RuntimeToolExecutor
	Worker     *Worker
	Supervisor *RuntimeSupervisor
}

func NewRuntime(
	repository *PostgresRepository,
	credentials credentialdomain.Resolver,
	configuration RuntimeConfig,
	logger *slog.Logger,
) (*RuntimeComponents, error) {
	if repository == nil || credentials == nil || logger == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent runtime", errors.New("PostgreSQL repository, credential resolver, and logger are required"))
	}
	provider, err := NewOpenAICompatibleProvider(credentials)
	if err != nil {
		return nil, err
	}
	tools, err := NewRuntimeToolExecutor(repository)
	if err != nil {
		return nil, err
	}
	worker, err := NewWorker(repository, provider, tools, WorkerConfig{
		Owner: configuration.WorkerOwner, LeaseDuration: configuration.LeaseDuration,
		MaximumContextItems: configuration.MaximumContextItems, MaximumToolRounds: configuration.MaximumToolRounds,
	})
	if err != nil {
		return nil, err
	}
	supervisor, err := NewRuntimeSupervisor(worker, RuntimeSupervisorConfig{PollInterval: configuration.PollInterval}, logger)
	if err != nil {
		return nil, err
	}
	return &RuntimeComponents{Repository: repository, Provider: provider, Tools: tools, Worker: worker, Supervisor: supervisor}, nil
}

type AgentWorkRunner interface {
	RunOne(context.Context) (*WorkerOutcome, error)
}

type RuntimeSupervisorConfig struct {
	PollInterval time.Duration
}

type RuntimeSupervisor struct {
	worker       AgentWorkRunner
	pollInterval time.Duration
	logger       *slog.Logger
}

func NewRuntimeSupervisor(worker AgentWorkRunner, configuration RuntimeSupervisorConfig, logger *slog.Logger) (*RuntimeSupervisor, error) {
	if worker == nil || logger == nil || configuration.PollInterval < 10*time.Millisecond || configuration.PollInterval > time.Minute {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent runtime supervisor", errors.New("worker, logger, and poll interval in [10ms, 1m] are required"))
	}
	return &RuntimeSupervisor{worker: worker, pollInterval: configuration.PollInterval, logger: logger}, nil
}

func (supervisor *RuntimeSupervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return domainError(ErrorInvalidInput, true, "run chat agent runtime supervisor", errors.New("context is required"))
	}
	for ctx.Err() == nil {
		outcome, err := supervisor.worker.RunOne(ctx)
		if context.Cause(ctx) != nil {
			return nil
		}
		if err != nil {
			supervisor.logger.ErrorContext(ctx, "chat agent worker iteration failed", "code", CodeOf(err))
		}
		if outcome != nil {
			attributes := []any{"runId", outcome.RunID, "disposition", outcome.Disposition}
			if outcome.FailureCode != nil {
				attributes = append(attributes, "failureCode", *outcome.FailureCode)
			}
			supervisor.logger.InfoContext(ctx, "chat agent worker iteration completed", attributes...)
		}
		if err != nil || outcome == nil {
			if !waitRuntimePoll(ctx, supervisor.pollInterval) {
				return nil
			}
		}
	}
	return nil
}

func waitRuntimePoll(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
