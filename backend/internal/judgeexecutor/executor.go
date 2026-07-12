// Package judgeexecutor provides the ascendanyd-side judgecontract.Executor
// implementation for one isolated systemd judge instance per durable attempt.
package judgeexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/judgeprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"golang.org/x/sys/unix"
)

var failureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

type ArtifactVerifier interface {
	Verify(context.Context, string, int64) (artifactstore.Artifact, error)
}

type InstanceLauncher interface {
	Start(context.Context, string) error
	Stop(context.Context, string) error
}

type Config struct {
	SocketDirectory  string
	ExpectedJudgeUID uint32
	StartupTimeout   time.Duration
	SessionTimeout   time.Duration
	StopTimeout      time.Duration
	Policy           oj.Policy
}

type Executor struct {
	artifacts ArtifactVerifier
	launcher  InstanceLauncher
	config    Config
}

func New(artifacts ArtifactVerifier, launcher InstanceLauncher, config Config) (*Executor, error) {
	if artifacts == nil || launcher == nil || !validConfig(config) || !realDirectory(config.SocketDirectory) {
		return nil, errors.New("artifact verifier, judge launcher, and bounded executor configuration are required")
	}
	return &Executor{artifacts: artifacts, launcher: launcher, config: config}, nil
}

func (executor *Executor) Execute(ctx context.Context, request judgecontract.ExecutionRequest) (result judgecontract.ExecutorResult, resultErr error) {
	if ctx == nil {
		return judgecontract.ExecutorResult{}, executionFailure("invalid_execution_request", true, "execution context is required")
	}
	if err := validateRequest(request, executor.config.Policy); err != nil {
		return judgecontract.ExecutorResult{}, executionFailure("invalid_execution_request", true, err.Error())
	}
	payloads, err := executor.openPayloads(ctx, request)
	if err != nil {
		return judgecontract.ExecutorResult{}, executionFailure("artifact_invalid", true, "execution artifact failed content verification")
	}
	defer payloads.close()
	socketPath := filepath.Join(executor.config.SocketDirectory, request.JudgeJobID+".sock")
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		return judgecontract.ExecutorResult{}, executionFailure("sandbox_stale_socket", false, "job socket already exists before launch")
	}
	launchContext, cancelLaunch := context.WithTimeout(ctx, executor.config.StartupTimeout)
	err = executor.launcher.Start(launchContext, request.JudgeJobID)
	cancelLaunch()
	if err != nil {
		return judgecontract.ExecutorResult{}, executionFailure("sandbox_unavailable", false, "systemd could not start the isolated judge instance")
	}
	defer func() {
		if stopErr := executor.stop(request.JudgeJobID); stopErr != nil {
			classified := executionFailure("sandbox_cleanup_failed", false, "systemd could not stop the isolated judge instance")
			if resultErr == nil {
				resultErr = classified
			} else {
				resultErr = errors.Join(resultErr, classified)
			}
		}
	}()
	connection, err := executor.connect(ctx, socketPath)
	if err != nil {
		return judgecontract.ExecutorResult{}, executionFailure("sandbox_unavailable", false, "isolated judge socket did not become ready")
	}
	defer connection.Close()
	if err := requirePeerUID(connection, executor.config.ExpectedJudgeUID); err != nil {
		return judgecontract.ExecutorResult{}, executionFailure("sandbox_peer_rejected", true, "judge socket peer UID does not match the configured identity")
	}
	sessionContext, cancelSession := context.WithTimeout(ctx, executor.config.SessionTimeout)
	defer cancelSession()
	closeOnCancel := context.AfterFunc(sessionContext, func() { _ = connection.Close() })
	defer closeOnCancel()
	header := judgeprotocol.HeaderFromExecution(request)
	var stdin io.Reader
	if payloads.stdin != nil {
		stdin = payloads.stdin
	}
	writeErr := judgeprotocol.WriteRequest(connection, header, judgeprotocol.Payloads{
		Source: payloads.source, Stdin: stdin, TestBundle: payloads.testBundle,
	})
	_ = connection.CloseWrite()
	response, output, readErr := judgeprotocol.ReadResponse(connection, request.OutputLimitBytes)
	if readErr != nil {
		if context.Cause(ctx) != nil {
			return judgecontract.ExecutorResult{}, context.Cause(ctx)
		}
		if errors.Is(context.Cause(sessionContext), context.DeadlineExceeded) {
			return judgecontract.ExecutorResult{}, executionFailure("sandbox_session_timeout", true, "isolated judge exceeded the whole-job hard timeout")
		}
		return judgecontract.ExecutorResult{}, executionFailure("judge_protocol_error", true, "isolated judge returned an invalid response")
	}
	if writeErr != nil && response.Failure == nil {
		return judgecontract.ExecutorResult{}, executionFailure("judge_protocol_error", true, "isolated judge rejected the request stream")
	}
	return resultFromResponse(request, response, output)
}

func (executor *Executor) connect(ctx context.Context, socketPath string) (*net.UnixConn, error) {
	startupContext, cancel := context.WithTimeout(ctx, executor.config.StartupTimeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
		if err == nil {
			return connection, nil
		}
		select {
		case <-startupContext.Done():
			return nil, context.Cause(startupContext)
		case <-ticker.C:
		}
	}
}

func (executor *Executor) stop(jobID string) error {
	stopContext, cancel := context.WithTimeout(context.Background(), executor.config.StopTimeout)
	defer cancel()
	return executor.launcher.Stop(stopContext, jobID)
}

type openedPayloads struct {
	source     *os.File
	stdin      *os.File
	testBundle *os.File
}

func (executor *Executor) openPayloads(ctx context.Context, request judgecontract.ExecutionRequest) (openedPayloads, error) {
	var opened openedPayloads
	var err error
	if opened.source, err = executor.openArtifact(ctx, request.Source); err != nil {
		return opened, err
	}
	if request.Stdin != nil {
		if opened.stdin, err = executor.openArtifact(ctx, *request.Stdin); err != nil {
			opened.close()
			return opened, err
		}
	}
	if opened.testBundle, err = executor.openArtifact(ctx, request.TestBundle); err != nil {
		opened.close()
		return opened, err
	}
	return opened, nil
}

func (executor *Executor) openArtifact(ctx context.Context, descriptor judgecontract.Artifact) (*os.File, error) {
	verified, err := executor.artifacts.Verify(ctx, descriptor.SHA256, descriptor.SizeBytes)
	if err != nil || verified.Hash != descriptor.SHA256 || verified.Size != descriptor.SizeBytes ||
		verified.StorageKey != descriptor.StorageKey || !filepath.IsAbs(verified.Path) {
		return nil, errors.New("verified artifact metadata mismatch")
	}
	descriptorFD, err := unix.Open(verified.Path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptorFD), verified.Path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o640 || info.Size() != descriptor.SizeBytes {
		file.Close()
		return nil, errors.New("verified artifact changed while opening")
	}
	return file, nil
}

func (opened openedPayloads) close() {
	for _, file := range []*os.File{opened.source, opened.stdin, opened.testBundle} {
		if file != nil {
			_ = file.Close()
		}
	}
}

func resultFromResponse(request judgecontract.ExecutionRequest, response judgeprotocol.ResponseHeader, output []byte) (judgecontract.ExecutorResult, error) {
	if response.Schema != judgeprotocol.ResponseSchemaV1 || response.JobID != request.JudgeJobID ||
		(response.Result == nil) == (response.Failure == nil) {
		return judgecontract.ExecutorResult{}, executionFailure("judge_protocol_error", true, "response envelope violates the protocol")
	}
	if response.Failure != nil {
		if !judgeprotocol.ValidFailure(response.Failure) || !failureCodePattern.MatchString(response.Failure.Code) {
			return judgecontract.ExecutorResult{}, executionFailure("judge_protocol_error", true, "failure envelope violates the protocol")
		}
		return judgecontract.ExecutorResult{}, executionFailure(response.Failure.Code, response.Failure.Permanent, response.Failure.Detail)
	}
	result := response.Result
	if result.Schema != judgeprotocol.ResultSchemaV1 || !judgecontract.ValidVerdict(result.Verdict) ||
		math.IsNaN(result.ScoreFraction) || math.IsInf(result.ScoreFraction, 0) ||
		result.ScoreFraction < 0 || result.ScoreFraction > 1 || result.PassedCaseCount < 0 ||
		result.TotalCaseCount < 0 || result.PassedCaseCount > result.TotalCaseCount ||
		result.MaxTimeMS < 0 || result.MaxMemoryBytes < 0 || int64(len(output)) > request.OutputLimitBytes {
		return judgecontract.ExecutorResult{}, executionFailure("judge_protocol_error", true, "result metrics violate the protocol")
	}
	manifest, _, err := canonicaljson.Object(result.ResultManifest, 256<<10)
	if err != nil || !bytes.Equal(manifest, result.ResultManifest) {
		return judgecontract.ExecutorResult{}, executionFailure("judge_protocol_error", true, "result manifest is not canonical JSON")
	}
	return judgecontract.ExecutorResult{
		Verdict: result.Verdict, ScoreFraction: result.ScoreFraction,
		PassedCaseCount: result.PassedCaseCount, TotalCaseCount: result.TotalCaseCount,
		MaxTimeMS: result.MaxTimeMS, MaxMemoryBytes: result.MaxMemoryBytes,
		Output: output, ResultManifest: json.RawMessage(manifest),
	}, nil
}

func validateRequest(request judgecontract.ExecutionRequest, policy oj.Policy) error {
	if !judgecontract.ValidPublicID(request.JudgeJobID) || !judgecontract.ValidPublicID(request.SubmissionID) ||
		!judgecontract.ValidPublicID(request.ProblemID) || request.ProblemVersion < 1 ||
		request.LanguageID != judgecontract.LanguageCPP20 || request.ProblemSchema != judgecontract.ProblemSchemaV1 ||
		request.TimeLimitMS < 1 || request.TimeLimitMS > policy.MaximumTimeLimitMS ||
		request.MemoryLimitBytes < 1 || request.MemoryLimitBytes > policy.MaximumMemoryBytes ||
		request.OutputLimitBytes < 1 || request.OutputLimitBytes > policy.MaximumOutputBytes {
		return errors.New("request identity, schema, language, or resources are invalid")
	}
	if err := judgecontract.ValidateArtifact(request.Source, judgecontract.CPP20SourceMediaType, policy.MaximumSourceBytes); err != nil {
		return err
	}
	if err := judgecontract.ValidateArtifact(request.TestBundle, judgecontract.TestBundleMediaType, policy.MaximumTestBundleBytes); err != nil {
		return err
	}
	if request.Mode == judgecontract.SubmissionRun {
		if request.Stdin == nil {
			return errors.New("run request requires stdin")
		}
		if err := judgecontract.ValidateArtifact(*request.Stdin, judgecontract.PlainTextMediaType, policy.MaximumStdinBytes); err != nil {
			return err
		}
	} else if request.Mode != judgecontract.SubmissionSubmit || request.Stdin != nil {
		return errors.New("submission mode and stdin are inconsistent")
	}
	if canonical, _, err := canonicaljson.Object(request.ProblemSpec, policy.MaximumProblemSpecBytes); err != nil || !bytes.Equal(canonical, request.ProblemSpec) {
		return errors.New("problem spec is not canonical JSON")
	}
	return nil
}

func executionFailure(code string, permanent bool, detail string) error {
	return &judgecontract.ExecutionFailure{Code: code, Permanent: permanent, Cause: errors.New(detail)}
}

func requirePeerUID(connection *net.UnixConn, expected uint32) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *unix.Ucred
	var controlErr error
	if err := raw.Control(func(descriptor uintptr) {
		credentials, controlErr = unix.GetsockoptUcred(int(descriptor), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	if credentials == nil || credentials.Uid != expected {
		return fmt.Errorf("peer UID mismatch")
	}
	return nil
}

func validConfig(config Config) bool {
	return config.ExpectedJudgeUID != 0 && config.SocketDirectory != "" && filepath.IsAbs(config.SocketDirectory) &&
		filepath.Clean(config.SocketDirectory) == config.SocketDirectory &&
		len(filepath.Join(config.SocketDirectory, "11111111-1111-4111-8111-111111111111.sock")) <= 107 &&
		config.StartupTimeout >= time.Second && config.StartupTimeout <= time.Minute &&
		config.SessionTimeout >= time.Second && config.SessionTimeout <= time.Hour &&
		config.StopTimeout >= time.Second && config.StopTimeout <= time.Minute && oj.ValidPolicy(config.Policy)
}

func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}
