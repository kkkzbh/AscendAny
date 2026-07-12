package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
)

type importStub struct {
	mu       sync.Mutex
	calls    int
	owners   []string
	outcomes []*importing.ImportOutcome
	errors   []error
}

func (worker *importStub) ClaimAndProcess(_ context.Context, owner string) (*importing.ImportOutcome, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	index := worker.calls
	worker.calls++
	worker.owners = append(worker.owners, owner)
	var outcome *importing.ImportOutcome
	if index < len(worker.outcomes) {
		outcome = worker.outcomes[index]
	}
	var err error
	if index < len(worker.errors) {
		err = worker.errors[index]
	}
	return outcome, err
}

func (worker *importStub) count() int {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.calls
}

type analyticsStub struct {
	mu       sync.Mutex
	calls    int
	outcomes []*analytics.RunOutcome
	errors   []error
}

type feedbackStub struct {
	mu       sync.Mutex
	calls    int
	outcomes []*feedback.DeliveryOutcome
	errors   []error
}

func (worker *feedbackStub) RunOne(context.Context) (*feedback.DeliveryOutcome, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	index := worker.calls
	worker.calls++
	var outcome *feedback.DeliveryOutcome
	if index < len(worker.outcomes) {
		outcome = worker.outcomes[index]
	}
	var err error
	if index < len(worker.errors) {
		err = worker.errors[index]
	}
	return outcome, err
}

func (worker *feedbackStub) count() int {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.calls
}

type reconcileStub struct {
	mu     sync.Mutex
	calls  int
	errors []error
}

func (reconciler *reconcileStub) Reconcile(context.Context) error {
	reconciler.mu.Lock()
	defer reconciler.mu.Unlock()
	index := reconciler.calls
	reconciler.calls++
	if index < len(reconciler.errors) {
		return reconciler.errors[index]
	}
	return nil
}

func (reconciler *reconcileStub) count() int {
	reconciler.mu.Lock()
	defer reconciler.mu.Unlock()
	return reconciler.calls
}

func (worker *analyticsStub) RunOne(context.Context) (*analytics.RunOutcome, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	index := worker.calls
	worker.calls++
	var outcome *analytics.RunOutcome
	if index < len(worker.outcomes) {
		outcome = worker.outcomes[index]
	}
	var err error
	if index < len(worker.errors) {
		err = worker.errors[index]
	}
	return outcome, err
}

func (worker *analyticsStub) count() int {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.calls
}

func TestNewRejectsIncompleteConfiguration(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	valid := Config{
		ImportOwner: "worker", ImportPollInterval: time.Millisecond,
		AnalyticsPollInterval: time.Millisecond, FeedbackPollInterval: time.Millisecond,
		ArtifactReconcileInterval: time.Millisecond,
	}
	imports := &importStub{}
	analyticsWorker := &analyticsStub{}
	feedbackWorker := &feedbackStub{}
	artifacts := &reconcileStub{}

	tests := []struct {
		name      string
		imports   ImportWorker
		analytics AnalyticsWorker
		feedback  FeedbackWorker
		artifacts ArtifactReconciler
		config    Config
		logger    *slog.Logger
	}{
		{name: "imports", analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: valid, logger: logger},
		{name: "analytics", imports: imports, feedback: feedbackWorker, artifacts: artifacts, config: valid, logger: logger},
		{name: "feedback", imports: imports, analytics: analyticsWorker, artifacts: artifacts, config: valid, logger: logger},
		{name: "artifacts", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, config: valid, logger: logger},
		{name: "owner", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: Config{ImportPollInterval: time.Millisecond, AnalyticsPollInterval: time.Millisecond, FeedbackPollInterval: time.Millisecond, ArtifactReconcileInterval: time.Millisecond}, logger: logger},
		{name: "import interval", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: Config{ImportOwner: "worker", AnalyticsPollInterval: time.Millisecond, FeedbackPollInterval: time.Millisecond, ArtifactReconcileInterval: time.Millisecond}, logger: logger},
		{name: "analytics interval", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: Config{ImportOwner: "worker", ImportPollInterval: time.Millisecond, FeedbackPollInterval: time.Millisecond, ArtifactReconcileInterval: time.Millisecond}, logger: logger},
		{name: "feedback interval", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: Config{ImportOwner: "worker", ImportPollInterval: time.Millisecond, AnalyticsPollInterval: time.Millisecond, ArtifactReconcileInterval: time.Millisecond}, logger: logger},
		{name: "artifact interval", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: Config{ImportOwner: "worker", ImportPollInterval: time.Millisecond, AnalyticsPollInterval: time.Millisecond, FeedbackPollInterval: time.Millisecond}, logger: logger},
		{name: "logger", imports: imports, analytics: analyticsWorker, feedback: feedbackWorker, artifacts: artifacts, config: valid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.imports, test.analytics, test.feedback, test.artifacts, test.config, test.logger); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestRunPollsIdleAndFailedWorkersUntilCancellation(t *testing.T) {
	t.Parallel()
	imports := &importStub{errors: []error{errors.New("temporary")}}
	analyticsWorker := &analyticsStub{}
	feedbackWorker := &feedbackStub{}
	artifacts := &reconcileStub{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	supervisor, err := New(imports, analyticsWorker, feedbackWorker, artifacts, Config{
		ImportOwner:               "km6-import",
		ImportPollInterval:        time.Millisecond,
		AnalyticsPollInterval:     time.Millisecond,
		FeedbackPollInterval:      time.Millisecond,
		ArtifactReconcileInterval: time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()

	deadline := time.After(500 * time.Millisecond)
	for imports.count() < 2 || analyticsWorker.count() < 2 || feedbackWorker.count() < 2 || artifacts.count() < 2 {
		select {
		case <-deadline:
			t.Fatalf("workers did not poll: imports=%d analytics=%d feedback=%d artifacts=%d", imports.count(), analyticsWorker.count(), feedbackWorker.count(), artifacts.count())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
	imports.mu.Lock()
	defer imports.mu.Unlock()
	for _, owner := range imports.owners {
		if owner != "km6-import" {
			t.Fatalf("owner = %q", owner)
		}
	}
}

func TestRunImmediatelyContinuesAfterCompletedWork(t *testing.T) {
	t.Parallel()
	completedImport := &importing.ImportOutcome{Disposition: importing.ImportCreated}
	completedAnalytics := &analytics.RunOutcome{Disposition: analytics.RunSucceeded}
	imports := &importStub{outcomes: []*importing.ImportOutcome{completedImport}}
	analyticsWorker := &analyticsStub{outcomes: []*analytics.RunOutcome{completedAnalytics}}
	completedFeedback := &feedback.DeliveryOutcome{Disposition: feedback.DeliverySucceeded}
	feedbackWorker := &feedbackStub{outcomes: []*feedback.DeliveryOutcome{completedFeedback}}
	artifacts := &reconcileStub{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	supervisor, err := New(imports, analyticsWorker, feedbackWorker, artifacts, Config{
		ImportOwner:               "worker",
		ImportPollInterval:        time.Hour,
		AnalyticsPollInterval:     time.Hour,
		FeedbackPollInterval:      time.Hour,
		ArtifactReconcileInterval: time.Hour,
	}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()
	deadline := time.After(500 * time.Millisecond)
	for imports.count() < 2 || analyticsWorker.count() < 2 || feedbackWorker.count() < 2 {
		select {
		case <-deadline:
			t.Fatalf("completed work did not trigger an immediate next claim: imports=%d analytics=%d feedback=%d", imports.count(), analyticsWorker.count(), feedbackWorker.count())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop")
	}
}

func TestRunFailsClosedOnArtifactReconciliationError(t *testing.T) {
	t.Parallel()
	imports := &importStub{}
	analyticsWorker := &analyticsStub{}
	feedbackWorker := &feedbackStub{}
	reconcileFailure := errors.New("corrupt artifact layout")
	artifacts := &reconcileStub{errors: []error{reconcileFailure}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	supervisor, err := New(imports, analyticsWorker, feedbackWorker, artifacts, Config{
		ImportOwner:               "worker",
		ImportPollInterval:        time.Hour,
		AnalyticsPollInterval:     time.Hour,
		FeedbackPollInterval:      time.Hour,
		ArtifactReconcileInterval: time.Hour,
	}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = supervisor.Run(context.Background())
	if !errors.Is(err, reconcileFailure) {
		t.Fatalf("Run() error = %v, want %v", err, reconcileFailure)
	}
}
