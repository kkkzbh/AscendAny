package judgerunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	imageDigestPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,255}@sha256:[0-9a-f]{64}$`)
	containerNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)
)

const podmanCleanupTimeout = 5 * time.Second

type ContainerCommand struct {
	Name                string
	Workspace           string
	RuntimeRoot         string
	ReadOnlyWorkspace   bool
	Executable          string
	Arguments           []string
	Stdin               io.Reader
	Timeout             time.Duration
	MemoryLimitBytes    int64
	OutputLimitBytes    int64
	PIDsLimit           int
	CPUs                float64
	TemporaryLimitBytes int64
}

type ContainerResult struct {
	ExitCode            int
	Stdout              []byte
	Stderr              []byte
	Duration            time.Duration
	TimedOut            bool
	OutputLimitExceeded bool
}

type ContainerEngine interface {
	Identity() string
	Run(context.Context, ContainerCommand) (ContainerResult, error)
}

type PodmanEngine struct {
	binary         string
	image          string
	hooksDirectory string
	now            func() time.Time
}

func NewPodmanEngine(binary, image, hooksDirectory string) (*PodmanEngine, error) {
	if binary == "" || !filepath.IsAbs(binary) || filepath.Clean(binary) != binary ||
		!imageDigestPattern.MatchString(image) || !safeEmptyHooksDirectory(hooksDirectory) {
		return nil, errors.New("absolute Podman binary, digest-pinned image, and safe empty hooks directory are required")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("Podman binary must be an executable regular file")
	}
	return &PodmanEngine{binary: binary, image: image, hooksDirectory: hooksDirectory, now: time.Now}, nil
}

func (engine *PodmanEngine) Identity() string {
	return engine.image
}

func (engine *PodmanEngine) Run(ctx context.Context, request ContainerCommand) (result ContainerResult, resultErr error) {
	if ctx == nil {
		return result, errors.New("container execution context is required")
	}
	arguments, err := engine.buildRunArguments(request)
	if err != nil {
		return result, err
	}
	runContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	capture := newCombinedCapture(request.OutputLimitBytes, cancel)
	command := exec.CommandContext(runContext, engine.binary, arguments...)
	command.Stdin = request.Stdin
	command.Stdout = capture.writer(true)
	command.Stderr = capture.writer(false)
	command.Env = minimalPodmanEnvironment()
	command.SysProcAttr = podmanProcessAttributes()
	command.Cancel = func() error {
		return engine.removeContainer(request.RuntimeRoot, request.Name)
	}
	command.WaitDelay = podmanCleanupTimeout
	startedAt := engine.now()
	err = command.Run()
	result.Duration = engine.now().Sub(startedAt)
	result.Stdout, result.Stderr, result.OutputLimitExceeded = capture.snapshot()
	result.TimedOut = errors.Is(runContext.Err(), context.DeadlineExceeded)
	result.ExitCode = exitCode(err)
	cleanupErr := engine.removeContainer(request.RuntimeRoot, request.Name)
	if err != nil && result.ExitCode < 0 && !result.TimedOut && !result.OutputLimitExceeded && context.Cause(ctx) == nil {
		resultErr = fmt.Errorf("start or supervise Podman container: %w", err)
	}
	if cleanupErr != nil {
		if resultErr == nil {
			resultErr = cleanupErr
		} else {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}
	return result, resultErr
}

func (engine *PodmanEngine) buildRunArguments(request ContainerCommand) ([]string, error) {
	if !validContainerCommand(request) {
		return nil, errors.New("container command violates the judge execution contract")
	}
	mountMode := "ro=true"
	if !request.ReadOnlyWorkspace {
		mountMode = "rw=true"
	}
	mount := "type=bind,src=" + request.Workspace + ",target=/workspace," + mountMode + ",relabel=private"
	memory := strconv.FormatInt(request.MemoryLimitBytes, 10) + "b"
	temporary := strconv.FormatInt(request.TemporaryLimitBytes, 10)
	arguments := []string{
		"--cgroup-manager=cgroupfs", "--events-backend=none", "--hooks-dir=" + engine.hooksDirectory,
		"--runroot=" + request.RuntimeRoot, "--transient-store",
		"run", "--rm", "--interactive", "--pull=never", "--name=" + request.Name,
		"--network=none", "--http-proxy=false", "--hosts-file=none",
		"--read-only", "--read-only-tmpfs=false", "--image-volume=ignore",
		"--cap-drop=all", "--security-opt=no-new-privileges",
		"--ipc=none", "--pid=private", "--uts=private", "--cgroupns=private",
		"--cgroups=enabled", "--cgroup-parent=/" + delegatedContainerCgroup,
		"--pids-limit=" + strconv.Itoa(request.PIDsLimit),
		"--memory=" + memory, "--memory-swap=" + memory,
		"--cpus=" + strconv.FormatFloat(request.CPUs, 'f', 3, 64),
		"--userns=host",
		"--workdir=/workspace", "--stop-signal=SIGKILL",
		"--log-driver=none", "--env=HOME=/tmp", "--env=LANG=C.UTF-8",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=" + temporary,
		"--mount=" + mount,
		engine.image, request.Executable,
	}
	arguments = append(arguments, request.Arguments...)
	return arguments, nil
}

func validContainerCommand(request ContainerCommand) bool {
	if !containerNameRegex.MatchString(request.Name) || request.Workspace == "" || !filepath.IsAbs(request.Workspace) ||
		filepath.Clean(request.Workspace) != request.Workspace || strings.ContainsAny(request.Workspace, ",\n\r\x00") ||
		request.RuntimeRoot == "" || !filepath.IsAbs(request.RuntimeRoot) || filepath.Clean(request.RuntimeRoot) != request.RuntimeRoot ||
		strings.ContainsAny(request.RuntimeRoot, ",\n\r\x00") ||
		request.Executable == "" || !filepath.IsAbs(request.Executable) || strings.IndexByte(request.Executable, 0) >= 0 ||
		request.Timeout < time.Millisecond || request.Timeout > time.Hour || request.MemoryLimitBytes < 1 ||
		request.MemoryLimitBytes > 64<<30 || request.OutputLimitBytes < 1 || request.OutputLimitBytes > 1<<30 ||
		request.PIDsLimit < 1 || request.PIDsLimit > 4096 || request.CPUs <= 0 || request.CPUs > 64 ||
		request.TemporaryLimitBytes < 1<<20 || request.TemporaryLimitBytes > 4<<30 {
		return false
	}
	for _, argument := range request.Arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return false
		}
	}
	workspaceInfo, err := os.Lstat(request.Workspace)
	if err != nil || !workspaceInfo.IsDir() || workspaceInfo.Mode().Perm() != 0o700 {
		return false
	}
	runtimeInfo, err := os.Lstat(request.RuntimeRoot)
	return err == nil && runtimeInfo.IsDir() && runtimeInfo.Mode().Perm() == 0o700
}

func (engine *PodmanEngine) removeContainer(runtimeRoot, name string) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), podmanCleanupTimeout)
	defer cancel()
	command := exec.CommandContext(cleanupContext, engine.binary, "--cgroup-manager=cgroupfs", "--events-backend=none", "--hooks-dir="+engine.hooksDirectory,
		"--runroot="+runtimeRoot, "--transient-store", "rm", "--force", "--ignore", name)
	command.Env = minimalPodmanEnvironment()
	command.SysProcAttr = podmanProcessAttributes()
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("remove Podman container: %w: %s", err, boundedText(output, 512))
	}
	return nil
}

func podmanProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

func minimalPodmanEnvironment() []string {
	values := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	for _, name := range []string{"HOME", "XDG_RUNTIME_DIR", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			values = append(values, name+"="+value)
		}
	}
	return values
}

func safeEmptyHooksDirectory(path string) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return false
	}
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

func boundedText(value []byte, maximum int) string {
	if len(value) > maximum {
		value = value[:maximum]
	}
	return strings.ToValidUTF8(string(value), "�")
}

type combinedCapture struct {
	mu        sync.Mutex
	remaining int64
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	exceeded  bool
	cancel    context.CancelFunc
}

type captureWriter struct {
	owner  *combinedCapture
	stdout bool
}

func newCombinedCapture(limit int64, cancel context.CancelFunc) *combinedCapture {
	return &combinedCapture{remaining: limit, cancel: cancel}
}

func (capture *combinedCapture) writer(stdout bool) io.Writer {
	return captureWriter{owner: capture, stdout: stdout}
}

func (writer captureWriter) Write(value []byte) (int, error) {
	writer.owner.mu.Lock()
	defer writer.owner.mu.Unlock()
	accepted := int64(len(value))
	if accepted > writer.owner.remaining {
		accepted = writer.owner.remaining
		writer.owner.exceeded = true
	}
	if accepted > 0 {
		destination := &writer.owner.stderr
		if writer.stdout {
			destination = &writer.owner.stdout
		}
		_, _ = destination.Write(value[:accepted])
		writer.owner.remaining -= accepted
	}
	if writer.owner.exceeded {
		writer.owner.cancel()
	}
	return len(value), nil
}

func (capture *combinedCapture) snapshot() ([]byte, []byte, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return bytes.Clone(capture.stdout.Bytes()), bytes.Clone(capture.stderr.Bytes()), capture.exceeded
}
