package analytics

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type workerRepositoryStub struct {
	claim         *Claim
	claimErr      error
	item          WorkItem
	loadErr       error
	publish       PublishResult
	publishErr    error
	failErr       error
	failCalls     int
	failureCode   ErrorCode
	failureDetail string
	renewCalls    atomic.Int32
	renewFailAt   int32
	renewErr      error
	blockLoad     bool
	loadStarted   chan struct{}
	loadOnce      sync.Once
	publishCalls  atomic.Int32
}

func (repository *workerRepositoryStub) Claim(context.Context, string, time.Duration) (*Claim, error) {
	return repository.claim, repository.claimErr
}

func (repository *workerRepositoryStub) RenewLease(context.Context, Claim, time.Duration) error {
	call := repository.renewCalls.Add(1)
	if repository.renewFailAt > 0 && call >= repository.renewFailAt {
		return repository.renewErr
	}
	return nil
}

func (repository *workerRepositoryStub) Load(ctx context.Context, _ Claim, _ ParsedConfig) (WorkItem, error) {
	if repository.blockLoad {
		repository.loadOnce.Do(func() { close(repository.loadStarted) })
		<-ctx.Done()
		return WorkItem{}, ctx.Err()
	}
	return repository.item, repository.loadErr
}

func (repository *workerRepositoryStub) Publish(context.Context, Claim, Result) (PublishResult, error) {
	repository.publishCalls.Add(1)
	return repository.publish, repository.publishErr
}

func (repository *workerRepositoryStub) FailPermanent(_ context.Context, _ Claim, code ErrorCode, detail string) error {
	repository.failCalls++
	repository.failureCode = code
	repository.failureDetail = detail
	return repository.failErr
}

func TestWorkerLeavesTransientFailureRunningForExpiredLeaseReclaim(t *testing.T) {
	t.Parallel()

	repository := &workerRepositoryStub{loadErr: analyticsError(ErrorDatabase, false, "load", errors.New("temporary outage"))}
	worker := mustTestWorker(t, repository)
	_, err := worker.Process(context.Background(), Claim{GenerationID: 1, LeaseOwner: "worker-a", AttemptCount: 1})
	if err == nil {
		t.Fatal("Process() error = nil")
	}
	if repository.failCalls != 0 {
		t.Fatalf("FailPermanent() calls = %d, want 0", repository.failCalls)
	}
	if code, ok := CodeOf(err); !ok || code != ErrorDatabase {
		t.Fatalf("Process() code = %q, %v", code, ok)
	}
}

func TestWorkerAtomicallyDelegatesPermanentFailure(t *testing.T) {
	t.Parallel()

	repository := &workerRepositoryStub{loadErr: analyticsError(ErrorInvalidManifest, true, "load", errors.New("bad manifest"))}
	worker := mustTestWorker(t, repository)
	outcome, err := worker.Process(context.Background(), Claim{GenerationID: 7, LeaseOwner: "worker-a", AttemptCount: 1})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if outcome.Disposition != RunFailed || outcome.FailureCode == nil || *outcome.FailureCode != ErrorInvalidManifest {
		t.Fatalf("outcome = %#v", outcome)
	}
	if repository.failCalls != 1 || repository.failureCode != ErrorInvalidManifest || repository.failureDetail == "" {
		t.Fatalf("permanent failure call = %d, %q, %q", repository.failCalls, repository.failureCode, repository.failureDetail)
	}
}

func TestWorkerReturnsSupersededReplacement(t *testing.T) {
	t.Parallel()

	replacementID := int64(44)
	repository := &workerRepositoryStub{
		item:    WorkItem{Dataset: testDataset()},
		publish: PublishResult{Disposition: PublishSuperseded, ReplacementGenerationID: &replacementID},
	}
	worker := mustTestWorker(t, repository)
	outcome, err := worker.Process(context.Background(), Claim{GenerationID: 7, LeaseOwner: "worker-a", AttemptCount: 1})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if outcome.Disposition != RunSuperseded || outcome.ReplacementGenerationID == nil || *outcome.ReplacementGenerationID != replacementID {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestWorkerCancelsBlockedLoadAndSkipsTerminalWritesAfterRenewalFailure(t *testing.T) {
	t.Parallel()

	repository := &workerRepositoryStub{
		blockLoad:   true,
		loadStarted: make(chan struct{}),
		renewFailAt: 2,
		renewErr: analyticsError(
			ErrorLeaseLost,
			false,
			"renew analytics lease",
			errors.New("attempt changed"),
		),
	}
	worker := mustTestWorkerWithLease(t, repository, 300*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := worker.Process(ctx, Claim{GenerationID: 7, LeaseOwner: "worker-a", AttemptCount: 2})
		result <- err
	}()
	select {
	case <-repository.loadStarted:
	case <-ctx.Done():
		t.Fatal("analytics load did not start")
	}
	select {
	case err := <-result:
		if code, ok := CodeOf(err); !ok || code != ErrorLeaseLost {
			t.Fatalf("Process() code = %q, %v; error = %v", code, ok, err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not stop after losing its lease")
	}
	if got := repository.renewCalls.Load(); got < 2 {
		t.Fatalf("RenewLease() calls = %d, want at least 2", got)
	}
	if repository.publishCalls.Load() != 0 || repository.failCalls != 0 {
		t.Fatalf("terminal calls after lease loss: publish=%d fail=%d", repository.publishCalls.Load(), repository.failCalls)
	}
}

func mustTestWorker(t *testing.T, repository analyticsRepository) *Worker {
	return mustTestWorkerWithLease(t, repository, time.Minute)
}

func mustTestWorkerWithLease(t *testing.T, repository analyticsRepository, leaseDuration time.Duration) *Worker {
	t.Helper()
	configuration, err := ParseConfig([]byte(validConfigJSON))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	worker, err := newWorker(repository, configuration, "worker-a", leaseDuration)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}
	return worker
}
