package traineragent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type WorkRunner interface {
	RunOne(context.Context) (*WorkerOutcome, error)
}

type SupervisorConfig struct {
	PollInterval time.Duration
}

type Supervisor struct {
	worker   WorkRunner
	recorder AcceptanceCandidateRecorder
	config   SupervisorConfig
	logger   *slog.Logger
}

func NewSupervisor(
	worker WorkRunner,
	recorder AcceptanceCandidateRecorder,
	config SupervisorConfig,
	logger *slog.Logger,
) (*Supervisor, error) {
	if worker == nil || recorder == nil {
		return nil, errors.New("trainer-agent worker and acceptance candidate recorder are required")
	}
	if config.PollInterval < 10*time.Millisecond {
		return nil, errors.New("trainer-agent poll interval must be at least 10 milliseconds")
	}
	if logger == nil {
		return nil, errors.New("trainer-agent logger is required")
	}
	return &Supervisor{worker: worker, recorder: recorder, config: config, logger: logger}, nil
}

func (supervisor *Supervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("trainer-agent supervisor context is required")
	}
	for ctx.Err() == nil {
		outcome, err := supervisor.worker.RunOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			supervisor.logger.ErrorContext(ctx, "trainer-agent iteration failed", "error", err)
		}
		if outcome != nil && err != nil {
			return errors.New("trainer-agent worker returned an outcome together with an error")
		}
		if outcome != nil {
			switch outcome.Disposition {
			case WorkerActivated, WorkerSuperseded:
				if recordErr := supervisor.recorder.Record(*outcome); recordErr != nil {
					return fmt.Errorf("record trainer acceptance candidate: %w", recordErr)
				}
			case WorkerFailed, WorkerRequeued:
			default:
				return errors.New("trainer-agent worker returned an unsupported disposition")
			}
			supervisor.logger.InfoContext(
				ctx, "trainer-agent iteration completed", "run_id", outcome.RunID, "disposition", outcome.Disposition,
			)
		}
		if err != nil || outcome == nil {
			if !waitForPoll(ctx, supervisor.config.PollInterval) {
				return nil
			}
		}
	}
	return nil
}

func waitForPoll(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
