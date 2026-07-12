package lspunix

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestRequirePeerUIDUsesKernelCredentials(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("peer contract requires the production non-root identity")
	}
	root := t.TempDir()
	socket := filepath.Join(root, "peer.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		connection, _ := listener.AcceptUnix()
		accepted <- connection
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()
	if err := RequirePeerUID(server, uint32(os.Getuid())); err != nil {
		t.Fatal(err)
	}
	if err := RequirePeerUID(server, uint32(os.Getuid()+1)); err == nil {
		t.Fatal("incorrect peer UID was accepted")
	}
}

func TestEnsureRealDirectoryRejectsSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRealDirectory(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRealDirectory(link); err == nil {
		t.Fatal("symlink directory was accepted")
	}
}

func TestRequireRootOwnedExecutableRejectsMutablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clangd")
	if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := RequireRootOwnedExecutable(path); err == nil {
		t.Fatal("user-controlled executable was accepted")
	}
	if _, err := os.Stat("/usr/bin/clangd"); err == nil {
		if err := RequireRootOwnedExecutable("/usr/bin/clangd"); err != nil {
			t.Fatal(err)
		}
	}
}
