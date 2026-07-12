package traineragent

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/recommendationprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/trainerprocess"
)

const productionTrainerRuntimeSelector = "/opt/ascendany-trainer-runtime/current"

var productionTrainerRuntimePaths = []string{
	"/lib",
	"/lib64",
	productionTrainerRuntimeSelector,
	"/sys",
}

var productionTrainerNVIDIADevices = []string{
	"/dev/nvidia-uvm",
	"/dev/nvidia0",
	"/dev/nvidiactl",
}

type Runtime struct {
	supervisor *Supervisor
}

func NewRuntime(
	transport Transport,
	trainer recommendationprotocol.Trainer,
	workerConfig WorkerConfig,
	supervisorConfig SupervisorConfig,
	recorder AcceptanceCandidateRecorder,
	logger *slog.Logger,
) (*Runtime, error) {
	worker, err := NewWorker(transport, trainer, workerConfig)
	if err != nil {
		return nil, err
	}
	supervisor, err := NewSupervisor(worker, recorder, supervisorConfig, logger)
	if err != nil {
		return nil, err
	}
	return &Runtime{supervisor: supervisor}, nil
}

func NewProductionRuntime(configuration Config, release ReleaseIdentity, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		return nil, errors.New("trainer-agent production logger is required")
	}
	if err := validateProductionConfig(configuration); err != nil {
		return nil, err
	}
	recorder, err := NewAcceptanceCandidateWriter(AcceptanceCandidateConfig{
		Path: configuration.AcceptanceCandidatePath, Release: release,
		AgentID: configuration.HTTP.AgentID, Origin: configuration.HTTP.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	transport, err := NewHTTPClient(configuration.HTTP)
	if err != nil {
		return nil, err
	}
	trainer, err := trainerprocess.NewSubprocessTrainer(configuration.Trainer)
	if err != nil {
		return nil, err
	}
	return NewRuntime(transport, trainer, configuration.Worker, configuration.Supervisor, recorder, logger)
}

func VerifyProductionRuntime(ctx context.Context, configuration Config) error {
	if ctx == nil {
		return errors.New("trainer-agent runtime verification context is required")
	}
	if err := validateProductionConfig(configuration); err != nil {
		return err
	}
	trainer, err := trainerprocess.NewSubprocessTrainer(configuration.Trainer)
	if err != nil {
		return err
	}
	return trainer.VerifyRuntime(ctx)
}

func validateProductionConfig(configuration Config) error {
	if err := validateAcceptanceCandidatePath(configuration.AcceptanceCandidatePath); err != nil {
		return err
	}
	if configuration.Worker.LeaseDuration != configuration.HTTP.LeaseDuration {
		return errors.New("trainer-agent HTTP and worker lease durations must match")
	}
	if configuration.Supervisor.PollInterval < 10*time.Millisecond ||
		configuration.Supervisor.PollInterval >= configuration.HTTP.LeaseDuration {
		return errors.New("trainer-agent poll interval must be at least 10 milliseconds and shorter than the lease duration")
	}
	if configuration.Trainer.MaximumInputBytes != configuration.HTTP.MaximumInputBundleBytes ||
		configuration.Trainer.MaximumOutputBytes != configuration.HTTP.MaximumOutputBundleBytes {
		return errors.New("trainer-agent HTTP and child-process bundle limits must match")
	}
	if configuration.Trainer.SandboxExecutable != "/usr/bin/bwrap" {
		return errors.New("trainer-agent sandbox executable must be exactly /usr/bin/bwrap")
	}
	if configuration.Trainer.PythonExecutable != productionTrainerRuntimeSelector+"/python/bin/python3.14" {
		return errors.New("trainer-agent Python executable must use the atomic runtime selector")
	}
	if configuration.Trainer.RuntimeRoot != productionTrainerRuntimeSelector {
		return errors.New("trainer-agent runtime root must use the atomic runtime selector")
	}
	if !slices.Equal(configuration.Trainer.RuntimeReadOnlyPaths, productionTrainerRuntimePaths) {
		return errors.New("trainer-agent runtime paths must contain exactly /lib, /lib64, the atomic runtime selector, and /sys")
	}
	if !slices.Equal(configuration.Trainer.NVIDIADevicePaths, productionTrainerNVIDIADevices) {
		return errors.New("trainer-agent NVIDIA device paths must contain exactly nvidia-uvm, nvidia0, and nvidiactl")
	}
	return nil
}

func (runtime *Runtime) Run(ctx context.Context) error {
	if runtime == nil || runtime.supervisor == nil {
		return errors.New("trainer-agent runtime is not initialized")
	}
	return runtime.supervisor.Run(ctx)
}
