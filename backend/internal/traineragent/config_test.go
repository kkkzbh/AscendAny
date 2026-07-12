package traineragent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigBuildsHTTPAndIsolatedTrainerBoundaries(t *testing.T) {
	t.Parallel()
	environment := validTrainerAgentEnvironment()
	configuration, err := LoadConfig(environmentLookup(environment), func(path string) ([]byte, error) {
		if path != "/run/credentials/trainer_agent_token" {
			t.Fatalf("credential path = %q", path)
		}
		return []byte(testBearerToken), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if configuration.HTTP.BaseURL != "https://trainer.example" || configuration.HTTP.BearerToken != testBearerToken ||
		configuration.HTTP.AgentID != "rtx-01" || configuration.HTTP.LeaseDuration != 30*time.Second ||
		configuration.Supervisor.PollInterval != 2*time.Second || configuration.Trainer.Timeout != 6*time.Hour ||
		configuration.Trainer.WorkRoot != "/var/lib/ascendany-trainer/work" ||
		configuration.Trainer.RuntimeRoot != "/opt/ascendany-trainer-runtime/current" ||
		configuration.Trainer.PythonExecutable != "/opt/ascendany-trainer-runtime/current/python/bin/python3.14" ||
		configuration.Trainer.TrainerPackageRoot != "/opt/ascendany/v2/trainers/recommendation" ||
		configuration.AcceptanceCandidatePath != "/var/lib/ascendany-trainer/acceptance/trainer-latest.json" ||
		configuration.LogLevel != "info" {
		t.Fatalf("configuration = %#v", configuration)
	}
	if !reflect.DeepEqual(configuration.Trainer.RuntimeReadOnlyPaths, []string{"/lib", "/lib64", "/opt/ascendany-trainer-runtime/current", "/sys"}) {
		t.Fatalf("runtime paths = %#v", configuration.Trainer.RuntimeReadOnlyPaths)
	}
	if !reflect.DeepEqual(configuration.Trainer.NVIDIADevicePaths, []string{"/dev/nvidia-uvm", "/dev/nvidia0", "/dev/nvidiactl"}) {
		t.Fatalf("NVIDIA devices = %#v", configuration.Trainer.NVIDIADevicePaths)
	}
	if !reflect.DeepEqual(configuration.Trainer.Environment, map[string]string{
		"CUBLAS_WORKSPACE_CONFIG": ":4096:8", "CUDA_VISIBLE_DEVICES": "0", "MKL_NUM_THREADS": "8",
		"OMP_NUM_THREADS": "8", "OPENBLAS_NUM_THREADS": "8",
	}) {
		t.Fatalf("trainer environment = %#v", configuration.Trainer.Environment)
	}
}

func TestLoadConfigRejectsNoncanonicalOrUnsafeValues(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		mutate   func(map[string]string)
		readFile ReadFile
		want     string
	}{
		"missing endpoint": {
			mutate: func(environment map[string]string) { delete(environment, "ASCENDANY_TRAINER_AGENT_ENDPOINT") },
			want:   "ASCENDANY_TRAINER_AGENT_ENDPOINT is required",
		},
		"missing acceptance candidate path": {
			mutate: func(environment map[string]string) {
				delete(environment, "ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH")
			},
			want: "ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH is required",
		},
		"HTTP endpoint": {
			mutate: func(environment map[string]string) {
				environment["ASCENDANY_TRAINER_AGENT_ENDPOINT"] = "http://trainer.example"
			},
			want: "canonical HTTPS origin",
		},
		"padded token": {
			readFile: func(string) ([]byte, error) { return []byte(testBearerToken + "\n"), nil },
			want:     "without padding",
		},
		"credential read failure": {
			readFile: func(string) ([]byte, error) { return nil, errors.New("secret path detail") },
			want:     "credential cannot be read",
		},
		"unsorted runtime paths": {
			mutate: func(environment map[string]string) {
				environment["ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS"] = "/usr,/lib"
			},
			want: "bytewise sorted",
		},
		"filesystem root runtime mount": {
			mutate: func(environment map[string]string) {
				environment["ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS"] = "/,/usr"
			},
			want: "below filesystem root",
		},
		"uppercase log level": {
			mutate: func(environment map[string]string) { environment["ASCENDANY_TRAINER_AGENT_LOG_LEVEL"] = "INFO" },
			want:   "must be debug, info, warn, or error",
		},
		"request exceeds renewal interval": {
			mutate: func(environment map[string]string) { environment["ASCENDANY_TRAINER_AGENT_REQUEST_TIMEOUT"] = "11s" },
			want:   "one lease renewal interval",
		},
		"poll exceeds lease": {
			mutate: func(environment map[string]string) { environment["ASCENDANY_TRAINER_AGENT_POLL_INTERVAL"] = "30s" },
			want:   "shorter than the lease",
		},
		"noncanonical trainer thread count": {
			mutate: func(environment map[string]string) {
				environment["ASCENDANY_TRAINER_AGENT_OMP_NUM_THREADS"] = "08"
			},
			want: "canonical integer from 1 to 256",
		},
		"trainer thread count exceeds bound": {
			mutate: func(environment map[string]string) {
				environment["ASCENDANY_TRAINER_AGENT_OPENBLAS_NUM_THREADS"] = "257"
			},
			want: "canonical integer from 1 to 256",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			environment := validTrainerAgentEnvironment()
			if test.mutate != nil {
				test.mutate(environment)
			}
			reader := test.readFile
			if reader == nil {
				reader = func(string) ([]byte, error) { return []byte(testBearerToken), nil }
			}
			_, err := LoadConfig(environmentLookup(environment), reader)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "secret path detail") {
				t.Fatalf("credential reader detail leaked: %v", err)
			}
		})
	}
}

func TestTrainerAgentProductionSourcesContainNoDatabaseOrArtifactStoreCapability(t *testing.T) {
	t.Parallel()
	paths := []string{".", "../trainerprocess", "../../cmd/ascendany-trainer-agent"}
	forbidden := []string{
		"ASCENDANY_DATABASE",
		"github.com/jackc/pgx",
		"internal/artifact",
		"internal/auth",
		"github.com/kkkzbh/AscendAny/backend/internal/recommendation\"",
		"NewPostgresRepository",
		"artifact.NewStore",
		"/var/lib/ascendany/artifacts",
	}
	for _, root := range paths {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range forbidden {
				if strings.Contains(string(source), value) {
					t.Fatalf("%s contains forbidden capability marker %q", path, value)
				}
			}
		}
	}
}

func validTrainerAgentEnvironment() map[string]string {
	return map[string]string{
		"ASCENDANY_TRAINER_AGENT_ENDPOINT":                  "https://trainer.example",
		"ASCENDANY_TRAINER_AGENT_TOKEN_FILE":                "/run/credentials/trainer_agent_token",
		"ASCENDANY_TRAINER_AGENT_ID":                        "rtx-01",
		"ASCENDANY_TRAINER_AGENT_LEASE_DURATION":            "30s",
		"ASCENDANY_TRAINER_AGENT_POLL_INTERVAL":             "2s",
		"ASCENDANY_TRAINER_AGENT_REQUEST_TIMEOUT":           "5s",
		"ASCENDANY_TRAINER_AGENT_MAX_INPUT_BYTES":           "134217728",
		"ASCENDANY_TRAINER_AGENT_MAX_OUTPUT_BYTES":          "134217728",
		"ASCENDANY_TRAINER_AGENT_MAX_STDERR_BYTES":          "16384",
		"ASCENDANY_TRAINER_AGENT_BWRAP":                     "/usr/bin/bwrap",
		"ASCENDANY_TRAINER_AGENT_RUNTIME_ROOT":              "/opt/ascendany-trainer-runtime/current",
		"ASCENDANY_TRAINER_AGENT_PYTHON":                    "/opt/ascendany-trainer-runtime/current/python/bin/python3.14",
		"ASCENDANY_TRAINER_AGENT_PACKAGE_ROOT":              "/opt/ascendany/v2/trainers/recommendation",
		"ASCENDANY_TRAINER_AGENT_WORK_ROOT":                 "/var/lib/ascendany-trainer/work",
		"ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH": "/var/lib/ascendany-trainer/acceptance/trainer-latest.json",
		"ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS":             "/lib,/lib64,/opt/ascendany-trainer-runtime/current,/sys",
		"ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS":       "/dev/nvidia-uvm,/dev/nvidia0,/dev/nvidiactl",
		"ASCENDANY_TRAINER_AGENT_TRAINER_TIMEOUT":           "6h",
		"ASCENDANY_TRAINER_AGENT_CUBLAS_WORKSPACE_CONFIG":   ":4096:8",
		"ASCENDANY_TRAINER_AGENT_CUDA_VISIBLE_DEVICES":      "0",
		"ASCENDANY_TRAINER_AGENT_MKL_NUM_THREADS":           "8",
		"ASCENDANY_TRAINER_AGENT_OMP_NUM_THREADS":           "8",
		"ASCENDANY_TRAINER_AGENT_OPENBLAS_NUM_THREADS":      "8",
		"ASCENDANY_TRAINER_AGENT_LOG_LEVEL":                 "info",
	}
}

func environmentLookup(environment map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}
}
