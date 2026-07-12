// Package artifact provides the Linux content-addressed artifact store.
//
// A successful Publish returns a Publication that still owns the per-hash
// flock. The caller must persist the corresponding database reference before
// calling Release. Every database writer and orphan reconciler must follow the
// same lock protocol.
package artifact

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	privateDirectoryMode   = 0o700
	publishedDirectoryMode = 0o750
	privateFileMode        = 0o600
	publishedFileMode      = 0o640
	copyBuffer             = 64 * 1024
	lockPollDelay          = 5 * time.Millisecond
	incomingRandomBytes    = 16
	incomingLinkAttempts   = 16
)

// Artifact identifies a verified object. Path is always derived from Hash and
// the configured store root.
type Artifact struct {
	Hash       string
	Size       int64
	StorageKey string
	Path       string
	ModTime    time.Time
}

// Publication owns an exclusive per-hash flock. Release it only after the
// database transaction referencing Artifact has committed or failed.
type Publication struct {
	Artifact Artifact

	mu       sync.Mutex
	lockFile *os.File
	released bool
}

// Release relinquishes the per-hash flock. It is safe to call more than once;
// only the first call touches the file descriptor.
func (p *Publication) Release() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return nil
	}
	p.released = true
	if p.lockFile == nil {
		return storeError(ErrorInvalidArgument, "release publication", errors.New("publication has no lock"))
	}

	var result error
	if err := syscall.Flock(int(p.lockFile.Fd()), syscall.LOCK_UN); err != nil {
		result = storeError(ErrorIO, "unlock publication", err)
	}
	if err := p.lockFile.Close(); err != nil {
		closeErr := storeError(ErrorIO, "close publication lock", err)
		if result == nil {
			result = closeErr
		} else {
			result = errors.Join(result, closeErr)
		}
	}
	return result
}

type fileOps struct {
	now     func() time.Time
	rename  func(string, string) error
	remove  func(string) error
	syncDir func(string) error
}

// Store owns one exact on-disk layout:
//
//	<root>/incoming/<temporary>
//	<root>/sha256/<first-two-hex>/<64-lowercase-hex>
//	<root>/.locks/<64-lowercase-hex>
type Store struct {
	root       string
	incoming   string
	sha256Root string
	locks      string
	maxBytes   int64
	ops        fileOps
	prefixMu   sync.Mutex
}

// NewStore initializes the exact artifact layout. root must be absolute and
// maxBytes must be positive.
func NewStore(root string, maxBytes int64) (*Store, error) {
	return newStore(root, maxBytes, fileOps{
		now:     time.Now,
		rename:  os.Rename,
		remove:  os.Remove,
		syncDir: syncDirectory,
	})
}

func newStore(root string, maxBytes int64, ops fileOps) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, storeError(ErrorInvalidConfiguration, "initialize", errors.New("root must be an absolute path"))
	}
	if filepath.Clean(root) != root {
		return nil, storeError(ErrorInvalidConfiguration, "initialize", errors.New("root must be a normalized path"))
	}
	if root == string(filepath.Separator) {
		return nil, storeError(ErrorInvalidConfiguration, "initialize", errors.New("filesystem root is forbidden"))
	}
	if maxBytes <= 0 {
		return nil, storeError(ErrorInvalidConfiguration, "initialize", errors.New("maximum bytes must be positive"))
	}
	if ops.now == nil || ops.rename == nil || ops.remove == nil || ops.syncDir == nil {
		return nil, storeError(ErrorInvalidConfiguration, "initialize", errors.New("file operations must be complete"))
	}

	store := &Store{
		root:       root,
		incoming:   filepath.Join(root, "incoming"),
		sha256Root: filepath.Join(root, "sha256"),
		locks:      filepath.Join(root, ".locks"),
		maxBytes:   maxBytes,
		ops:        ops,
	}

	for _, directory := range []struct {
		path string
		mode os.FileMode
	}{
		{store.root, publishedDirectoryMode},
		{store.incoming, privateDirectoryMode},
		{store.sha256Root, publishedDirectoryMode},
		{store.locks, privateDirectoryMode},
	} {
		if err := ensureDirectory(directory.path, directory.mode); err != nil {
			return nil, storeError(ErrorIO, "initialize layout", err)
		}
	}
	if err := store.validateIncomingProtocol(); err != nil {
		return nil, storeError(ErrorInvalidConfiguration, "initialize incoming protocol", err)
	}
	return store, nil
}

// Publish streams an object into incoming, enforces the byte limit without
// writing an over-limit byte, fsyncs it, and publishes it by atomic rename.
// The returned Publication retains the per-hash lock.
func (s *Store) Publish(ctx context.Context, source io.Reader) (_ *Publication, resultErr error) {
	if ctx == nil {
		return nil, storeError(ErrorInvalidArgument, "publish", errors.New("context is required"))
	}
	if source == nil {
		return nil, storeError(ErrorInvalidArgument, "publish", errors.New("source is required"))
	}
	if err := contextError(ctx, "publish"); err != nil {
		return nil, err
	}

	temporary, temporaryPath, err := s.createLockedIncoming()
	if err != nil {
		return nil, err
	}

	defer func() {
		// The pathname is removed while the inode flock is still held. A
		// reconciler can therefore never unlink a live publisher's inode.
		if temporaryPath != "" {
			if err := s.ops.remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr := storeError(ErrorIO, "remove incoming file", err)
				if resultErr == nil {
					resultErr = cleanupErr
				} else {
					resultErr = errors.Join(resultErr, cleanupErr)
				}
			} else if err == nil {
				if syncErr := s.ops.syncDir(s.incoming); syncErr != nil {
					cleanupErr := storeError(ErrorIO, "fsync incoming directory", syncErr)
					if resultErr == nil {
						resultErr = cleanupErr
					} else {
						resultErr = errors.Join(resultErr, cleanupErr)
					}
				}
			}
		}
		if temporary != nil {
			if err := closeLockedIncoming(temporary); err != nil {
				if resultErr == nil {
					resultErr = err
				} else {
					resultErr = errors.Join(resultErr, err)
				}
			}
		}
	}()

	digest := sha256.New()
	size, err := copyLimited(ctx, temporary, digest, source, s.maxBytes)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, storeError(ErrorEmptyArtifact, "stream upload", errors.New("artifact must contain at least one byte"))
	}
	if err := temporary.Sync(); err != nil {
		return nil, storeError(ErrorIO, "fsync incoming file", err)
	}
	if err := contextError(ctx, "publish"); err != nil {
		return nil, err
	}

	hashValue := hex.EncodeToString(digest.Sum(nil))
	lockFile, err := s.lockHash(ctx, hashValue)
	if err != nil {
		return nil, err
	}
	publication := &Publication{lockFile: lockFile}
	keepLock := false
	defer func() {
		if !keepLock {
			if err := publication.Release(); err != nil {
				if resultErr == nil {
					resultErr = err
				} else {
					resultErr = errors.Join(resultErr, err)
				}
			}
		}
	}()

	prefix := filepath.Join(s.sha256Root, hashValue[:2])
	if err := s.ensurePublishedPrefix(prefix); err != nil {
		return nil, storeError(ErrorIO, "create hash prefix", err)
	}
	target := filepath.Join(prefix, hashValue)

	info, statErr := os.Lstat(target)
	switch {
	case statErr == nil:
		artifact, err := s.verifyPath(ctx, target, hashValue, size, info)
		if err != nil {
			return nil, err
		}
		if err := s.ops.remove(temporaryPath); err != nil {
			return nil, storeError(ErrorIO, "remove duplicate incoming file", err)
		}
		temporaryPath = ""
		if err := s.ops.syncDir(s.incoming); err != nil {
			return nil, storeError(ErrorIO, "fsync incoming directory", err)
		}
		if err := closeLockedIncoming(temporary); err != nil {
			temporary = nil
			return nil, err
		}
		temporary = nil
		publication.Artifact = artifact
		keepLock = true
		return publication, nil
	case !errors.Is(statErr, os.ErrNotExist):
		return nil, storeError(ErrorIO, "stat artifact target", statErr)
	}

	if err := temporary.Chmod(publishedFileMode); err != nil {
		return nil, storeError(ErrorIO, "set published file mode", err)
	}
	if err := temporary.Sync(); err != nil {
		return nil, storeError(ErrorIO, "fsync published file mode", err)
	}
	if err := contextError(ctx, "publish"); err != nil {
		return nil, err
	}
	if err := s.ops.rename(temporaryPath, target); err != nil {
		return nil, storeError(ErrorIO, "publish artifact", err)
	}
	temporaryPath = ""

	// Once renamed, finish the durability protocol even if the context is
	// canceled. Returning before these fsync calls would expose a non-durable
	// path to a later database transaction.
	for _, directory := range []struct {
		path string
		op   string
	}{
		{s.incoming, "fsync incoming directory"},
		{prefix, "fsync hash prefix"},
		{s.sha256Root, "fsync hash parent"},
	} {
		if err := s.ops.syncDir(directory.path); err != nil {
			return nil, storeError(ErrorIO, directory.op, err)
		}
	}
	if err := closeLockedIncoming(temporary); err != nil {
		temporary = nil
		return nil, err
	}
	temporary = nil
	if err := contextError(ctx, "publish"); err != nil {
		return nil, err
	}

	info, err = os.Lstat(target)
	if err != nil {
		return nil, storeError(ErrorIO, "stat published artifact", err)
	}
	artifact, err := s.verifyPath(ctx, target, hashValue, size, info)
	if err != nil {
		return nil, err
	}
	publication.Artifact = artifact
	keepLock = true
	return publication, nil
}

// createLockedIncoming creates an unnamed inode, takes its exclusive flock,
// sets its exact private mode, and only then links it into incoming. O_TMPFILE
// and linkat(AT_EMPTY_PATH) are intentional filesystem requirements: they make
// it impossible for reconciliation to observe an unlocked live pathname.
func (s *Store) createLockedIncoming() (_ *os.File, _ string, resultErr error) {
	fileDescriptor, err := unix.Open(
		s.incoming,
		unix.O_RDWR|unix.O_CLOEXEC|unix.O_TMPFILE,
		uint32(privateFileMode),
	)
	if err != nil {
		return nil, "", storeError(ErrorIO, "create anonymous incoming file", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), filepath.Join(s.incoming, "<anonymous>"))
	linkedPath := ""
	locked := false
	defer func() {
		if resultErr == nil {
			return
		}
		if linkedPath != "" {
			if err := os.Remove(linkedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, storeError(ErrorIO, "remove failed incoming link", err))
			} else if err == nil {
				if syncErr := syncDirectory(s.incoming); syncErr != nil {
					resultErr = errors.Join(resultErr, storeError(ErrorIO, "fsync incoming directory", syncErr))
				}
			}
		}
		if locked {
			if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
				resultErr = errors.Join(resultErr, storeError(ErrorIO, "unlock incoming file", err))
			}
		}
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, storeError(ErrorIO, "close incoming file", err))
		}
	}()

	if err := file.Chmod(privateFileMode); err != nil {
		return nil, "", storeError(ErrorIO, "set incoming file mode", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, "", storeError(ErrorIO, "lock incoming file", err)
	}
	locked = true

	directoryDescriptor, err := unix.Open(s.incoming, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", storeError(ErrorIO, "open incoming directory", err)
	}
	directory := os.NewFile(uintptr(directoryDescriptor), s.incoming)
	defer func() {
		if err := directory.Close(); err != nil {
			closeErr := storeError(ErrorIO, "close incoming directory", err)
			if resultErr == nil {
				resultErr = closeErr
			} else {
				resultErr = errors.Join(resultErr, closeErr)
			}
		}
	}()

	for range incomingLinkAttempts {
		name, err := newIncomingName()
		if err != nil {
			return nil, "", err
		}
		if err := unix.Linkat(int(file.Fd()), "", int(directory.Fd()), name, unix.AT_EMPTY_PATH); err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return nil, "", storeError(ErrorIO, "link incoming file", err)
		}
		linkedPath = filepath.Join(s.incoming, name)
		return file, linkedPath, nil
	}
	return nil, "", storeError(ErrorIO, "link incoming file", errors.New("exhausted unique incoming names"))
}

func (s *Store) validateIncomingProtocol() (resultErr error) {
	file, path, err := s.createLockedIncoming()
	if err != nil {
		return err
	}
	defer func() {
		if err := closeLockedIncoming(file); err != nil {
			if resultErr == nil {
				resultErr = err
			} else {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if err := os.Remove(path); err != nil {
		return storeError(ErrorIO, "remove incoming protocol probe", err)
	}
	if err := syncDirectory(s.incoming); err != nil {
		return storeError(ErrorIO, "fsync incoming protocol probe", err)
	}
	return nil
}

func newIncomingName() (string, error) {
	randomBytes := make([]byte, incomingRandomBytes)
	if _, err := io.ReadFull(rand.Reader, randomBytes); err != nil {
		return "", storeError(ErrorIO, "generate incoming file name", err)
	}
	return "upload-" + hex.EncodeToString(randomBytes), nil
}

func closeLockedIncoming(file *os.File) (resultErr error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		resultErr = storeError(ErrorIO, "unlock incoming file", err)
	}
	if err := file.Close(); err != nil {
		closeErr := storeError(ErrorIO, "close incoming file", err)
		if resultErr == nil {
			resultErr = closeErr
		} else {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}
	return resultErr
}

func (s *Store) ensurePublishedPrefix(path string) error {
	// Different hashes can share a prefix while using different per-hash locks.
	// Serialize the private-create/fchmod transition so another publisher never
	// observes a valid new prefix in its intentionally restrictive initial mode.
	s.prefixMu.Lock()
	defer s.prefixMu.Unlock()
	return ensureDirectory(path, publishedDirectoryMode)
}

// Verify rechecks a worker's database metadata against both the file size and
// the full SHA-256 content digest.
func (s *Store) Verify(ctx context.Context, hashValue string, expectedSize int64) (Artifact, error) {
	if ctx == nil {
		return Artifact{}, storeError(ErrorInvalidArgument, "verify", errors.New("context is required"))
	}
	path, err := s.artifactPath(hashValue)
	if err != nil {
		return Artifact{}, err
	}
	if expectedSize < 0 {
		return Artifact{}, storeError(ErrorInvalidArgument, "verify", errors.New("expected size must be positive"))
	}
	if expectedSize == 0 {
		return Artifact{}, storeError(ErrorEmptyArtifact, "verify", errors.New("artifact must contain at least one byte"))
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Artifact{}, storeError(ErrorNotFound, "verify", err)
	}
	if err != nil {
		return Artifact{}, storeError(ErrorIO, "stat artifact", err)
	}
	return s.verifyPath(ctx, path, hashValue, expectedSize, info)
}

func (s *Store) artifactPath(hashValue string) (string, error) {
	if err := validateHash(hashValue); err != nil {
		return "", err
	}
	return filepath.Join(s.sha256Root, hashValue[:2], hashValue), nil
}

func (s *Store) verifyPath(ctx context.Context, path, hashValue string, expectedSize int64, lstat os.FileInfo) (Artifact, error) {
	if !regularFileHasExactMode(lstat, publishedFileMode) {
		return Artifact{}, storeError(ErrorCorrupt, "verify", fmt.Errorf("artifact is not an exact mode %#o regular file", publishedFileMode))
	}
	if lstat.Size() != expectedSize {
		return Artifact{}, storeError(ErrorCorrupt, "verify", fmt.Errorf("size is %d, expected %d", lstat.Size(), expectedSize))
	}

	fileDescriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, syscall.ELOOP) {
		return Artifact{}, storeError(ErrorCorrupt, "verify", errors.New("artifact path is a symbolic link"))
	}
	if errors.Is(err, os.ErrNotExist) {
		return Artifact{}, storeError(ErrorNotFound, "verify", err)
	}
	if err != nil {
		return Artifact{}, storeError(ErrorIO, "open artifact", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), path)
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return Artifact{}, storeError(ErrorIO, "stat opened artifact", err)
	}
	if !regularFileHasExactMode(openedInfo, publishedFileMode) || !os.SameFile(lstat, openedInfo) {
		return Artifact{}, storeError(ErrorCorrupt, "verify", errors.New("artifact changed while opening"))
	}

	digest := sha256.New()
	readSize, err := copyWithContext(ctx, digest, file)
	if err != nil {
		return Artifact{}, err
	}
	if readSize != expectedSize {
		return Artifact{}, storeError(ErrorCorrupt, "verify", fmt.Errorf("read %d bytes, expected %d", readSize, expectedSize))
	}
	actualHash := hex.EncodeToString(digest.Sum(nil))
	if actualHash != hashValue {
		return Artifact{}, storeError(ErrorCorrupt, "verify", fmt.Errorf("content digest is %s, expected %s", actualHash, hashValue))
	}
	return Artifact{
		Hash:       hashValue,
		Size:       expectedSize,
		StorageKey: storageKey(hashValue),
		Path:       path,
		ModTime:    openedInfo.ModTime(),
	}, nil
}

func regularFileHasExactMode(info os.FileInfo, expected os.FileMode) bool {
	const specialPermissions = os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	return info.Mode().IsRegular() && info.Mode()&(os.ModePerm|specialPermissions) == expected
}

func storageKey(hashValue string) string {
	return "sha256/" + hashValue[:2] + "/" + hashValue
}

func (s *Store) lockHash(ctx context.Context, hashValue string) (*os.File, error) {
	if err := validateHash(hashValue); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(s.locks, hashValue)
	fileDescriptor, err := syscall.Open(
		lockPath,
		syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
		privateFileMode,
	)
	if errors.Is(err, syscall.ELOOP) {
		return nil, storeError(ErrorCorrupt, "open hash lock", errors.New("hash lock path is a symbolic link"))
	}
	if err != nil {
		return nil, storeError(ErrorIO, "open hash lock", err)
	}
	lockFile := os.NewFile(uintptr(fileDescriptor), lockPath)
	lockInfo, err := lockFile.Stat()
	if err != nil {
		_ = lockFile.Close()
		return nil, storeError(ErrorIO, "stat hash lock", err)
	}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != privateFileMode {
		_ = lockFile.Close()
		return nil, storeError(ErrorCorrupt, "open hash lock", errors.New("hash lock must be a mode 0600 regular file"))
	}
	if err := lockExclusive(ctx, lockFile); err != nil {
		if closeErr := lockFile.Close(); closeErr != nil {
			return nil, errors.Join(err, storeError(ErrorIO, "close hash lock", closeErr))
		}
		return nil, err
	}
	return lockFile, nil
}

func lockExclusive(ctx context.Context, file *os.File) error {
	for {
		if err := contextError(ctx, "acquire hash lock"); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return storeError(ErrorIO, "acquire hash lock", err)
		}

		timer := time.NewTimer(lockPollDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return storeError(ErrorCanceled, "acquire hash lock", ctx.Err())
		case <-timer.C:
		}
	}
}

func copyLimited(ctx context.Context, destination io.Writer, digest hash.Hash, source io.Reader, limit int64) (int64, error) {
	buffer := make([]byte, copyBuffer)
	var total int64
	emptyReads := 0
	for {
		if err := contextError(ctx, "stream upload"); err != nil {
			return 0, err
		}
		remaining := limit - total
		readLimit := int64(len(buffer))
		if remaining < readLimit {
			readLimit = remaining + 1
		}
		n, readErr := source.Read(buffer[:readLimit])
		if err := contextError(ctx, "stream upload"); err != nil {
			return 0, err
		}
		if n < 0 || n > int(readLimit) {
			return 0, storeError(ErrorIO, "read upload", errors.New("reader returned an invalid byte count"))
		}
		if n > 0 {
			emptyReads = 0
			if int64(n) > remaining {
				return 0, storeError(ErrorPayloadTooLarge, "stream upload", fmt.Errorf("payload exceeds %d bytes", limit))
			}
			if err := writeAll(destination, buffer[:n]); err != nil {
				return 0, storeError(ErrorIO, "write incoming file", err)
			}
			if _, err := digest.Write(buffer[:n]); err != nil {
				return 0, storeError(ErrorIO, "hash upload", err)
			}
			total += int64(n)
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return 0, storeError(ErrorIO, "stream upload", io.ErrNoProgress)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if err := contextError(ctx, "stream upload"); err != nil {
					return 0, err
				}
				return total, nil
			}
			return 0, storeError(ErrorIO, "read upload", readErr)
		}
	}
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, copyBuffer)
	var total int64
	emptyReads := 0
	for {
		if err := contextError(ctx, "read artifact"); err != nil {
			return 0, err
		}
		n, readErr := source.Read(buffer)
		if n < 0 || n > len(buffer) {
			return 0, storeError(ErrorIO, "read artifact", errors.New("reader returned an invalid byte count"))
		}
		if n > 0 {
			emptyReads = 0
			if err := writeAll(destination, buffer[:n]); err != nil {
				return 0, storeError(ErrorIO, "hash artifact", err)
			}
			total += int64(n)
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return 0, storeError(ErrorIO, "read artifact", io.ErrNoProgress)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if err := contextError(ctx, "read artifact"); err != nil {
					return 0, err
				}
				return total, nil
			}
			return 0, storeError(ErrorIO, "read artifact", readErr)
		}
	}
}

func writeAll(destination io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := destination.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func validateHash(hashValue string) error {
	if len(hashValue) != sha256.Size*2 {
		return storeError(ErrorInvalidHash, "validate hash", errors.New("hash must contain exactly 64 lowercase hexadecimal characters"))
	}
	for _, character := range []byte(hashValue) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return storeError(ErrorInvalidHash, "validate hash", errors.New("hash must contain exactly 64 lowercase hexadecimal characters"))
		}
	}
	return nil
}

func contextError(ctx context.Context, op string) error {
	if err := ctx.Err(); err != nil {
		return storeError(ErrorCanceled, op, err)
	}
	return nil
}

func ensureDirectory(path string, expectedMode os.FileMode) error {
	info, err := os.Lstat(path)
	if err == nil {
		return validateDirectory(path, info, expectedMode)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, expectedMode); err != nil {
		return err
	}

	// Mkdir and MkdirAll apply the process umask. Open the newly created path
	// without following symlinks and set the store contract on that exact inode.
	// Existing directories were validated above and are never silently repaired.
	fileDescriptor, err := syscall.Open(
		path,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(fileDescriptor), path)
	defer directory.Close()
	if err := directory.Chmod(expectedMode); err != nil {
		return err
	}
	openedInfo, err := directory.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("%s changed while setting its mode", path)
	}
	return validateDirectory(path, openedInfo, expectedMode)
}

func validateDirectory(path string, info os.FileInfo, expectedMode os.FileMode) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real directory", path)
	}
	if info.Mode().Perm() != expectedMode {
		return fmt.Errorf("%s has mode %#o, expected %#o", path, info.Mode().Perm(), expectedMode)
	}
	return nil
}

func syncDirectory(path string) (resultErr error) {
	fileDescriptor, err := syscall.Open(
		path,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(fileDescriptor), path)
	defer func() {
		if err := directory.Close(); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	return directory.Sync()
}
