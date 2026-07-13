package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	targetName        = "v2"
	stageNamePrefix   = ".v2.installing."
	removeNamePrefix  = ".v2.removing."
	stageSuffixSize   = 10
	committedExitCode = 3
)

type command struct {
	operation              operation
	parentFD               int
	stageName              string
	expectedDev            uint64
	expectedInode          uint64
	expectedInstalledDev   uint64
	expectedInstalledInode uint64
	removeName             string
	expectedTargetDev      uint64
	expectedTargetInode    uint64
}

type operation uint8

const (
	operationPromote operation = iota + 1
	operationReplace
	operationRemoveRetired
)

type committedOperationError struct {
	err error
}

func (err committedOperationError) Error() string {
	return err.err.Error()
}

func (err committedOperationError) Unwrap() error {
	return err.err
}

func committed(err error) error {
	return committedOperationError{err: err}
}

type ownership struct {
	uid uint32
	gid uint32
}

func main() {
	if err := validateBootstrapEnvironment(os.Environ()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release operation environment rejected: %v\n", err)
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], os.Stderr, os.Geteuid()))
}

func validateBootstrapEnvironment(entries []string) error {
	required := map[string]string{
		"ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV": "1",
		"LC_ALL":                                "C",
		"PATH":                                  "/usr/bin:/bin",
	}
	allowedDynamic := map[string]bool{"PWD": true, "SHLVL": true, "_": true}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || seen[name] {
			return errors.New("environment contains an invalid or duplicate entry")
		}
		seen[name] = true
		if expected, ok := required[name]; ok {
			if value != expected {
				return fmt.Errorf("%s has an invalid value", name)
			}
			continue
		}
		if !allowedDynamic[name] {
			return fmt.Errorf("undeclared variable %s", name)
		}
		if value == "" {
			return fmt.Errorf("%s is empty", name)
		}
	}
	for name := range required {
		if !seen[name] {
			return fmt.Errorf("required variable %s is missing", name)
		}
	}
	return nil
}

func run(args []string, stderr io.Writer, effectiveUID int) int {
	if effectiveUID != 0 {
		_, _ = fmt.Fprintln(stderr, "ascendany-release-ops requires effective UID 0")
		return 1
	}
	parsed, err := parseCommand(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "usage: ascendany-release-ops promote --parent-fd FD --stage-name .v2.installing.<10 safe alnum> --expected-device DECIMAL --expected-inode DECIMAL")
		_, _ = fmt.Fprintln(stderr, "   or: ascendany-release-ops replace --parent-fd FD --stage-name .v2.installing.<10 safe alnum> --expected-device DECIMAL --expected-inode DECIMAL --expected-installed-device DECIMAL --expected-installed-inode DECIMAL")
		_, _ = fmt.Fprintln(stderr, "   or: ascendany-release-ops remove-retired --parent-fd FD --stage-name .v2.installing.<10 safe alnum> --remove-name .v2.removing.<same suffix> --expected-device DECIMAL --expected-inode DECIMAL --expected-target-device DECIMAL --expected-target-inode DECIMAL")
		return 2
	}
	var operationErr error
	switch parsed.operation {
	case operationPromote:
		operationErr = promote(parsed, ownership{uid: 0, gid: 0})
	case operationReplace:
		operationErr = replace(parsed, ownership{uid: 0, gid: 0})
	case operationRemoveRetired:
		operationErr = removeRetired(parsed, ownership{uid: 0, gid: 0})
	default:
		operationErr = errors.New("unknown release operation")
	}
	if operationErr != nil {
		_, _ = fmt.Fprintf(stderr, "release operation failed: %v\n", operationErr)
		var committedErr committedOperationError
		if errors.As(operationErr, &committedErr) {
			return committedExitCode
		}
		return 1
	}
	return 0
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New("release operation is missing")
	}
	switch args[0] {
	case "promote":
		return parsePromoteCommand(args)
	case "replace":
		return parseReplaceCommand(args)
	case "remove-retired":
		return parseRemoveRetiredCommand(args)
	default:
		return command{}, errors.New("invalid release operation")
	}
}

func parseRemoveRetiredCommand(args []string) (command, error) {
	if len(args) != 15 ||
		args[1] != "--parent-fd" ||
		args[3] != "--stage-name" ||
		args[5] != "--remove-name" ||
		args[7] != "--expected-device" ||
		args[9] != "--expected-inode" ||
		args[11] != "--expected-target-device" ||
		args[13] != "--expected-target-inode" {
		return command{}, errors.New("invalid remove-retired arguments")
	}
	parentFD, err := parseFD(args[2])
	if err != nil {
		return command{}, fmt.Errorf("parent fd: %w", err)
	}
	if !validStageName(args[4]) {
		return command{}, errors.New("invalid stage name")
	}
	if !validRemoveName(args[6]) || strings.TrimPrefix(args[4], stageNamePrefix) != strings.TrimPrefix(args[6], removeNamePrefix) {
		return command{}, errors.New("invalid remove name")
	}
	expectedDev, err := parseDecimal(args[8], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected device: %w", err)
	}
	expectedInode, err := parseDecimal(args[10], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected inode: %w", err)
	}
	expectedTargetDev, err := parseDecimal(args[12], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected target device: %w", err)
	}
	expectedTargetInode, err := parseDecimal(args[14], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected target inode: %w", err)
	}
	return command{
		operation:           operationRemoveRetired,
		parentFD:            parentFD,
		stageName:           args[4],
		removeName:          args[6],
		expectedDev:         expectedDev,
		expectedInode:       expectedInode,
		expectedTargetDev:   expectedTargetDev,
		expectedTargetInode: expectedTargetInode,
	}, nil
}

func parsePromoteCommand(args []string) (command, error) {
	if len(args) != 9 ||
		args[1] != "--parent-fd" ||
		args[3] != "--stage-name" ||
		args[5] != "--expected-device" ||
		args[7] != "--expected-inode" {
		return command{}, errors.New("invalid command arguments")
	}

	parentFD, err := parseFD(args[2])
	if err != nil {
		return command{}, fmt.Errorf("parent fd: %w", err)
	}
	if !validStageName(args[4]) {
		return command{}, errors.New("invalid stage name")
	}
	expectedDev, err := parseDecimal(args[6], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected device: %w", err)
	}
	expectedInode, err := parseDecimal(args[8], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected inode: %w", err)
	}

	return command{
		operation:     operationPromote,
		parentFD:      parentFD,
		stageName:     args[4],
		expectedDev:   expectedDev,
		expectedInode: expectedInode,
	}, nil
}

func parseReplaceCommand(args []string) (command, error) {
	if len(args) != 13 ||
		args[1] != "--parent-fd" ||
		args[3] != "--stage-name" ||
		args[5] != "--expected-device" ||
		args[7] != "--expected-inode" ||
		args[9] != "--expected-installed-device" ||
		args[11] != "--expected-installed-inode" {
		return command{}, errors.New("invalid replace arguments")
	}

	parsed, err := parsePromoteCommand(args[:9])
	if err != nil {
		return command{}, err
	}
	parsed.operation = operationReplace
	parsed.expectedInstalledDev, err = parseDecimal(args[10], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected installed device: %w", err)
	}
	parsed.expectedInstalledInode, err = parseDecimal(args[12], 64)
	if err != nil {
		return command{}, fmt.Errorf("expected installed inode: %w", err)
	}
	return parsed, nil
}

func parseFD(value string) (int, error) {
	parsed, err := parseDecimal(value, 31)
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func parseDecimal(value string, bitSize int) (uint64, error) {
	if value == "" {
		return 0, errors.New("decimal value is empty")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("value is not decimal")
		}
	}
	parsed, err := strconv.ParseUint(value, 10, bitSize)
	if err != nil {
		return 0, errors.New("decimal value is out of range")
	}
	return parsed, nil
}

func validStageName(name string) bool {
	return validPrivateName(name, stageNamePrefix)
}

func validRemoveName(name string) bool {
	return validPrivateName(name, removeNamePrefix)
}

func validPrivateName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+stageSuffixSize {
		return false
	}
	for _, character := range name[len(prefix):] {
		if (character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func promote(parsed command, expectedOwner ownership) error {
	return promoteWithBeforeRename(parsed, expectedOwner, nil)
}

func promoteWithBeforeRename(parsed command, expectedOwner ownership, beforeRename func() error) error {
	var parentStat unix.Stat_t
	if err := unix.Fstat(parsed.parentFD, &parentStat); err != nil {
		return fmt.Errorf("inspect parent directory fd: %w", err)
	}
	if err := validateDirectory(parentStat, expectedOwner); err != nil {
		return fmt.Errorf("parent directory rejected: %w", err)
	}

	stageFD, err := unix.Openat(
		parsed.parentFD,
		parsed.stageName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return fmt.Errorf("open stage directory: %w", err)
	}
	defer unix.Close(stageFD)

	var stageStat unix.Stat_t
	if err := unix.Fstat(stageFD, &stageStat); err != nil {
		return fmt.Errorf("inspect stage directory: %w", err)
	}
	if err := validateDirectory(stageStat, expectedOwner); err != nil {
		return fmt.Errorf("stage directory rejected: %w", err)
	}
	if uint64(stageStat.Dev) != parsed.expectedDev || stageStat.Ino != parsed.expectedInode {
		return errors.New("stage directory identity does not match the verified identity")
	}

	var targetStat unix.Stat_t
	if err := unix.Fstatat(parsed.parentFD, targetName, &targetStat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		return errors.New("target already exists")
	} else if !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("inspect target: %w", err)
	}

	// Fail before publication when the inherited descriptor cannot durably sync
	// the directory. The same descriptor is synced again after renameat2.
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return fmt.Errorf("preflight parent directory sync: %w", err)
	}
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return fmt.Errorf("before-rename operation: %w", err)
		}
	}
	if err := unix.Renameat2(
		parsed.parentFD,
		parsed.stageName,
		parsed.parentFD,
		targetName,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return fmt.Errorf("publish target with renameat2 RENAME_NOREPLACE: %w", err)
	}
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return committed(fmt.Errorf("sync published parent directory: %w", err))
	}

	targetFD, err := unix.Openat(
		parsed.parentFD,
		targetName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return committed(fmt.Errorf("open published target: %w", err))
	}
	defer unix.Close(targetFD)

	if err := unix.Fstat(targetFD, &targetStat); err != nil {
		return committed(fmt.Errorf("inspect published target: %w", err))
	}
	if err := validateDirectory(targetStat, expectedOwner); err != nil {
		return committed(fmt.Errorf("published target rejected: %w", err))
	}
	if uint64(targetStat.Dev) != parsed.expectedDev || targetStat.Ino != parsed.expectedInode {
		return committed(errors.New("published target identity does not match the verified stage"))
	}
	return nil
}

func replace(parsed command, expectedOwner ownership) error {
	return replaceWithBeforeExchange(parsed, expectedOwner, nil)
}

func replaceWithBeforeExchange(parsed command, expectedOwner ownership, beforeExchange func() error) error {
	var parentStat unix.Stat_t
	if err := unix.Fstat(parsed.parentFD, &parentStat); err != nil {
		return fmt.Errorf("inspect parent directory fd: %w", err)
	}
	if err := validateDirectory(parentStat, expectedOwner); err != nil {
		return fmt.Errorf("parent directory rejected: %w", err)
	}

	stageFD, err := unix.Openat(
		parsed.parentFD,
		parsed.stageName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return fmt.Errorf("open stage directory: %w", err)
	}
	defer unix.Close(stageFD)
	var stageStat unix.Stat_t
	if err := unix.Fstat(stageFD, &stageStat); err != nil {
		return fmt.Errorf("inspect stage directory: %w", err)
	}
	if err := validateDirectory(stageStat, expectedOwner); err != nil {
		return fmt.Errorf("stage directory rejected: %w", err)
	}
	if uint64(stageStat.Dev) != parsed.expectedDev || stageStat.Ino != parsed.expectedInode {
		return errors.New("stage directory identity does not match the verified identity")
	}

	installedFD, err := unix.Openat(
		parsed.parentFD,
		targetName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return fmt.Errorf("open installed directory: %w", err)
	}
	defer unix.Close(installedFD)
	var installedStat unix.Stat_t
	if err := unix.Fstat(installedFD, &installedStat); err != nil {
		return fmt.Errorf("inspect installed directory: %w", err)
	}
	if err := validateDirectory(installedStat, expectedOwner); err != nil {
		return fmt.Errorf("installed directory rejected: %w", err)
	}
	if uint64(installedStat.Dev) != parsed.expectedInstalledDev || installedStat.Ino != parsed.expectedInstalledInode {
		return errors.New("installed directory identity does not match the trusted identity")
	}

	// Both names and their content were verified by the caller. Preflight the
	// directory durability boundary before the single namespace commit.
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return fmt.Errorf("preflight parent directory sync: %w", err)
	}
	if beforeExchange != nil {
		if err := beforeExchange(); err != nil {
			return fmt.Errorf("before-exchange operation: %w", err)
		}
	}
	if err := unix.Renameat2(
		parsed.parentFD,
		parsed.stageName,
		parsed.parentFD,
		targetName,
		unix.RENAME_EXCHANGE,
	); err != nil {
		return fmt.Errorf("replace target with renameat2 RENAME_EXCHANGE: %w", err)
	}
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return committed(fmt.Errorf("sync exchanged parent directory: %w", err))
	}

	newTargetFD, err := unix.Openat(
		parsed.parentFD,
		targetName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return committed(fmt.Errorf("open replacement target: %w", err))
	}
	defer unix.Close(newTargetFD)
	var newTargetStat unix.Stat_t
	if err := unix.Fstat(newTargetFD, &newTargetStat); err != nil {
		return committed(fmt.Errorf("inspect replacement target: %w", err))
	}
	if err := validateDirectory(newTargetStat, expectedOwner); err != nil {
		return committed(fmt.Errorf("replacement target rejected: %w", err))
	}
	if uint64(newTargetStat.Dev) != parsed.expectedDev || newTargetStat.Ino != parsed.expectedInode {
		return committed(errors.New("replacement target identity does not match the verified stage"))
	}

	retiredFD, err := unix.Openat(
		parsed.parentFD,
		parsed.stageName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return committed(fmt.Errorf("open exchanged installed directory: %w", err))
	}
	defer unix.Close(retiredFD)
	var retiredStat unix.Stat_t
	if err := unix.Fstat(retiredFD, &retiredStat); err != nil {
		return committed(fmt.Errorf("inspect exchanged installed directory: %w", err))
	}
	if err := validateDirectory(retiredStat, expectedOwner); err != nil {
		return committed(fmt.Errorf("exchanged installed directory rejected: %w", err))
	}
	if uint64(retiredStat.Dev) != parsed.expectedInstalledDev || retiredStat.Ino != parsed.expectedInstalledInode {
		return committed(errors.New("exchanged installed directory identity does not match the trusted identity"))
	}
	return nil
}

type removeRetiredHooks struct {
	beforeRename     func() error
	beforeRootUnlink func() error
}

func removeRetired(parsed command, expectedOwner ownership) error {
	return removeRetiredWithHooks(parsed, expectedOwner, removeRetiredHooks{})
}

func removeRetiredWithHooks(parsed command, expectedOwner ownership, hooks removeRetiredHooks) error {
	var parentStat unix.Stat_t
	if err := unix.Fstat(parsed.parentFD, &parentStat); err != nil {
		return fmt.Errorf("inspect parent directory fd: %w", err)
	}
	if err := validateDirectory(parentStat, expectedOwner); err != nil {
		return fmt.Errorf("parent directory rejected: %w", err)
	}

	targetFD, targetStat, err := openDirectoryAt(parsed.parentFD, targetName, unix.O_PATH, expectedOwner)
	if err != nil {
		return fmt.Errorf("open replacement target: %w", err)
	}
	defer unix.Close(targetFD)
	if uint64(targetStat.Dev) != parsed.expectedTargetDev || targetStat.Ino != parsed.expectedTargetInode {
		return errors.New("replacement target identity does not match the verified identity")
	}

	stageFD, stageStat, err := openDirectoryAt(parsed.parentFD, parsed.stageName, unix.O_RDONLY, expectedOwner)
	if err != nil {
		return fmt.Errorf("open retired stage directory: %w", err)
	}
	defer unix.Close(stageFD)
	if uint64(stageStat.Dev) != parsed.expectedDev || stageStat.Ino != parsed.expectedInode {
		return errors.New("retired stage directory identity does not match the trusted identity")
	}
	if err := requireNameAbsent(parsed.parentFD, parsed.removeName); err != nil {
		return fmt.Errorf("removal tombstone rejected: %w", err)
	}
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return fmt.Errorf("preflight parent directory sync: %w", err)
	}
	if hooks.beforeRename != nil {
		if err := hooks.beforeRename(); err != nil {
			return fmt.Errorf("before-removal-rename operation: %w", err)
		}
	}
	if err := unix.Renameat2(
		parsed.parentFD,
		parsed.stageName,
		parsed.parentFD,
		parsed.removeName,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return fmt.Errorf("move retired tree to removal tombstone with renameat2 RENAME_NOREPLACE: %w", err)
	}
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return fmt.Errorf("sync removal tombstone publication: %w", err)
	}

	removalFD, removalStat, err := openDirectoryAt(parsed.parentFD, parsed.removeName, unix.O_RDONLY, expectedOwner)
	if err != nil {
		return fmt.Errorf("open removal tombstone: %w", err)
	}
	defer unix.Close(removalFD)
	if uint64(removalStat.Dev) != parsed.expectedDev || removalStat.Ino != parsed.expectedInode {
		return errors.New("removal tombstone identity does not match the trusted retired identity")
	}
	if err := requireNameAbsent(parsed.parentFD, parsed.stageName); err != nil {
		return fmt.Errorf("retired stage name was recreated during cleanup: %w", err)
	}
	if err := validateNamedIdentity(parsed.parentFD, targetName, targetStat); err != nil {
		return fmt.Errorf("replacement target changed before retired-tree removal: %w", err)
	}
	if err := clearDirectory(removalFD, uint64(removalStat.Dev), expectedOwner); err != nil {
		return fmt.Errorf("clear trusted retired tree: %w", err)
	}
	if err := validateNamedIdentity(parsed.parentFD, parsed.removeName, removalStat); err != nil {
		return fmt.Errorf("removal tombstone changed before root unlink: %w", err)
	}
	if hooks.beforeRootUnlink != nil {
		if err := hooks.beforeRootUnlink(); err != nil {
			return fmt.Errorf("before-root-unlink operation: %w", err)
		}
	}
	if err := unix.Unlinkat(parsed.parentFD, parsed.removeName, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("unlink empty removal tombstone: %w", err)
	}
	var unlinkedStat unix.Stat_t
	if err := unix.Fstat(removalFD, &unlinkedStat); err != nil {
		return fmt.Errorf("inspect unlinked retired root: %w", err)
	}
	if unlinkedStat.Nlink != 0 {
		return errors.New("trusted retired root remains linked after tombstone unlink")
	}
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return fmt.Errorf("sync retired-tree removal: %w", err)
	}
	if err := requireNameAbsent(parsed.parentFD, parsed.stageName); err != nil {
		return fmt.Errorf("retired stage name exists after cleanup: %w", err)
	}
	if err := requireNameAbsent(parsed.parentFD, parsed.removeName); err != nil {
		return fmt.Errorf("removal tombstone exists after cleanup: %w", err)
	}
	if err := validateNamedIdentity(parsed.parentFD, targetName, targetStat); err != nil {
		return fmt.Errorf("replacement target changed during retired-tree removal: %w", err)
	}
	return nil
}

func openDirectoryAt(parentFD int, name string, accessFlag int, expectedOwner ownership) (int, unix.Stat_t, error) {
	fd, err := unix.Openat(
		parentFD,
		name,
		accessFlag|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return 0, unix.Stat_t{}, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return 0, unix.Stat_t{}, err
	}
	if err := validateDirectory(stat, expectedOwner); err != nil {
		_ = unix.Close(fd)
		return 0, unix.Stat_t{}, err
	}
	return fd, stat, nil
}

func requireNameAbsent(parentFD int, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("name already exists")
}

func validateNamedIdentity(parentFD int, name string, expected unix.Stat_t) error {
	var actual unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &actual, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if actual.Dev != expected.Dev || actual.Ino != expected.Ino || actual.Mode&unix.S_IFMT != expected.Mode&unix.S_IFMT {
		return errors.New("name identity changed")
	}
	return nil
}

func clearDirectory(directoryFD int, expectedDev uint64, expectedOwner ownership) error {
	names, err := readDirectoryNames(directoryFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !validEntryName(name) {
			return errors.New("retired tree contains an unsafe entry name")
		}
		objectFD, err := unix.Openat(directoryFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open retired entry: %w", err)
		}
		var objectStat unix.Stat_t
		if err := unix.Fstat(objectFD, &objectStat); err != nil {
			_ = unix.Close(objectFD)
			return fmt.Errorf("inspect retired entry: %w", err)
		}
		if uint64(objectStat.Dev) != expectedDev {
			_ = unix.Close(objectFD)
			return errors.New("retired tree crosses a filesystem boundary")
		}
		switch objectStat.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			if err := validateDirectory(objectStat, expectedOwner); err != nil {
				_ = unix.Close(objectFD)
				return fmt.Errorf("retired directory rejected: %w", err)
			}
			childFD, childStat, err := openDirectoryAt(directoryFD, name, unix.O_RDONLY, expectedOwner)
			if err != nil {
				_ = unix.Close(objectFD)
				return fmt.Errorf("open retired child directory: %w", err)
			}
			if childStat.Dev != objectStat.Dev || childStat.Ino != objectStat.Ino {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return errors.New("retired child directory identity changed while opening")
			}
			if err := clearDirectory(childFD, expectedDev, expectedOwner); err != nil {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return err
			}
			if err := unix.Fstat(childFD, &childStat); err != nil {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return fmt.Errorf("inspect cleared retired child directory: %w", err)
			}
			if err := validateNamedIdentity(directoryFD, name, childStat); err != nil {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return fmt.Errorf("retired child directory name changed: %w", err)
			}
			if err := unix.Unlinkat(directoryFD, name, unix.AT_REMOVEDIR); err != nil {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return fmt.Errorf("unlink retired child directory: %w", err)
			}
			if err := unix.Fstat(childFD, &childStat); err != nil {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return fmt.Errorf("inspect unlinked retired child directory: %w", err)
			}
			if childStat.Nlink != 0 {
				_ = unix.Close(childFD)
				_ = unix.Close(objectFD)
				return errors.New("retired child directory remains linked after unlink")
			}
			_ = unix.Close(childFD)
		case unix.S_IFREG:
			if err := validateRegularFile(objectStat, expectedOwner); err != nil {
				_ = unix.Close(objectFD)
				return fmt.Errorf("retired regular file rejected: %w", err)
			}
			if err := validateNamedIdentity(directoryFD, name, objectStat); err != nil {
				_ = unix.Close(objectFD)
				return fmt.Errorf("retired regular file name changed: %w", err)
			}
			if err := unix.Unlinkat(directoryFD, name, 0); err != nil {
				_ = unix.Close(objectFD)
				return fmt.Errorf("unlink retired regular file: %w", err)
			}
			if err := unix.Fstat(objectFD, &objectStat); err != nil {
				_ = unix.Close(objectFD)
				return fmt.Errorf("inspect unlinked retired regular file: %w", err)
			}
			if objectStat.Nlink != 0 {
				_ = unix.Close(objectFD)
				return errors.New("retired regular file remains linked after unlink")
			}
		default:
			_ = unix.Close(objectFD)
			return errors.New("retired tree contains a symbolic link or special node")
		}
		_ = unix.Close(objectFD)
	}
	if err := unix.Fsync(directoryFD); err != nil {
		return fmt.Errorf("sync cleared retired directory: %w", err)
	}
	remaining, err := readDirectoryNames(directoryFD)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return errors.New("retired directory changed while clearing")
	}
	return nil
}

func readDirectoryNames(directoryFD int) ([]string, error) {
	readFD, err := unix.Openat(directoryFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open retired directory for enumeration: %w", err)
	}
	file := os.NewFile(uintptr(readFD), "retired-directory")
	if file == nil {
		_ = unix.Close(readFD)
		return nil, errors.New("create retired directory file handle")
	}
	entries, err := file.ReadDir(-1)
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("enumerate retired directory: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close retired directory enumeration handle: %w", closeErr)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func validEntryName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return false
	}
	for _, character := range name {
		if (character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') &&
			character != '_' && character != '@' && character != '.' &&
			character != '+' && character != '-' {
			return false
		}
	}
	return true
}

func validateRegularFile(stat unix.Stat_t, expectedOwner ownership) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("object is not a regular file")
	}
	if stat.Uid != expectedOwner.uid || stat.Gid != expectedOwner.gid {
		return errors.New("file ownership is invalid")
	}
	mode := stat.Mode & 0o7777
	if mode != 0o555 && mode != 0o644 && mode != 0o755 {
		return errors.New("file mode is not 0555, 0644, or 0755")
	}
	if stat.Nlink != 1 {
		return errors.New("file link count is not one")
	}
	return nil
}

func validateDirectory(stat unix.Stat_t, expectedOwner ownership) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("object is not a directory")
	}
	if stat.Uid != expectedOwner.uid || stat.Gid != expectedOwner.gid {
		return errors.New("directory ownership is invalid")
	}
	if stat.Mode&0o7777 != 0o755 {
		return errors.New("directory mode is not 0755")
	}
	return nil
}
