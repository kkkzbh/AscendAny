package lspunix

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func RequirePeerUID(connection *net.UnixConn, expected uint32) error {
	if connection == nil || expected == 0 {
		return errors.New("Unix peer and non-root expected UID are required")
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return fmt.Errorf("access LSP peer credentials: %w", err)
	}
	var credentials *unix.Ucred
	var controlErr error
	if err := raw.Control(func(descriptor uintptr) {
		credentials, controlErr = unix.GetsockoptUcred(int(descriptor), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("read LSP peer credentials: %w", err)
	}
	if controlErr != nil {
		return fmt.Errorf("read LSP peer credentials: %w", controlErr)
	}
	if credentials == nil || credentials.Uid != expected {
		return errors.New("LSP Unix peer UID is not authorized")
	}
	return nil
}

func EnsureRealDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("directory path must be canonical and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path must be an existing real directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("directory path must not traverse symbolic links")
	}
	return nil
}

func RequireRootOwnedExecutable(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("executable path must be canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("executable path must not traverse symbolic links")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("executable must be a non-writable executable regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return errors.New("executable must be root-owned")
	}
	return nil
}
