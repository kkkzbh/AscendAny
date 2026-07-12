package judgeexecutor

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

type runtimeRepository struct{}

func (runtimeRepository) ClaimJudge(context.Context, string, string, time.Duration) (*oj.JudgeClaim, error) {
	panic("unused")
}
func (runtimeRepository) RenewJudgeLease(context.Context, oj.JudgeClaim, time.Duration) error {
	panic("unused")
}
func (runtimeRepository) LoadExecution(context.Context, oj.JudgeClaim) (judgecontract.ExecutionRequest, error) {
	panic("unused")
}
func (runtimeRepository) CompleteJudge(context.Context, oj.CompleteJudgeCommand) (oj.JudgeResult, error) {
	panic("unused")
}
func (runtimeRepository) RequeueJudge(context.Context, oj.JudgeClaim, time.Duration, string) error {
	panic("unused")
}
func (runtimeRepository) FailJudge(context.Context, oj.JudgeClaim, string, string) error {
	panic("unused")
}

type runtimeArtifactStore struct{}

func (runtimeArtifactStore) Verify(context.Context, string, int64) (artifactstore.Artifact, error) {
	panic("unused")
}

func (runtimeArtifactStore) Publish(context.Context, io.Reader) (*artifactstore.Publication, error) {
	panic("unused")
}

type runtimeLauncher struct{}

func (runtimeLauncher) Start(context.Context, string) error { panic("unused") }
func (runtimeLauncher) Stop(context.Context, string) error  { panic("unused") }

func TestRuntimeConstructsExactJudgePipeline(t *testing.T) {
	t.Parallel()

	runtime, err := NewRuntime(
		runtimeRepository{},
		runtimeArtifactStore{},
		runtimeLauncher{},
		validRuntimeConfig(t.TempDir()),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if runtime == nil || runtime.supervisor == nil {
		t.Fatal("NewRuntime() returned an incomplete runtime")
	}
}

func TestRuntimeRejectsMissingCapabilitiesAndInvalidProductionLauncher(t *testing.T) {
	t.Parallel()

	configuration := validRuntimeConfig(t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewRuntime(nil, runtimeArtifactStore{}, runtimeLauncher{}, configuration, logger); err == nil {
		t.Fatal("nil repository accepted")
	}
	if _, err := NewRuntime(runtimeRepository{}, nil, runtimeLauncher{}, configuration, logger); err == nil {
		t.Fatal("nil artifact store accepted")
	}
	if _, err := NewRuntime(runtimeRepository{}, runtimeArtifactStore{}, nil, configuration, logger); err == nil {
		t.Fatal("nil launcher accepted")
	}
	if _, err := NewRuntime(runtimeRepository{}, runtimeArtifactStore{}, runtimeLauncher{}, configuration, nil); err == nil {
		t.Fatal("nil logger accepted")
	}
	configuration.SystemctlPath = "/definitely/missing/systemctl"
	if _, err := NewProductionRuntime(runtimeRepository{}, runtimeArtifactStore{}, configuration, logger); err == nil ||
		strings.Contains(err.Error(), configuration.SystemctlPath) {
		t.Fatalf("NewProductionRuntime() error = %v", err)
	}
	if err := (*Runtime)(nil).Run(context.Background()); err == nil {
		t.Fatal("nil runtime accepted")
	}
}

func validRuntimeConfig(socketDirectory string) RuntimeConfig {
	return RuntimeConfig{
		SystemctlPath: "/usr/bin/systemctl",
		Executor: Config{
			SocketDirectory:  socketDirectory,
			ExpectedJudgeUID: 991,
			StartupTimeout:   time.Second,
			SessionTimeout:   time.Second,
			StopTimeout:      time.Second,
			Policy:           oj.DefaultPolicy(),
		},
		Worker: oj.WorkerConfig{
			Owner: "judge-runtime-test", LeaseDuration: time.Second,
			RetryDelay: time.Second, MaximumAttempts: 3,
		},
		Supervisor: SupervisorConfig{PollInterval: 10 * time.Millisecond},
	}
}
