package workerlease

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MinimumDuration keeps the renewal interval large enough for time.Timer to be
// meaningful while still permitting deterministic short-duration tests.
const MinimumDuration = 300 * time.Millisecond

type Renew func(context.Context) error

// Renewer owns one cancellable processing context and one renewal goroutine.
// A renewal failure is recorded once, becomes the processing context's cause,
// and cannot be hidden by a later orderly stop.
type Renewer struct {
	parent context.Context
	ctx    context.Context
	cancel context.CancelCauseFunc
	renew  Renew

	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	mu       sync.RWMutex
	stopping bool
	failure  error
}

func ValidateDuration(duration time.Duration) (time.Duration, error) {
	if duration < MinimumDuration {
		return 0, errors.New("lease duration must be at least 300 milliseconds")
	}
	interval := duration / 3
	if interval < time.Millisecond {
		return 0, errors.New("lease renewal interval must be at least one millisecond")
	}
	return interval, nil
}

// Start renews synchronously before starting background processing. This
// rejects a stale claim before any expensive work begins and gives the worker
// a full lease window for its first processing step.
func Start(parent context.Context, duration time.Duration, renew Renew) (*Renewer, error) {
	if parent == nil {
		return nil, errors.New("lease parent context is required")
	}
	if renew == nil {
		return nil, errors.New("lease renewal function is required")
	}
	interval, err := ValidateDuration(duration)
	if err != nil {
		return nil, err
	}
	if err := renewWithin(parent, interval, renew); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	renewer := &Renewer{
		parent:   parent,
		ctx:      ctx,
		cancel:   cancel,
		renew:    renew,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go renewer.run()
	return renewer, nil
}

func (renewer *Renewer) Context() context.Context {
	return renewer.ctx
}

// Failure returns only a background renewal failure. Parent cancellation and
// an orderly Stop remain available through Context without being misreported
// as a lease-store failure.
func (renewer *Renewer) Failure() error {
	renewer.mu.RLock()
	defer renewer.mu.RUnlock()
	return renewer.failure
}

// Stop cancels any in-flight renewal and waits for the goroutine to exit.
func (renewer *Renewer) Stop() {
	renewer.stopOnce.Do(func() {
		renewer.mu.Lock()
		renewer.stopping = true
		renewer.mu.Unlock()
		close(renewer.stop)
		renewer.cancel(context.Canceled)
	})
	<-renewer.done
}

func (renewer *Renewer) run() {
	defer close(renewer.done)
	ticker := time.NewTicker(renewer.interval)
	defer ticker.Stop()
	for {
		select {
		case <-renewer.stop:
			return
		case <-renewer.parent.Done():
			return
		case <-ticker.C:
			if err := renewWithin(renewer.ctx, renewer.interval, renewer.renew); err != nil {
				if renewer.recordFailure(err) {
					renewer.cancel(err)
				}
				return
			}
		}
	}
}

func renewWithin(parent context.Context, timeout time.Duration, renew Renew) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return renew(ctx)
}

func (renewer *Renewer) recordFailure(err error) bool {
	renewer.mu.Lock()
	defer renewer.mu.Unlock()
	if renewer.stopping || renewer.parent.Err() != nil {
		return false
	}
	renewer.failure = err
	return true
}
