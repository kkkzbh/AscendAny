package judgeexecutor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

type WorkRunner interface {
	RunOne(context.Context) (*oj.JudgeOutcome, error)
}

type SupervisorConfig struct {
	PollInterval time.Duration
}

type Supervisor struct {
	worker WorkRunner
	config SupervisorConfig
	logger *slog.Logger
}

func NewSupervisor(worker WorkRunner, config SupervisorConfig, logger *slog.Logger) (*Supervisor, error) {
	if worker == nil || logger == nil || config.PollInterval < 10*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("OJ worker, logger, and bounded poll interval are required")
	}
	return &Supervisor{worker: worker, config: config, logger: logger}, nil
}

func (supervisor *Supervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("OJ supervisor context is required")
	}
	for ctx.Err() == nil {
		outcome, err := supervisor.worker.RunOne(ctx)
		if context.Cause(ctx) != nil {
			return nil
		}
		if err != nil {
			supervisor.logger.ErrorContext(ctx, "OJ worker iteration failed", "code", oj.CodeOf(err))
		}
		if outcome != nil {
			attributes := []any{"jobId", outcome.JobID, "disposition", outcome.Disposition}
			if outcome.FailureCode != nil {
				attributes = append(attributes, "failureCode", *outcome.FailureCode)
			}
			supervisor.logger.InfoContext(ctx, "OJ worker iteration completed", attributes...)
		}
		if err != nil || outcome == nil {
			if !wait(ctx, supervisor.config.PollInterval) {
				return nil
			}
		}
	}
	return nil
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
