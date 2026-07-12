package judgeexecutor

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/judgeprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/judgerunner"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

const integrationImage = "localhost/ascendany-cpp20@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type integrationEngine struct{}

func (*integrationEngine) Identity() string { return integrationImage }

func (*integrationEngine) Run(_ context.Context, command judgerunner.ContainerCommand) (judgerunner.ContainerResult, error) {
	if command.Executable == "/usr/local/bin/g++" {
		if err := os.WriteFile(filepath.Join(command.Workspace, "program"), []byte("executable"), 0o500); err != nil {
			return judgerunner.ContainerResult{}, err
		}
		return judgerunner.ContainerResult{ExitCode: 0, Duration: time.Millisecond}, nil
	}
	input, err := io.ReadAll(command.Stdin)
	return judgerunner.ContainerResult{ExitCode: 0, Stdout: input, Duration: time.Millisecond}, err
}

type inProcessLauncher struct {
	server *judgerunner.Server
	done   chan error
}

func (launcher *inProcessLauncher) Start(context.Context, string) error {
	go func() { launcher.done <- launcher.server.ServeOne(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-launcher.done:
		return fmt.Errorf("server exited during startup: %w", err)
	default:
		return nil
	}
}

func (launcher *inProcessLauncher) Stop(ctx context.Context, _ string) error {
	select {
	case err := <-launcher.done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func TestExecutorAndIsolatedServerStreamVerifiedJob(t *testing.T) {
	jobID := "11111111-1111-4111-8111-111111111111"
	root, err := os.MkdirTemp("/tmp", "aj-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketDirectory := filepath.Join(root, "sockets")
	workParent := filepath.Join(root, "jobs")
	for _, directory := range []string{socketDirectory, workParent} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workRoot := filepath.Join(workParent, jobID)
	runner, err := judgerunner.New(&integrationEngine{}, judgerunner.DefaultConfig(jobID, workRoot))
	if err != nil {
		t.Fatal(err)
	}
	server, err := judgerunner.NewServer(runner, judgerunner.ServerConfig{
		SocketPath: filepath.Join(socketDirectory, jobID+".sock"), AllowedClientUID: uint32(os.Getuid()),
		AcceptTimeout: 5 * time.Second, MaximumSessionDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := artifactstore.NewStore(filepath.Join(root, "artifacts"), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	source := publishArtifact(t, store, []byte("int main() { return 0; }"), judgecontract.CPP20SourceMediaType)
	bundle := publishArtifact(t, store, strictBundle(t, "answer\n", "answer\n"), judgecontract.TestBundleMediaType)
	launcher := &inProcessLauncher{server: server, done: make(chan error, 1)}
	executor, err := New(store, launcher, Config{
		SocketDirectory: socketDirectory, ExpectedJudgeUID: uint32(os.Getuid()),
		StartupTimeout: 5 * time.Second, SessionTimeout: 10 * time.Second,
		StopTimeout: 5 * time.Second, Policy: oj.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), judgecontract.ExecutionRequest{
		JudgeJobID: jobID, SubmissionID: "22222222-2222-4222-8222-222222222222",
		ProblemID: "33333333-3333-4333-8333-333333333333", ProblemVersion: 1,
		Mode: judgecontract.SubmissionSubmit, LanguageID: judgecontract.LanguageCPP20,
		Source: source, TestBundle: bundle, ProblemSchema: judgecontract.ProblemSchemaV1,
		ProblemSpec: json.RawMessage(`{"checker":"exact","schema":"ascendany.oj.problem-spec.v1"}`),
		TimeLimitMS: 1000, MemoryLimitBytes: 64 << 20, OutputLimitBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != judgecontract.VerdictAccepted || result.ScoreFraction != 1 || result.PassedCaseCount != 1 || result.TotalCaseCount != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Lstat(workRoot); !os.IsNotExist(err) {
		t.Fatalf("work root remains after execution: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(socketDirectory, jobID+".sock")); !os.IsNotExist(err) {
		t.Fatalf("socket remains after execution: %v", err)
	}
}

func TestResultResponseRejectsAmbiguousEnvelope(t *testing.T) {
	request := judgecontract.ExecutionRequest{JudgeJobID: "11111111-1111-4111-8111-111111111111", OutputLimitBytes: 1024}
	_, err := resultFromResponse(request, structResponseBoth(), nil)
	var failure *judgecontract.ExecutionFailure
	if !errorsAs(err, &failure) || failure.Code != "judge_protocol_error" || !failure.Permanent {
		t.Fatalf("resultFromResponse() error = %#v", err)
	}
}

func structResponseBoth() judgeprotocol.ResponseHeader {
	return judgeprotocol.ResponseHeader{
		Schema: judgeprotocol.ResponseSchemaV1, JobID: "11111111-1111-4111-8111-111111111111",
		Result: &judgeprotocol.Result{}, Failure: &judgeprotocol.Failure{Code: "x", Detail: "x"},
	}
}

func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

func publishArtifact(t *testing.T, store *artifactstore.Store, content []byte, mediaType string) judgecontract.Artifact {
	t.Helper()
	publication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.Release(); err != nil {
		t.Fatal(err)
	}
	return judgecontract.Artifact{
		SHA256: publication.Artifact.Hash, SizeBytes: publication.Artifact.Size,
		StorageKey: publication.Artifact.StorageKey, MediaType: mediaType,
	}
}

func strictBundle(t *testing.T, input, expected string) []byte {
	t.Helper()
	manifest := []byte(`{"cases":[{"id":"case","weight":1}],"schema":"ascendany.oj.test-bundle.v1"}`)
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, item := range []struct {
		name    string
		content []byte
	}{
		{"manifest.json", manifest}, {"cases/case.in", []byte(input)}, {"cases/case.out", []byte(expected)},
	} {
		header := &tar.Header{
			Name: item.name, Mode: 0o600, Size: int64(len(item.content)), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(item.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
