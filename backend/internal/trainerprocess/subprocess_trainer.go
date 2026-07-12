// Package trainerprocess owns the capability-minimal Python child-process
// boundary. It has no database, queue, artifact-store, authentication, or
// HTTP transport dependency.
package trainerprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const (
	sandboxTrainerPackageRoot           = "/trainer/recommendation"
	sandboxOutputDirectory              = "/output"
	trainerOutputFilename               = "output.json"
	maximumTrainerLimit                 = 1 << 30
	maximumTrainerEnvironmentValueBytes = 4096
	maximumFailureDetailBytes           = 2048
	maximumManifestBytes                = 1 << 20
)

const trainerBootstrap = `import runpy,sys;sys.path.insert(0,"/trainer/recommendation");runpy.run_module("ascendany_recommendation_trainer",run_name="__main__",alter_sys=True)`
const runtimeAttestationProbe = `import json,os,sys;sys.path.insert(0,"/trainer/recommendation");from ascendany_recommendation_trainer.attestation import attest_runtime;sys.stdout.write(json.dumps(attest_runtime(dict(os.environ)),sort_keys=True,separators=(",",":")))`

var (
	canonicalUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	lowercaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var trainerEnvironmentWhitelist = map[string]struct{}{
	"CUBLAS_WORKSPACE_CONFIG": {},
	"CUDA_VISIBLE_DEVICES":    {},
	"MKL_NUM_THREADS":         {},
	"OMP_NUM_THREADS":         {},
	"OPENBLAS_NUM_THREADS":    {},
}

var nvidiaDevicePattern = regexp.MustCompile(`^/dev/nvidia(?:[0-9]+|ctl|-uvm|-uvm-tools|-modeset)$`)

var trainerPackageSourceFiles = []string{
	"__init__.py",
	"__main__.py",
	"attestation.py",
	"cli.py",
	"contract.py",
	"model.py",
	"train.py",
}

var forbiddenTrainerRuntimePrefixes = []string{
	"/etc/ascendany",
	"/opt/ascendany",
	"/run/credentials",
	"/var/backups/ascendany",
	"/var/lib/ascendany",
}

var reservedTrainerSandboxPrefixes = []string{
	"/dev",
	"/output",
	"/proc",
	"/run",
	"/tmp",
	"/trainer",
}

// SubprocessTrainerConfig defines every capability visible to the Python
// child. RuntimeReadOnlyPaths are mounted at the same absolute paths inside a
// fresh bubblewrap namespace. NVIDIADevicePaths are the only host devices that
// can be added to the sandbox. The host environment is never inherited.
type SubprocessTrainerConfig struct {
	SandboxExecutable    string
	PythonExecutable     string
	RuntimeRoot          string
	TrainerPackageRoot   string
	RuntimeReadOnlyPaths []string
	NVIDIADevicePaths    []string
	WorkRoot             string
	Timeout              time.Duration
	MaximumInputBytes    int
	MaximumOutputBytes   int
	MaximumStderrBytes   int
	Environment          map[string]string
}

// SubprocessTrainer implements the Trainer port with a capability-minimal
// bubblewrap child process.
type SubprocessTrainer struct {
	config            SubprocessTrainerConfig
	runtimeSourceRoot string
	attestation       runtimeAttestationIdentity
}

var _ Trainer = (*SubprocessTrainer)(nil)

func NewSubprocessTrainer(config SubprocessTrainerConfig) (*SubprocessTrainer, error) {
	normalized, err := validateSubprocessTrainerConfig(config)
	if err != nil {
		return nil, err
	}
	attestation, runtimeSourceRoot, err := loadRuntimeAttestationIdentity(normalized)
	if err != nil {
		return nil, err
	}
	return &SubprocessTrainer{config: normalized, runtimeSourceRoot: runtimeSourceRoot, attestation: attestation}, nil
}

// Train launches exactly one isolated Python child. stdin is the immutable
// input bundle; stdout and output/output.json must contain the same canonical
// output bundle. The staging directory is removed on every outcome.
func (trainer *SubprocessTrainer) Train(ctx context.Context, request TrainingRequest) (_ []byte, resultErr error) {
	if ctx == nil {
		return nil, trainerFailure("trainer_invalid_request", "trainer context is required", nil)
	}
	input, err := validateSubprocessTrainingRequest(request, trainer.config.MaximumInputBytes)
	if err != nil {
		return nil, err
	}
	invocationDirectory, err := os.MkdirTemp(trainer.config.WorkRoot, request.RunID+"-")
	if err != nil {
		return nil, trainerFailure("trainer_staging_failed", "failed to create isolated trainer staging directory", err)
	}
	defer func() {
		if err := os.RemoveAll(invocationDirectory); err != nil {
			cleanupErr := &TrainerFailure{Code: "trainer_cleanup_failed", Detail: "failed to remove isolated trainer staging directory", Retryable: false, Cause: err}
			if resultErr == nil {
				resultErr = cleanupErr
			} else {
				resultErr = errors.Join(resultErr, cleanupErr)
			}
		}
	}()
	if err := os.Chmod(invocationDirectory, 0o700); err != nil {
		return nil, trainerFailure("trainer_staging_failed", "failed to secure isolated trainer staging directory", err)
	}
	outputDirectory := filepath.Join(invocationDirectory, "output")
	if err := os.Mkdir(outputDirectory, 0o700); err != nil {
		return nil, trainerFailure("trainer_staging_failed", "failed to create isolated trainer output directory", err)
	}

	processContext, cancel := context.WithTimeout(ctx, trainer.config.Timeout)
	defer cancel()
	arguments := trainer.commandArguments(outputDirectory, request)
	command := exec.CommandContext(processContext, trainer.config.SandboxExecutable, arguments...)
	command.Env = []string{}
	command.Stdin = bytes.NewReader(input.CanonicalJSON)
	stdout := newBoundedProcessCapture(trainer.config.MaximumOutputBytes)
	stderr := newBoundedProcessCapture(trainer.config.MaximumStderrBytes)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}

	waitErr := command.Run()
	if ctxErr := context.Cause(ctx); ctxErr != nil {
		return nil, ctxErr
	}
	if errors.Is(processContext.Err(), context.DeadlineExceeded) {
		return nil, &TrainerFailure{
			Code: "trainer_timeout", Detail: fmt.Sprintf("trainer exceeded %s", trainer.config.Timeout), Retryable: false,
		}
	}
	if stdout.overflow {
		return nil, &TrainerFailure{
			Code: "trainer_output_limit", Detail: fmt.Sprintf("trainer stdout exceeded %d bytes", trainer.config.MaximumOutputBytes), Retryable: false,
		}
	}
	if stderr.overflow {
		return nil, &TrainerFailure{
			Code: "trainer_stderr_limit", Detail: fmt.Sprintf("trainer stderr exceeded %d bytes", trainer.config.MaximumStderrBytes), Retryable: false,
		}
	}
	stderrText, err := validatedTrainerStderr(stderr.Bytes())
	if err != nil {
		return nil, err
	}
	if waitErr != nil {
		return nil, classifyTrainerProcessFailure(waitErr, stderrText)
	}
	if stderrText != "" {
		return nil, trainerFailure("trainer_unexpected_stderr", "successful trainer wrote to stderr: "+stderrText, nil)
	}

	canonicalOutput, err := validateTrainingOutput(
		stdout.Bytes(), trainer.config.MaximumOutputBytes, request.InputManifestSHA256, trainer.attestation,
	)
	if err != nil {
		return nil, trainerFailure("trainer_invalid_output", "trainer output violates its canonical provenance envelope", err)
	}
	stagedOutput, err := readTrainerOutputDirectory(outputDirectory, trainer.config.MaximumOutputBytes)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(stagedOutput, canonicalOutput) {
		return nil, &TrainerFailure{Code: "trainer_output_mismatch", Detail: "stdout differs from output/output.json", Retryable: false}
	}
	return slices.Clone(canonicalOutput), nil
}

// VerifyRuntime launches the production interpreter inside the exact training
// namespace and requires its same-process CUDA and provenance attestation to
// match the supervisor's independently loaded identity.
func (trainer *SubprocessTrainer) VerifyRuntime(ctx context.Context) (_err error) {
	if trainer == nil {
		return configurationFailure("verify isolated trainer runtime", errors.New("trainer is required"))
	}
	if ctx == nil {
		return configurationFailure("verify isolated trainer runtime", errors.New("context is required"))
	}
	invocationDirectory, err := os.MkdirTemp(trainer.config.WorkRoot, "runtime-verification-")
	if err != nil {
		return configurationFailure("verify isolated trainer runtime", fmt.Errorf("create verification directory: %w", err))
	}
	defer func() {
		if cleanupErr := os.RemoveAll(invocationDirectory); cleanupErr != nil {
			_err = errors.Join(_err, configurationFailure("verify isolated trainer runtime", fmt.Errorf("remove verification directory: %w", cleanupErr)))
		}
	}()
	if err := os.Chmod(invocationDirectory, 0o700); err != nil {
		return configurationFailure("verify isolated trainer runtime", fmt.Errorf("secure verification directory: %w", err))
	}
	outputDirectory := filepath.Join(invocationDirectory, "output")
	if err := os.Mkdir(outputDirectory, 0o700); err != nil {
		return configurationFailure("verify isolated trainer runtime", fmt.Errorf("create verification output directory: %w", err))
	}

	command := exec.CommandContext(ctx, trainer.config.SandboxExecutable, trainer.runtimeVerificationArguments(outputDirectory)...)
	command.Env = []string{}
	stdout := newBoundedProcessCapture(maximumManifestBytes)
	stderr := newBoundedProcessCapture(trainer.config.MaximumStderrBytes)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := command.Run(); err != nil {
		return configurationFailure("verify isolated trainer runtime", fmt.Errorf("runtime attestation child failed: %w: %s", err, boundedFailureDetail(errors.New(string(stderr.Bytes())))))
	}
	if stdout.overflow || stderr.overflow || len(stderr.Bytes()) != 0 {
		return configurationFailure("verify isolated trainer runtime", errors.New("runtime attestation child exceeded its output contract"))
	}
	if err := validateRuntimeAttestationOutput(stdout.Bytes(), trainer.attestation); err != nil {
		return configurationFailure("verify isolated trainer runtime", err)
	}
	return nil
}

func (trainer *SubprocessTrainer) commandArguments(outputDirectory string, request TrainingRequest) []string {
	environment := trainer.runtimeEnvironment()
	environment["ASCENDANY_TRAINER_INPUT_MANIFEST_SHA256"] = request.InputManifestSHA256
	environment["ASCENDANY_TRAINER_MAX_INPUT_BYTES"] = strconv.Itoa(trainer.config.MaximumInputBytes)
	environment["ASCENDANY_TRAINER_MAX_OUTPUT_BYTES"] = strconv.Itoa(trainer.config.MaximumOutputBytes)
	environment["ASCENDANY_TRAINER_OUTPUT_DIR"] = sandboxOutputDirectory
	arguments := trainer.sandboxArguments(outputDirectory, environment)
	return append(arguments,
		"--",
		trainer.config.PythonExecutable,
		"-B",
		"-s",
		"-P",
		"-c",
		trainerBootstrap,
	)
}

func (trainer *SubprocessTrainer) runtimeVerificationArguments(outputDirectory string) []string {
	arguments := trainer.sandboxArguments(outputDirectory, trainer.runtimeEnvironment())
	return append(arguments,
		"--",
		trainer.config.PythonExecutable,
		"-B",
		"-s",
		"-P",
		"-c",
		runtimeAttestationProbe,
	)
}

func (trainer *SubprocessTrainer) sandboxArguments(outputDirectory string, environment map[string]string) []string {
	arguments := []string{
		"--unshare-all",
		"--die-with-parent",
		"--new-session",
		"--cap-drop", "ALL",
		"--hostname", "ascendany-trainer",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--dir", "/run",
		"--dir", "/opt",
		"--dir", "/opt/ascendany-trainer-runtime",
		"--dir", "/trainer",
	}
	for _, runtimePath := range trainer.config.RuntimeReadOnlyPaths {
		source := runtimePath
		if runtimePath == trainer.config.RuntimeRoot {
			source = trainer.runtimeSourceRoot
		}
		arguments = append(arguments, "--ro-bind", source, runtimePath)
	}
	for _, devicePath := range trainer.config.NVIDIADevicePaths {
		arguments = append(arguments, "--dev-bind", devicePath, devicePath)
	}
	arguments = append(arguments,
		"--ro-bind", trainer.config.TrainerPackageRoot, sandboxTrainerPackageRoot,
		"--bind", outputDirectory, sandboxOutputDirectory,
		"--chdir", sandboxOutputDirectory,
		"--clearenv",
	)
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		arguments = append(arguments, "--setenv", key, environment[key])
	}
	return arguments
}

func (trainer *SubprocessTrainer) runtimeEnvironment() map[string]string {
	environment := map[string]string{
		"ASCENDANY_TRAINER_EXPECTED_HOST_CAPABILITY_SHA256":      trainer.attestation.HostCapabilitySHA256,
		"ASCENDANY_TRAINER_EXPECTED_RUNTIME_CONSTRUCTION_SHA256": trainer.attestation.RuntimeConstructionSHA256,
		"ASCENDANY_TRAINER_EXPECTED_RUNTIME_PROVENANCE_SHA256":   trainer.attestation.RuntimeProvenanceSHA256,
		"ASCENDANY_TRAINER_EXPECTED_RUNTIME_TREE_SHA256":         trainer.attestation.RuntimeTreeSHA256,
		"ASCENDANY_TRAINER_RUNTIME_ROOT":                         trainer.config.RuntimeRoot,
		"HOME":                                                   "/nonexistent",
		"LANG":                                                   "C.UTF-8",
		"LC_ALL":                                                 "C.UTF-8",
		"PWD":                                                    sandboxOutputDirectory,
		"PYTHONHASHSEED":                                         "0",
		"TZ":                                                     "UTC",
	}
	for key, value := range trainer.config.Environment {
		environment[key] = value
	}
	return environment
}

func validateSubprocessTrainerConfig(config SubprocessTrainerConfig) (SubprocessTrainerConfig, error) {
	operation := "configure isolated recommendation trainer"
	paths := []struct {
		label string
		value string
	}{
		{label: "sandbox executable", value: config.SandboxExecutable},
		{label: "Python executable", value: config.PythonExecutable},
		{label: "runtime root", value: config.RuntimeRoot},
		{label: "trainer package root", value: config.TrainerPackageRoot},
		{label: "work root", value: config.WorkRoot},
	}
	for _, path := range paths {
		if path.value == "" || !filepath.IsAbs(path.value) || filepath.Clean(path.value) != path.value {
			return SubprocessTrainerConfig{}, configurationFailure(operation, fmt.Errorf("%s must be a normalized absolute path", path.label))
		}
	}
	if config.Timeout < 100*time.Millisecond || config.Timeout > 24*time.Hour {
		return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("timeout must be between 100 milliseconds and 24 hours"))
	}
	if config.MaximumInputBytes <= 0 || config.MaximumInputBytes > maximumTrainerLimit ||
		config.MaximumOutputBytes <= 0 || config.MaximumOutputBytes > maximumTrainerLimit ||
		config.MaximumStderrBytes <= 0 || config.MaximumStderrBytes > 1<<20 {
		return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("input/output limits must be at most 1 GiB and stderr at most 1 MiB"))
	}
	if err := validateExecutable(config.SandboxExecutable, "sandbox executable"); err != nil {
		return SubprocessTrainerConfig{}, configurationFailure(operation, err)
	}
	if err := validateExecutable(config.PythonExecutable, "Python executable"); err != nil {
		return SubprocessTrainerConfig{}, configurationFailure(operation, err)
	}
	if err := validateTrainerPackageRoot(config.TrainerPackageRoot); err != nil {
		return SubprocessTrainerConfig{}, configurationFailure(operation, err)
	}
	if err := validatePrivateWorkRoot(config.WorkRoot); err != nil {
		return SubprocessTrainerConfig{}, configurationFailure(operation, err)
	}
	expectedRuntimePaths := []string{"/lib", "/lib64", config.RuntimeRoot, "/sys"}
	sort.Strings(expectedRuntimePaths)
	if !slices.Equal(config.RuntimeReadOnlyPaths, expectedRuntimePaths) {
		return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("runtime paths must contain exactly /lib, /lib64, the selected runtime root, and /sys"))
	}
	runtimePaths := slices.Clone(config.RuntimeReadOnlyPaths)
	sort.Strings(runtimePaths)
	runtimePaths = slices.Compact(runtimePaths)
	for _, runtimePath := range runtimePaths {
		if err := validateTrainerRuntimePath(runtimePath, config.WorkRoot); err != nil {
			return SubprocessTrainerConfig{}, configurationFailure(operation, err)
		}
		if pathsIntersect(runtimePath, config.TrainerPackageRoot) {
			return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("trainer package root must be mounted separately from runtime paths"))
		}
	}
	if pathsIntersect(config.TrainerPackageRoot, config.WorkRoot) {
		return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("trainer package root cannot intersect the writable work root"))
	}
	if config.PythonExecutable != filepath.Join(config.RuntimeRoot, "python/bin/python3.14") {
		return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("Python executable must be the selected runtime's exact CPython entry point"))
	}
	nvidiaDevicePaths := slices.Clone(config.NVIDIADevicePaths)
	sort.Strings(nvidiaDevicePaths)
	nvidiaDevicePaths = slices.Compact(nvidiaDevicePaths)
	if !slices.Equal(nvidiaDevicePaths, []string{"/dev/nvidia-uvm", "/dev/nvidia0", "/dev/nvidiactl"}) {
		return SubprocessTrainerConfig{}, configurationFailure(operation, errors.New("NVIDIA device paths must contain exactly nvidia-uvm, nvidia0, and nvidiactl"))
	}
	if err := validateNVIDIADeviceSet(nvidiaDevicePaths, runtimePaths); err != nil {
		return SubprocessTrainerConfig{}, configurationFailure(operation, err)
	}
	environmentKeys := make([]string, 0, len(config.Environment))
	for key := range config.Environment {
		environmentKeys = append(environmentKeys, key)
	}
	sort.Strings(environmentKeys)
	for _, key := range environmentKeys {
		value := config.Environment[key]
		if _, allowed := trainerEnvironmentWhitelist[key]; !allowed {
			return SubprocessTrainerConfig{}, configurationFailure(operation, fmt.Errorf("environment variable %q is outside the trainer whitelist", key))
		}
		if value == "" || len(value) > maximumTrainerEnvironmentValueBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || strings.ContainsRune(value, '=') {
			return SubprocessTrainerConfig{}, configurationFailure(operation, fmt.Errorf("environment variable %q has an invalid value", key))
		}
	}
	config.RuntimeReadOnlyPaths = runtimePaths
	config.NVIDIADevicePaths = nvidiaDevicePaths
	config.Environment = cloneStringMap(config.Environment)
	return config, nil
}

func configurationFailure(operation string, cause error) *ConfigurationError {
	return &ConfigurationError{Operation: operation, Cause: cause}
}

func validateNVIDIADeviceSet(devicePaths, runtimePaths []string) error {
	if len(devicePaths) == 0 {
		return nil
	}
	if !slices.Contains(runtimePaths, "/sys") {
		return errors.New("NVIDIA device access requires an explicit read-only /sys runtime path")
	}
	hasGPU, hasControl, hasUnifiedMemory := false, false, false
	for _, path := range devicePaths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || !nvidiaDevicePattern.MatchString(path) {
			return fmt.Errorf("NVIDIA device path %q is outside the explicit device whitelist", path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("stat NVIDIA device path %q: %w", path, err)
		}
		if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("NVIDIA device path %q must be a real character device", path)
		}
		switch {
		case path == "/dev/nvidiactl":
			hasControl = true
		case path == "/dev/nvidia-uvm":
			hasUnifiedMemory = true
		case strings.HasPrefix(path, "/dev/nvidia") && path[len("/dev/nvidia")] >= '0' && path[len("/dev/nvidia")] <= '9':
			hasGPU = true
		}
	}
	if !hasGPU || !hasControl || !hasUnifiedMemory {
		return errors.New("NVIDIA device access requires a GPU, nvidiactl, and nvidia-uvm character device")
	}
	return nil
}

func validateExecutable(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be an executable regular file", label)
	}
	return nil
}

func validateTrainerPackageRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat trainer package root: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("trainer package root must be a non-group-writable, non-world-writable real directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("trainer package root must be canonical and contain no path symlink")
	}
	rootEntries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read trainer package root: %w", err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != "ascendany_recommendation_trainer" || !rootEntries[0].IsDir() || rootEntries[0].Type()&os.ModeSymlink != 0 {
		return errors.New("trainer package root must contain exactly the ascendany_recommendation_trainer package directory")
	}
	packageDirectory := filepath.Join(path, rootEntries[0].Name())
	packageInfo, err := os.Lstat(packageDirectory)
	if err != nil || !packageInfo.IsDir() || packageInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("trainer package directory must be a non-writable real directory")
	}
	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		return fmt.Errorf("read trainer package directory: %w", err)
	}
	if len(entries) != len(trainerPackageSourceFiles) {
		return errors.New("trainer package source file set differs from the executable contract")
	}
	for index, expected := range trainerPackageSourceFiles {
		entry := entries[index]
		if entry.Name() != expected || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("trainer package source file set differs from the executable contract")
		}
		sourceInfo, err := entry.Info()
		if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Mode().Perm()&0o022 != 0 || sourceInfo.Size() <= 0 || sourceInfo.Size() > 4<<20 {
			return fmt.Errorf("trainer package source %q violates its file contract", expected)
		}
	}
	return nil
}

func validatePrivateWorkRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat trainer work root: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("trainer work root must be a real directory with mode 0700")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("trainer work root must be owned by the current process identity")
	}
	return nil
}

func validateTrainerRuntimePath(path, workRoot string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return errors.New("trainer runtime paths must be normalized absolute paths below filesystem root")
	}
	for _, forbidden := range forbiddenTrainerRuntimePrefixes {
		if pathsIntersect(path, forbidden) {
			return fmt.Errorf("trainer runtime path %q intersects production state %q", path, forbidden)
		}
	}
	for _, reserved := range reservedTrainerSandboxPrefixes {
		if pathsIntersect(path, reserved) {
			return fmt.Errorf("trainer runtime path %q intersects reserved sandbox path %q", path, reserved)
		}
	}
	if pathsIntersect(path, workRoot) {
		return errors.New("trainer runtime paths cannot expose the writable work root")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve trainer runtime path: %w", err)
	}
	if !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved {
		return errors.New("trainer runtime path must resolve to a normalized absolute path")
	}
	for _, forbidden := range forbiddenTrainerRuntimePrefixes {
		if pathsIntersect(resolved, forbidden) {
			return fmt.Errorf("resolved trainer runtime path %q intersects production state %q", resolved, forbidden)
		}
	}
	for _, reserved := range reservedTrainerSandboxPrefixes {
		if pathsIntersect(resolved, reserved) {
			return fmt.Errorf("resolved trainer runtime path %q intersects reserved sandbox path %q", resolved, reserved)
		}
	}
	if pathsIntersect(resolved, workRoot) {
		return errors.New("resolved trainer runtime path cannot expose the writable work root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat trainer runtime path: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("trainer runtime path must resolve to a non-group-writable, non-world-writable directory")
	}
	return nil
}

func pathWithinAny(path string, roots []string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func pathsIntersect(left, right string) bool {
	return pathWithinAny(left, []string{right}) || pathWithinAny(right, []string{left})
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

type validatedTrainingInput struct {
	CanonicalJSON json.RawMessage
}

type trainingInputEnvelope struct {
	Protocol string          `json:"protocol"`
	Manifest json.RawMessage `json:"manifest"`
}

type trainingOutputEnvelope struct {
	Protocol            string                      `json:"protocol"`
	InputManifestSHA256 string                      `json:"inputManifestSha256"`
	Model               trainingOutputModelEnvelope `json:"model"`
}

type trainingOutputModelEnvelope struct {
	Manifest json.RawMessage `json:"manifest"`
}

type trainingOutputRuntimeManifest struct {
	RuntimeConstructionSHA256 string `json:"runtimeConstructionSha256"`
	RuntimeProvenanceSHA256   string `json:"runtimeProvenanceSha256"`
	RuntimeTreeSHA256         string `json:"runtimeTreeSha256"`
	HostCapabilitySHA256      string `json:"hostCapabilitySha256"`
	RuntimeAttestationSHA256  string `json:"runtimeAttestationSha256"`
}

func validateSubprocessTrainingRequest(request TrainingRequest, maximumBytes int) (validatedTrainingInput, error) {
	if !canonicalUUIDv4Pattern.MatchString(request.RunID) || !lowercaseSHA256Pattern.MatchString(request.InputManifestSHA256) ||
		len(request.InputBundle) == 0 || len(request.InputBundle) > maximumBytes {
		return validatedTrainingInput{}, trainerFailure(
			"trainer_invalid_request", "canonical run ID, manifest hash, and bounded input bundle are required", nil,
		)
	}
	canonical, _, err := canonicaljson.Object(request.InputBundle, maximumBytes)
	if err != nil || !bytes.Equal(canonical, request.InputBundle) {
		return validatedTrainingInput{}, trainerFailure("trainer_invalid_request", "input bundle must be a canonical JSON object", err)
	}
	var envelope trainingInputEnvelope
	if err := json.Unmarshal(canonical, &envelope); err != nil || envelope.Protocol != TrainingBundleProtocolV2 {
		return validatedTrainingInput{}, trainerFailure("trainer_invalid_request", "input bundle protocol is unsupported", err)
	}
	_, manifestSHA256, err := canonicaljson.Object(envelope.Manifest, maximumManifestBytes)
	if err != nil || manifestSHA256 != request.InputManifestSHA256 {
		return validatedTrainingInput{}, trainerFailure("trainer_invalid_request", "input manifest hash differs from the trainer request", err)
	}
	return validatedTrainingInput{CanonicalJSON: canonical}, nil
}

func validateTrainingOutput(
	raw []byte,
	maximumBytes int,
	inputManifestSHA256 string,
	expected runtimeAttestationIdentity,
) (json.RawMessage, error) {
	canonical, _, err := canonicaljson.Object(raw, maximumBytes)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, errors.New("training output must be a bounded canonical JSON object")
	}
	var envelope trainingOutputEnvelope
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		return nil, errors.New("training output envelope cannot be decoded")
	}
	if envelope.Protocol != TrainingOutputProtocolV2 || envelope.InputManifestSHA256 != inputManifestSHA256 {
		return nil, errors.New("training output protocol or input manifest provenance differs")
	}
	var runtimeManifest trainingOutputRuntimeManifest
	if len(envelope.Model.Manifest) == 0 || json.Unmarshal(envelope.Model.Manifest, &runtimeManifest) != nil {
		return nil, errors.New("training output runtime manifest cannot be decoded")
	}
	actual := runtimeAttestationIdentity{
		RuntimeConstructionSHA256: runtimeManifest.RuntimeConstructionSHA256,
		RuntimeProvenanceSHA256:   runtimeManifest.RuntimeProvenanceSHA256,
		RuntimeTreeSHA256:         runtimeManifest.RuntimeTreeSHA256,
		HostCapabilitySHA256:      runtimeManifest.HostCapabilitySHA256,
		RuntimeAttestationSHA256:  runtimeManifest.RuntimeAttestationSHA256,
	}
	if actual != expected || !lowercaseSHA256Pattern.MatchString(actual.RuntimeConstructionSHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeProvenanceSHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeTreeSHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.HostCapabilitySHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeAttestationSHA256) {
		return nil, errors.New("training output runtime attestation differs from the supervisor identity")
	}
	return canonical, nil
}

func validateRuntimeAttestationOutput(raw []byte, expected runtimeAttestationIdentity) error {
	canonical, _, err := canonicaljson.Object(raw, maximumManifestBytes)
	if err != nil || !bytes.Equal(canonical, raw) {
		return errors.New("runtime attestation output must be one exact canonical JSON object")
	}
	var actual runtimeAttestationIdentity
	if err := decodeRuntimeProvenanceStrict(raw, &actual); err != nil {
		return errors.New("runtime attestation output differs from its exact field contract")
	}
	if actual != expected ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeConstructionSHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeProvenanceSHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeTreeSHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.HostCapabilitySHA256) ||
		!lowercaseSHA256Pattern.MatchString(actual.RuntimeAttestationSHA256) {
		return errors.New("runtime attestation output differs from the supervisor identity")
	}
	return nil
}

type boundedProcessCapture struct {
	limit    int
	buffer   bytes.Buffer
	overflow bool
}

func newBoundedProcessCapture(limit int) *boundedProcessCapture {
	return &boundedProcessCapture{limit: limit}
}

func (capture *boundedProcessCapture) Write(value []byte) (int, error) {
	if capture.overflow {
		return len(value), errors.New("process output limit exceeded")
	}
	remaining := capture.limit - capture.buffer.Len()
	if len(value) <= remaining {
		_, _ = capture.buffer.Write(value)
		return len(value), nil
	}
	if remaining > 0 {
		_, _ = capture.buffer.Write(value[:remaining])
	}
	capture.overflow = true
	return len(value), errors.New("process output limit exceeded")
}

func (capture *boundedProcessCapture) Bytes() []byte {
	return capture.buffer.Bytes()
}

func validatedTrainerStderr(value []byte) (string, error) {
	if !utf8.Valid(value) {
		return "", &TrainerFailure{Code: "trainer_stderr_invalid", Detail: "trainer stderr is not valid UTF-8", Retryable: false}
	}
	text := strings.TrimSpace(string(value))
	if strings.ContainsRune(text, 0) {
		return "", &TrainerFailure{Code: "trainer_stderr_invalid", Detail: "trainer stderr contains a NUL byte", Retryable: false}
	}
	return text, nil
}

func classifyTrainerProcessFailure(cause error, stderr string) error {
	detailSuffix := ""
	if stderr != "" {
		detailSuffix = ": " + stderr
	}
	var exitError *exec.ExitError
	if !errors.As(cause, &exitError) {
		return trainerFailure("trainer_start_failed", "failed to start or wait for isolated trainer"+detailSuffix, cause)
	}
	status, ok := exitError.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return trainerFailure("trainer_signaled", "trainer terminated by signal "+status.Signal().String()+detailSuffix, cause)
	}
	exitCode := exitError.ExitCode()
	if exitCode >= 129 && exitCode <= 192 {
		signal := syscall.Signal(exitCode - 128)
		return trainerFailure("trainer_signaled", "trainer terminated by signal "+signal.String()+detailSuffix, cause)
	}
	return trainerFailure("trainer_exit_nonzero", fmt.Sprintf("trainer exited with code %d%s", exitCode, detailSuffix), cause)
}

func trainerFailure(code, detail string, cause error) *TrainerFailure {
	return &TrainerFailure{Code: code, Detail: boundedFailureDetail(errors.New(detail)), Retryable: false, Cause: cause}
}

func boundedFailureDetail(cause error) string {
	const generic = "isolated trainer execution failed"
	if cause == nil {
		return generic
	}
	value := strings.TrimSpace(cause.Error())
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return generic
	}
	if len(value) <= maximumFailureDetailBytes {
		return value
	}
	value = value[:maximumFailureDetailBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func readTrainerOutputDirectory(outputDirectory string, maximumBytes int) ([]byte, error) {
	entries, err := os.ReadDir(outputDirectory)
	if err != nil {
		return nil, &TrainerFailure{Code: "trainer_output_directory_invalid", Detail: "failed to read trainer output directory", Retryable: false, Cause: err}
	}
	if len(entries) != 1 || entries[0].Name() != trainerOutputFilename || entries[0].IsDir() {
		return nil, &TrainerFailure{Code: "trainer_output_directory_invalid", Detail: "trainer output directory must contain only output.json", Retryable: false}
	}
	path := filepath.Join(outputDirectory, trainerOutputFilename)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, &TrainerFailure{Code: "trainer_output_directory_invalid", Detail: "failed to inspect output.json", Retryable: false, Cause: err}
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > int64(maximumBytes) {
		return nil, &TrainerFailure{Code: "trainer_output_directory_invalid", Detail: "output.json must be a bounded regular file with mode 0600", Retryable: false}
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, &TrainerFailure{Code: "trainer_output_directory_invalid", Detail: "failed to read output.json", Retryable: false, Cause: err}
	}
	return value, nil
}
