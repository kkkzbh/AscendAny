package judgerunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServeOneCancellationClosesListenerAndUnlinksSocket(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "aaj.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	jobID := "11111111-1111-4111-8111-111111111111"
	workParent := filepath.Join(root, "jobs")
	socketDirectory := filepath.Join(root, "sockets")
	for _, directory := range []string{workParent, socketDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := New(&fakeEngine{}, DefaultConfig(jobID, filepath.Join(workParent, jobID)))
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(socketDirectory, jobID+".sock")
	server, err := NewServer(runner, ServerConfig{
		SocketPath: socketPath, AllowedClientUID: 1,
		AcceptTimeout: time.Second, MaximumSessionDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.ServeOne(ctx) }()

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		if info, statErr := os.Lstat(socketPath); statErr == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("judge socket was not created")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case serveErr := <-done:
		if !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("ServeOne error = %v", serveErr)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("ServeOne did not react to context cancellation")
	}
	if _, statErr := os.Lstat(socketPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("judge socket remains after cancellation: %v", statErr)
	}
}
