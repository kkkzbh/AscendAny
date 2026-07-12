package judgerunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/judgeprotocol"
)

type fakeEngine struct {
	results []ContainerResult
	calls   []ContainerCommand
}

func (*fakeEngine) Identity() string { return testImage }

func (engine *fakeEngine) Run(_ context.Context, command ContainerCommand) (ContainerResult, error) {
	engine.calls = append(engine.calls, command)
	if command.Executable == cpp20Compiler {
		if err := os.WriteFile(filepath.Join(command.Workspace, "program"), []byte("executable"), 0o500); err != nil {
			return ContainerResult{}, err
		}
		if len(engine.results) == 0 {
			return ContainerResult{ExitCode: 0, Duration: time.Millisecond}, nil
		}
	}
	if len(engine.results) == 0 {
		value, _ := io.ReadAll(command.Stdin)
		return ContainerResult{ExitCode: 0, Stdout: value, Duration: time.Millisecond}, nil
	}
	result := engine.results[0]
	engine.results = engine.results[1:]
	return result, nil
}

func TestRunnerCompilesThenExecutesHiddenCases(t *testing.T) {
	jobID := "11111111-1111-4111-8111-111111111111"
	workRoot := filepath.Join(t.TempDir(), jobID)
	if err := os.Mkdir(workRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	compileRoot := filepath.Join(workRoot, "compile")
	casesRoot := filepath.Join(workRoot, "cases")
	if err := os.Mkdir(compileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(casesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(compileRoot, "main.cpp")
	inputPath := filepath.Join(casesRoot, "a.in")
	expectedPath := filepath.Join(casesRoot, "a.out")
	for path, content := range map[string]string{sourcePath: "int main(){}", inputPath: "answer\n", expectedPath: "answer\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	engine := &fakeEngine{}
	runner, err := New(engine, DefaultConfig(jobID, workRoot))
	if err != nil {
		t.Fatal(err)
	}
	request := materializedRequest{
		header: executionHeader(jobID), sourcePath: sourcePath,
		cases: []testCase{{ID: "a", Weight: 1, InputPath: inputPath, ExpectedPath: expectedPath}},
	}
	result, output, err := runner.execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != judgecontract.VerdictAccepted || result.ScoreFraction != 1 || result.PassedCaseCount != 1 || len(output) != 0 {
		t.Fatalf("result=%#v output=%q", result, output)
	}
	if len(engine.calls) != 2 || engine.calls[1].Workspace != filepath.Join(workRoot, "run") || !engine.calls[1].ReadOnlyWorkspace {
		t.Fatalf("engine calls = %#v", engine.calls)
	}
	if _, err := os.Stat(filepath.Join(workRoot, "run", "program")); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerMapsResourceFailuresToVerdicts(t *testing.T) {
	for name, containerResult := range map[string]ContainerResult{
		"timeout": {ExitCode: -1, TimedOut: true},
		"memory":  {ExitCode: 137},
		"output":  {ExitCode: -1, OutputLimitExceeded: true},
		"runtime": {ExitCode: 9},
	} {
		t.Run(name, func(t *testing.T) {
			if verdict := verdictForContainer(containerResult); verdict == judgecontract.VerdictAccepted {
				t.Fatalf("verdictForContainer(%#v) = accepted", containerResult)
			}
		})
	}
}

func TestRunnerCompileFailureDoesNotExecuteProgram(t *testing.T) {
	jobID := "11111111-1111-4111-8111-111111111111"
	workRoot := filepath.Join(t.TempDir(), jobID)
	if err := os.MkdirAll(filepath.Join(workRoot, "compile"), 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(workRoot, "compile", "main.cpp")
	if err := os.WriteFile(sourcePath, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{results: []ContainerResult{{ExitCode: 1, Stderr: []byte("compile failed")}}}
	runner, err := New(engine, DefaultConfig(jobID, workRoot))
	if err != nil {
		t.Fatal(err)
	}
	result, output, err := runner.execute(context.Background(), materializedRequest{
		header: executionHeader(jobID), sourcePath: sourcePath,
		cases: []testCase{{ID: "a", Weight: 1, InputPath: sourcePath, ExpectedPath: sourcePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != judgecontract.VerdictCompileError || string(output) != "compile failed" || len(engine.calls) != 1 {
		t.Fatalf("result=%#v output=%q calls=%d", result, output, len(engine.calls))
	}
}

func executionHeader(jobID string) judgeprotocol.RequestHeader {
	source := []byte("source")
	bundle := []byte("bundle")
	return judgeprotocol.RequestHeader{
		Schema: judgeprotocol.RequestSchemaV1, JudgeJobID: jobID,
		SubmissionID: "22222222-2222-4222-8222-222222222222",
		ProblemID:    "33333333-3333-4333-8333-333333333333", ProblemVersion: 1,
		Mode: judgecontract.SubmissionSubmit, LanguageID: judgecontract.LanguageCPP20,
		Source: testProtocolArtifact(source), TestBundle: testProtocolArtifact(bundle),
		ProblemSchema: judgecontract.ProblemSchemaV1,
		ProblemSpec:   json.RawMessage(`{"checker":"exact","schema":"ascendany.oj.problem-spec.v1"}`),
		TimeLimitMS:   1000, MemoryLimitBytes: 64 << 20, OutputLimitBytes: 1 << 20,
	}
}

func testProtocolArtifact(value []byte) judgeprotocol.Artifact {
	digest := sha256.Sum256(value)
	return judgeprotocol.Artifact{SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(value))}
}
