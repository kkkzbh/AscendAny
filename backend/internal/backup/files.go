package backup

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

var (
	bundleIDPattern      = regexp.MustCompile(`^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$`)
	sha256Pattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

const (
	backupRootMode        = 0o750
	backupBundleMode      = 0o750
	backupBundleFileMode  = 0o640
	backupPGPassFilename  = "backup.pgpass"
	restorePGPassFilename = "restore.pgpass"
)

func newBundleID(now time.Time, random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("random source is required")
	}
	suffix := make([]byte, 8)
	if _, err := io.ReadFull(random, suffix); err != nil {
		return "", fmt.Errorf("generate backup identifier: %w", err)
	}
	return "backup-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix), nil
}

func validateBundleID(value string) error {
	if !bundleIDPattern.MatchString(value) {
		return errors.New("backup id must have canonical backup-YYYYMMDDTHHMMSSZ-hex format")
	}
	return nil
}

func validateExistingDirectory(path string, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path must be a real directory")
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("directory mode must be %04o", mode)
	}
	return nil
}

func openOwnedRuntimeRoot(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime directory: %w", err)
	}
	if err := validateOwnedRuntimeDirectoryInfo(info); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.New("open runtime directory")
	}
	openedInfo, err := root.Lstat(".")
	if err != nil {
		_ = root.Close()
		return nil, errors.New("inspect opened runtime directory")
	}
	if err := validateOwnedRuntimeDirectoryInfo(openedInfo); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func validateOwnedRuntimeDirectoryInfo(info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("runtime root must be a real directory")
	}
	if info.Mode().Perm() != 0o700 {
		return errors.New("runtime root mode must be 0700")
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(metadata.Uid) != os.Geteuid() {
		return errors.New("runtime root must be owned by the current uid")
	}
	return nil
}

func requireRuntimeFileAbsent(root *os.Root, name string) error {
	if root == nil {
		return errors.New("runtime root is required")
	}
	if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return errors.New("inspect runtime pgpass file")
	}
	return errors.New("runtime pgpass file already exists")
}

func removePrivateRuntimeFile(root *os.Root, name string) error {
	if root == nil {
		return errors.New("runtime root is required")
	}
	info, err := root.Lstat(name)
	if err != nil {
		return errors.New("inspect runtime pgpass file")
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ok ||
		int(metadata.Uid) != os.Geteuid() || metadata.Nlink != 1 {
		return errors.New("runtime pgpass file metadata drifted")
	}
	if err := root.Remove(name); err != nil {
		return errors.New("remove runtime pgpass file")
	}
	return nil
}

func cleanupStaleBackupStaging(backupRoot string) error {
	root, err := os.OpenRoot(backupRoot)
	if err != nil {
		return errors.New("open backup root for stale staging cleanup")
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return errors.New("list backup root for stale staging cleanup")
	}
	staleNames := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".incoming-") {
			continue
		}
		backupID := strings.TrimPrefix(name, ".incoming-")
		if validateBundleID(backupID) != nil {
			return fmt.Errorf("unsafe backup staging entry %q", name)
		}
		info, err := root.Lstat(name)
		if err != nil {
			return errors.New("inspect stale backup staging entry")
		}
		metadata, ok := info.Sys().(*syscall.Stat_t)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 ||
			!ok || int(metadata.Uid) != os.Geteuid() {
			return fmt.Errorf("unsafe backup staging entry %q", name)
		}
		staleNames = append(staleNames, name)
	}
	for _, name := range staleNames {
		if err := root.RemoveAll(name); err != nil {
			return errors.New("remove stale backup staging entry")
		}
	}
	if len(staleNames) > 0 {
		if err := syncDirectory(backupRoot); err != nil {
			return errors.New("sync backup root after stale staging cleanup")
		}
	}
	return nil
}

func validateRegularFile(path string, mode fs.FileMode) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path must be a regular file")
	}
	if info.Mode().Perm() != mode {
		return nil, fmt.Errorf("file mode must be %04o", mode)
	}
	return info, nil
}

func createPrivateFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func syncAndClose(file *os.File) error {
	if file == nil {
		return errors.New("file is required")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncExistingFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	return syncAndClose(file)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func fileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func readExactBundleFile(root *os.Root, name string, maximum int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != backupBundleFileMode {
		return nil, fmt.Errorf("%s must be a regular %04o file", name, backupBundleFileMode)
	}
	if info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("%s has an invalid size", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) != info.Size() {
		return nil, fmt.Errorf("%s changed while it was read", name)
	}
	return contents, nil
}

func prepareBundleForPublication(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := ensureExactBundleEntries(root); err != nil {
		return err
	}
	for _, name := range []string{
		ArtifactArchiveFilename,
		DatabaseDumpFilename,
		ManifestDigestFilename,
		ManifestFilename,
	} {
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return fmt.Errorf("unpublished backup file %s must be a regular 0600 file", name)
		}
		if err := root.Chmod(name, backupBundleFileMode); err != nil {
			return err
		}
		if err := syncExistingFile(filepath.Join(path, name)); err != nil {
			return err
		}
	}
	if err := os.Chmod(path, backupBundleMode); err != nil {
		return err
	}
	return syncDirectory(path)
}

func ensureExactBundleEntries(root *os.Root) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("unexpected directory %q in backup bundle", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	expected := []string{ArtifactArchiveFilename, DatabaseDumpFilename, ManifestDigestFilename, ManifestFilename}
	sort.Strings(expected)
	if len(actual) != len(expected) {
		return fmt.Errorf("backup bundle contains %d entries; expected %d", len(actual), len(expected))
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return fmt.Errorf("unexpected backup bundle entry %q", actual[index])
		}
	}
	return nil
}

func secureRemoveAll(path, requiredParent string) error {
	if filepath.Dir(path) != requiredParent || path == requiredParent || !filepath.IsAbs(path) {
		return errors.New("refusing to remove path outside the required parent")
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || strings.Contains(base, string(filepath.Separator)) {
		return errors.New("refusing to remove invalid path")
	}
	return os.RemoveAll(path)
}

func defaultRandomReader() io.Reader {
	return rand.Reader
}
