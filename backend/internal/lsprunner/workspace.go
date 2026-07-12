package lsprunner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspunix"
)

func prepareWorkspace(workspace string) error {
	parent := filepath.Dir(workspace)
	grandparent := filepath.Dir(parent)
	if err := lspunix.EnsureRealDirectory(grandparent); err != nil {
		return fmt.Errorf("validate workspace parent root: %w", err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := requirePrivateOwnedDirectory(parent); err != nil {
		return err
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return errors.New("LSP workspace already exists")
		}
		return err
	}
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"cache", "config", "tmp"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			_ = os.RemoveAll(workspace)
			return err
		}
	}
	return nil
}

func requirePrivateOwnedDirectory(path string) error {
	if err := lspunix.EnsureRealDirectory(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o700 {
		return errors.New("LSP workspace directory must have mode 0700")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("LSP workspace directory must be owned by the worker UID")
	}
	return nil
}

func monitorWorkspace(ctx context.Context, workspace string, policy lsp.Policy) <-chan error {
	result := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(policy.WorkspacePollInterval)
		defer ticker.Stop()
		for {
			if err := inspectWorkspace(workspace, policy); err != nil {
				result <- err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return result
}

func inspectWorkspace(workspace string, policy lsp.Policy) error {
	files := 0
	var bytes int64
	err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == workspace {
			return nil
		}
		files++
		if files > policy.MaximumWorkspaceFiles {
			return errors.New("LSP workspace file count exceeds the limit")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("LSP workspace contains a symbolic link")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("LSP workspace contains a special file")
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			if bytes > policy.MaximumWorkspaceBytes {
				return errors.New("LSP workspace bytes exceed the limit")
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect LSP workspace: %w", err)
	}
	return nil
}

func cleanupWorkspace(workspace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := os.RemoveAll(workspace); err != nil && time.Now().After(deadline) {
			return fmt.Errorf("remove LSP workspace: %w", err)
		}
		if _, err := os.Lstat(workspace); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("LSP workspace remained after cleanup deadline")
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
}
