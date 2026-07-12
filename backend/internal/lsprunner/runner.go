package lsprunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspunix"
	"github.com/kkkzbh/AscendAny/backend/internal/lspwire"
)

type Config struct {
	SessionID      string
	ControlSocket  string
	Workspace      string
	ClangdBinary   string
	ConnectTimeout time.Duration
	CleanupTimeout time.Duration
	Policy         lsp.Policy
}

type Runner struct {
	config Config
	start  processStarter
}

type childProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
}

type processStarter func(Config) (*childProcess, error)

func DefaultConfig(sessionID, controlSocket, workspace string) Config {
	return Config{
		SessionID: sessionID, ControlSocket: controlSocket, Workspace: workspace,
		ClangdBinary:   "/usr/bin/clangd",
		ConnectTimeout: 10 * time.Second, CleanupTimeout: 2 * time.Second,
		Policy: lsp.DefaultPolicy(),
	}
}

func New(config Config) (*Runner, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Runner{config: config, start: startClangd}, nil
}

func (runner *Runner) Serve(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errors.New("LSP runner context is required")
	}
	if err := prepareWorkspace(runner.config.Workspace); err != nil {
		return fmt.Errorf("prepare LSP workspace: %w", err)
	}
	defer func() {
		if cleanupErr := cleanupWorkspace(runner.config.Workspace, runner.config.CleanupTimeout); cleanupErr != nil {
			if resultErr == nil {
				resultErr = cleanupErr
			} else {
				resultErr = errors.Join(resultErr, cleanupErr)
			}
		}
	}()

	sessionContext, cancelSession := context.WithTimeout(ctx, runner.config.Policy.MaximumSessionDuration)
	defer cancelSession()
	connection, err := runner.connect(sessionContext)
	if err != nil {
		return err
	}
	defer connection.Close()
	process, err := runner.start(runner.config)
	if err != nil {
		return fmt.Errorf("start clangd: %w", err)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- process.command.Wait() }()
	if err := lspwire.WriteHello(connection, runner.config.SessionID, runner.config.Policy); err != nil {
		return errors.Join(fmt.Errorf("write LSP control hello: %w", err), stopStartedProcess(process, waitResult))
	}
	return runner.relay(sessionContext, connection, process, waitResult)
}

func (runner *Runner) connect(ctx context.Context) (*net.UnixConn, error) {
	if err := lspunix.EnsureRealDirectory(filepath.Dir(runner.config.ControlSocket)); err != nil {
		return nil, fmt.Errorf("validate LSP control socket directory: %w", err)
	}
	before, err := os.Lstat(runner.config.ControlSocket)
	expectedServerUID, err := controlSocketOwnerUID(before, err)
	if err != nil {
		return nil, err
	}
	connectContext, cancel := context.WithTimeout(ctx, runner.config.ConnectTimeout)
	defer cancel()
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(connectContext, "unix", runner.config.ControlSocket)
	if err != nil {
		return nil, fmt.Errorf("connect LSP control socket: %w", err)
	}
	connection, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, errors.New("LSP control connection is not Unix")
	}
	after, statErr := os.Lstat(runner.config.ControlSocket)
	if statErr != nil || !os.SameFile(before, after) {
		_ = connection.Close()
		return nil, errors.New("LSP control socket identity changed during connect")
	}
	if err := lspunix.RequirePeerUID(connection, expectedServerUID); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func controlSocketOwnerUID(info os.FileInfo, statErr error) (uint32, error) {
	if statErr != nil || info == nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o660 {
		return 0, errors.New("LSP control socket must be an existing mode-0660 Unix socket")
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !ok || metadata.Uid == 0 {
		return 0, errors.New("LSP control socket must have one non-root owner UID")
	}
	return metadata.Uid, nil
}

func (runner *Runner) relay(
	ctx context.Context,
	control *net.UnixConn,
	process *childProcess,
	waitResult <-chan error,
) (resultErr error) {
	processStopped := false
	defer func() {
		if !processStopped {
			resultErr = errors.Join(resultErr, stopStartedProcess(process, waitResult))
		}
	}()
	controlReader, err := lspwire.NewReader(control, runner.config.Policy)
	if err != nil {
		return err
	}
	controlWriter, err := lspwire.NewWriter(control, runner.config.Policy)
	if err != nil {
		return err
	}
	clangdReader, err := lspwire.NewReader(process.output, runner.config.Policy)
	if err != nil {
		return err
	}
	clangdWriter, err := lspwire.NewWriter(process.input, runner.config.Policy)
	if err != nil {
		return err
	}

	relayErrors := make(chan error, 2)
	var relays sync.WaitGroup
	relays.Add(2)
	go func() {
		defer relays.Done()
		for {
			body, readErr := controlReader.Read()
			if readErr != nil {
				relayErrors <- readErr
				return
			}
			if validateErr := lsp.ValidateClientMessage(body); validateErr != nil {
				relayErrors <- validateErr
				return
			}
			if writeErr := clangdWriter.Write(body); writeErr != nil {
				relayErrors <- writeErr
				return
			}
		}
	}()
	go func() {
		defer relays.Done()
		for {
			body, readErr := clangdReader.Read()
			if readErr != nil {
				relayErrors <- readErr
				return
			}
			if writeErr := controlWriter.Write(body); writeErr != nil {
				relayErrors <- writeErr
				return
			}
		}
	}()

	workspaceFailure := monitorWorkspace(ctx, runner.config.Workspace, runner.config.Policy)

	var selected error
	select {
	case <-ctx.Done():
		selected = context.Cause(ctx)
	case selected = <-relayErrors:
	case selected = <-workspaceFailure:
	case selected = <-waitResult:
		processStopped = true
		if selected == nil {
			selected = errors.New("clangd exited before the LSP session disconnected")
		}
	}

	_ = control.Close()
	if !processStopped {
		selected = errors.Join(selected, stopStartedProcess(process, waitResult))
		processStopped = true
	}
	relaysDone := make(chan struct{})
	go func() {
		relays.Wait()
		close(relaysDone)
	}()
	select {
	case <-relaysDone:
	case <-time.After(runner.config.CleanupTimeout):
		selected = errors.Join(selected, errors.New("LSP relay goroutines did not stop before cleanup deadline"))
	}
	if errors.Is(selected, io.EOF) || errors.Is(selected, net.ErrClosed) || errors.Is(selected, context.Canceled) {
		return nil
	}
	return selected
}

func stopStartedProcess(process *childProcess, waitResult <-chan error) error {
	if process == nil || process.command == nil || waitResult == nil {
		return errors.New("started clangd process and waiter are required")
	}
	if process.input != nil {
		_ = process.input.Close()
	}
	var result error
	if err := killProcessGroup(process.command); err != nil {
		result = err
	}
	if err := <-waitResult; err != nil && !killedProcess(err) {
		result = errors.Join(result, fmt.Errorf("wait for clangd: %w", err))
	}
	return result
}

func startClangd(config Config) (*childProcess, error) {
	arguments := []string{
		"--background-index=false",
		"--clang-tidy=false",
		"--completion-style=bundled",
		"--enable-config=false",
		"--header-insertion=never",
		"--limit-references=100",
		"--limit-results=100",
		"--rename-file-limit=8",
		"--pch-storage=memory",
		"--log=error",
		"-j=2",
		"--compile-commands-dir=" + config.Workspace,
		"--path-mappings=/workspace=" + config.Workspace,
	}
	command := exec.Command(config.ClangdBinary, arguments...)
	command.Dir = config.Workspace
	command.Env = []string{
		"HOME=" + config.Workspace,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"PATH=/usr/bin:/bin",
		"TMPDIR=" + filepath.Join(config.Workspace, "tmp"),
		"XDG_CACHE_HOME=" + filepath.Join(config.Workspace, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(config.Workspace, "config"),
	}
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	return &childProcess{command: command, input: input, output: output}, nil
}

func killProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil || command.Process.Pid < 1 {
		return errors.New("clangd process is unavailable for termination")
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill clangd process group: %w", err)
	}
	return nil
}

func killedProcess(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

func validateConfig(config Config) error {
	if !lsp.ValidPublicID(config.SessionID) || !lsp.ValidPolicy(config.Policy) {
		return errors.New("canonical session ID and valid LSP policy are required")
	}
	if config.ControlSocket == "" || !filepath.IsAbs(config.ControlSocket) || filepath.Clean(config.ControlSocket) != config.ControlSocket || len(config.ControlSocket) > 107 {
		return errors.New("LSP control socket path must be canonical, absolute, and Unix-compatible")
	}
	if config.Workspace == "" || !filepath.IsAbs(config.Workspace) || filepath.Clean(config.Workspace) != config.Workspace || filepath.Base(config.Workspace) != config.SessionID || strings.ContainsAny(config.Workspace, "=,") {
		return errors.New("LSP workspace must be a canonical per-session path")
	}
	if config.ConnectTimeout < time.Second || config.ConnectTimeout > time.Minute || config.CleanupTimeout < time.Second || config.CleanupTimeout > 10*time.Second {
		return errors.New("LSP connection and cleanup timeouts are invalid")
	}
	if err := lspunix.RequireRootOwnedExecutable(config.ClangdBinary); err != nil {
		return fmt.Errorf("validate clangd binary: %w", err)
	}
	return nil
}
