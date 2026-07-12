package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimePGPassLifecycleIsPrivateAndRootBound(t *testing.T) {
	t.Parallel()
	runtimePath := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimePath, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := openOwnedRuntimeRoot(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := requireRuntimeFileAbsent(root, backupPGPassFilename); err != nil {
		t.Fatal(err)
	}
	password := strings.Repeat("p", 24)
	if err := writePGPass(
		root,
		backupPGPassFilename,
		"postgresql://ascendany_backup_login@127.0.0.1:5432/ascendany_v2",
		password,
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtimePath, backupPGPassFilename)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime pgpass mode = %v", info.Mode())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "127.0.0.1:5432:ascendany_v2:ascendany_backup_login:"+password+"\n" {
		t.Fatal("runtime pgpass bytes differ from the closed contract")
	}
	if err := removePrivateRuntimeFile(root, backupPGPassFilename); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("runtime pgpass remains after removal: %v", err)
	}
}

func TestRuntimeRootRejectsModeAndSymlinkDrift(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	wrongMode := filepath.Join(parent, "wrong-mode")
	if err := os.Mkdir(wrongMode, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wrongMode, 0o755); err != nil {
		t.Fatal(err)
	}
	if root, err := openOwnedRuntimeRoot(wrongMode); err == nil {
		root.Close()
		t.Fatal("mode-0755 runtime root accepted")
	}
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if root, err := openOwnedRuntimeRoot(linked); err == nil {
		root.Close()
		t.Fatal("linked runtime root accepted")
	}
}

func TestRuntimePGPassRemovalRejectsHardlinkDrift(t *testing.T) {
	t.Parallel()
	runtimePath := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimePath, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := openOwnedRuntimeRoot(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writePGPass(
		root,
		restorePGPassFilename,
		"postgresql://ascendany_restore_login@127.0.0.1:5432/ascendany_v2_restore_verify",
		strings.Repeat("r", 24),
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(
		filepath.Join(runtimePath, restorePGPassFilename),
		filepath.Join(runtimePath, "foreign-link"),
	); err != nil {
		t.Fatal(err)
	}
	if err := removePrivateRuntimeFile(root, restorePGPassFilename); err == nil ||
		!strings.Contains(err.Error(), "metadata drifted") {
		t.Fatalf("hardlinked runtime credential removal error = %v", err)
	}
}
