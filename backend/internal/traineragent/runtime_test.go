package traineragent

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/trainerprocess"
)

func TestProductionConfigRequiresOneLeaseAndBundleLimitContract(t *testing.T) {
	t.Parallel()
	configuration := Config{
		HTTP: HTTPClientConfig{
			LeaseDuration: 5 * time.Minute, MaximumInputBundleBytes: 100, MaximumOutputBundleBytes: 200,
		},
		Worker:                  WorkerConfig{LeaseDuration: 5 * time.Minute},
		Supervisor:              SupervisorConfig{PollInterval: time.Second},
		AcceptanceCandidatePath: "/var/lib/ascendany-trainer/acceptance/trainer-latest.json",
		Trainer: trainerprocess.SubprocessTrainerConfig{
			SandboxExecutable:    "/usr/bin/bwrap",
			PythonExecutable:     productionTrainerRuntimeSelector + "/python/bin/python3.14",
			RuntimeRoot:          productionTrainerRuntimeSelector,
			RuntimeReadOnlyPaths: []string{"/lib", "/lib64", productionTrainerRuntimeSelector, "/sys"},
			NVIDIADevicePaths:    []string{"/dev/nvidia-uvm", "/dev/nvidia0", "/dev/nvidiactl"},
			MaximumInputBytes:    100, MaximumOutputBytes: 200,
		},
	}
	if err := validateProductionConfig(configuration); err != nil {
		t.Fatal(err)
	}

	configuration.Worker.LeaseDuration = time.Minute
	if err := validateProductionConfig(configuration); err == nil {
		t.Fatal("mismatched lease error = nil")
	}
	configuration.Worker.LeaseDuration = configuration.HTTP.LeaseDuration
	configuration.Trainer.MaximumOutputBytes++
	if err := validateProductionConfig(configuration); err == nil {
		t.Fatal("mismatched bundle limit error = nil")
	}
	configuration.Trainer.MaximumOutputBytes = configuration.HTTP.MaximumOutputBundleBytes
	configuration.Supervisor.PollInterval = configuration.HTTP.LeaseDuration
	if err := validateProductionConfig(configuration); err == nil {
		t.Fatal("long poll interval error = nil")
	}
}

func TestProductionRuntimeRejectsDevelopmentBuildIdentity(t *testing.T) {
	t.Parallel()
	configuration := Config{
		HTTP: HTTPClientConfig{
			BaseURL: "https://trainer.example", AgentID: "rtx-01", LeaseDuration: 5 * time.Minute,
			MaximumInputBundleBytes: 100, MaximumOutputBundleBytes: 200,
		},
		Worker:     WorkerConfig{LeaseDuration: 5 * time.Minute},
		Supervisor: SupervisorConfig{PollInterval: time.Second},
		Trainer: trainerprocess.SubprocessTrainerConfig{
			SandboxExecutable:    "/usr/bin/bwrap",
			PythonExecutable:     productionTrainerRuntimeSelector + "/python/bin/python3.14",
			RuntimeRoot:          productionTrainerRuntimeSelector,
			RuntimeReadOnlyPaths: []string{"/lib", "/lib64", productionTrainerRuntimeSelector, "/sys"},
			NVIDIADevicePaths:    []string{"/dev/nvidia-uvm", "/dev/nvidia0", "/dev/nvidiactl"},
			MaximumInputBytes:    100, MaximumOutputBytes: 200,
		},
		AcceptanceCandidatePath: "/var/lib/ascendany-trainer/acceptance/trainer-latest.json",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, release := range []ReleaseIdentity{
		{Version: "dev", Commit: strings.Repeat("a", 40)},
		{Version: "1.2.3", Commit: "unknown"},
	} {
		if _, err := NewProductionRuntime(configuration, release, logger); err == nil ||
			!strings.Contains(err.Error(), "canonical release") {
			t.Fatalf("release %#v error = %v", release, err)
		}
	}
}
