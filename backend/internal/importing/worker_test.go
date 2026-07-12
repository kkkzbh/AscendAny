package importing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func TestWorkerPermanentlyFailsMissingOrCorruptArtifact(t *testing.T) {
	for _, code := range []artifact.ErrorCode{artifact.ErrorNotFound, artifact.ErrorCorrupt} {
		t.Run(string(code), func(t *testing.T) {
			repository := newWorkerRepository(t)
			verifier := fakeArtifactVerifier{err: &artifact.StoreError{Code: code, Op: "verify", Err: errors.New("broken")}}
			worker := testWorker(t, repository, verifier)

			outcome, err := worker.Process(context.Background(), repository.claim)
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if outcome.Disposition != ImportFailed || outcome.FailureCode == nil || *outcome.FailureCode != ErrorArtifactVerification {
				t.Fatalf("outcome = %#v", outcome)
			}
			if repository.failedCode != ErrorArtifactVerification || repository.requeued {
				t.Fatalf("failedCode=%q requeued=%v", repository.failedCode, repository.requeued)
			}
		})
	}
}

func TestWorkerClassifiesClaimedArtifactMetadataAndDatabaseFailures(t *testing.T) {
	tests := []struct {
		name        string
		loadErr     error
		disposition ImportDisposition
		failedCode  ErrorCode
		requeued    bool
		wantError   bool
	}{
		{
			name:        "permanent metadata conflict",
			loadErr:     importError(ErrorArtifactMetadata, true, "load", errors.New("metadata changed")),
			disposition: ImportFailed,
			failedCode:  ErrorArtifactMetadata,
		},
		{
			name:        "transient database failure",
			loadErr:     importError(ErrorDatabase, false, "load", errors.New("database unavailable")),
			disposition: ImportRetry,
			requeued:    true,
		},
		{
			name:      "lost lease",
			loadErr:   importError(ErrorLeaseLost, false, "load", errors.New("lease expired")),
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newWorkerRepository(t)
			repository.loadErr = test.loadErr
			worker := testWorker(t, repository, fakeArtifactVerifier{})

			outcome, err := worker.Process(context.Background(), repository.claim)
			if test.wantError {
				if err == nil {
					t.Fatal("Process() error = nil")
				}
				assertImportCode(t, err, ErrorLeaseLost)
				if repository.requeued || repository.failedCode != "" {
					t.Fatalf("requeued=%v failed=%q", repository.requeued, repository.failedCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if outcome.Disposition != test.disposition || repository.failedCode != test.failedCode || repository.requeued != test.requeued {
				t.Fatalf("outcome=%#v failed=%q requeued=%v", outcome, repository.failedCode, repository.requeued)
			}
		})
	}
}

func TestWorkerRetriesTransientArtifactIO(t *testing.T) {
	repository := newWorkerRepository(t)
	verifier := fakeArtifactVerifier{err: &artifact.StoreError{Code: artifact.ErrorIO, Op: "verify", Err: errors.New("temporary I/O")}}
	worker := testWorker(t, repository, verifier)

	outcome, err := worker.Process(context.Background(), repository.claim)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if outcome.Disposition != ImportRetry || !repository.requeued || repository.failedCode != "" {
		t.Fatalf("outcome=%#v requeued=%v failed=%q", outcome, repository.requeued, repository.failedCode)
	}
}

func TestWorkerPermanentlyFailsInvalidPintiaPayload(t *testing.T) {
	tests := map[string][]byte{
		"structural":              []byte(`{"schema":"wrong"}`),
		"malformed duplicate key": []byte(`{"schema":"a","schema":"b"}`),
		"malformed truncated":     []byte(`{"schema":`),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			repository := newWorkerRepository(t)
			path := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			verifier := fakeArtifactVerifier{artifact: artifact.Artifact{
				Hash:       repository.metadata.Hash,
				Size:       repository.metadata.Size,
				StorageKey: repository.metadata.StorageKey,
				Path:       path,
			}}
			worker := testWorker(t, repository, verifier)

			outcome, err := worker.Process(context.Background(), repository.claim)
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if outcome.Disposition != ImportFailed || repository.failedCode != ErrorValidation || repository.requeued {
				t.Fatalf("outcome=%#v failed=%q requeued=%v", outcome, repository.failedCode, repository.requeued)
			}
			if repository.importRequest.Snapshot != nil {
				t.Fatalf("invalid payload reached ImportSnapshot: %#v", repository.importRequest)
			}
		})
	}
}

func TestWorkerValidatesAndImportsCompleteSnapshot(t *testing.T) {
	repository := newWorkerRepository(t)
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "pintia", "fixtures", "valid", "complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "complete.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	repository.metadata.Size = int64(len(payload))
	verifier := fakeArtifactVerifier{artifact: artifact.Artifact{
		Hash:       repository.metadata.Hash,
		Size:       repository.metadata.Size,
		StorageKey: repository.metadata.StorageKey,
		Path:       path,
	}}
	worker := testWorker(t, repository, verifier)

	outcome, err := worker.Process(context.Background(), repository.claim)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if outcome.Disposition != ImportCreated || !repository.markedImporting || repository.importRequest.Snapshot == nil {
		t.Fatalf("outcome=%#v marked=%v request=%#v", outcome, repository.markedImporting, repository.importRequest)
	}
	if !lowercaseSHA256Pattern.MatchString(repository.importRequest.DomainHash) {
		t.Fatalf("domain hash = %q", repository.importRequest.DomainHash)
	}
}

func TestWorkerRollsPermanentIdentityConflictIntoFailedJob(t *testing.T) {
	repository := newWorkerRepository(t)
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "pintia", "fixtures", "valid", "complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "complete.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	repository.metadata.Size = int64(len(payload))
	repository.importErr = importError(ErrorIdentityConflict, true, "import", errors.New("student number collision"))
	verifier := fakeArtifactVerifier{artifact: artifact.Artifact{
		Hash: repository.metadata.Hash, Size: repository.metadata.Size,
		StorageKey: repository.metadata.StorageKey, Path: path,
	}}
	worker := testWorker(t, repository, verifier)

	outcome, err := worker.Process(context.Background(), repository.claim)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if outcome.Disposition != ImportFailed || repository.failedCode != ErrorIdentityConflict || repository.requeued {
		t.Fatalf("outcome=%#v failed=%q requeued=%v", outcome, repository.failedCode, repository.requeued)
	}
}

func TestWorkerCancelsBlockedVerificationWhenLeaseRenewalIsLost(t *testing.T) {
	repository := newWorkerRepository(t)
	repository.renewFailureAt = 2
	repository.renewErr = importError(ErrorLeaseLost, false, "renew import lease", errors.New("attempt changed"))
	verifier := &blockingArtifactVerifier{started: make(chan struct{})}
	worker := testWorkerWithLease(t, repository, verifier, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := worker.Process(ctx, repository.claim)
		result <- err
	}()
	select {
	case <-verifier.started:
	case <-ctx.Done():
		t.Fatal("artifact verification did not start")
	}
	select {
	case err := <-result:
		assertImportCode(t, err, ErrorLeaseLost)
	case <-ctx.Done():
		t.Fatal("worker did not stop after losing its lease")
	}
	if got := repository.renewCalls.Load(); got < 2 {
		t.Fatalf("RenewLease() calls = %d, want at least 2", got)
	}
	if repository.requeued || repository.failedCode != "" || repository.importRequest.Snapshot != nil {
		t.Fatalf("terminal write after lease loss: requeued=%v failed=%q request=%#v", repository.requeued, repository.failedCode, repository.importRequest)
	}
}

type fakeArtifactVerifier struct {
	artifact artifact.Artifact
	err      error
}

func (v fakeArtifactVerifier) Verify(context.Context, string, int64) (artifact.Artifact, error) {
	return v.artifact, v.err
}

type blockingArtifactVerifier struct {
	started chan struct{}
	once    sync.Once
}

func (verifier *blockingArtifactVerifier) Verify(ctx context.Context, _ string, _ int64) (artifact.Artifact, error) {
	verifier.once.Do(func() { close(verifier.started) })
	<-ctx.Done()
	return artifact.Artifact{}, ctx.Err()
}

type workerRepository struct {
	t               *testing.T
	claim           Claim
	metadata        ArtifactMetadata
	markedImporting bool
	requeued        bool
	failedCode      ErrorCode
	importRequest   ImportRequest
	importErr       error
	loadErr         error
	markErr         error
	renewCalls      atomic.Int32
	renewFailureAt  int32
	renewErr        error
}

func newWorkerRepository(t *testing.T) *workerRepository {
	t.Helper()
	owner := "worker-1"
	expires := time.Now().Add(time.Hour)
	hash := strings.Repeat("a", 64)
	return &workerRepository{
		t: t,
		claim: Claim{Job: Job{
			ID:             1,
			PublicID:       "11111111-1111-4111-8111-111111111111",
			ArtifactID:     2,
			Status:         JobRunning,
			Stage:          StageValidating,
			AttemptCount:   1,
			LeaseOwner:     &owner,
			LeaseExpiresAt: &expires,
		}},
		metadata: ArtifactMetadata{
			ID: 2, Hash: hash, Size: 16,
			MediaType:  PintiaSnapshotV2MediaType,
			StorageKey: "sha256/aa/" + hash,
		},
	}
}

func (r *workerRepository) QueueArtifact(context.Context, artifact.Artifact, string, string) (QueueResult, error) {
	return QueueResult{}, errors.New("not implemented")
}
func (r *workerRepository) Claim(context.Context, string, time.Duration) (*Claim, error) {
	claim := r.claim
	return &claim, nil
}
func (r *workerRepository) RenewLease(context.Context, Claim, time.Duration) error {
	call := r.renewCalls.Add(1)
	if r.renewFailureAt > 0 && call >= r.renewFailureAt {
		return r.renewErr
	}
	return nil
}
func (r *workerRepository) LoadArtifact(context.Context, Claim) (ArtifactMetadata, error) {
	return r.metadata, r.loadErr
}
func (r *workerRepository) MarkImporting(_ context.Context, claim Claim, _ time.Duration) (Claim, error) {
	if r.markErr != nil {
		return Claim{}, r.markErr
	}
	r.markedImporting = true
	claim.Stage = StageImporting
	r.claim = claim
	return claim, nil
}
func (r *workerRepository) Requeue(context.Context, Claim, time.Duration, ErrorCode) error {
	r.requeued = true
	return nil
}
func (r *workerRepository) FailPermanent(_ context.Context, _ Claim, code ErrorCode, detail string) error {
	if detail == "" {
		r.t.Fatal("permanent failure detail is empty")
	}
	r.failedCode = code
	return nil
}
func (r *workerRepository) ImportSnapshot(_ context.Context, request ImportRequest) (ImportOutcome, error) {
	r.importRequest = request
	if r.importErr != nil {
		return ImportOutcome{}, r.importErr
	}
	snapshotID := int64(10)
	publicID := request.PublicIDs.Snapshot
	generationID := int64(20)
	return ImportOutcome{
		Disposition: ImportCreated, SnapshotID: &snapshotID,
		SnapshotPublicID: &publicID, AnalyticsGenerationID: &generationID,
	}, nil
}
func testWorker(t *testing.T, repository workerStore, verifier artifactVerifier) *Worker {
	return testWorkerWithLease(t, repository, verifier, time.Minute)
}

func testWorkerWithLease(t *testing.T, repository workerStore, verifier artifactVerifier, leaseDuration time.Duration) *Worker {
	t.Helper()
	validator, err := pintia.NewEmbeddedValidator(pintia.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	worker, err := newWorker(
		repository,
		verifier,
		validator,
		sequenceUUIDs(
			"22222222-2222-4222-8222-222222222222",
			"33333333-3333-4333-8333-333333333333",
		),
		WorkerConfig{
			LeaseDuration: leaseDuration,
			RetryDelay:    time.Second,
			PintiaLimits:  pintia.DefaultLimits(),
			Analytics: AnalyticsConfig{
				AlgorithmVersion: "analytics_v1",
				ConfigSHA256:     strings.Repeat("b", 64),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
