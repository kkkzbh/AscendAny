package judgerunner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/judgeprotocol"
)

type Config struct {
	JobID                    string
	WorkRoot                 string
	MaximumSourceBytes       int64
	MaximumStdinBytes        int64
	MaximumTestBundleBytes   int64
	MaximumCases             int
	MaximumCaseBytes         int64
	MaximumTimeLimit         time.Duration
	MaximumMemoryBytes       int64
	MaximumOutputBytes       int64
	CompileTimeout           time.Duration
	CompileMemoryBytes       int64
	CompileOutputBytes       int64
	MaximumCompiledBytes     int64
	PIDsLimit                int
	CPUs                     float64
	TemporaryFilesystemBytes int64
}

func DefaultConfig(jobID, workRoot string) Config {
	return Config{
		JobID: jobID, WorkRoot: workRoot,
		MaximumSourceBytes: 1 << 20, MaximumStdinBytes: 1 << 20,
		MaximumTestBundleBytes: 256 << 20, MaximumCases: 1024,
		MaximumCaseBytes: 16 << 20, MaximumTimeLimit: 120 * time.Second,
		MaximumMemoryBytes: 2 << 30, MaximumOutputBytes: 16 << 20,
		CompileTimeout: 30 * time.Second, CompileMemoryBytes: 1 << 30,
		CompileOutputBytes: 1 << 20, MaximumCompiledBytes: 256 << 20,
		PIDsLimit: 64, CPUs: 1, TemporaryFilesystemBytes: 64 << 20,
	}
}

type Runner struct {
	engine ContainerEngine
	config Config
}

const cpp20Compiler = "/usr/local/bin/g++"

type Failure struct {
	Code      string
	Permanent bool
	Detail    string
}

func (failure *Failure) Error() string {
	if failure == nil {
		return "<nil>"
	}
	return failure.Code + ": " + failure.Detail
}

type materializedRequest struct {
	header     judgeprotocol.RequestHeader
	sourcePath string
	stdinPath  string
	cases      []testCase
}

type executionCaseManifest struct {
	ID      string                `json:"id"`
	TimeMS  int64                 `json:"timeMs"`
	Verdict judgecontract.Verdict `json:"verdict"`
}

type executionManifest struct {
	Cases          []executionCaseManifest      `json:"cases"`
	Checker        judgecontract.Checker        `json:"checker"`
	ContainerImage string                       `json:"containerImage"`
	Mode           judgecontract.SubmissionMode `json:"mode"`
	Schema         string                       `json:"schema"`
}

func New(engine ContainerEngine, config Config) (*Runner, error) {
	if engine == nil || !validConfig(config) || !imageDigestPattern.MatchString(engine.Identity()) {
		return nil, errors.New("container engine and bounded judge runner configuration are required")
	}
	return &Runner{engine: engine, config: config}, nil
}

func (runner *Runner) execute(ctx context.Context, request materializedRequest) (judgeprotocol.Result, []byte, error) {
	if ctx == nil {
		return judgeprotocol.Result{}, nil, &Failure{Code: "invalid_execution_request", Permanent: true, Detail: "execution context is required"}
	}
	if err := runner.validateRequest(request); err != nil {
		return judgeprotocol.Result{}, nil, &Failure{Code: "invalid_execution_request", Permanent: true, Detail: err.Error()}
	}
	spec, err := judgecontract.ParseProblemSpec(request.header.ProblemSpec)
	if err != nil {
		return judgeprotocol.Result{}, nil, &Failure{Code: "invalid_problem_spec", Permanent: true, Detail: err.Error()}
	}
	programPath, compileResult, compileDiagnostics, err := runner.compile(ctx, request)
	if err != nil {
		return judgeprotocol.Result{}, nil, err
	}
	if compileResult != nil {
		manifest, manifestErr := runner.manifest(spec.Checker, request.header.Mode, nil)
		if manifestErr != nil {
			return judgeprotocol.Result{}, nil, manifestErr
		}
		compileResult.ResultManifest = manifest
		return *compileResult, compileDiagnostics, nil
	}
	if request.header.Mode == judgecontract.SubmissionRun {
		return runner.runCustom(ctx, request, spec, programPath)
	}
	return runner.runCases(ctx, request, spec, programPath)
}

func (runner *Runner) compile(ctx context.Context, request materializedRequest) (string, *judgeprotocol.Result, []byte, error) {
	workspace := filepath.Dir(request.sourcePath)
	runtimeRoot, err := runner.ensureRuntimeRoot()
	if err != nil {
		return "", nil, nil, err
	}
	outputLimit := min(request.header.OutputLimitBytes, runner.config.CompileOutputBytes)
	containerResult, err := runner.engine.Run(ctx, ContainerCommand{
		Name: containerName(request.header.JudgeJobID, "compile"), Workspace: workspace,
		RuntimeRoot: runtimeRoot,
		Executable:  cpp20Compiler, Arguments: []string{
			"-std=c++20", "-O2", "-pipe", "-fdiagnostics-color=never", "-fno-ident",
			"-o", "/workspace/program", "/workspace/main.cpp",
		},
		Timeout: runner.config.CompileTimeout, MemoryLimitBytes: runner.config.CompileMemoryBytes,
		OutputLimitBytes: outputLimit, PIDsLimit: runner.config.PIDsLimit, CPUs: runner.config.CPUs,
		TemporaryLimitBytes: runner.config.TemporaryFilesystemBytes,
	})
	if err != nil {
		return "", nil, nil, &Failure{Code: "sandbox_unavailable", Detail: "container runtime could not supervise the compiler"}
	}
	if context.Cause(ctx) != nil {
		return "", nil, nil, context.Cause(ctx)
	}
	diagnostics := joinOutput(containerResult.Stdout, containerResult.Stderr, outputLimit)
	if containerResult.OutputLimitExceeded || containerResult.TimedOut || containerResult.ExitCode != 0 {
		return "", &judgeprotocol.Result{Schema: judgeprotocol.ResultSchemaV1, Verdict: judgecontract.VerdictCompileError}, diagnostics, nil
	}
	compiled := filepath.Join(workspace, "program")
	info, err := os.Lstat(compiled)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() < 1 || info.Size() > runner.config.MaximumCompiledBytes {
		return "", nil, nil, &Failure{Code: "compiler_contract", Permanent: true, Detail: "compiler did not produce one bounded executable"}
	}
	runRoot := filepath.Join(runner.config.WorkRoot, "run")
	if err := os.Mkdir(runRoot, 0o700); err != nil {
		return "", nil, nil, &Failure{Code: "workspace_failure", Detail: "could not create run workspace"}
	}
	destination := filepath.Join(runRoot, "program")
	if err := copyExecutable(compiled, destination, runner.config.MaximumCompiledBytes); err != nil {
		return "", nil, nil, &Failure{Code: "workspace_failure", Detail: "could not materialize compiled program"}
	}
	return destination, nil, nil, nil
}

func (runner *Runner) runCustom(ctx context.Context, request materializedRequest, spec judgecontract.ProblemSpec, programPath string) (judgeprotocol.Result, []byte, error) {
	stdin, err := os.Open(request.stdinPath)
	if err != nil {
		return judgeprotocol.Result{}, nil, &Failure{Code: "workspace_failure", Detail: "could not open custom input"}
	}
	defer stdin.Close()
	result, err := runner.runProgram(ctx, request, programPath, stdin, "run")
	if err != nil {
		return judgeprotocol.Result{}, nil, err
	}
	verdict := verdictForContainer(result)
	caseManifest := []executionCaseManifest{{ID: "run", Verdict: verdict, TimeMS: durationMilliseconds(result.Duration)}}
	manifest, err := runner.manifest(spec.Checker, request.header.Mode, caseManifest)
	if err != nil {
		return judgeprotocol.Result{}, nil, err
	}
	passed := int64(0)
	if verdict == judgecontract.VerdictAccepted {
		passed = 1
	}
	return judgeprotocol.Result{
		Schema: judgeprotocol.ResultSchemaV1, Verdict: verdict,
		ScoreFraction: float64(passed), PassedCaseCount: passed, TotalCaseCount: 1,
		MaxTimeMS: durationMilliseconds(result.Duration), MaxMemoryBytes: 0, ResultManifest: manifest,
	}, joinOutput(result.Stdout, result.Stderr, request.header.OutputLimitBytes), nil
}

func (runner *Runner) runCases(ctx context.Context, request materializedRequest, spec judgecontract.ProblemSpec, programPath string) (judgeprotocol.Result, []byte, error) {
	weightTotal := int64(0)
	for _, item := range request.cases {
		weightTotal += item.Weight
	}
	passedWeight, passedCases := int64(0), int64(0)
	verdict := judgecontract.VerdictAccepted
	maxTime := int64(0)
	caseResults := make([]executionCaseManifest, 0, len(request.cases))
	var output []byte
	for _, item := range request.cases {
		input, err := os.Open(item.InputPath)
		if err != nil {
			return judgeprotocol.Result{}, nil, &Failure{Code: "workspace_failure", Detail: "could not open test case input"}
		}
		containerResult, executeErr := runner.runProgram(ctx, request, programPath, input, item.ID)
		closeErr := input.Close()
		if executeErr != nil {
			return judgeprotocol.Result{}, nil, executeErr
		}
		if closeErr != nil {
			return judgeprotocol.Result{}, nil, &Failure{Code: "workspace_failure", Detail: "could not close test case input"}
		}
		caseVerdict := verdictForContainer(containerResult)
		if caseVerdict == judgecontract.VerdictAccepted {
			expected, readErr := os.ReadFile(item.ExpectedPath)
			if readErr != nil {
				return judgeprotocol.Result{}, nil, &Failure{Code: "workspace_failure", Detail: "could not read expected output"}
			}
			if !judgecontract.CompareOutput(spec.Checker, containerResult.Stdout, expected) {
				caseVerdict = judgecontract.VerdictWrongAnswer
			}
		}
		elapsed := durationMilliseconds(containerResult.Duration)
		maxTime = max(maxTime, elapsed)
		caseResults = append(caseResults, executionCaseManifest{ID: item.ID, Verdict: caseVerdict, TimeMS: elapsed})
		if caseVerdict == judgecontract.VerdictAccepted {
			passedCases++
			passedWeight += item.Weight
			continue
		}
		verdict = caseVerdict
		output = []byte("case " + item.ID + ": " + string(caseVerdict) + "\n")
		break
	}
	manifest, err := runner.manifest(spec.Checker, request.header.Mode, caseResults)
	if err != nil {
		return judgeprotocol.Result{}, nil, err
	}
	return judgeprotocol.Result{
		Schema: judgeprotocol.ResultSchemaV1, Verdict: verdict,
		ScoreFraction:   float64(passedWeight) / float64(weightTotal),
		PassedCaseCount: passedCases, TotalCaseCount: int64(len(request.cases)),
		MaxTimeMS: maxTime, MaxMemoryBytes: 0, ResultManifest: manifest,
	}, output, nil
}

func (runner *Runner) runProgram(ctx context.Context, request materializedRequest, programPath string, stdin io.Reader, phase string) (ContainerResult, error) {
	runtimeRoot, err := runner.ensureRuntimeRoot()
	if err != nil {
		return ContainerResult{}, err
	}
	result, err := runner.engine.Run(ctx, ContainerCommand{
		Name: containerName(request.header.JudgeJobID, phase), Workspace: filepath.Dir(programPath),
		RuntimeRoot:       runtimeRoot,
		ReadOnlyWorkspace: true, Executable: "/workspace/program", Stdin: stdin,
		Timeout:          time.Duration(request.header.TimeLimitMS) * time.Millisecond,
		MemoryLimitBytes: request.header.MemoryLimitBytes,
		OutputLimitBytes: request.header.OutputLimitBytes, PIDsLimit: runner.config.PIDsLimit,
		CPUs: runner.config.CPUs, TemporaryLimitBytes: runner.config.TemporaryFilesystemBytes,
	})
	if err != nil {
		return ContainerResult{}, &Failure{Code: "sandbox_unavailable", Detail: "container runtime could not supervise the program"}
	}
	if context.Cause(ctx) != nil {
		return ContainerResult{}, context.Cause(ctx)
	}
	return result, nil
}

func (runner *Runner) ensureRuntimeRoot() (string, error) {
	path := filepath.Join(runner.config.WorkRoot, "podman-runroot")
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", &Failure{Code: "workspace_failure", Detail: "could not create Podman runtime root"}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", &Failure{Code: "workspace_failure", Detail: "Podman runtime root violates its mode contract"}
	}
	return path, nil
}

func (runner *Runner) manifest(checker judgecontract.Checker, mode judgecontract.SubmissionMode, cases []executionCaseManifest) (json.RawMessage, error) {
	if cases == nil {
		cases = make([]executionCaseManifest, 0)
	}
	raw, err := json.Marshal(executionManifest{
		Cases: cases, Checker: checker, ContainerImage: runner.engine.Identity(),
		Mode: mode, Schema: judgecontract.ExecutionManifestSchemaV1,
	})
	if err != nil {
		return nil, &Failure{Code: "executor_result_contract", Permanent: true, Detail: "could not encode execution manifest"}
	}
	canonical, _, err := canonicaljson.Object(raw, maximumManifestBytes)
	if err != nil {
		return nil, &Failure{Code: "executor_result_contract", Permanent: true, Detail: "execution manifest violates its hard limit"}
	}
	return canonical, nil
}

func (runner *Runner) validateRequest(request materializedRequest) error {
	header := request.header
	if err := runner.validateHeader(header); err != nil {
		return err
	}
	if request.sourcePath == "" || len(request.cases) < 1 || len(request.cases) > runner.config.MaximumCases ||
		(request.header.Mode == judgecontract.SubmissionRun && request.stdinPath == "") ||
		(request.header.Mode == judgecontract.SubmissionSubmit && request.stdinPath != "") {
		return errors.New("materialized source or test cases are invalid")
	}
	return nil
}

func (runner *Runner) validateHeader(header judgeprotocol.RequestHeader) error {
	if header.Schema != judgeprotocol.RequestSchemaV1 || header.JudgeJobID != runner.config.JobID ||
		!judgecontract.ValidPublicID(header.JudgeJobID) || !judgecontract.ValidPublicID(header.SubmissionID) ||
		!judgecontract.ValidPublicID(header.ProblemID) || header.ProblemVersion < 1 || header.LanguageID != judgecontract.LanguageCPP20 ||
		header.ProblemSchema != judgecontract.ProblemSchemaV1 || header.TimeLimitMS < 1 ||
		time.Duration(header.TimeLimitMS)*time.Millisecond > runner.config.MaximumTimeLimit ||
		header.MemoryLimitBytes < 1 || header.MemoryLimitBytes > runner.config.MaximumMemoryBytes ||
		header.OutputLimitBytes < 1 || header.OutputLimitBytes > runner.config.MaximumOutputBytes {
		return errors.New("request identity, schema, language, or resource policy is invalid")
	}
	if header.Source.SizeBytes < 1 || header.Source.SizeBytes > runner.config.MaximumSourceBytes ||
		header.TestBundle.SizeBytes < 1 || header.TestBundle.SizeBytes > runner.config.MaximumTestBundleBytes {
		return errors.New("request artifact policy is invalid")
	}
	if header.Mode == judgecontract.SubmissionRun {
		if header.Stdin == nil || header.Stdin.SizeBytes < 1 || header.Stdin.SizeBytes > runner.config.MaximumStdinBytes {
			return errors.New("run request requires one bounded stdin payload")
		}
	} else if header.Mode != judgecontract.SubmissionSubmit || header.Stdin != nil {
		return errors.New("submission mode and stdin contract are inconsistent")
	}
	return nil
}

func validConfig(config Config) bool {
	return judgecontract.ValidPublicID(config.JobID) && config.WorkRoot != "" && filepath.IsAbs(config.WorkRoot) &&
		filepath.Clean(config.WorkRoot) == config.WorkRoot && filepath.Base(config.WorkRoot) == config.JobID &&
		config.MaximumSourceBytes >= 1 && config.MaximumSourceBytes <= 16<<20 &&
		config.MaximumStdinBytes >= 1 && config.MaximumStdinBytes <= 16<<20 &&
		config.MaximumTestBundleBytes >= 1 && config.MaximumTestBundleBytes <= 1<<30 &&
		config.MaximumCases >= 1 && config.MaximumCases <= 10000 &&
		config.MaximumCaseBytes >= 1 && config.MaximumCaseBytes <= 1<<30 &&
		config.MaximumTimeLimit >= time.Millisecond && config.MaximumTimeLimit <= time.Hour &&
		config.MaximumMemoryBytes >= 16<<20 && config.MaximumMemoryBytes <= 64<<30 &&
		config.MaximumOutputBytes >= 1 && config.MaximumOutputBytes <= 1<<30 &&
		config.CompileTimeout >= time.Second && config.CompileTimeout <= 10*time.Minute &&
		config.CompileMemoryBytes >= 16<<20 && config.CompileMemoryBytes <= 64<<30 &&
		config.CompileOutputBytes >= 1 && config.CompileOutputBytes <= config.MaximumOutputBytes &&
		config.MaximumCompiledBytes >= 1 && config.MaximumCompiledBytes <= 1<<30 &&
		config.PIDsLimit >= 1 && config.PIDsLimit <= 4096 && config.CPUs > 0 && config.CPUs <= 64 &&
		config.TemporaryFilesystemBytes >= 1<<20 && config.TemporaryFilesystemBytes <= 4<<30
}

func verdictForContainer(result ContainerResult) judgecontract.Verdict {
	if result.OutputLimitExceeded {
		return judgecontract.VerdictOutputLimitExceeded
	}
	if result.TimedOut {
		return judgecontract.VerdictTimeLimitExceeded
	}
	if result.ExitCode == 137 {
		return judgecontract.VerdictMemoryLimitExceeded
	}
	if result.ExitCode != 0 {
		return judgecontract.VerdictRuntimeError
	}
	return judgecontract.VerdictAccepted
}

func containerName(jobID, phase string) string {
	phase = strings.ReplaceAll(phase, "_", "-")
	if len(phase) > 32 {
		phase = phase[:32]
	}
	return "ascendany-" + jobID + "-" + phase
}

func durationMilliseconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return max(int64(1), value.Milliseconds())
}

func joinOutput(stdout, stderr []byte, maximum int64) []byte {
	if maximum < 1 {
		return nil
	}
	capacity := len(stdout) + len(stderr) + 1
	if int64(capacity) > maximum {
		capacity = int(maximum)
	}
	value := make([]byte, 0, capacity)
	value = append(value, stdout...)
	if len(stderr) > 0 {
		if len(value) > 0 {
			value = append(value, '\n')
		}
		value = append(value, stderr...)
	}
	if int64(len(value)) > maximum {
		value = value[:maximum]
	}
	return value
}

func copyExecutable(sourcePath, destinationPath string, maximumBytes int64) (resultErr error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destination.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	written, err := io.Copy(destination, io.LimitReader(source, maximumBytes+1))
	if err != nil || written < 1 || written > maximumBytes {
		return errors.New("compiled program copy exceeds the hard limit")
	}
	return destination.Sync()
}
