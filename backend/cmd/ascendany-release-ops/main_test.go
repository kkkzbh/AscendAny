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

func TestReplaceAtomicallyExchangesVerifiedDirectories(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.R3pL4cE7mN"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	if err := os.WriteFile(filepath.Join(stagePath, "marker"), []byte("new release"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	if err := os.WriteFile(filepath.Join(targetPath, "marker"), []byte("installed release"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageStat := statPath(t, stagePath)
	installedStat := statPath(t, targetPath)

	err := replace(command{
		parentFD:               parentFD,
		stageName:              stageName,
		expectedDev:            uint64(stageStat.Dev),
		expectedInode:          stageStat.Ino,
		expectedInstalledDev:   uint64(installedStat.Dev),
		expectedInstalledInode: installedStat.Ino,
	}, testOwnership())
	if err != nil {
		t.Fatalf("replace() error = %v", err)
	}

	replacementStat := statPath(t, targetPath)
	if replacementStat.Dev != stageStat.Dev || replacementStat.Ino != stageStat.Ino {
		t.Fatalf("replacement identity = (%d, %d), want stage identity (%d, %d)", replacementStat.Dev, replacementStat.Ino, stageStat.Dev, stageStat.Ino)
	}
	retiredStat := statPath(t, stagePath)
	if retiredStat.Dev != installedStat.Dev || retiredStat.Ino != installedStat.Ino {
		t.Fatalf("retired identity = (%d, %d), want installed identity (%d, %d)", retiredStat.Dev, retiredStat.Ino, installedStat.Dev, installedStat.Ino)
	}
	if got, err := os.ReadFile(filepath.Join(targetPath, "marker")); err != nil || string(got) != "new release" {
		t.Fatalf("replacement marker = %q, err = %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(stagePath, "marker")); err != nil || string(got) != "installed release" {
		t.Fatalf("retired marker = %q, err = %v", got, err)
	}
}

func TestReplaceRejectsUntrustedInstalledIdentityWithoutExchanging(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.I6dE8nT1tY"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	stageStat := statPath(t, stagePath)
	installedStat := statPath(t, targetPath)

	err := replace(command{
		parentFD:               parentFD,
		stageName:              stageName,
		expectedDev:            uint64(stageStat.Dev),
		expectedInode:          stageStat.Ino,
		expectedInstalledDev:   uint64(installedStat.Dev),
		expectedInstalledInode: installedStat.Ino + 1,
	}, testOwnership())
	if err == nil || !strings.Contains(err.Error(), "trusted identity") {
		t.Fatalf("replace() error = %v, want trusted installed identity rejection", err)
	}
	if got := statPath(t, stagePath); got.Ino != stageStat.Ino || got.Dev != stageStat.Dev {
		t.Fatal("stage identity changed after installed identity rejection")
	}
	if got := statPath(t, targetPath); got.Ino != installedStat.Ino || got.Dev != installedStat.Dev {
		t.Fatal("installed identity changed after identity rejection")
	}
}

func TestReplaceDetectsInstalledNameRaceAfterExchangeWithoutRollback(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.X4cH8aN6gE"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	if err := os.WriteFile(filepath.Join(stagePath, "marker"), []byte("verified new release"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	if err := os.WriteFile(filepath.Join(targetPath, "marker"), []byte("trusted installed release"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageStat := statPath(t, stagePath)
	installedStat := statPath(t, targetPath)
	displacedPath := filepath.Join(parent, "displaced-installed")
	var racingTargetStat unix.Stat_t

	err := replaceWithBeforeExchange(command{
		parentFD:               parentFD,
		stageName:              stageName,
		expectedDev:            uint64(stageStat.Dev),
		expectedInode:          stageStat.Ino,
		expectedInstalledDev:   uint64(installedStat.Dev),
		expectedInstalledInode: installedStat.Ino,
	}, testOwnership(), func() error {
		if err := os.Rename(targetPath, displacedPath); err != nil {
			return err
		}
		mkdir0755(t, targetPath)
		if err := os.WriteFile(filepath.Join(targetPath, "marker"), []byte("racing target"), 0o644); err != nil {
			return err
		}
		racingTargetStat = statPath(t, targetPath)
		return nil
	})
	var committedErr committedOperationError
	if !errors.As(err, &committedErr) {
		t.Fatalf("replaceWithBeforeExchange() error = %v, want committed operation error", err)
	}
	if err == nil || !strings.Contains(err.Error(), "exchanged installed directory identity") {
		t.Fatalf("replaceWithBeforeExchange() error = %v, want post-exchange identity rejection", err)
	}
	if got := statPath(t, targetPath); got.Dev != stageStat.Dev || got.Ino != stageStat.Ino {
		t.Fatal("replacement target was rolled back after the committed exchange")
	}
	if got := statPath(t, stagePath); got.Dev != racingTargetStat.Dev || got.Ino != racingTargetStat.Ino {
		t.Fatal("racing target did not remain at the exchanged stage name")
	}
	if got := statPath(t, displacedPath); got.Dev != installedStat.Dev || got.Ino != installedStat.Ino {
		t.Fatal("trusted installed tree identity changed during the namespace race")
	}
	if got, err := os.ReadFile(filepath.Join(targetPath, "marker")); err != nil || string(got) != "verified new release" {
		t.Fatalf("replacement marker = %q, err = %v", got, err)
	}
}

func TestRemoveRetiredDeletesOnlyTheTrustedTree(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.C1eA2nU3pX"
	removeName := ".v2.removing.C1eA2nU3pX"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	nestedPath := filepath.Join(stagePath, "nested")
	mkdir0755(t, nestedPath)
	if err := os.WriteFile(filepath.Join(stagePath, "manifest"), []byte("retired manifest"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedPath, "binary"), []byte("retired binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedPath, "operator"), []byte("retired operator bundle"), 0o555); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	if err := os.WriteFile(filepath.Join(targetPath, "marker"), []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageStat := statPath(t, stagePath)
	targetStat := statPath(t, targetPath)

	err := removeRetired(command{
		parentFD:            parentFD,
		stageName:           stageName,
		removeName:          removeName,
		expectedDev:         uint64(stageStat.Dev),
		expectedInode:       stageStat.Ino,
		expectedTargetDev:   uint64(targetStat.Dev),
		expectedTargetInode: targetStat.Ino,
	}, testOwnership())
	if err != nil {
		t.Fatalf("removeRetired() error = %v", err)
	}
	if _, err := os.Lstat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("retired stage remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, removeName)); !os.IsNotExist(err) {
		t.Fatalf("removal tombstone remains: %v", err)
	}
	unchangedTarget := statPath(t, targetPath)
	if unchangedTarget.Dev != targetStat.Dev || unchangedTarget.Ino != targetStat.Ino {
		t.Fatal("replacement target identity changed during retired-tree removal")
	}
}

func TestRemoveRetiredDetectsStageRaceBeforeDeletingContent(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.R4aC5eS6tG"
	removeName := ".v2.removing.R4aC5eS6tG"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	if err := os.WriteFile(filepath.Join(stagePath, "trusted"), []byte("trusted retired tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	stageStat := statPath(t, stagePath)
	targetStat := statPath(t, targetPath)
	displacedPath := filepath.Join(parent, "displaced-retired")

	err := removeRetiredWithHooks(command{
		parentFD:            parentFD,
		stageName:           stageName,
		removeName:          removeName,
		expectedDev:         uint64(stageStat.Dev),
		expectedInode:       stageStat.Ino,
		expectedTargetDev:   uint64(targetStat.Dev),
		expectedTargetInode: targetStat.Ino,
	}, testOwnership(), removeRetiredHooks{
		beforeRename: func() error {
			if err := os.Rename(stagePath, displacedPath); err != nil {
				return err
			}
			mkdir0755(t, stagePath)
			return os.WriteFile(filepath.Join(stagePath, "racing"), []byte("racing tree"), 0o644)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "removal tombstone identity") {
		t.Fatalf("removeRetiredWithHooks() error = %v, want tombstone identity rejection", err)
	}
	if got, err := os.ReadFile(filepath.Join(displacedPath, "trusted")); err != nil || string(got) != "trusted retired tree" {
		t.Fatalf("trusted retired content = %q, err = %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(parent, removeName, "racing")); err != nil || string(got) != "racing tree" {
		t.Fatalf("racing tombstone content = %q, err = %v", got, err)
	}
}

func TestRemoveRetiredDetectsRootUnlinkRace(t *testing.T) {
	parent, parentFD := testParent(t)
	stageName := ".v2.installing.U7nL8iN9kQ"
	removeName := ".v2.removing.U7nL8iN9kQ"
	stagePath := filepath.Join(parent, stageName)
	mkdir0755(t, stagePath)
	if err := os.WriteFile(filepath.Join(stagePath, "trusted"), []byte("trusted retired tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(parent, targetName)
	mkdir0755(t, targetPath)
	stageStat := statPath(t, stagePath)
	targetStat := statPath(t, targetPath)
	displacedPath := filepath.Join(parent, "displaced-removal")

	err := removeRetiredWithHooks(command{
		parentFD:            parentFD,
		stageName:           stageName,
		removeName:          removeName,
		expectedDev:         uint64(stageStat.Dev),
		expectedInode:       stageStat.Ino,
		expectedTargetDev:   uint64(targetStat.Dev),
		expectedTargetInode: targetStat.Ino,
	}, testOwnership(), removeRetiredHooks{
		beforeRootUnlink: func() error {
			if err := os.Rename(filepath.Join(parent, removeName), displacedPath); err != nil {
				return err
			}
			mkdir0755(t, filepath.Join(parent, removeName))
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "remains linked") {
		t.Fatalf("removeRetiredWithHooks() error = %v, want linked-root race rejection", err)
	}
	if _, err := os.Stat(displacedPath); err != nil {
		t.Fatalf("trusted retired root disappeared during race: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, removeName)); !os.IsNotExist(err) {
		t.Fatalf("racing empty tombstone was not consumed by the detected unlink race: %v", err)
	}
	unchangedTarget := statPath(t, targetPath)
	if unchangedTarget.Dev != targetStat.Dev || unchangedTarget.Ino != targetStat.Ino {
		t.Fatal("replacement target identity changed during root unlink race")
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
	if parsed.operation != operationPromote {
		t.Fatalf("parseCommand(valid).operation = %v, want promote", parsed.operation)
	}
	replaceArgs := append(append([]string{}, valid...),
		"--expected-installed-device", "4098",
		"--expected-installed-inode", "192837",
	)
	replaceArgs[0] = "replace"
	replacement, err := parseCommand(replaceArgs)
	if err != nil {
		t.Fatalf("parseCommand(replace) error = %v", err)
	}
	if replacement.operation != operationReplace || replacement.expectedInstalledDev != 4098 || replacement.expectedInstalledInode != 192837 {
		t.Fatalf("parseCommand(replace) = %#v", replacement)
	}
	removeArgs := []string{
		"remove-retired",
		"--parent-fd", "12",
		"--stage-name", ".v2.installing.A1b2C3d4E5",
		"--remove-name", ".v2.removing.A1b2C3d4E5",
		"--expected-device", "2049",
		"--expected-inode", "918273",
		"--expected-target-device", "2049",
		"--expected-target-inode", "918274",
	}
	removal, err := parseCommand(removeArgs)
	if err != nil {
		t.Fatalf("parseCommand(remove-retired) error = %v", err)
	}
	if removal.operation != operationRemoveRetired || removal.removeName != removeArgs[6] || removal.expectedTargetInode != 918274 {
		t.Fatalf("parseCommand(remove-retired) = %#v", removal)
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
		withArgument(replaceArgs, 9, "--expected-installed-inode"),
		withArgument(replaceArgs, 10, "-1"),
		withArgument(replaceArgs, 12, "18446744073709551616"),
		withArgument(removeArgs, 5, "--stage-name"),
		withArgument(removeArgs, 6, ".v2.removing.Z9y8X7w6V5"),
		withArgument(removeArgs, 12, "-1"),
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
