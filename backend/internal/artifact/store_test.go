package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPublishCreatesExactLayoutAndVerifyRechecksContent(t *testing.T) {
	store := newTestStore(t, 1024)
	for path, expectedMode := range map[string]os.FileMode{
		store.root:       publishedDirectoryMode,
		store.sha256Root: publishedDirectoryMode,
		store.incoming:   privateDirectoryMode,
		store.locks:      privateDirectoryMode,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(%q) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != expectedMode {
			t.Fatalf("directory %q mode = %#o, want %#o", path, got, expectedMode)
		}
	}
	content := []byte("immutable pintia snapshot")
	digest := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(digest[:])

	publication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	t.Cleanup(func() { _ = publication.Release() })

	expectedPath := filepath.Join(store.root, "sha256", expectedHash[:2], expectedHash)
	if publication.Artifact.Hash != expectedHash {
		t.Fatalf("hash = %q, want %q", publication.Artifact.Hash, expectedHash)
	}
	if publication.Artifact.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", publication.Artifact.Size, len(content))
	}
	if publication.Artifact.Path != expectedPath {
		t.Fatalf("path = %q, want %q", publication.Artifact.Path, expectedPath)
	}
	expectedStorageKey := "sha256/" + expectedHash[:2] + "/" + expectedHash
	if publication.Artifact.StorageKey != expectedStorageKey {
		t.Fatalf("storage key = %q, want %q", publication.Artifact.StorageKey, expectedStorageKey)
	}

	info, err := os.Lstat(expectedPath)
	if err != nil {
		t.Fatalf("Lstat(artifact) error = %v", err)
	}
	if got := info.Mode().Perm(); got != publishedFileMode {
		t.Fatalf("artifact mode = %#o, want %#o", got, publishedFileMode)
	}
	prefixInfo, err := os.Lstat(filepath.Dir(expectedPath))
	if err != nil {
		t.Fatalf("Lstat(hash prefix) error = %v", err)
	}
	if got := prefixInfo.Mode().Perm(); got != publishedDirectoryMode {
		t.Fatalf("hash prefix mode = %#o, want %#o", got, publishedDirectoryMode)
	}
	lockInfo, err := os.Lstat(filepath.Join(store.root, ".locks", expectedHash))
	if err != nil {
		t.Fatalf("Lstat(lock file) error = %v", err)
	}
	if got := lockInfo.Mode().Perm(); got != privateFileMode {
		t.Fatalf("lock file mode = %#o, want %#o", got, privateFileMode)
	}
	assertDirectoryEmpty(t, filepath.Join(store.root, "incoming"))

	verified, err := store.Verify(context.Background(), expectedHash, int64(len(content)))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified != publication.Artifact {
		t.Fatalf("Verify() = %#v, want %#v", verified, publication.Artifact)
	}

	if err := publication.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := publication.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}

func TestPublishEnforcesHardByteLimitAndCleansTemporaryFile(t *testing.T) {
	store := newTestStore(t, 3)
	reader := &recordingReader{Reader: strings.NewReader("four")}

	_, err := store.Publish(context.Background(), reader)
	assertErrorCode(t, err, ErrorPayloadTooLarge)
	if reader.maxBuffer > 4 {
		t.Fatalf("largest source read buffer = %d, want at most limit+1", reader.maxBuffer)
	}
	assertDirectoryEmpty(t, store.incoming)
	assertNoArtifacts(t, store.sha256Root)

	publication, err := store.Publish(context.Background(), strings.NewReader("123"))
	if err != nil {
		t.Fatalf("Publish(exact limit) error = %v", err)
	}
	defer publication.Release()
	if publication.Artifact.Size != 3 {
		t.Fatalf("exact-limit size = %d, want 3", publication.Artifact.Size)
	}
}

func TestPublishRejectsEmptyArtifactAndCleansTemporaryFile(t *testing.T) {
	store := newTestStore(t, 1024)
	_, err := store.Publish(context.Background(), bytes.NewReader(nil))
	assertErrorCode(t, err, ErrorEmptyArtifact)
	assertDirectoryEmpty(t, store.incoming)
	assertNoArtifacts(t, store.sha256Root)
}

func TestIncomingTemporaryFileUsesPrivateMode(t *testing.T) {
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
		t.Fatalf("ReadDir(incoming) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("incoming entries = %d, want 1", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("Info(temporary) error = %v", err)
	}
	if got := info.Mode().Perm(); got != privateFileMode {
		t.Fatalf("temporary mode = %#o, want %#o", got, privateFileMode)
	}
	close(source.proceed)
	outcome := <-completed
	if outcome.err != nil {
		t.Fatalf("Publish() error = %v", outcome.err)
	}
	defer outcome.publication.Release()
	assertDirectoryEmpty(t, store.incoming)
}

func TestNewStoreRejectsPermissiveExistingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(root, 0o770); err != nil {
		t.Fatalf("Mkdir(root) error = %v", err)
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}

	_, err := NewStore(root, 1024)
	assertErrorCode(t, err, ErrorIO)
}

func TestStoreCreatesExactModesUnderRestrictiveUmask(t *testing.T) {
	parent := t.TempDir()
	previousUmask := syscall.Umask(0o077)
	defer syscall.Umask(previousUmask)

	store, err := NewStore(filepath.Join(parent, "artifacts"), 1024)
	if err != nil {
		t.Fatalf("NewStore() under umask 077 error = %v", err)
	}
	for _, expected := range []struct {
		path string
		mode os.FileMode
	}{
		{store.root, publishedDirectoryMode},
		{store.incoming, privateDirectoryMode},
		{store.sha256Root, publishedDirectoryMode},
		{store.locks, privateDirectoryMode},
	} {
		assertPathMode(t, expected.path, expected.mode)
	}

	publication, err := store.Publish(context.Background(), strings.NewReader("new prefix under restrictive umask"))
	if err != nil {
		t.Fatalf("Publish() under umask 077 error = %v", err)
	}
	defer publication.Release()
	assertPathMode(t, filepath.Dir(publication.Artifact.Path), publishedDirectoryMode)
	assertPathMode(t, publication.Artifact.Path, publishedFileMode)
	assertPathMode(t, filepath.Join(store.locks, publication.Artifact.Hash), privateFileMode)
}

func TestConcurrentNewSharedPrefixUnderRestrictiveUmask(t *testing.T) {
	parent := t.TempDir()
	previousUmask := syscall.Umask(0o077)
	defer syscall.Umask(previousUmask)

	store, err := NewStore(filepath.Join(parent, "artifacts"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	contents := []string{"prefix-race-12", "prefix-race-18"}
	digests := make([][sha256.Size]byte, len(contents))
	for index, content := range contents {
		digests[index] = sha256.Sum256([]byte(content))
	}
	if digests[0][0] != digests[1][0] {
		t.Fatal("test contents must share one SHA-256 prefix")
	}

	type result struct {
		publication *Publication
		err         error
	}
	results := make(chan result, len(contents))
	for _, content := range contents {
		go func() {
			publication, err := store.Publish(context.Background(), strings.NewReader(content))
			results <- result{publication: publication, err: err}
		}()
	}
	for range contents {
		outcome := <-results
		if outcome.err != nil {
			t.Fatalf("concurrent Publish() error = %v", outcome.err)
		}
		if err := outcome.publication.Release(); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		assertPathMode(t, outcome.publication.Artifact.Path, publishedFileMode)
	}
	prefix := filepath.Join(store.sha256Root, hex.EncodeToString(digests[0][:])[:2])
	assertPathMode(t, prefix, publishedDirectoryMode)
}

func TestPublishFsyncsIncomingPrefixAndHashParent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	var synced []string
	store, err := newStore(root, 1024, fileOps{
		now:    time.Now,
		rename: os.Rename,
		remove: os.Remove,
		syncDir: func(path string) error {
			synced = append(synced, path)
			return syncDirectory(path)
		},
	})
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	publication, err := store.Publish(context.Background(), strings.NewReader("durable directories"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	defer publication.Release()
	want := []string{store.incoming, filepath.Dir(publication.Artifact.Path), store.sha256Root}
	if !slicesEqual(synced, want) {
		t.Fatalf("synced directories = %v, want %v", synced, want)
	}
}

func TestPublishCleansTemporaryFileAfterReaderFailure(t *testing.T) {
	store := newTestStore(t, 1024)
	sourceErr := errors.New("source disconnected")
	_, err := store.Publish(context.Background(), &errorReader{data: []byte("partial"), err: sourceErr})
	assertErrorCode(t, err, ErrorIO)
	if !errors.Is(err, sourceErr) {
		t.Fatalf("Publish() error = %v, want wrapped source error", err)
	}
	assertDirectoryEmpty(t, store.incoming)
	assertNoArtifacts(t, store.sha256Root)
}

func TestPublishCancellationReturnedWithFinalReadCleansTemporary(t *testing.T) {
	store := newTestStore(t, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := store.Publish(ctx, &cancelingReader{cancel: cancel, data: []byte("canceled")})
	assertErrorCode(t, err, ErrorCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v, want wrapped cancellation", err)
	}
	assertDirectoryEmpty(t, store.incoming)
	assertNoArtifacts(t, store.sha256Root)
}

func TestSameHashConcurrentPublishIsIdempotentAndSerialized(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	firstStore, err := NewStore(root, 1024)
	if err != nil {
		t.Fatalf("NewStore(first) error = %v", err)
	}
	secondStore, err := NewStore(root, 1024)
	if err != nil {
		t.Fatalf("NewStore(second) error = %v", err)
	}
	content := []byte("same bytes")

	first, err := firstStore.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	type publishResult struct {
		publication *Publication
		err         error
	}
	started := make(chan struct{})
	completed := make(chan publishResult, 1)
	go func() {
		close(started)
		publication, publishErr := secondStore.Publish(context.Background(), bytes.NewReader(content))
		completed <- publishResult{publication: publication, err: publishErr}
	}()
	<-started
	select {
	case result := <-completed:
		if result.publication != nil {
			_ = result.publication.Release()
		}
		t.Fatalf("second Publish() completed while first publication lock was held: %v", result.err)
	case <-time.After(40 * time.Millisecond):
	}

	if err := first.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	select {
	case result := <-completed:
		if result.err != nil {
			t.Fatalf("second Publish() error = %v", result.err)
		}
		defer result.publication.Release()
		if result.publication.Artifact != first.Artifact {
			t.Fatalf("second artifact = %#v, want %#v", result.publication.Artifact, first.Artifact)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Publish() stayed blocked after first publication release")
	}
	assertDirectoryEmpty(t, firstStore.incoming)
}

func TestPublishRejectsExistingCorruptTargetWithoutReplacingIt(t *testing.T) {
	store := newTestStore(t, 1024)
	content := []byte("original")
	publication := publishAndRelease(t, store, content)
	corrupt := []byte("tampered")
	if len(corrupt) != len(content) {
		t.Fatal("test corruption must preserve size")
	}
	if err := os.WriteFile(publication.Artifact.Path, corrupt, publishedFileMode); err != nil {
		t.Fatalf("WriteFile(corrupt) error = %v", err)
	}

	_, err := store.Publish(context.Background(), bytes.NewReader(content))
	assertErrorCode(t, err, ErrorCorrupt)
	got, err := os.ReadFile(publication.Artifact.Path)
	if err != nil {
		t.Fatalf("ReadFile(corrupt target) error = %v", err)
	}
	if !bytes.Equal(got, corrupt) {
		t.Fatalf("corrupt target was replaced: got %q, want %q", got, corrupt)
	}
	assertDirectoryEmpty(t, store.incoming)
}

func TestPublishRejectsTargetSymlinkWithoutTouchingOutsideFile(t *testing.T) {
	store := newTestStore(t, 1024)
	content := []byte("symlink target")
	digest := sha256.Sum256(content)
	hashValue := hex.EncodeToString(digest[:])
	prefix := filepath.Join(store.sha256Root, hashValue[:2])
	if err := os.Mkdir(prefix, publishedDirectoryMode); err != nil {
		t.Fatalf("Mkdir(prefix) error = %v", err)
	}
	outside := filepath.Join(filepath.Dir(store.root), "outside-target")
	if err := os.WriteFile(outside, []byte("outside"), privateFileMode); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(prefix, hashValue)); err != nil {
		t.Fatalf("Symlink(target) error = %v", err)
	}

	_, err := store.Publish(context.Background(), bytes.NewReader(content))
	assertErrorCode(t, err, ErrorCorrupt)
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("ReadFile(outside) error = %v", err)
	}
	if string(got) != "outside" {
		t.Fatalf("outside file = %q, want outside", got)
	}
	assertDirectoryEmpty(t, store.incoming)
}

func TestPublishRejectsLockSymlinkWithoutTouchingOutsideFile(t *testing.T) {
	store := newTestStore(t, 1024)
	content := []byte("symlink lock")
	digest := sha256.Sum256(content)
	hashValue := hex.EncodeToString(digest[:])
	outside := filepath.Join(filepath.Dir(store.root), "outside-lock")
	if err := os.WriteFile(outside, []byte("outside"), privateFileMode); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(store.locks, hashValue)); err != nil {
		t.Fatalf("Symlink(lock) error = %v", err)
	}

	_, err := store.Publish(context.Background(), bytes.NewReader(content))
	assertErrorCode(t, err, ErrorCorrupt)
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("ReadFile(outside) error = %v", err)
	}
	if string(got) != "outside" {
		t.Fatalf("outside file = %q, want outside", got)
	}
	assertDirectoryEmpty(t, store.incoming)
}

func TestWorkerVerifyDetectsSizeAndDigestCorruption(t *testing.T) {
	store := newTestStore(t, 1024)
	publication := publishAndRelease(t, store, []byte("worker input"))
	emptyDigest := sha256.Sum256(nil)
	_, err := store.Verify(context.Background(), hex.EncodeToString(emptyDigest[:]), 0)
	assertErrorCode(t, err, ErrorEmptyArtifact)

	_, err = store.Verify(context.Background(), publication.Artifact.Hash, publication.Artifact.Size+1)
	assertErrorCode(t, err, ErrorCorrupt)

	corrupt := []byte("worker inpuX")
	if int64(len(corrupt)) != publication.Artifact.Size {
		t.Fatal("test corruption must preserve size")
	}
	if err := os.WriteFile(publication.Artifact.Path, corrupt, publishedFileMode); err != nil {
		t.Fatalf("WriteFile(corrupt) error = %v", err)
	}
	_, err = store.Verify(context.Background(), publication.Artifact.Hash, publication.Artifact.Size)
	assertErrorCode(t, err, ErrorCorrupt)
}

func TestCanceledLockWaitCleansTemporaryAndLockDescriptor(t *testing.T) {
	store := newTestStore(t, 1024)
	content := []byte("lock cancellation")
	first, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = store.Publish(ctx, bytes.NewReader(content))
	assertErrorCode(t, err, ErrorCanceled)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Publish() error = %v, want wrapped deadline", err)
	}
	assertDirectoryEmpty(t, store.incoming)

	if err := first.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	third, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish() after cancellation error = %v", err)
	}
	defer third.Release()
}

func TestPublishRenameFailureCleansTemporaryAndReleasesLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	renameErr := errors.New("rename failed")
	store, err := newStore(root, 1024, fileOps{
		now:    time.Now,
		remove: os.Remove,
		rename: func(string, string) error {
			return renameErr
		},
		syncDir: syncDirectory,
	})
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	content := []byte("rename boundary")
	_, err = store.Publish(context.Background(), bytes.NewReader(content))
	assertErrorCode(t, err, ErrorIO)
	if !errors.Is(err, renameErr) {
		t.Fatalf("Publish() error = %v, want wrapped rename error", err)
	}
	assertDirectoryEmpty(t, store.incoming)
	assertNoArtifacts(t, store.sha256Root)

	normalStore, err := NewStore(root, 1024)
	if err != nil {
		t.Fatalf("NewStore(normal) error = %v", err)
	}
	publication, err := normalStore.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish() after rename failure error = %v", err)
	}
	defer publication.Release()
}

func TestPublishSyncFailureLeavesVerifiableOrphanAndReleasesLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	syncErr := errors.New("directory fsync failed")
	store, err := newStore(root, 1024, fileOps{
		now:    time.Now,
		remove: os.Remove,
		rename: os.Rename,
		syncDir: func(path string) error {
			if filepath.Base(path) != "incoming" {
				return syncErr
			}
			return syncDirectory(path)
		},
	})
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	content := []byte("fsync boundary")
	_, err = store.Publish(context.Background(), bytes.NewReader(content))
	assertErrorCode(t, err, ErrorIO)
	if !errors.Is(err, syncErr) {
		t.Fatalf("Publish() error = %v, want wrapped fsync error", err)
	}
	assertDirectoryEmpty(t, store.incoming)

	normalStore, err := NewStore(root, 1024)
	if err != nil {
		t.Fatalf("NewStore(normal) error = %v", err)
	}
	digest := sha256.Sum256(content)
	hashValue := hex.EncodeToString(digest[:])
	if _, err := normalStore.Verify(context.Background(), hashValue, int64(len(content))); err != nil {
		t.Fatalf("Verify(orphan after fsync failure) error = %v", err)
	}
	publication, err := normalStore.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish() after fsync failure error = %v", err)
	}
	defer publication.Release()
}

func TestInvalidHashesCannotEscapeStoreRoot(t *testing.T) {
	store := newTestStore(t, 1024)
	outside := filepath.Join(filepath.Dir(store.root), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), privateFileMode); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}

	for _, hashValue := range []string{
		"../outside",
		strings.Repeat("a", 63) + "/",
		strings.Repeat("A", 64),
		strings.Repeat("g", 64),
	} {
		t.Run(hashValue, func(t *testing.T) {
			_, err := store.Verify(context.Background(), hashValue, 0)
			assertErrorCode(t, err, ErrorInvalidHash)
			_, err = store.ReconcileOrphan(context.Background(), hashValue, 0, func(context.Context, Artifact) (bool, error) {
				t.Fatal("reference callback must not run for invalid hash")
				return false, nil
			})
			assertErrorCode(t, err, ErrorInvalidHash)
		})
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("ReadFile(outside) error = %v", err)
	}
	if string(got) != "untouched" {
		t.Fatalf("outside file = %q, want untouched", got)
	}
}

func TestNewStoreRejectsRelativeRootAndInvalidLimit(t *testing.T) {
	_, err := NewStore("relative", 1)
	assertErrorCode(t, err, ErrorInvalidConfiguration)
	unnormalized := filepath.Join(t.TempDir(), "artifacts") + string(filepath.Separator) + ".." + string(filepath.Separator) + "artifacts"
	_, err = NewStore(unnormalized, 1)
	assertErrorCode(t, err, ErrorInvalidConfiguration)
	_, err = NewStore(string(filepath.Separator), 1)
	assertErrorCode(t, err, ErrorInvalidConfiguration)
	_, err = NewStore(filepath.Join(t.TempDir(), "artifacts"), 0)
	assertErrorCode(t, err, ErrorInvalidConfiguration)
}

type recordingReader struct {
	io.Reader
	maxBuffer int
}

func (r *recordingReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.maxBuffer {
		r.maxBuffer = len(buffer)
	}
	return r.Reader.Read(buffer)
}

type errorReader struct {
	data []byte
	err  error
	done bool
}

type gatedReader struct {
	entered chan struct{}
	proceed chan struct{}
	done    bool
}

type cancelingReader struct {
	cancel context.CancelFunc
	data   []byte
	done   bool
}

func (r *cancelingReader) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(buffer, r.data), io.EOF
}

func (r *gatedReader) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	close(r.entered)
	<-r.proceed
	return copy(buffer, "private temporary"), nil
}

func (r *errorReader) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(buffer, r.data), r.err
}

func newTestStore(t *testing.T, maxBytes int64) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "artifacts"), maxBytes)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func publishAndRelease(t *testing.T, store *Store, content []byte) *Publication {
	t.Helper()
	publication, err := store.Publish(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := publication.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	return publication
}

func assertErrorCode(t *testing.T, err error, expected ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", expected)
	}
	actual, ok := CodeOf(err)
	if !ok {
		t.Fatalf("error = %T %v, want StoreError code %q", err, err, expected)
	}
	if actual != expected {
		t.Fatalf("error code = %q, want %q (error: %v)", actual, expected, err)
	}
}

func assertPathMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", path, err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("mode for %q = %#o, want %#o", path, actual, expected)
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("ReadDir(%q) = %v, want empty", path, entries)
	}
}

func assertNoArtifacts(t *testing.T, shaRoot string) {
	t.Helper()
	var files []string
	err := filepath.WalkDir(shaRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", shaRoot, err)
	}
	if len(files) != 0 {
		t.Fatalf("artifact files = %v, want none", files)
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
