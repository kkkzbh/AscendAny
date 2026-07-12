package workerlease

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenewerPerformsInitialAndPeriodicRenewal(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	periodic := make(chan struct{})
	renewer, err := Start(context.Background(), 300*time.Millisecond, func(context.Context) error {
		if calls.Add(1) == 2 {
			close(periodic)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer renewer.Stop()

	select {
	case <-periodic:
	case <-time.After(time.Second):
		t.Fatal("periodic renewal did not run")
	}
	if calls.Load() < 2 {
		t.Fatalf("renewal calls = %d, want at least 2", calls.Load())
	}
}

func TestRenewerCancelsProcessingWithRenewalCause(t *testing.T) {
	t.Parallel()

	want := errors.New("lease changed")
	var calls atomic.Int32
	renewer, err := Start(context.Background(), 300*time.Millisecond, func(context.Context) error {
		if calls.Add(1) == 1 {
			return nil
		}
		return want
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer renewer.Stop()

	select {
	case <-renewer.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("processing context was not canceled")
	}
	if !errors.Is(context.Cause(renewer.Context()), want) || !errors.Is(renewer.Failure(), want) {
		t.Fatalf("context cause = %v, failure = %v", context.Cause(renewer.Context()), renewer.Failure())
	}
}

func TestValidateDurationRejectsUnsafeTimerWindow(t *testing.T) {
	t.Parallel()

	if _, err := ValidateDuration(MinimumDuration - time.Nanosecond); err == nil {
		t.Fatal("ValidateDuration() error = nil")
	}
}

func TestStartBoundsInitialRenewal(t *testing.T) {
	t.Parallel()

	started := time.Now()
	_, err := Start(context.Background(), 300*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("initial renewal elapsed = %s", elapsed)
	}
}
