package judgerunner

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

const ProductionDelegatedCgroupRoot = "/sys/fs/cgroup"

var requiredDelegatedControllers = []string{"cpu", "memory", "pids"}

const delegatedContainerCgroup = "containers"

type delegatedCgroupAccess struct {
	lstat        func(string) (os.FileInfo, error)
	readFile     func(string) ([]byte, error)
	writeControl func(string, []byte) error
	mkdir        func(string, os.FileMode) error
	chmod        func(string, os.FileMode) error
}

func PrepareDelegatedCgroup(root string) error {
	if root != ProductionDelegatedCgroupRoot {
		return errors.New("production delegated cgroup root is required")
	}
	return prepareDelegatedCgroup(root, delegatedCgroupAccess{
		lstat: os.Lstat, readFile: os.ReadFile, writeControl: writeCgroupControl,
		mkdir: os.Mkdir, chmod: os.Chmod,
	})
}

func prepareDelegatedCgroup(root string, access delegatedCgroupAccess) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root ||
		access.lstat == nil || access.readFile == nil || access.writeControl == nil ||
		access.mkdir == nil || access.chmod == nil {
		return errors.New("absolute cgroup root and complete access contract are required")
	}
	for _, directory := range []string{root, filepath.Join(root, "supervisor")} {
		info, err := access.lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("delegated cgroup directory is invalid: %s", directory)
		}
	}
	self, err := access.readFile("/proc/self/cgroup")
	if err != nil || !bytes.Equal(self, []byte("0::/supervisor\n")) {
		return errors.New("judge process is not in the delegated supervisor subgroup")
	}
	processes, err := access.readFile(filepath.Join(root, "cgroup.procs"))
	if err != nil || len(bytes.TrimSpace(processes)) != 0 {
		return errors.New("delegated cgroup root contains processes")
	}
	controllers, err := access.readFile(filepath.Join(root, "cgroup.controllers"))
	if err != nil || !containsAllControllers(strings.Fields(string(controllers)), requiredDelegatedControllers) {
		return errors.New("delegated cgroup root lacks cpu, memory, or pids controller")
	}
	subtreePath := filepath.Join(root, "cgroup.subtree_control")
	before, err := access.readFile(subtreePath)
	if err != nil || len(bytes.TrimSpace(before)) != 0 {
		return errors.New("delegated cgroup root was already configured")
	}
	if err := access.writeControl(subtreePath, []byte("+cpu +memory +pids\n")); err != nil {
		return fmt.Errorf("enable delegated cgroup controllers: %w", err)
	}
	after, err := access.readFile(subtreePath)
	if err != nil || !slices.Equal(strings.Fields(string(after)), requiredDelegatedControllers) {
		return errors.New("delegated cgroup controller activation differs from the contract")
	}
	containerRoot := filepath.Join(root, delegatedContainerCgroup)
	if _, err := access.lstat(containerRoot); !os.IsNotExist(err) {
		return errors.New("delegated container cgroup root already exists")
	}
	if err := access.mkdir(containerRoot, 0o755); err != nil {
		return fmt.Errorf("create delegated container cgroup root: %w", err)
	}
	if err := access.chmod(containerRoot, 0o755); err != nil {
		return fmt.Errorf("set delegated container cgroup root mode: %w", err)
	}
	info, err := access.lstat(containerRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
		return errors.New("delegated container cgroup root mode is invalid")
	}
	containerProcesses, err := access.readFile(filepath.Join(containerRoot, "cgroup.procs"))
	if err != nil || len(bytes.TrimSpace(containerProcesses)) != 0 {
		return errors.New("delegated container cgroup root contains processes")
	}
	containerControllers, err := access.readFile(filepath.Join(containerRoot, "cgroup.controllers"))
	if err != nil || !slices.Equal(strings.Fields(string(containerControllers)), requiredDelegatedControllers) {
		return errors.New("delegated container cgroup controllers differ from the contract")
	}
	containerSubtree := filepath.Join(containerRoot, "cgroup.subtree_control")
	containerBefore, err := access.readFile(containerSubtree)
	if err != nil || len(bytes.TrimSpace(containerBefore)) != 0 {
		return errors.New("delegated container cgroup root was already configured")
	}
	if err := access.writeControl(containerSubtree, []byte("+cpu +memory +pids\n")); err != nil {
		return fmt.Errorf("enable delegated container cgroup controllers: %w", err)
	}
	containerAfter, err := access.readFile(containerSubtree)
	if err != nil || !slices.Equal(strings.Fields(string(containerAfter)), requiredDelegatedControllers) {
		return errors.New("delegated container cgroup controller activation differs from the contract")
	}
	return nil
}

func containsAllControllers(actual, required []string) bool {
	for _, controller := range required {
		if !slices.Contains(actual, controller) {
			return false
		}
	}
	return true
}

func writeCgroupControl(path string, value []byte) error {
	descriptor, err := unix.Open(path, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	written, writeErr := unix.Write(descriptor, value)
	closeErr := unix.Close(descriptor)
	if writeErr != nil {
		return writeErr
	}
	if written != len(value) {
		return errors.New("short cgroup control write")
	}
	return closeErr
}
