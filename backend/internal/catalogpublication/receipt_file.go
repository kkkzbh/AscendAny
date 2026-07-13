package catalogpublication

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

const ProductionReceiptDirectory = "/var/lib/ascendany-catalog-publisher/receipts"

const (
	ReceiptDirectoryMode os.FileMode = 0o750
	ReceiptFileMode      os.FileMode = 0o640
)

// ReceiptPath names one immutable publication receipt by its database-owned
// publication identity.
func ReceiptPath(directory, knowledgeCatalogPublicationID string) (string, error) {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || filepath.Base(directory) == "." {
		return "", errors.New("publication receipt directory must be an absolute normalized path")
	}
	if !canonicalPositiveInt64(knowledgeCatalogPublicationID) {
		return "", errors.New("publication receipt identity must be a canonical positive int64")
	}
	return filepath.Join(directory, knowledgeCatalogPublicationID+".json"), nil
}

// WriteReceipt durably publishes one immutable canonical receipt. It uses an
// unnamed inode so interruption before the final link cannot leave a staging
// directory entry. An exact existing receipt is idempotent; every successful
// return includes an fsync of both the receipt inode and its directory.
func WriteReceipt(directoryPath, knowledgeCatalogPublicationID string, canonical []byte) (resultErr error) {
	targetPath, err := ReceiptPath(directoryPath, knowledgeCatalogPublicationID)
	if err != nil {
		return err
	}
	receipt, err := ParseReceipt(canonical)
	if err != nil {
		return fmt.Errorf("validate publication receipt: %w", err)
	}
	if receipt.KnowledgeCatalogPublicationID != knowledgeCatalogPublicationID {
		return errors.New("publication receipt identity does not match its filename")
	}

	directoryFD, err := unix.Open(directoryPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open publication receipt directory: %w", err)
	}
	directory := os.NewFile(uintptr(directoryFD), directoryPath)
	if directory == nil {
		_ = unix.Close(directoryFD)
		return errors.New("construct publication receipt directory handle")
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close publication receipt directory: %w", closeErr)
		}
	}()

	expectedUID := uint32(os.Geteuid())
	expectedGID := uint32(os.Getegid())
	var directoryStat unix.Stat_t
	if err := unix.Fstat(directoryFD, &directoryStat); err != nil {
		return fmt.Errorf("stat publication receipt directory: %w", err)
	}
	if err := validateReceiptDirectoryStat(directoryStat, expectedUID, expectedGID); err != nil {
		return err
	}

	// O_TMPFILE keeps the prepared receipt unreachable through the namespace.
	// Linking through /proc/self/fd avoids the capability required by
	// linkat(AT_EMPTY_PATH) while retaining a single no-replace commit point.
	temporaryFD, err := unix.Openat(
		directoryFD,
		".",
		unix.O_WRONLY|unix.O_TMPFILE|unix.O_CLOEXEC,
		uint32(ReceiptFileMode),
	)
	if err != nil {
		return fmt.Errorf("create unnamed publication receipt: %w", err)
	}
	temporary := os.NewFile(uintptr(temporaryFD), "publication-receipt")
	if temporary == nil {
		_ = unix.Close(temporaryFD)
		return errors.New("construct unnamed publication receipt handle")
	}
	defer func() {
		if closeErr := temporary.Close(); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close publication receipt: %w", closeErr)
		}
	}()

	if err := unix.Fchmod(temporaryFD, uint32(ReceiptFileMode)); err != nil {
		return fmt.Errorf("set publication receipt mode: %w", err)
	}
	var temporaryStat unix.Stat_t
	if err := unix.Fstat(temporaryFD, &temporaryStat); err != nil {
		return fmt.Errorf("stat unnamed publication receipt: %w", err)
	}
	if err := validateReceiptFileStat(temporaryStat, expectedUID, expectedGID, 0, "unnamed publication receipt"); err != nil {
		return err
	}
	if _, err := temporary.Write(canonical); err != nil {
		return fmt.Errorf("write publication receipt: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync unpublished publication receipt: %w", err)
	}

	targetName := filepath.Base(targetPath)
	procFDPath := "/proc/self/fd/" + strconv.Itoa(temporaryFD)
	if err := unix.Linkat(unix.AT_FDCWD, procFDPath, directoryFD, targetName, unix.AT_SYMLINK_FOLLOW); err != nil {
		if errors.Is(err, unix.EEXIST) {
			if existingErr := verifyExistingReceipt(directoryFD, targetName, canonical, expectedUID, expectedGID); existingErr != nil {
				return existingErr
			}
			return syncAndValidateReceiptDirectory(directory, directoryFD, expectedUID, expectedGID, "after idempotent replay")
		}
		return fmt.Errorf("publish publication receipt: %w", err)
	}

	if err := unix.Fstat(temporaryFD, &temporaryStat); err != nil {
		return fmt.Errorf("stat published publication receipt: %w", err)
	}
	if err := validateReceiptFileStat(temporaryStat, expectedUID, expectedGID, 1, "published publication receipt"); err != nil {
		return err
	}
	// Persist the link-count transition before persisting the directory entry.
	// A retry after either sync failure reaches the exact-byte EEXIST path and
	// completes both syncs before reporting success.
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync published publication receipt: %w", err)
	}
	return syncAndValidateReceiptDirectory(directory, directoryFD, expectedUID, expectedGID, "after publication")
}

func verifyExistingReceipt(
	directoryFD int,
	name string,
	expected []byte,
	expectedUID uint32,
	expectedGID uint32,
) (resultErr error) {
	fd, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open existing publication receipt: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("construct existing publication receipt handle")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close existing publication receipt: %w", closeErr)
		}
	}()

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("stat existing publication receipt: %w", err)
	}
	if err := validateReceiptFileStat(stat, expectedUID, expectedGID, 1, "existing publication receipt"); err != nil {
		return err
	}
	if stat.Size != int64(len(expected)) {
		return errors.New("publication receipt revision already contains different bytes")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	if err != nil {
		return fmt.Errorf("read existing publication receipt: %w", err)
	}
	if !bytes.Equal(raw, expected) {
		return errors.New("publication receipt revision already contains different bytes")
	}
	var afterRead unix.Stat_t
	if err := unix.Fstat(fd, &afterRead); err != nil {
		return fmt.Errorf("restat existing publication receipt: %w", err)
	}
	if !stableReceiptFileStat(stat, afterRead) {
		return errors.New("existing publication receipt changed while it was read")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync existing publication receipt: %w", err)
	}
	return nil
}

func syncAndValidateReceiptDirectory(
	directory *os.File,
	directoryFD int,
	expectedUID uint32,
	expectedGID uint32,
	phase string,
) error {
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync publication receipt directory %s: %w", phase, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(directoryFD, &stat); err != nil {
		return fmt.Errorf("restat publication receipt directory %s: %w", phase, err)
	}
	return validateReceiptDirectoryStat(stat, expectedUID, expectedGID)
}

func stableReceiptFileStat(before, after unix.Stat_t) bool {
	return before.Dev == after.Dev && before.Ino == after.Ino &&
		before.Mode == after.Mode && before.Nlink == after.Nlink &&
		before.Uid == after.Uid && before.Gid == after.Gid &&
		before.Size == after.Size && before.Mtim == after.Mtim && before.Ctim == after.Ctim
}

func validateReceiptDirectoryStat(stat unix.Stat_t, expectedUID, expectedGID uint32) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		os.FileMode(stat.Mode&0o777) != ReceiptDirectoryMode ||
		stat.Nlink != 2 || stat.Uid != expectedUID || stat.Gid != expectedGID {
		return fmt.Errorf(
			"publication receipt directory must be an owner-matched group-matched single-level directory with mode %04o",
			ReceiptDirectoryMode,
		)
	}
	return nil
}

func validateReceiptFileStat(
	stat unix.Stat_t,
	expectedUID uint32,
	expectedGID uint32,
	expectedLinks uint64,
	description string,
) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		os.FileMode(stat.Mode&0o777) != ReceiptFileMode ||
		stat.Nlink != expectedLinks || stat.Uid != expectedUID || stat.Gid != expectedGID {
		return fmt.Errorf("%s violates the immutable owner, group, mode, type, or link-count contract", description)
	}
	return nil
}
