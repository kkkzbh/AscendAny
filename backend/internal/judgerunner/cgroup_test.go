package judgerunner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDelegatedCgroupEnablesExactControllers(t *testing.T) {
	root := delegatedCgroupFixture(t)
	writes := 0
	access := delegatedCgroupFixtureAccess(root, func(path string, value []byte) error {
		writes++
		if (path != filepath.Join(root, "cgroup.subtree_control") &&
			path != filepath.Join(root, delegatedContainerCgroup, "cgroup.subtree_control")) ||
			string(value) != "+cpu +memory +pids\n" {
			t.Fatalf("write path=%q value=%q", path, value)
		}
		return os.WriteFile(path, []byte("cpu memory pids\n"), 0o600)
	})
	if err := prepareDelegatedCgroup(root, access); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Fatalf("writes = %d", writes)
	}
}

func TestPrepareDelegatedCgroupRejectsInvalidOwnershipState(t *testing.T) {
	for name, mutate := range map[string]func(string, *delegatedCgroupAccess){
		"wrong self subgroup": func(_ string, access *delegatedCgroupAccess) {
			original := access.readFile
			access.readFile = func(path string) ([]byte, error) {
				if path == "/proc/self/cgroup" {
					return []byte("0::/\n"), nil
				}
				return original(path)
			}
		},
		"process at root": func(root string, _ *delegatedCgroupAccess) {
			writeFixtureFile(t, filepath.Join(root, "cgroup.procs"), "123\n")
		},
		"controller missing": func(root string, _ *delegatedCgroupAccess) {
			writeFixtureFile(t, filepath.Join(root, "cgroup.controllers"), "cpu memory\n")
		},
		"preconfigured root": func(root string, _ *delegatedCgroupAccess) {
			writeFixtureFile(t, filepath.Join(root, "cgroup.subtree_control"), "cpu\n")
		},
		"activation drift": func(_ string, access *delegatedCgroupAccess) {
			access.writeControl = func(string, []byte) error { return nil }
		},
		"write failure": func(_ string, access *delegatedCgroupAccess) {
			access.writeControl = func(string, []byte) error { return errors.New("denied") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := delegatedCgroupFixture(t)
			access := delegatedCgroupFixtureAccess(root, func(path string, _ []byte) error {
				return os.WriteFile(path, []byte("cpu memory pids\n"), 0o600)
			})
			mutate(root, &access)
			if err := prepareDelegatedCgroup(root, access); err == nil {
				t.Fatal("prepareDelegatedCgroup() error = nil")
			}
		})
	}
}

func TestPrepareDelegatedCgroupRejectsNonProductionRoot(t *testing.T) {
	if err := PrepareDelegatedCgroup(t.TempDir()); err == nil {
		t.Fatal("PrepareDelegatedCgroup() error = nil")
	}
}

func delegatedCgroupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "supervisor"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "cgroup.controllers"), "cpuset cpu io memory pids\n")
	writeFixtureFile(t, filepath.Join(root, "cgroup.procs"), "")
	writeFixtureFile(t, filepath.Join(root, "cgroup.subtree_control"), "")
	return root
}

func delegatedCgroupFixtureAccess(root string, write func(string, []byte) error) delegatedCgroupAccess {
	return delegatedCgroupAccess{
		lstat: os.Lstat,
		readFile: func(path string) ([]byte, error) {
			if path == "/proc/self/cgroup" {
				return []byte("0::/supervisor\n"), nil
			}
			return os.ReadFile(path)
		},
		writeControl: write,
		mkdir: func(path string, mode os.FileMode) error {
			if err := os.Mkdir(path, mode); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(path, "cgroup.controllers"), []byte("cpu memory pids\n"), 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(path, "cgroup.procs"), nil, 0o600); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), nil, 0o600)
		},
		chmod: os.Chmod,
	}
}

func writeFixtureFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
