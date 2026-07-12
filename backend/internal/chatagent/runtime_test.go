package chatagent

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type runtimeCredentialResolver struct{}

func (runtimeCredentialResolver) Resolve(context.Context, string, string) (string, error) {
	return "token", nil
}

type inertRuntimeDatabase struct{}

func (inertRuntimeDatabase) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	panic("BeginTx must not be called during runtime construction")
}

type runtimeWorkerStub struct {
	mu       sync.Mutex
	calls    int
	outcomes []*WorkerOutcome
}

func (worker *runtimeWorkerStub) RunOne(context.Context) (*WorkerOutcome, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	index := worker.calls
	worker.calls++
	if index < len(worker.outcomes) {
		return worker.outcomes[index], nil
	}
	return nil, nil
}

func (worker *runtimeWorkerStub) count() int {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.calls
}

func TestNewRuntimeBuildsEveryProductionComponentWithoutDatabaseIO(t *testing.T) {
	t.Parallel()
	repository, err := NewPostgresRepository(inertRuntimeDatabase{})
	if err != nil {
		t.Fatal(err)
	}
	components, err := NewRuntime(repository, runtimeCredentialResolver{}, RuntimeConfig{
		WorkerOwner: "km6-agent", LeaseDuration: time.Minute, PollInterval: time.Second,
		MaximumContextItems: 100, MaximumToolRounds: 8,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if components.Repository == nil || components.Provider == nil || components.Tools == nil || components.Worker == nil || components.Supervisor == nil {
		t.Fatalf("components=%#v", components)
	}
	if components.Repository != repository {
		t.Fatal("runtime replaced its repository owner")
	}
}

func TestRuntimeSupervisorContinuesAfterWorkAndPollsWhenIdle(t *testing.T) {
	worker := &runtimeWorkerStub{outcomes: []*WorkerOutcome{{RunID: testRunID, Disposition: WorkerSucceeded}}}
	supervisor, err := NewRuntimeSupervisor(worker, RuntimeSupervisorConfig{PollInterval: 10 * time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	deadline := time.After(time.Second)
	for worker.count() < 3 {
		select {
		case <-deadline:
			t.Fatalf("calls=%d", worker.count())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSupervisorRejectsInvalidConfigurationAndNilContext(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewRuntimeSupervisor(nil, RuntimeSupervisorConfig{PollInterval: time.Second}, logger); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil worker error=%v", err)
	}
	if _, err := NewRuntimeSupervisor(&runtimeWorkerStub{}, RuntimeSupervisorConfig{PollInterval: time.Millisecond}, logger); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("poll error=%v", err)
	}
	supervisor, _ := NewRuntimeSupervisor(&runtimeWorkerStub{}, RuntimeSupervisorConfig{PollInterval: time.Second}, logger)
	if err := supervisor.Run(nil); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("nil context error=%v", err)
	}
}
