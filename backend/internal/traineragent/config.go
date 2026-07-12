package traineragent

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/trainerprocess"
)

type LookupEnv func(string) (string, bool)
type ReadFile func(string) ([]byte, error)

type Config struct {
	HTTP                    HTTPClientConfig
	Worker                  WorkerConfig
	Supervisor              SupervisorConfig
	Trainer                 trainerprocess.SubprocessTrainerConfig
	AcceptanceCandidatePath string
	LogLevel                string
}

func LoadConfig(lookup LookupEnv, readFile ReadFile) (Config, error) {
	if lookup == nil || readFile == nil {
		return Config{}, errors.New("trainer-agent environment lookup and credential reader are required")
	}
	baseURL, err := requiredEnvironment(lookup, "ASCENDANY_TRAINER_AGENT_ENDPOINT")
	if err != nil {
		return Config{}, err
	}
	tokenPath, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_TOKEN_FILE")
	if err != nil {
		return Config{}, err
	}
	token, err := readBoundedToken(readFile, tokenPath)
	if err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_TRAINER_AGENT_TOKEN_FILE: %w", err)
	}
	agentID, err := requiredEnvironment(lookup, "ASCENDANY_TRAINER_AGENT_ID")
	if err != nil {
		return Config{}, err
	}
	leaseDuration, err := requiredDuration(lookup, "ASCENDANY_TRAINER_AGENT_LEASE_DURATION")
	if err != nil {
		return Config{}, err
	}
	pollInterval, err := requiredDuration(lookup, "ASCENDANY_TRAINER_AGENT_POLL_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	if pollInterval >= leaseDuration {
		return Config{}, errors.New("ASCENDANY_TRAINER_AGENT_POLL_INTERVAL must be shorter than the lease duration")
	}
	requestTimeout, err := requiredDuration(lookup, "ASCENDANY_TRAINER_AGENT_REQUEST_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	maximumInputBytes, err := requiredInteger(lookup, "ASCENDANY_TRAINER_AGENT_MAX_INPUT_BYTES")
	if err != nil {
		return Config{}, err
	}
	maximumOutputBytes, err := requiredInteger(lookup, "ASCENDANY_TRAINER_AGENT_MAX_OUTPUT_BYTES")
	if err != nil {
		return Config{}, err
	}
	maximumStderrBytes, err := requiredInteger(lookup, "ASCENDANY_TRAINER_AGENT_MAX_STDERR_BYTES")
	if err != nil {
		return Config{}, err
	}
	sandboxExecutable, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_BWRAP")
	if err != nil {
		return Config{}, err
	}
	pythonExecutable, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_PYTHON")
	if err != nil {
		return Config{}, err
	}
	runtimeRoot, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_RUNTIME_ROOT")
	if err != nil {
		return Config{}, err
	}
	trainerPackageRoot, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_PACKAGE_ROOT")
	if err != nil {
		return Config{}, err
	}
	workRoot, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_WORK_ROOT")
	if err != nil {
		return Config{}, err
	}
	acceptanceCandidatePath, err := requiredAbsolutePath(lookup, "ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH")
	if err != nil {
		return Config{}, err
	}
	runtimePaths, err := requiredPathList(lookup, "ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS")
	if err != nil {
		return Config{}, err
	}
	devicePaths, err := optionalPathList(lookup, "ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS")
	if err != nil {
		return Config{}, err
	}
	trainerTimeout, err := requiredDuration(lookup, "ASCENDANY_TRAINER_AGENT_TRAINER_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	environment, err := loadComputeEnvironment(lookup)
	if err != nil {
		return Config{}, err
	}
	logLevel, err := requiredEnvironment(lookup, "ASCENDANY_TRAINER_AGENT_LOG_LEVEL")
	if err != nil {
		return Config{}, err
	}
	if logLevel != strings.ToLower(logLevel) || !slices.Contains([]string{"debug", "info", "warn", "error"}, logLevel) {
		return Config{}, errors.New("ASCENDANY_TRAINER_AGENT_LOG_LEVEL must be debug, info, warn, or error")
	}

	configuration := Config{
		HTTP: HTTPClientConfig{
			BaseURL: baseURL, BearerToken: token, AgentID: agentID, LeaseDuration: leaseDuration,
			RequestTimeout: requestTimeout, MaximumInputBundleBytes: maximumInputBytes,
			MaximumOutputBundleBytes: maximumOutputBytes,
		},
		Worker:     WorkerConfig{LeaseDuration: leaseDuration},
		Supervisor: SupervisorConfig{PollInterval: pollInterval},
		Trainer: trainerprocess.SubprocessTrainerConfig{
			SandboxExecutable: sandboxExecutable, PythonExecutable: pythonExecutable, RuntimeRoot: runtimeRoot, TrainerPackageRoot: trainerPackageRoot,
			RuntimeReadOnlyPaths: runtimePaths, NVIDIADevicePaths: devicePaths, WorkRoot: workRoot,
			Timeout: trainerTimeout, MaximumInputBytes: maximumInputBytes, MaximumOutputBytes: maximumOutputBytes,
			MaximumStderrBytes: maximumStderrBytes, Environment: environment,
		},
		AcceptanceCandidatePath: acceptanceCandidatePath,
		LogLevel:                logLevel,
	}
	if err := validateHTTPClientConfig(configuration.HTTP); err != nil {
		return Config{}, fmt.Errorf("trainer-agent HTTP configuration: %w", err)
	}
	if err := validateProductionConfig(configuration); err != nil {
		return Config{}, fmt.Errorf("trainer-agent runtime configuration: %w", err)
	}
	return configuration, nil
}

func requiredEnvironment(lookup LookupEnv, name string) (string, error) {
	value, ok := lookup(name)
	if !ok || value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%s must be unpadded UTF-8 without NUL", name)
	}
	return value, nil
}

func optionalEnvironment(lookup LookupEnv, name string) (string, bool, error) {
	value, ok := lookup(name)
	if !ok || value == "" {
		return "", false, nil
	}
	if strings.TrimSpace(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", false, fmt.Errorf("%s must be unpadded UTF-8 without NUL", name)
	}
	return value, true, nil
}

func requiredAbsolutePath(lookup LookupEnv, name string) (string, error) {
	value, err := requiredEnvironment(lookup, name)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) {
		return "", fmt.Errorf("%s must be a normalized absolute path below filesystem root", name)
	}
	return value, nil
}

func requiredDuration(lookup LookupEnv, name string) (time.Duration, error) {
	value, err := requiredEnvironment(lookup, name)
	if err != nil {
		return 0, err
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration%time.Millisecond != 0 {
		return 0, fmt.Errorf("%s must be a positive whole-millisecond Go duration", name)
	}
	return duration, nil
}

func requiredInteger(lookup LookupEnv, name string) (int, error) {
	value, err := requiredEnvironment(lookup, name)
	if err != nil {
		return 0, err
	}
	return parsePositiveInt(value, name)
}

func requiredPathList(lookup LookupEnv, name string) ([]string, error) {
	value, err := requiredEnvironment(lookup, name)
	if err != nil {
		return nil, err
	}
	return parsePathList(value, name)
}

func optionalPathList(lookup LookupEnv, name string) ([]string, error) {
	value, ok, err := optionalEnvironment(lookup, name)
	if err != nil || !ok {
		return nil, err
	}
	return parsePathList(value, name)
}

func parsePathList(value, name string) ([]string, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 64 {
		return nil, fmt.Errorf("%s must contain 1 to 64 paths", name)
	}
	for index, path := range parts {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
			return nil, fmt.Errorf("%s must contain normalized absolute paths below filesystem root", name)
		}
		if index > 0 && path <= parts[index-1] {
			return nil, fmt.Errorf("%s paths must be unique and bytewise sorted", name)
		}
	}
	return slices.Clone(parts), nil
}

func readBoundedToken(readFile ReadFile, path string) (string, error) {
	value, err := readFile(path)
	if err != nil {
		return "", errors.New("credential cannot be read")
	}
	if len(value) > maximumTokenBytes || !utf8.Valid(value) || bytesContainNUL(value) ||
		!bytesEqualTrimmed(value) || !bearerTokenPattern.Match(value) {
		return "", errors.New("credential must contain 32 to 512 canonical token characters without padding")
	}
	return string(value), nil
}

func bytesContainNUL(value []byte) bool {
	for _, item := range value {
		if item == 0 {
			return true
		}
	}
	return false
}

func bytesEqualTrimmed(value []byte) bool {
	return string(value) == strings.TrimSpace(string(value))
}

func loadComputeEnvironment(lookup LookupEnv) (map[string]string, error) {
	mapping := []struct {
		source string
		target string
	}{
		{source: "ASCENDANY_TRAINER_AGENT_CUBLAS_WORKSPACE_CONFIG", target: "CUBLAS_WORKSPACE_CONFIG"},
		{source: "ASCENDANY_TRAINER_AGENT_CUDA_VISIBLE_DEVICES", target: "CUDA_VISIBLE_DEVICES"},
		{source: "ASCENDANY_TRAINER_AGENT_MKL_NUM_THREADS", target: "MKL_NUM_THREADS"},
		{source: "ASCENDANY_TRAINER_AGENT_OMP_NUM_THREADS", target: "OMP_NUM_THREADS"},
		{source: "ASCENDANY_TRAINER_AGENT_OPENBLAS_NUM_THREADS", target: "OPENBLAS_NUM_THREADS"},
	}
	result := make(map[string]string, len(mapping))
	for _, entry := range mapping {
		value, err := requiredEnvironment(lookup, entry.source)
		if err != nil {
			return nil, err
		}
		result[entry.target] = value
	}
	if result["CUBLAS_WORKSPACE_CONFIG"] != ":4096:8" {
		return nil, errors.New("ASCENDANY_TRAINER_AGENT_CUBLAS_WORKSPACE_CONFIG must be exactly :4096:8")
	}
	if result["CUDA_VISIBLE_DEVICES"] != "0" {
		return nil, errors.New("ASCENDANY_TRAINER_AGENT_CUDA_VISIBLE_DEVICES must expose exactly device 0")
	}
	for _, key := range []string{"MKL_NUM_THREADS", "OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS"} {
		value, err := parsePositiveInt(result[key], "ASCENDANY_TRAINER_AGENT_"+key)
		if err != nil || strconv.Itoa(value) != result[key] || value > 256 {
			return nil, fmt.Errorf("ASCENDANY_TRAINER_AGENT_%s must be a canonical integer from 1 to 256", key)
		}
	}
	return result, nil
}
