package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	targetName      = "v2"
	stageNamePrefix = ".v2.installing."
	stageSuffixSize = 10
)

type command struct {
	parentFD      int
	stageName     string
	expectedDev   uint64
	expectedInode uint64
}

type ownership struct {
	uid uint32
	gid uint32
}

func main() {
	if err := validateBootstrapEnvironment(os.Environ()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release promotion environment rejected: %v\n", err)
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
		return 2
	}
	if err := promote(parsed, ownership{uid: 0, gid: 0}); err != nil {
		_, _ = fmt.Fprintf(stderr, "release promotion failed: %v\n", err)
		return 1
	}
	return 0
}

func parseCommand(args []string) (command, error) {
	if len(args) != 9 ||
		args[0] != "promote" ||
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
		parentFD:      parentFD,
		stageName:     args[4],
		expectedDev:   expectedDev,
		expectedInode: expectedInode,
	}, nil
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
	if !strings.HasPrefix(name, stageNamePrefix) || len(name) != len(stageNamePrefix)+stageSuffixSize {
		return false
	}
	for _, character := range name[len(stageNamePrefix):] {
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

	targetFD, err := unix.Openat(
		parsed.parentFD,
		targetName,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return fmt.Errorf("open published target: %w", err)
	}
	defer unix.Close(targetFD)

	if err := unix.Fstat(targetFD, &targetStat); err != nil {
		return fmt.Errorf("inspect published target: %w", err)
	}
	if err := validateDirectory(targetStat, expectedOwner); err != nil {
		return fmt.Errorf("published target rejected: %w", err)
	}
	if uint64(targetStat.Dev) != parsed.expectedDev || targetStat.Ino != parsed.expectedInode {
		return errors.New("published target identity does not match the verified stage")
	}
	if err := unix.Fsync(parsed.parentFD); err != nil {
		return fmt.Errorf("sync published parent directory: %w", err)
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
