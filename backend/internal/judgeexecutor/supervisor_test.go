package judgeexecutor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

type workerStub struct {
	mu       sync.Mutex
	calls    int
	outcomes []*oj.JudgeOutcome
	errors   []error
}

func (worker *workerStub) RunOne(context.Context) (*oj.JudgeOutcome, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	index := worker.calls
	worker.calls++
	var outcome *oj.JudgeOutcome
	if index < len(worker.outcomes) {
		outcome = worker.outcomes[index]
	}
	var err error
	if index < len(worker.errors) {
		err = worker.errors[index]
	}
	return outcome, err
}

func (worker *workerStub) count() int {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.calls
}

func TestSupervisorContinuesImmediatelyAfterWorkAndPollsIdle(t *testing.T) {
	worker := &workerStub{outcomes: []*oj.JudgeOutcome{{JobID: "job", Disposition: "completed"}}}
	supervisor, err := NewSupervisor(worker, SupervisorConfig{PollInterval: 10 * time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
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
			t.Fatalf("worker calls = %d", worker.count())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorValidatesDependenciesAndStopsOnCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewSupervisor(nil, SupervisorConfig{PollInterval: time.Second}, logger); err == nil {
		t.Fatal("NewSupervisor(nil) error = nil")
	}
	if _, err := NewSupervisor(&workerStub{}, SupervisorConfig{PollInterval: time.Millisecond}, logger); err == nil {
		t.Fatal("NewSupervisor(short interval) error = nil")
	}
	worker := &workerStub{errors: []error{errors.New("temporary")}}
	supervisor, err := NewSupervisor(worker, SupervisorConfig{PollInterval: time.Second}, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := supervisor.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
