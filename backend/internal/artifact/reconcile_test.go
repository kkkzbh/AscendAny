package artifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicationLockBlocksReconcilerUntilRelease(t *testing.T) {
	store := newTestStore(t, 1024)
	publication, err := store.Publish(context.Background(), bytes.NewReader([]byte("orphan candidate")))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	t.Cleanup(func() { _ = publication.Release() })

	started := make(chan struct{})
	callbackCalled := make(chan struct{})
	completed := make(chan struct {
		result ReconcileResult
		err    error
	}, 1)
	go func() {
		close(started)
		result, reconcileErr := store.ReconcileOrphan(
			context.Background(),
			publication.Artifact.Hash,
			0,
			func(context.Context, Artifact) (bool, error) {
				close(callbackCalled)
				return false, nil
			},
		)
		completed <- struct {
			result ReconcileResult
			err    error
		}{result: result, err: reconcileErr}
	}()
	<-started
	select {
	case <-callbackCalled:
		t.Fatal("reconciler entered reference callback while publication lock was held")
	case <-time.After(40 * time.Millisecond):
	}

	if err := publication.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	select {
	case outcome := <-completed:
		if outcome.err != nil {
			t.Fatalf("ReconcileOrphan() error = %v", outcome.err)
		}
		if outcome.result.Disposition != ReconcileRemoved {
			t.Fatalf("disposition = %q, want %q", outcome.result.Disposition, ReconcileRemoved)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconciler stayed blocked after publication release")
	}
	if _, err := os.Lstat(publication.Artifact.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(removed artifact) error = %v, want not exist", err)
	}
}

func TestCommittedReferenceWinsUploaderReconcilerRace(t *testing.T) {
	store := newTestStore(t, 1024)
	publication, err := store.Publish(context.Background(), bytes.NewReader([]byte("database commit race")))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	t.Cleanup(func() { _ = publication.Release() })

	var referenced atomic.Bool
	reconcilerStarted := make(chan struct{})
	callbackObserved := make(chan bool, 1)
	completed := make(chan struct {
		result ReconcileResult
		err    error
	}, 1)
	go func() {
		close(reconcilerStarted)
		result, reconcileErr := store.ReconcileOrphan(
			context.Background(),
			publication.Artifact.Hash,
			0,
			func(context.Context, Artifact) (bool, error) {
				value := referenced.Load()
				callbackObserved <- value
				return value, nil
			},
		)
		completed <- struct {
			result ReconcileResult
			err    error
		}{result: result, err: reconcileErr}
	}()
	<-reconcilerStarted

	// This models the DB transaction commit while the uploader still owns the
	// publication lock. Reconciliation can observe it only after Release.
	referenced.Store(true)
	if err := publication.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	select {
	case observed := <-callbackObserved:
		if !observed {
			t.Fatal("reference callback did not observe committed reference")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reference callback was not called")
	}
	outcome := <-completed
	if outcome.err != nil {
		t.Fatalf("ReconcileOrphan() error = %v", outcome.err)
	}
	if outcome.result.Disposition != ReconcileRetainedReferenced {
		t.Fatalf("disposition = %q, want %q", outcome.result.Disposition, ReconcileRetainedReferenced)
	}
	if _, err := os.Lstat(publication.Artifact.Path); err != nil {
		t.Fatalf("referenced artifact was removed: %v", err)
	}
}

func TestReconcileOrphanHonorsAgeGateAndRestatsAfterReferenceCheck(t *testing.T) {
	store := newTestStore(t, 1024)
	publication := publishAndRelease(t, store, []byte("young orphan"))
	now := time.Unix(2_000_000_000, 0)
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(publication.Artifact.Path, old, old); err != nil {
		t.Fatalf("Chtimes(old) error = %v", err)
	}
	store.ops.now = func() time.Time { return now }

	result, err := store.ReconcileOrphan(
		context.Background(),
		publication.Artifact.Hash,
		time.Hour,
		func(_ context.Context, artifact Artifact) (bool, error) {
			// Move the mtime forward inside the callback. The mandatory restat
			// must use this new age and retain the file.
			if err := os.Chtimes(artifact.Path, now, now); err != nil {
				return false, err
			}
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("ReconcileOrphan() error = %v", err)
	}
	if result.Disposition != ReconcileRetainedTooYoung {
		t.Fatalf("disposition = %q, want %q", result.Disposition, ReconcileRetainedTooYoung)
	}
	if !result.Artifact.ModTime.Equal(now) {
		t.Fatalf("restated modtime = %v, want %v", result.Artifact.ModTime, now)
	}
	if _, err := os.Lstat(publication.Artifact.Path); err != nil {
		t.Fatalf("young artifact was removed: %v", err)
	}
}

func TestReconcileOrphanUnlinksAndFsyncsHashPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	var removedPaths []string
	var syncedPaths []string
	store, err := newStore(root, 1024, fileOps{
		now:    time.Now,
		rename: os.Rename,
		remove: func(path string) error {
			removedPaths = append(removedPaths, path)
			return os.Remove(path)
		},
		syncDir: func(path string) error {
			syncedPaths = append(syncedPaths, path)
			return syncDirectory(path)
		},
	})
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	publication := publishAndRelease(t, store, []byte("old orphan"))
	removedPaths = nil
	syncedPaths = nil

	result, err := store.ReconcileOrphan(
		context.Background(),
		publication.Artifact.Hash,
		0,
		func(context.Context, Artifact) (bool, error) { return false, nil },
	)
	if err != nil {
		t.Fatalf("ReconcileOrphan() error = %v", err)
	}
	if result.Disposition != ReconcileRemoved {
		t.Fatalf("disposition = %q, want %q", result.Disposition, ReconcileRemoved)
	}
	if len(removedPaths) != 1 || removedPaths[0] != publication.Artifact.Path {
		t.Fatalf("removed paths = %v, want [%q]", removedPaths, publication.Artifact.Path)
	}
	expectedPrefix := filepath.Dir(publication.Artifact.Path)
	if len(syncedPaths) != 1 || syncedPaths[0] != expectedPrefix {
		t.Fatalf("synced paths = %v, want [%q]", syncedPaths, expectedPrefix)
	}
}

func TestReferenceCheckFailureReleasesHashLock(t *testing.T) {
	store := newTestStore(t, 1024)
	publication := publishAndRelease(t, store, []byte("reference check failure"))
	referenceErr := errors.New("database unavailable")

	_, err := store.ReconcileOrphan(
		context.Background(),
		publication.Artifact.Hash,
		0,
		func(context.Context, Artifact) (bool, error) { return false, referenceErr },
	)
	assertErrorCode(t, err, ErrorReferenceCheck)
	if !errors.Is(err, referenceErr) {
		t.Fatalf("ReconcileOrphan() error = %v, want wrapped callback error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := store.Publish(ctx, bytes.NewReader([]byte("reference check failure")))
	if err != nil {
		t.Fatalf("Publish() after callback error = %v", err)
	}
	defer next.Release()
}

func TestCanceledReferenceCheckRetainsArtifactAndReleasesLock(t *testing.T) {
	store := newTestStore(t, 1024)
	publication := publishAndRelease(t, store, []byte("canceled reconciliation"))
	ctx, cancel := context.WithCancel(context.Background())

	_, err := store.ReconcileOrphan(
		ctx,
		publication.Artifact.Hash,
		0,
		func(context.Context, Artifact) (bool, error) {
			cancel()
			return false, nil
		},
	)
	assertErrorCode(t, err, ErrorCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReconcileOrphan() error = %v, want wrapped cancellation", err)
	}
	if _, err := os.Lstat(publication.Artifact.Path); err != nil {
		t.Fatalf("artifact removed after cancellation: %v", err)
	}

	next, err := store.Publish(context.Background(), bytes.NewReader([]byte("canceled reconciliation")))
	if err != nil {
		t.Fatalf("Publish() after canceled reconciliation error = %v", err)
	}
	defer next.Release()
}

func TestReconcilePublishedWalksExactLayout(t *testing.T) {
	store := newTestStore(t, 1024)
	referenced := publishAndRelease(t, store, []byte("referenced artifact"))
	orphan := publishAndRelease(t, store, []byte("unreferenced artifact"))
	now := time.Unix(2_000_000_000, 0)
	old := now.Add(-2 * time.Hour)
	for _, candidate := range []Artifact{referenced.Artifact, orphan.Artifact} {
		if err := os.Chtimes(candidate.Path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	store.ops.now = func() time.Time { return now }

	results, err := store.ReconcilePublished(context.Background(), time.Hour, func(_ context.Context, candidate Artifact) (bool, error) {
		return candidate.Hash == referenced.Artifact.Hash, nil
	})
	if err != nil {
		t.Fatalf("ReconcilePublished() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	dispositions := make(map[string]ReconcileDisposition, len(results))
	for _, result := range results {
		dispositions[result.Artifact.Hash] = result.Disposition
	}
	if dispositions[referenced.Artifact.Hash] != ReconcileRetainedReferenced || dispositions[orphan.Artifact.Hash] != ReconcileRemoved {
		t.Fatalf("dispositions = %#v", dispositions)
	}
	if _, err := os.Stat(referenced.Artifact.Path); err != nil {
		t.Fatalf("referenced artifact missing: %v", err)
	}
	if _, err := os.Stat(orphan.Artifact.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan still exists: %v", err)
	}
}

func TestReconcilePublishedRejectsUnknownLayoutEntry(t *testing.T) {
	store := newTestStore(t, 1024)
	unknown := filepath.Join(store.sha256Root, "not-a-prefix")
	if err := os.Mkdir(unknown, publishedDirectoryMode); err != nil {
		t.Fatal(err)
	}

	_, err := store.ReconcilePublished(context.Background(), 0, func(context.Context, Artifact) (bool, error) { return false, nil })
	assertErrorCode(t, err, ErrorCorrupt)
}

func TestReconcilePublishedRejectsModeDriftBeforeReferenceCheck(t *testing.T) {
	store := newTestStore(t, 1024)
	publication := publishAndRelease(t, store, []byte("referenced mode drift"))
	if err := os.Chmod(publication.Artifact.Path, 0o660); err != nil {
		t.Fatal(err)
	}
	callbackCalled := false

	_, err := store.ReconcilePublished(context.Background(), 0, func(context.Context, Artifact) (bool, error) {
		callbackCalled = true
		return true, nil
	})
	assertErrorCode(t, err, ErrorCorrupt)
	if callbackCalled {
		t.Fatal("reference callback was called for an artifact with mode drift")
	}
	if _, statErr := os.Lstat(publication.Artifact.Path); statErr != nil {
		t.Fatalf("mode-drifted artifact was removed: %v", statErr)
	}
}

func TestReconcilePublishedHashesReferencedArtifactEveryCycle(t *testing.T) {
	store := newTestStore(t, 1024)
	content := []byte("referenced digest scrub")
	publication := publishAndRelease(t, store, content)
	corrupt := append([]byte(nil), content...)
	corrupt[0] ^= 0xff
	if err := os.WriteFile(publication.Artifact.Path, corrupt, publishedFileMode); err != nil {
		t.Fatal(err)
	}
	callbackCalled := false

	_, err := store.ReconcilePublished(context.Background(), 0, func(_ context.Context, candidate Artifact) (bool, error) {
		callbackCalled = true
		return candidate.Hash == publication.Artifact.Hash, nil
	})
	assertErrorCode(t, err, ErrorCorrupt)
	if !callbackCalled {
		t.Fatal("reference callback was not called before the referenced digest scrub")
	}
	if _, statErr := os.Lstat(publication.Artifact.Path); statErr != nil {
		t.Fatalf("digest-corrupt referenced artifact was removed: %v", statErr)
	}
}

func TestReconcileIncomingRemovesOnlyOldExactTemporaryFiles(t *testing.T) {
	store := newTestStore(t, 1024)
	now := time.Unix(2_000_000_000, 0)
	store.ops.now = func() time.Time { return now }
	oldPath := filepath.Join(store.incoming, "upload-"+stringsOf('a', incomingRandomBytes*2))
	youngPath := filepath.Join(store.incoming, "upload-"+stringsOf('b', incomingRandomBytes*2))
	for _, path := range []string{oldPath, youngPath} {
		if err := os.WriteFile(path, []byte("temporary"), privateFileMode); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(youngPath, now, now); err != nil {
		t.Fatal(err)
	}

	removed, err := store.ReconcileIncoming(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("ReconcileIncoming() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old incoming file remains: %v", err)
	}
	if _, err := os.Stat(youngPath); err != nil {
		t.Fatalf("young incoming file was removed: %v", err)
	}
}

func TestReconcileIncomingCannotUnlinkPausedOldUpload(t *testing.T) {
	store := newTestStore(t, 1024)
	source := &gatedReader{entered: make(chan struct{}), proceed: make(chan struct{})}
	completed := make(chan struct {
		publication *Publication
		err         error
	}, 1)
	go func() {
		publication, err := store.Publish(context.Background(), source)
		completed <- struct {
			publication *Publication
			err         error
		}{publication: publication, err: err}
	}()
	<-source.entered
	entries, err := os.ReadDir(store.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("incoming entries = %d, want 1", len(entries))
	}
	activePath := filepath.Join(store.incoming, entries[0].Name())
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(activePath, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := store.ReconcileIncoming(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("ReconcileIncoming() error = %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if _, err := os.Lstat(activePath); err != nil {
		t.Fatalf("active incoming upload was unlinked: %v", err)
	}

	close(source.proceed)
	outcome := <-completed
	if outcome.err != nil {
		t.Fatalf("Publish() error = %v", outcome.err)
	}
	defer outcome.publication.Release()
}

func TestReconcileIncomingFsyncsDeletionBeforeReturningLaterError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	syncErr := errors.New("incoming fsync failed")
	var syncedPaths []string
	store, err := newStore(root, 1024, fileOps{
		now:    time.Now,
		rename: os.Rename,
		remove: os.Remove,
		syncDir: func(path string) error {
			syncedPaths = append(syncedPaths, path)
			return syncErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(store.incoming, "upload-"+stringsOf('0', incomingRandomBytes*2))
	if err := os.WriteFile(oldPath, []byte("crash-left"), privateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldPath, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.incoming, "zz-unexpected"), []byte("corrupt layout"), privateFileMode); err != nil {
		t.Fatal(err)
	}

	removed, err := store.ReconcileIncoming(context.Background(), 0)
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	assertErrorCode(t, err, ErrorCorrupt)
	if !errors.Is(err, syncErr) {
		t.Fatalf("ReconcileIncoming() error = %v, want joined fsync error", err)
	}
	if len(syncedPaths) != 1 || syncedPaths[0] != store.incoming {
		t.Fatalf("synced paths = %v, want [%q]", syncedPaths, store.incoming)
	}
}

func TestReconcileIncomingFsyncsDeletionBeforeReturningCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	ctx, cancel := context.WithCancel(context.Background())
	var syncedPaths []string
	store, err := newStore(root, 1024, fileOps{
		now:    time.Now,
		rename: os.Rename,
		remove: func(path string) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			cancel()
			return nil
		},
		syncDir: func(path string) error {
			syncedPaths = append(syncedPaths, path)
			return syncDirectory(path)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(store.incoming, "upload-"+stringsOf('1', incomingRandomBytes*2))
	if err := os.WriteFile(oldPath, []byte("crash-left"), privateFileMode); err != nil {
		t.Fatal(err)
	}

	removed, err := store.ReconcileIncoming(ctx, 0)
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	assertErrorCode(t, err, ErrorCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReconcileIncoming() error = %v, want cancellation", err)
	}
	if len(syncedPaths) != 1 || syncedPaths[0] != store.incoming {
		t.Fatalf("synced paths = %v, want [%q]", syncedPaths, store.incoming)
	}
}

func TestReconcileOrphanValidatesArgumentsAndMissingArtifact(t *testing.T) {
	store := newTestStore(t, 1024)
	hashValue := stringsOf('a', 64)
	callback := func(context.Context, Artifact) (bool, error) { return false, nil }

	_, err := store.ReconcileOrphan(context.Background(), hashValue, -time.Second, callback)
	assertErrorCode(t, err, ErrorInvalidArgument)
	_, err = store.ReconcileOrphan(context.Background(), hashValue, 0, nil)
	assertErrorCode(t, err, ErrorInvalidArgument)
	_, err = store.ReconcileOrphan(context.Background(), hashValue, 0, callback)
	assertErrorCode(t, err, ErrorNotFound)
}

func stringsOf(character byte, count int) string {
	buffer := make([]byte, count)
	for index := range buffer {
		buffer[index] = character
	}
	return string(buffer)
}
