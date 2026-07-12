package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPromotePublishesVerifiedStage(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.Ab3dE5fG7h"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	marker := []byte("verified release")
	if err := os.WriteFile(filepath.Join(stagePath, "marker"), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	stageStat := statPath(t, stagePath)

	err := promote(command{
		parentFD:      parentFD,
		stageName:     stageName,
		expectedDev:   uint64(stageStat.Dev),
		expectedInode: stageStat.Ino,
	}, testOwnership())
	if err != nil {
		t.Fatalf("promote() error = %v", err)
	}
	if _, err := os.Lstat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("stage still exists after promotion: %v", err)
	}
	targetPath := filepath.Join(parent, targetName)
	targetStat := statPath(t, targetPath)
	if targetStat.Dev != stageStat.Dev || targetStat.Ino != stageStat.Ino {
		t.Fatalf("target identity = (%d, %d), want (%d, %d)", targetStat.Dev, targetStat.Ino, stageStat.Dev, stageStat.Ino)
	}
	got, err := os.ReadFile(filepath.Join(targetPath, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(marker) {
		t.Fatalf("published marker = %q, want %q", got, marker)
	}
}

func TestPromoteRejectsExistingTargetWithoutChangingEitherDirectory(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.12345abcDE"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	stageMarker := filepath.Join(stagePath, "stage-marker")
	if err := os.WriteFile(stageMarker, []byte("stage"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	targetMarker := filepath.Join(targetPath, "target-marker")
	if err := os.WriteFile(targetMarker, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageStat := statPath(t, stagePath)
	targetStat := statPath(t, targetPath)

	err := promote(command{
		parentFD:      parentFD,
		stageName:     stageName,
		expectedDev:   uint64(stageStat.Dev),
		expectedInode: stageStat.Ino,
	}, testOwnership())
	if err == nil || !strings.Contains(err.Error(), "target already exists") {
		t.Fatalf("promote() error = %v, want existing-target rejection", err)
	}
	if _, err := os.Stat(stageMarker); err != nil {
		t.Fatalf("stage was changed: %v", err)
	}
	unchangedTarget := statPath(t, targetPath)
	if unchangedTarget.Dev != targetStat.Dev || unchangedTarget.Ino != targetStat.Ino {
		t.Fatal("existing target identity changed")
	}
	got, err := os.ReadFile(targetMarker)
	if err != nil || string(got) != "target" {
		t.Fatalf("existing target marker = %q, err = %v", got, err)
	}
}

func TestPromoteRenameNoReplaceRejectsTargetCreatedAfterAbsenceCheck(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.Z9y8X7w6V5"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	stageMarker := filepath.Join(stagePath, "stage-marker")
	if err := os.WriteFile(stageMarker, []byte("verified-stage"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageStat := statPath(t, stagePath)
	targetPath := filepath.Join(parent, targetName)
	var racingTargetStat unix.Stat_t

	err := promoteWithBeforeRename(command{
		parentFD:      parentFD,
		stageName:     stageName,
		expectedDev:   uint64(stageStat.Dev),
		expectedInode: stageStat.Ino,
	}, testOwnership(), func() error {
		mkdir0755(t, targetPath)
		if err := os.WriteFile(filepath.Join(targetPath, "target-marker"), []byte("racing-target"), 0o644); err != nil {
			return err
		}
		racingTargetStat = statPath(t, targetPath)
		return nil
	})
	if !errors.Is(err, unix.EEXIST) {
		t.Fatalf("promoteWithBeforeRename() error = %v, want EEXIST", err)
	}
	unchangedStage := statPath(t, stagePath)
	if unchangedStage.Dev != stageStat.Dev || unchangedStage.Ino != stageStat.Ino {
		t.Fatal("stage identity changed after RENAME_NOREPLACE rejection")
	}
	if got, err := os.ReadFile(stageMarker); err != nil || string(got) != "verified-stage" {
		t.Fatalf("stage marker = %q, err = %v", got, err)
	}
	unchangedTarget := statPath(t, targetPath)
	if unchangedTarget.Dev != racingTargetStat.Dev || unchangedTarget.Ino != racingTargetStat.Ino {
		t.Fatal("racing target identity changed after RENAME_NOREPLACE rejection")
	}
	if got, err := os.ReadFile(filepath.Join(targetPath, "target-marker")); err != nil || string(got) != "racing-target" {
		t.Fatalf("target marker = %q, err = %v", got, err)
	}
}

func TestPromoteRejectsStageIdentityMismatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(unix.Stat_t) (uint64, uint64)
	}{
		{
			name: "device",
			change: func(stat unix.Stat_t) (uint64, uint64) {
				return uint64(stat.Dev) + 1, stat.Ino
			},
		},
		{
			name: "inode",
			change: func(stat unix.Stat_t) (uint64, uint64) {
				return uint64(stat.Dev), stat.Ino + 1
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent, parentFD := testParent(t)
			stageName := ".v2.installing.A1b2C3d4E5"
			stagePath := filepath.Join(parent, stageName)
			mkdir0755(t, stagePath)
			stageStat := statPath(t, stagePath)
			expectedDev, expectedInode := test.change(stageStat)

			err := promote(command{
				parentFD:      parentFD,
				stageName:     stageName,
				expectedDev:   expectedDev,
				expectedInode: expectedInode,
			}, testOwnership())
			if err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("promote() error = %v, want identity rejection", err)
			}
			if _, err := os.Stat(stagePath); err != nil {
				t.Fatalf("stage was changed: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(parent, targetName)); !os.IsNotExist(err) {
				t.Fatalf("target was created: %v", err)
			}
		})
	}
}

func TestParseCommandRejectsInvalidStageNameAndArguments(t *testing.T) {
	valid := []string{
		"promote",
		"--parent-fd", "12",
		"--stage-name", ".v2.installing.A1b2C3d4E5",
		"--expected-device", "2049",
		"--expected-inode", "918273",
	}
	parsed, err := parseCommand(valid)
	if err != nil {
		t.Fatalf("parseCommand(valid) error = %v", err)
	}
	if parsed.parentFD != 12 || parsed.stageName != valid[4] || parsed.expectedDev != 2049 || parsed.expectedInode != 918273 {
		t.Fatalf("parseCommand(valid) = %#v", parsed)
	}

	invalid := [][]string{
		nil,
		{"inspect"},
		valid[:8],
		append(append([]string{}, valid...), "extra"),
		withArgument(valid, 1, "--stage-name"),
		withArgument(valid, 2, "-1"),
		withArgument(valid, 2, "fd"),
		withArgument(valid, 4, ".v2.installing.short"),
		withArgument(valid, 4, ".v2.installing.A1b2C3d4E-"),
		withArgument(valid, 4, ".v2.installing.A1b2C3d4E/"),
		withArgument(valid, 6, "0x10"),
		withArgument(valid, 8, "18446744073709551616"),
	}
	for _, args := range invalid {
		if _, err := parseCommand(args); err == nil {
			t.Fatalf("parseCommand(%q) unexpectedly succeeded", args)
		}
	}
}

func TestRunRequiresEffectiveRoot(t *testing.T) {
	var stderr strings.Builder
	if code := run(nil, &stderr, 1000); code != 1 {
		t.Fatalf("run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "effective UID 0") {
		t.Fatalf("run() stderr = %q", stderr.String())
	}
}

func TestValidateBootstrapEnvironment(t *testing.T) {
	valid := []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV=1",
		"PWD=/",
		"SHLVL=1",
		"_=/proc/123/fd/14",
	}
	if err := validateBootstrapEnvironment(valid); err != nil {
		t.Fatalf("validateBootstrapEnvironment(valid) error = %v", err)
	}
	invalid := [][]string{
		nil,
		append(append([]string{}, valid...), "ASCENDANY_ATTACK_ENVIRONMENT=present"),
		withEnvironment(valid, "PATH", "/tmp/attacker"),
		withEnvironment(valid, "LC_ALL", "en_US.UTF-8"),
		withoutEnvironment(valid, "ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV"),
		append(append([]string{}, valid...), "PATH=/usr/bin:/bin"),
	}
	for _, environment := range invalid {
		if err := validateBootstrapEnvironment(environment); err == nil {
			t.Fatalf("validateBootstrapEnvironment(%q) unexpectedly succeeded", environment)
		}
	}
}

func testParent(t *testing.T) (string, int) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "parent")
	mkdir0755(t, parent)
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Close(parentFD); err != nil {
			t.Errorf("close parent fd: %v", err)
		}
	})
	return parent, parentFD
}

func mkdir0755(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func statPath(t *testing.T, path string) unix.Stat_t {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return stat
}

func testOwnership() ownership {
	return ownership{uid: uint32(os.Geteuid()), gid: uint32(os.Getegid())}
}

func withArgument(args []string, index int, replacement string) []string {
	result := append([]string{}, args...)
	result[index] = replacement
	return result
}

func withEnvironment(entries []string, name, value string) []string {
	result := append([]string{}, entries...)
	for index, entry := range result {
		if strings.HasPrefix(entry, name+"=") {
			result[index] = name + "=" + value
			return result
		}
	}
	return append(result, name+"="+value)
}

func withoutEnvironment(entries []string, name string) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry, name+"=") {
			result = append(result, entry)
		}
	}
	return result
}

func Example_parseCommand() {
	parsed, err := parseCommand([]string{
		"promote",
		"--parent-fd", "7",
		"--stage-name", ".v2.installing.A1b2C3d4E5",
		"--expected-device", "2049",
		"--expected-inode", "918273",
	})
	fmt.Println(parsed.stageName, err)
	// Output: .v2.installing.A1b2C3d4E5 <nil>
}
