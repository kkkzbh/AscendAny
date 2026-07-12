package judgerunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/judgeprotocol"
	"golang.org/x/sys/unix"
)

type ServerConfig struct {
	SocketPath             string
	AllowedClientUID       uint32
	AcceptTimeout          time.Duration
	MaximumSessionDuration time.Duration
}

type Server struct {
	runner *Runner
	config ServerConfig
}

func NewServer(runner *Runner, config ServerConfig) (*Server, error) {
	if runner == nil || !validServerConfig(runner.config.JobID, config) {
		return nil, errors.New("judge runner and bounded Unix server configuration are required")
	}
	return &Server{runner: runner, config: config}, nil
}

func (server *Server) ServeOne(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errors.New("judge server context is required")
	}
	if err := ensureRealDirectory(filepath.Dir(server.config.SocketPath)); err != nil {
		return fmt.Errorf("validate judge socket directory: %w", err)
	}
	if _, err := os.Lstat(server.config.SocketPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("judge socket path already exists")
		}
		return fmt.Errorf("inspect judge socket path: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.config.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on judge socket: %w", err)
	}
	listener.SetUnlinkOnClose(true)
	defer func() {
		if closeErr := listener.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close judge listener: %w", closeErr)
		}
	}()
	if err := os.Chmod(server.config.SocketPath, 0o660); err != nil {
		return fmt.Errorf("set judge socket mode: %w", err)
	}
	if err := listener.SetDeadline(time.Now().Add(server.config.AcceptTimeout)); err != nil {
		return fmt.Errorf("set judge accept deadline: %w", err)
	}
	stopClosingListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	connection, err := listener.AcceptUnix()
	stopClosingListener()
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return fmt.Errorf("accept judge client: %w", err)
	}
	defer connection.Close()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err := requirePeerUID(connection, server.config.AllowedClientUID); err != nil {
		return err
	}
	sessionContext, cancel := context.WithTimeout(ctx, server.config.MaximumSessionDuration)
	defer cancel()
	closeOnCancel := context.AfterFunc(sessionContext, func() { _ = connection.Close() })
	defer closeOnCancel()
	return server.serveConnection(sessionContext, connection)
}

func (server *Server) serveConnection(ctx context.Context, connection *net.UnixConn) (resultErr error) {
	if err := server.prepareWorkRoot(); err != nil {
		return err
	}
	cleaned := false
	defer func() {
		if !cleaned {
			if cleanupErr := cleanupWorkRoot(server.runner.config.WorkRoot); cleanupErr != nil {
				if resultErr == nil {
					resultErr = cleanupErr
				} else {
					resultErr = errors.Join(resultErr, cleanupErr)
				}
			}
		}
	}()
	compileRoot := filepath.Join(server.runner.config.WorkRoot, "compile")
	if err := os.Mkdir(compileRoot, 0o700); err != nil {
		return fmt.Errorf("create compiler workspace: %w", err)
	}
	materialized := materializedRequest{}
	header, err := judgeprotocol.ReadRequest(connection, server.runner.validateHeader, func(
		header judgeprotocol.RequestHeader,
		kind judgeprotocol.PayloadKind,
		_ judgeprotocol.Artifact,
		content io.Reader,
	) error {
		materialized.header = header
		switch kind {
		case judgeprotocol.PayloadSource:
			materialized.sourcePath = filepath.Join(compileRoot, "main.cpp")
			return writePrivateFile(materialized.sourcePath, content)
		case judgeprotocol.PayloadStdin:
			materialized.stdinPath = filepath.Join(server.runner.config.WorkRoot, "stdin")
			return writePrivateFile(materialized.stdinPath, content)
		case judgeprotocol.PayloadTestBundle:
			cases, bundleErr := extractTestBundle(content, filepath.Join(server.runner.config.WorkRoot, "cases"),
				server.runner.config.MaximumCases, server.runner.config.MaximumCaseBytes)
			materialized.cases = cases
			return bundleErr
		default:
			return errors.New("unsupported judge payload kind")
		}
	})
	if err != nil {
		failure := judgeprotocol.ResponseHeader{
			Schema: judgeprotocol.ResponseSchemaV1, JobID: server.runner.config.JobID,
			Failure: &judgeprotocol.Failure{
				Code: "invalid_request_payload", Permanent: true,
				Detail: "request payload violates the judge protocol or test bundle contract",
			},
		}
		if cleanupErr := cleanupWorkRoot(server.runner.config.WorkRoot); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		cleaned = true
		if writeErr := judgeprotocol.WriteResponse(connection, failure, nil); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		return nil
	}
	materialized.header = header
	result, output, err := server.runner.execute(ctx, materialized)
	if err != nil {
		if context.Cause(ctx) != nil {
			return context.Cause(ctx)
		}
		failure := failureResponse(server.runner.config.JobID, err)
		if cleanupErr := cleanupWorkRoot(server.runner.config.WorkRoot); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		cleaned = true
		return judgeprotocol.WriteResponse(connection, failure, nil)
	}
	response := judgeprotocol.ResponseHeader{
		Schema: judgeprotocol.ResponseSchemaV1, JobID: server.runner.config.JobID, Result: &result,
	}
	if err := cleanupWorkRoot(server.runner.config.WorkRoot); err != nil {
		return err
	}
	cleaned = true
	return judgeprotocol.WriteResponse(connection, response, output)
}

func (server *Server) prepareWorkRoot() error {
	parent := filepath.Dir(server.runner.config.WorkRoot)
	if err := ensureRealDirectory(parent); err != nil {
		return fmt.Errorf("validate judge work parent: %w", err)
	}
	if _, err := os.Lstat(server.runner.config.WorkRoot); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("judge work root already exists")
		}
		return fmt.Errorf("inspect judge work root: %w", err)
	}
	if err := os.Mkdir(server.runner.config.WorkRoot, 0o700); err != nil {
		return fmt.Errorf("create judge work root: %w", err)
	}
	return nil
}

func failureResponse(jobID string, err error) judgeprotocol.ResponseHeader {
	failure := &Failure{Code: "executor_internal", Detail: "judge executor failed before producing a result"}
	_ = errors.As(err, &failure)
	detail := failure.Detail
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	return judgeprotocol.ResponseHeader{
		Schema: judgeprotocol.ResponseSchemaV1, JobID: jobID,
		Failure: &judgeprotocol.Failure{Code: failure.Code, Permanent: failure.Permanent, Detail: detail},
	}
}

func writePrivateFile(path string, source io.Reader) (resultErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	if _, err := io.Copy(file, source); err != nil {
		return err
	}
	return file.Sync()
}

func requirePeerUID(connection *net.UnixConn, expected uint32) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return fmt.Errorf("access judge peer credentials: %w", err)
	}
	var credentials *unix.Ucred
	var controlErr error
	if err := raw.Control(func(descriptor uintptr) {
		credentials, controlErr = unix.GetsockoptUcred(int(descriptor), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("read judge peer credentials: %w", err)
	}
	if controlErr != nil {
		return fmt.Errorf("read judge peer credentials: %w", controlErr)
	}
	if credentials == nil || credentials.Uid != expected {
		return errors.New("judge peer UID is not authorized")
	}
	return nil
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return errors.New("path must be an existing directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("directory path must not traverse symbolic links")
	}
	return nil
}

func validServerConfig(jobID string, config ServerConfig) bool {
	return config.AllowedClientUID != 0 && config.SocketPath != "" && filepath.IsAbs(config.SocketPath) &&
		filepath.Clean(config.SocketPath) == config.SocketPath && len(config.SocketPath) <= 107 && filepath.Base(config.SocketPath) == jobID+".sock" &&
		config.AcceptTimeout >= time.Second && config.AcceptTimeout <= time.Minute &&
		config.MaximumSessionDuration >= time.Second && config.MaximumSessionDuration <= time.Hour
}

func cleanupWorkRoot(path string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := os.RemoveAll(path); err != nil {
			if time.Now().After(deadline) {
				return fmt.Errorf("remove judge work root: %w", err)
			}
		} else {
			timer := time.NewTimer(20 * time.Millisecond)
			<-timer.C
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("judge work root remained after cleanup deadline")
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
}
