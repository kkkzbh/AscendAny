package traineragent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type workRunnerFunc func(context.Context) (*WorkerOutcome, error)

func (function workRunnerFunc) RunOne(ctx context.Context) (*WorkerOutcome, error) {
	return function(ctx)
}

type acceptanceRecorderFunc func(WorkerOutcome) error

func (function acceptanceRecorderFunc) Record(outcome WorkerOutcome) error {
	return function(outcome)
}

func TestSupervisorDrainsAvailableWorkWithoutPollDelay(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mutex sync.Mutex
	calls := 0
	recorded := 0
	supervisor, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
		mutex.Lock()
		defer mutex.Unlock()
		calls++
		if calls <= 2 {
			return &WorkerOutcome{RunID: testRunID, Disposition: WorkerActivated}, nil
		}
		cancel()
		return nil, nil
	}), acceptanceRecorderFunc(func(outcome WorkerOutcome) error {
		if outcome.Disposition != WorkerActivated {
			t.Fatalf("recorded outcome = %#v", outcome)
		}
		recorded++
		return nil
	}), SupervisorConfig{PollInterval: time.Second}, discardTrainerAgentLogger())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := supervisor.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("available work was poll-delayed for %s", elapsed)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if calls != 3 {
		t.Fatalf("worker calls = %d", calls)
	}
	if recorded != 2 {
		t.Fatalf("recorded candidates = %d", recorded)
	}
}

func TestSupervisorPollsAfterEmptyClaimAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan time.Time, 1)
	second := make(chan time.Time, 1)
	calls := 0
	supervisor, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
		calls++
		switch calls {
		case 1:
			first <- time.Now()
		case 2:
			second <- time.Now()
			cancel()
		}
		return nil, nil
	}), discardAcceptanceRecorder(), SupervisorConfig{PollInterval: 60 * time.Millisecond}, discardTrainerAgentLogger())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	firstAt := <-first
	select {
	case secondAt := <-second:
		if elapsed := secondAt.Sub(firstAt); elapsed < 45*time.Millisecond {
			t.Fatalf("empty claim retried after %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not poll after an empty claim")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop after cancellation")
	}
}

func TestNewSupervisorRejectsIncompleteDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewSupervisor(nil, discardAcceptanceRecorder(), SupervisorConfig{PollInterval: time.Second}, discardTrainerAgentLogger()); err == nil {
		t.Fatal("nil worker error = nil")
	}
	if _, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
		return nil, nil
	}), nil, SupervisorConfig{PollInterval: time.Second}, discardTrainerAgentLogger()); err == nil {
		t.Fatal("nil acceptance recorder error = nil")
	}
	if _, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
		return nil, nil
	}), discardAcceptanceRecorder(), SupervisorConfig{PollInterval: time.Millisecond}, discardTrainerAgentLogger()); err == nil {
		t.Fatal("short poll error = nil")
	}
	if _, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
		return nil, nil
	}), discardAcceptanceRecorder(), SupervisorConfig{PollInterval: time.Second}, nil); err == nil {
		t.Fatal("nil logger error = nil")
	}
}

func TestSupervisorRecordsOnlySuccessfulPublications(t *testing.T) {
	t.Parallel()
	for name, result := range map[string]struct {
		outcome *WorkerOutcome
		err     error
	}{
		"failed":   {outcome: &WorkerOutcome{RunID: testRunID, Disposition: WorkerFailed}},
		"requeued": {outcome: &WorkerOutcome{RunID: testRunID, Disposition: WorkerRequeued}},
		"error":    {err: errors.New("transport failed")},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			recorded := 0
			supervisor, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
				cancel()
				return result.outcome, result.err
			}), acceptanceRecorderFunc(func(WorkerOutcome) error {
				recorded++
				return nil
			}), SupervisorConfig{PollInterval: time.Second}, discardTrainerAgentLogger())
			if err != nil {
				t.Fatal(err)
			}
			if err := supervisor.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if recorded != 0 {
				t.Fatalf("recorded candidates = %d", recorded)
			}
		})
	}
}

func TestSupervisorStopsWhenAcceptanceCandidateCannotBeRecorded(t *testing.T) {
	t.Parallel()
	recordFailure := errors.New("candidate write failed")
	supervisor, err := NewSupervisor(workRunnerFunc(func(context.Context) (*WorkerOutcome, error) {
		return &WorkerOutcome{RunID: testRunID, Disposition: WorkerSuperseded}, nil
	}), acceptanceRecorderFunc(func(WorkerOutcome) error {
		return recordFailure
	}), SupervisorConfig{PollInterval: time.Second}, discardTrainerAgentLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(context.Background()); !errors.Is(err, recordFailure) {
		t.Fatalf("supervisor error = %v", err)
	}
}

func discardTrainerAgentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func discardAcceptanceRecorder() AcceptanceCandidateRecorder {
	return acceptanceRecorderFunc(func(WorkerOutcome) error { return nil })
}
