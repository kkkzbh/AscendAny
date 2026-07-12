package importing

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

func TestQueuePublicationIsIdempotentAndReleasesHashLock(t *testing.T) {
	store := testArtifactStore(t)
	repository := newMemoryRepository()
	service, err := newService(repository, sequenceUUIDs(
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
	))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("durable queue payload")

	firstPublication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	first, err := service.QueuePublication(context.Background(), firstPublication, PintiaSnapshotV2MediaType)
	if err != nil {
		t.Fatalf("QueuePublication(first) error = %v", err)
	}
	if !first.Created || first.Job.Status != JobQueued || first.Job.Stage != StageReceived {
		t.Fatalf("first queue result = %#v", first)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	secondPublication, err := store.Publish(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish(second) stayed locked: %v", err)
	}
	second, err := service.QueuePublication(context.Background(), secondPublication, PintiaSnapshotV2MediaType)
	if err != nil {
		t.Fatalf("QueuePublication(second) error = %v", err)
	}
	if second.Created {
		t.Fatal("duplicate bytes created a second job")
	}
	if second.Job.ID != first.Job.ID || second.Job.PublicID != first.Job.PublicID {
		t.Fatalf("duplicate job = %#v, want %#v", second.Job, first.Job)
	}
	if got := repository.eventCount(first.Job.ID); got != 1 {
		t.Fatalf("durable event count = %d, want 1", got)
	}
}

func TestQueuePublicationHoldsHashLockUntilRepositoryTransactionReturns(t *testing.T) {
	store := testArtifactStore(t)
	repository := &blockingQueueRepository{
		memoryRepository: newMemoryRepository(),
		entered:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	service, err := newService(repository, sequenceUUIDs("11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("transaction lock ordering")
	publication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}

	queued := make(chan error, 1)
	go func() {
		_, queueErr := service.QueuePublication(context.Background(), publication, PintiaSnapshotV2MediaType)
		queued <- queueErr
	}()
	select {
	case <-repository.entered:
	case <-time.After(time.Second):
		t.Fatal("repository transaction was not entered")
	}

	blockedContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second, err := store.Publish(blockedContext, bytes.NewReader(content))
	if second != nil {
		_ = second.Release()
	}
	if code, ok := artifact.CodeOf(err); !ok || code != artifact.ErrorCanceled {
		close(repository.release)
		t.Fatalf("concurrent Publish() error = %v, code = %q/%v", err, code, ok)
	}

	close(repository.release)
	select {
	case err := <-queued:
		if err != nil {
			t.Fatalf("QueuePublication() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("QueuePublication() did not return")
	}

	afterCommit, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish() after repository return = %v", err)
	}
	if err := afterCommit.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestQueuePublicationRejectsNonContractMediaTypeAndStillReleases(t *testing.T) {
	store := testArtifactStore(t)
	service, err := newService(newMemoryRepository(), sequenceUUIDs("11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("wrong media")
	publication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.QueuePublication(context.Background(), publication, "application/json")
	assertImportCode(t, err, ErrorInvalidMediaType)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := store.Publish(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("publication lock remained held: %v", err)
	}
	defer next.Release()
}

func TestQueuePublicationWithNilContextStillReleases(t *testing.T) {
	store := testArtifactStore(t)
	service, err := newService(newMemoryRepository(), sequenceUUIDs("11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("nil context")
	publication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.QueuePublication(nil, publication, PintiaSnapshotV2MediaType)
	assertImportCode(t, err, ErrorInvalidPublication)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := store.Publish(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("publication lock remained held: %v", err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestVendorMediaTypeMatchesOpenAPIContract(t *testing.T) {
	if PintiaSnapshotV2MediaType != "application/vnd.ascendany.pintia.snapshot.v2+json" {
		t.Fatalf("media type = %q", PintiaSnapshotV2MediaType)
	}
	openAPI, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "openapi", "ascendany-v2.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(openAPI, []byte(PintiaSnapshotV2MediaType+":")) {
		t.Fatalf("OpenAPI does not declare %q", PintiaSnapshotV2MediaType)
	}
	if bytes.Contains(openAPI, []byte("application/vnd.ascendany.pintia-snapshot-v2+json")) {
		t.Fatal("OpenAPI contains obsolete hyphenated media type")
	}
}

type memoryRepository struct {
	mu     sync.Mutex
	nextID int64
	jobs   map[string]QueueResult
	events map[int64]int
}

type blockingQueueRepository struct {
	*memoryRepository
	entered chan struct{}
	release chan struct{}
}

func (r *blockingQueueRepository) QueueArtifact(
	ctx context.Context,
	published artifact.Artifact,
	mediaType string,
	publicID string,
) (QueueResult, error) {
	close(r.entered)
	<-r.release
	return r.memoryRepository.QueueArtifact(ctx, published, mediaType, publicID)
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{nextID: 1, jobs: make(map[string]QueueResult), events: make(map[int64]int)}
}

func (r *memoryRepository) QueueArtifact(
	_ context.Context,
	published artifact.Artifact,
	_ string,
	publicID string,
) (QueueResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.jobs[published.Hash]; ok {
		existing.Created = false
		return existing, nil
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	result := QueueResult{
		Created: true,
		Job: Job{
			ID:         r.nextID,
			PublicID:   publicID,
			ArtifactID: r.nextID,
			Status:     JobQueued,
			Stage:      StageReceived,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
	r.nextID++
	r.jobs[published.Hash] = result
	r.events[result.Job.ID] = 1
	return result, nil
}

func (r *memoryRepository) eventCount(jobID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[jobID]
}

func (r *memoryRepository) Claim(context.Context, string, time.Duration) (*Claim, error) {
	return nil, nil
}
func (r *memoryRepository) LoadArtifact(context.Context, Claim) (ArtifactMetadata, error) {
	return ArtifactMetadata{}, errors.New("not implemented")
}
func (r *memoryRepository) MarkImporting(context.Context, Claim, time.Duration) (Claim, error) {
	return Claim{}, errors.New("not implemented")
}
func (r *memoryRepository) Requeue(context.Context, Claim, time.Duration, ErrorCode) error {
	return errors.New("not implemented")
}
func (r *memoryRepository) FailPermanent(context.Context, Claim, ErrorCode, string) error {
	return errors.New("not implemented")
}
func (r *memoryRepository) ImportSnapshot(context.Context, ImportRequest) (ImportOutcome, error) {
	return ImportOutcome{}, errors.New("not implemented")
}
func testArtifactStore(t *testing.T) *artifact.Store {
	t.Helper()
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sequenceUUIDs(values ...string) uuidGenerator {
	index := 0
	return func() (string, error) {
		if index >= len(values) {
			return "", errors.New("UUID sequence exhausted")
		}
		value := values[index]
		index++
		return value, nil
	}
}

func assertImportCode(t *testing.T, err error, expected ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", expected)
	}
	actual, ok := CodeOf(err)
	if !ok || actual != expected {
		t.Fatalf("error = %v, code = %q/%v, want %q", err, actual, ok, expected)
	}
}
