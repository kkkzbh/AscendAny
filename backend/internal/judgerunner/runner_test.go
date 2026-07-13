package judgerunner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	identity string
	results  []ContainerResult
	calls    []ContainerCommand
}

func (engine *fakeEngine) Identity() string { return engine.identity }

func (engine *fakeEngine) Run(_ context.Context, command ContainerCommand) (ContainerResult, error) {
	engine.calls = append(engine.calls, command)
	if command.Executable == cpp20Compiler {
		if err := os.WriteFile(filepath.Join(command.Workspace, "program"), staticLinuxAMD64ELF(), 0o500); err != nil {
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
	compilerEngine := &fakeEngine{identity: testCompilerImage}
	runtimeEngine := &fakeEngine{identity: testRuntimeImage}
	runner, err := New(compilerEngine, runtimeEngine, DefaultConfig(jobID, workRoot))
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
	if len(compilerEngine.calls) != 1 || len(runtimeEngine.calls) != 1 ||
		runtimeEngine.calls[0].Workspace != filepath.Join(workRoot, "run") || !runtimeEngine.calls[0].ReadOnlyWorkspace {
		t.Fatalf("compiler calls = %#v, runtime calls = %#v", compilerEngine.calls, runtimeEngine.calls)
	}
	if !containsArgument(compilerEngine.calls[0].Arguments, "-static") {
		t.Fatalf("compiler arguments do not require static linking: %#v", compilerEngine.calls[0].Arguments)
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
	compilerEngine := &fakeEngine{identity: testCompilerImage, results: []ContainerResult{{ExitCode: 1, Stderr: []byte("compile failed")}}}
	runtimeEngine := &fakeEngine{identity: testRuntimeImage}
	runner, err := New(compilerEngine, runtimeEngine, DefaultConfig(jobID, workRoot))
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
	if result.Verdict != judgecontract.VerdictCompileError || string(output) != "compile failed" ||
		len(compilerEngine.calls) != 1 || len(runtimeEngine.calls) != 0 {
		t.Fatalf("result=%#v output=%q compiler calls=%d runtime calls=%d", result, output, len(compilerEngine.calls), len(runtimeEngine.calls))
	}
}

func TestRunnerRejectsDynamicOrMalformedCompilerOutput(t *testing.T) {
	for name, output := range map[string][]byte{
		"malformed": []byte("executable"),
		"dynamic":   dynamicLinuxAMD64ELF(),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "program")
			if err := os.WriteFile(path, output, 0o500); err != nil {
				t.Fatal(err)
			}
			if err := verifyStaticLinuxAMD64Executable(path); err == nil {
				t.Fatal("verifyStaticLinuxAMD64Executable() error = nil")
			}
		})
	}
}

func TestExecutionManifestV2BindsBothImageIdentities(t *testing.T) {
	jobID := "11111111-1111-4111-8111-111111111111"
	workRoot := filepath.Join(t.TempDir(), jobID)
	runner, err := New(
		&fakeEngine{identity: testCompilerImage},
		&fakeEngine{identity: testRuntimeImage},
		DefaultConfig(jobID, workRoot),
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := runner.manifest(judgecontract.CheckerExact, judgecontract.SubmissionSubmit, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"cases":[],"checker":"exact","compilerImage":"` + testCompilerImage + `","mode":"submit","runtimeImage":"` + testRuntimeImage + `","schema":"ascendany.oj.execution-manifest.v2"}`
	if string(manifest) != expected {
		t.Fatalf("manifest = %s", manifest)
	}
}

func TestRunnerRequiresDistinctPinnedImageIdentities(t *testing.T) {
	jobID := "11111111-1111-4111-8111-111111111111"
	workRoot := filepath.Join(t.TempDir(), jobID)
	config := DefaultConfig(jobID, workRoot)
	for name, engines := range map[string]struct {
		compiler ContainerEngine
		runtime  ContainerEngine
	}{
		"same image": {
			compiler: &fakeEngine{identity: testCompilerImage},
			runtime:  &fakeEngine{identity: testCompilerImage},
		},
		"unpinned compiler": {
			compiler: &fakeEngine{identity: "localhost/compiler:latest"},
			runtime:  &fakeEngine{identity: testRuntimeImage},
		},
		"unpinned runtime": {
			compiler: &fakeEngine{identity: testCompilerImage},
			runtime:  &fakeEngine{identity: "localhost/runtime:latest"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(engines.compiler, engines.runtime, config); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func staticLinuxAMD64ELF() []byte {
	const (
		headerSize  = 64
		programSize = 56
	)
	value := make([]byte, headerSize+programSize)
	copy(value[:4], []byte{0x7f, 'E', 'L', 'F'})
	value[4] = byte(2)                                      // ELFCLASS64
	value[5] = byte(1)                                      // little endian
	value[6] = byte(1)                                      // ELF version
	binary.LittleEndian.PutUint16(value[16:18], uint16(2))  // ET_EXEC
	binary.LittleEndian.PutUint16(value[18:20], uint16(62)) // EM_X86_64
	binary.LittleEndian.PutUint32(value[20:24], uint32(1))
	binary.LittleEndian.PutUint64(value[24:32], uint64(0x400000))
	binary.LittleEndian.PutUint64(value[32:40], uint64(headerSize))
	binary.LittleEndian.PutUint16(value[52:54], uint16(headerSize))
	binary.LittleEndian.PutUint16(value[54:56], uint16(programSize))
	binary.LittleEndian.PutUint16(value[56:58], uint16(1))
	program := value[headerSize:]
	binary.LittleEndian.PutUint32(program[0:4], uint32(1)) // PT_LOAD
	binary.LittleEndian.PutUint32(program[4:8], uint32(5)) // PF_R | PF_X
	binary.LittleEndian.PutUint64(program[16:24], uint64(0x400000))
	binary.LittleEndian.PutUint64(program[24:32], uint64(0x400000))
	binary.LittleEndian.PutUint64(program[32:40], uint64(len(value)))
	binary.LittleEndian.PutUint64(program[40:48], uint64(len(value)))
	binary.LittleEndian.PutUint64(program[48:56], uint64(0x1000))
	return value
}

func dynamicLinuxAMD64ELF() []byte {
	value := staticLinuxAMD64ELF()
	binary.LittleEndian.PutUint32(value[64:68], uint32(3)) // PT_INTERP
	return value
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
