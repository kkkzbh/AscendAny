package artifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ReferenceCheck runs while the artifact's exclusive hash lock is held. A
// writer that creates database references must hold the same lock through its
// commit, so this callback observes every completed publication transaction.
type ReferenceCheck func(context.Context, Artifact) (bool, error)

type ReconcileDisposition string

const (
	ReconcileRetainedReferenced ReconcileDisposition = "retained_referenced"
	ReconcileRetainedTooYoung   ReconcileDisposition = "retained_too_young"
	ReconcileRemoved            ReconcileDisposition = "removed"
)

type ReconcileResult struct {
	Artifact    Artifact
	Disposition ReconcileDisposition
}

// ReconcilePublished walks the exact content-addressed layout and applies the
// same per-hash reconciliation protocol to every published object. Any unknown
// entry is treated as corruption so cleanup cannot silently skip an invalid
// storage layout.
func (s *Store) ReconcilePublished(
	ctx context.Context,
	minAge time.Duration,
	referenceCheck ReferenceCheck,
) ([]ReconcileResult, error) {
	if ctx == nil {
		return nil, storeError(ErrorInvalidArgument, "reconcile published artifacts", errors.New("context is required"))
	}
	if minAge < 0 {
		return nil, storeError(ErrorInvalidArgument, "reconcile published artifacts", errors.New("minimum age must be non-negative"))
	}
	if referenceCheck == nil {
		return nil, storeError(ErrorInvalidArgument, "reconcile published artifacts", errors.New("reference check is required"))
	}
	prefixes, err := os.ReadDir(s.sha256Root)
	if err != nil {
		return nil, storeError(ErrorIO, "list published hash prefixes", err)
	}
	results := make([]ReconcileResult, 0)
	for _, prefix := range prefixes {
		if err := contextError(ctx, "reconcile published artifacts"); err != nil {
			return nil, err
		}
		if !validHashPrefix(prefix.Name()) || prefix.Type()&os.ModeSymlink != 0 || !prefix.IsDir() {
			return nil, storeError(ErrorCorrupt, "inspect published hash prefixes", fmt.Errorf("unexpected entry %q", prefix.Name()))
		}
		prefixPath := filepath.Join(s.sha256Root, prefix.Name())
		prefixInfo, err := prefix.Info()
		if err != nil {
			return nil, storeError(ErrorIO, "stat published hash prefix", err)
		}
		if prefixInfo.Mode().Perm() != publishedDirectoryMode {
			return nil, storeError(ErrorCorrupt, "inspect published hash prefix", fmt.Errorf("%q has mode %#o", prefix.Name(), prefixInfo.Mode().Perm()))
		}
		entries, err := os.ReadDir(prefixPath)
		if err != nil {
			return nil, storeError(ErrorIO, "list published artifacts", err)
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || validateHash(entry.Name()) != nil || !strings.HasPrefix(entry.Name(), prefix.Name()) {
				return nil, storeError(ErrorCorrupt, "inspect published artifact", fmt.Errorf("unexpected entry %q/%q", prefix.Name(), entry.Name()))
			}
			result, err := s.ReconcileOrphan(ctx, entry.Name(), minAge, referenceCheck)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}
	return results, nil
}

// ReconcileIncoming removes crash-left incoming files only after the age gate
// and after taking a nonblocking exclusive flock on the exact opened inode.
// Active publishers retain that inode flock for the pathname's full lifetime.
func (s *Store) ReconcileIncoming(ctx context.Context, minAge time.Duration) (removed int, resultErr error) {
	if ctx == nil {
		return 0, storeError(ErrorInvalidArgument, "reconcile incoming artifacts", errors.New("context is required"))
	}
	if minAge < 0 {
		return 0, storeError(ErrorInvalidArgument, "reconcile incoming artifacts", errors.New("minimum age must be non-negative"))
	}
	entries, err := os.ReadDir(s.incoming)
	if err != nil {
		return 0, storeError(ErrorIO, "list incoming artifacts", err)
	}
	defer func() {
		if removed == 0 {
			return
		}
		if err := s.ops.syncDir(s.incoming); err != nil {
			syncErr := storeError(ErrorIO, "fsync incoming directory", err)
			if resultErr == nil {
				resultErr = syncErr
			} else {
				resultErr = errors.Join(resultErr, syncErr)
			}
		}
	}()
	for _, entry := range entries {
		if err := contextError(ctx, "reconcile incoming artifacts"); err != nil {
			return removed, err
		}
		if !validIncomingName(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return removed, storeError(ErrorCorrupt, "inspect incoming artifact", fmt.Errorf("unexpected entry %q", entry.Name()))
		}
		candidateRemoved, err := s.reconcileIncomingEntry(ctx, entry.Name(), minAge)
		if candidateRemoved {
			removed++
		}
		if err != nil {
			return removed, err
		}
		if err := contextError(ctx, "reconcile incoming artifacts"); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func (s *Store) reconcileIncomingEntry(ctx context.Context, name string, minAge time.Duration) (_ bool, resultErr error) {
	path := filepath.Join(s.incoming, name)
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, storeError(ErrorIO, "stat incoming artifact", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return false, storeError(ErrorCorrupt, "inspect incoming artifact", fmt.Errorf("%q is a symbolic link", name))
	}

	fileDescriptor, err := syscall.Open(
		path,
		syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
		0,
	)
	if errors.Is(err, syscall.ELOOP) {
		return false, storeError(ErrorCorrupt, "open incoming artifact", fmt.Errorf("%q is a symbolic link", name))
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, storeError(ErrorIO, "open incoming artifact", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), path)
	locked := false
	defer func() {
		if locked {
			if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
				unlockErr := storeError(ErrorIO, "unlock incoming candidate", err)
				if resultErr == nil {
					resultErr = unlockErr
				} else {
					resultErr = errors.Join(resultErr, unlockErr)
				}
			}
		}
		if err := file.Close(); err != nil {
			closeErr := storeError(ErrorIO, "close incoming candidate", err)
			if resultErr == nil {
				resultErr = closeErr
			} else {
				resultErr = errors.Join(resultErr, closeErr)
			}
		}
	}()

	openedInfo, err := file.Stat()
	if err != nil {
		return false, storeError(ErrorIO, "stat opened incoming artifact", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return false, storeError(ErrorCorrupt, "inspect incoming artifact", fmt.Errorf("%q changed while opening", name))
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	if err != nil {
		return false, storeError(ErrorIO, "lock incoming candidate", err)
	}
	locked = true

	openedInfo, err = file.Stat()
	if err != nil {
		return false, storeError(ErrorIO, "restat opened incoming artifact", err)
	}
	pathInfo, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, storeError(ErrorIO, "restat incoming artifact", err)
	}
	if !os.SameFile(pathInfo, openedInfo) {
		return false, storeError(ErrorCorrupt, "inspect incoming artifact", fmt.Errorf("%q changed before reconciliation", name))
	}
	if !regularFileHasExactMode(openedInfo, privateFileMode) {
		return false, storeError(ErrorCorrupt, "inspect incoming artifact", fmt.Errorf("%q is not an exact mode 0600 regular file", name))
	}
	if s.ops.now().Before(openedInfo.ModTime().Add(minAge)) {
		return false, nil
	}
	if err := contextError(ctx, "reconcile incoming artifacts"); err != nil {
		return false, err
	}

	// Recheck the pathname immediately before unlink. Unique cryptographic
	// names and the flock protocol exclude valid writers from replacing it.
	finalPathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, storeError(ErrorIO, "final stat incoming artifact", err)
	}
	if !os.SameFile(finalPathInfo, openedInfo) {
		return false, storeError(ErrorCorrupt, "inspect incoming artifact", fmt.Errorf("%q changed before unlink", name))
	}
	if err := s.ops.remove(path); err != nil {
		return false, storeError(ErrorIO, "remove incoming artifact", err)
	}
	return true, nil
}

func validHashPrefix(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, character := range []byte(value) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// ReconcileOrphan removes one unreferenced artifact only after taking the same
// per-hash lock used by Publish, rechecking the database reference, and
// restating the file's age under that lock.
func (s *Store) ReconcileOrphan(
	ctx context.Context,
	hashValue string,
	minAge time.Duration,
	referenceCheck ReferenceCheck,
) (_ ReconcileResult, resultErr error) {
	if ctx == nil {
		return ReconcileResult{}, storeError(ErrorInvalidArgument, "reconcile orphan", errors.New("context is required"))
	}
	if minAge < 0 {
		return ReconcileResult{}, storeError(ErrorInvalidArgument, "reconcile orphan", errors.New("minimum age must be non-negative"))
	}
	if referenceCheck == nil {
		return ReconcileResult{}, storeError(ErrorInvalidArgument, "reconcile orphan", errors.New("reference check is required"))
	}
	path, err := s.artifactPath(hashValue)
	if err != nil {
		return ReconcileResult{}, err
	}

	lockFile, err := s.lockHash(ctx, hashValue)
	if err != nil {
		return ReconcileResult{}, err
	}
	publication := &Publication{lockFile: lockFile}
	defer func() {
		if err := publication.Release(); err != nil {
			if resultErr == nil {
				resultErr = err
			} else {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()

	firstInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ReconcileResult{}, storeError(ErrorNotFound, "reconcile orphan", err)
	}
	if err != nil {
		return ReconcileResult{}, storeError(ErrorIO, "stat orphan candidate", err)
	}
	if !regularFileHasExactMode(firstInfo, publishedFileMode) {
		return ReconcileResult{}, storeError(ErrorCorrupt, "reconcile orphan", fmt.Errorf("artifact is not an exact mode %#o regular file", publishedFileMode))
	}
	artifact := Artifact{
		Hash:       hashValue,
		Size:       firstInfo.Size(),
		StorageKey: storageKey(hashValue),
		Path:       path,
		ModTime:    firstInfo.ModTime(),
	}

	referenced, err := referenceCheck(ctx, artifact)
	if err != nil {
		return ReconcileResult{}, storeError(ErrorReferenceCheck, "reconcile orphan", err)
	}
	if err := contextError(ctx, "reconcile orphan"); err != nil {
		return ReconcileResult{}, err
	}
	if referenced {
		verified, err := s.verifyPath(ctx, path, hashValue, firstInfo.Size(), firstInfo)
		if err != nil {
			return ReconcileResult{}, err
		}
		return ReconcileResult{Artifact: verified, Disposition: ReconcileRetainedReferenced}, nil
	}

	// Restat after the database callback. Age and identity used for deletion are
	// therefore both observed immediately before unlink while the lock is held.
	secondInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ReconcileResult{}, storeError(ErrorNotFound, "reconcile orphan", err)
	}
	if err != nil {
		return ReconcileResult{}, storeError(ErrorIO, "restat orphan candidate", err)
	}
	if !regularFileHasExactMode(secondInfo, publishedFileMode) || !os.SameFile(firstInfo, secondInfo) {
		return ReconcileResult{}, storeError(ErrorCorrupt, "reconcile orphan", errors.New("artifact changed during reconciliation"))
	}
	artifact.Size = secondInfo.Size()
	artifact.ModTime = secondInfo.ModTime()
	if s.ops.now().Before(secondInfo.ModTime().Add(minAge)) {
		return ReconcileResult{Artifact: artifact, Disposition: ReconcileRetainedTooYoung}, nil
	}
	if err := contextError(ctx, "reconcile orphan"); err != nil {
		return ReconcileResult{}, err
	}

	if err := s.ops.remove(path); err != nil {
		return ReconcileResult{}, storeError(ErrorIO, "unlink orphan artifact", err)
	}
	prefix := filepath.Dir(path)
	if err := s.ops.syncDir(prefix); err != nil {
		return ReconcileResult{}, storeError(ErrorIO, "fsync orphan hash prefix", err)
	}
	return ReconcileResult{Artifact: artifact, Disposition: ReconcileRemoved}, nil
}

func validIncomingName(value string) bool {
	const prefix = "upload-"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+incomingRandomBytes*2 {
		return false
	}
	for _, character := range []byte(value[len(prefix):]) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
