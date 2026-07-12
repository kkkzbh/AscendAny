package judgeexecutor

import (
	"context"
	"errors"
	"log/slog"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

type RuntimeArtifactStore interface {
	ArtifactVerifier
	oj.ArtifactStore
}

type RuntimeConfig struct {
	SystemctlPath string
	Executor      Config
	Worker        oj.WorkerConfig
	Supervisor    SupervisorConfig
}

type Runtime struct {
	supervisor *Supervisor
}

func NewProductionRuntime(
	repository oj.JudgeRepository,
	artifacts RuntimeArtifactStore,
	configuration RuntimeConfig,
	logger *slog.Logger,
) (*Runtime, error) {
	launcher, err := NewSystemdLauncher(configuration.SystemctlPath)
	if err != nil {
		return nil, err
	}
	return NewRuntime(repository, artifacts, launcher, configuration, logger)
}

func NewRuntime(
	repository oj.JudgeRepository,
	artifacts RuntimeArtifactStore,
	launcher InstanceLauncher,
	configuration RuntimeConfig,
	logger *slog.Logger,
) (*Runtime, error) {
	if repository == nil || artifacts == nil || launcher == nil || logger == nil {
		return nil, errors.New("judge repository, artifact store, launcher, and logger are required")
	}
	executor, err := New(artifacts, launcher, configuration.Executor)
	if err != nil {
		return nil, err
	}
	outputs, err := oj.NewArtifactOutputPublisher(artifacts)
	if err != nil {
		return nil, err
	}
	worker, err := oj.NewWorker(repository, executor, outputs, configuration.Worker)
	if err != nil {
		return nil, err
	}
	supervisor, err := NewSupervisor(worker, configuration.Supervisor, logger)
	if err != nil {
		return nil, err
	}
	return &Runtime{supervisor: supervisor}, nil
}

func (runtime *Runtime) Run(ctx context.Context) error {
	if runtime == nil || runtime.supervisor == nil {
		return errors.New("judge runtime is not initialized")
	}
	return runtime.supervisor.Run(ctx)
}

var _ RuntimeArtifactStore = (*artifactstore.Store)(nil)
