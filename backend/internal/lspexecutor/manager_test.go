package lspexecutor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspwire"
)

const (
	testOwnerID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherOwnerID  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testSessionID = "00000000-0000-4000-8000-000000000000"
	testOrigin    = "https://ascendany.example"
	otherOrigin   = "https://attacker.example"
)

type inProcessLauncher struct {
	socket string
	policy lsp.Policy

	mu          sync.Mutex
	connections map[string]*net.UnixConn
	starts      []string
	stops       []string
}

func (launcher *inProcessLauncher) Start(_ context.Context, sessionID string) error {
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: launcher.socket, Net: "unix"})
	if err != nil {
		return err
	}
	if err := lspwire.WriteHello(connection, sessionID, launcher.policy); err != nil {
		_ = connection.Close()
		return err
	}
	launcher.mu.Lock()
	launcher.connections[sessionID] = connection
	launcher.starts = append(launcher.starts, sessionID)
	launcher.mu.Unlock()
	go launcher.serveWorker(connection)
	return nil
}

func (launcher *inProcessLauncher) Stop(_ context.Context, sessionID string) error {
	launcher.mu.Lock()
	connection := launcher.connections[sessionID]
	delete(launcher.connections, sessionID)
	launcher.stops = append(launcher.stops, sessionID)
	launcher.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
	return nil
}

func (launcher *inProcessLauncher) serveWorker(connection *net.UnixConn) {
	reader, err := lspwire.NewReader(connection, launcher.policy)
	if err != nil {
		return
	}
	writer, err := lspwire.NewWriter(connection, launcher.policy)
	if err != nil {
		return
	}
	for {
		body, err := reader.Read()
		if err != nil {
			return
		}
		if bytes.Contains(body, []byte(`"id":1`)) {
			_ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}`))
		}
	}
}

func (launcher *inProcessLauncher) stopped(sessionID string) bool {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	for _, stopped := range launcher.stops {
		if stopped == sessionID {
			return true
		}
	}
	return false
}

type clientRead struct {
	body []byte
	err  error
}

type channelClient struct {
	reads  chan clientRead
	writes chan []byte
}

func (client *channelClient) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case item := <-client.reads:
		return item.body, item.err
	}
}

func (client *channelClient) WriteMessage(ctx context.Context, body []byte) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case client.writes <- append([]byte(nil), body...):
		return nil
	}
}

func TestManagerPairsKernelAuthenticatedWorkerAndBridgesFrames(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("production manager requires a non-root worker UID")
	}
	manager, launcher, cancelServe, served := startTestManager(t, 2, bytes.NewReader(make([]byte, 48)))
	defer func() {
		cancelServe()
		if err := <-served; err != nil {
			t.Error(err)
		}
	}()
	session, err := manager.CreateSession(context.Background(), testOwnerID, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != testSessionID || session.WorkspaceURI != lsp.PublicWorkspaceURI || session.WebSocketPath != "/api/v2/lsp/sessions/"+testSessionID+"/websocket" || !lsp.ValidAttachTicket(session.AttachTicket) {
		t.Fatalf("session = %#v", session)
	}
	if _, err := manager.ClaimSession(context.Background(), session.ID, otherOrigin, session.AttachTicket); lspFailure(err) != lsp.FailureSessionNotFound {
		t.Fatalf("wrong-origin claim error = %v", err)
	}
	attachment, err := manager.ClaimSession(context.Background(), session.ID, testOrigin, session.AttachTicket)
	if err != nil {
		t.Fatal(err)
	}
	client := &channelClient{reads: make(chan clientRead, 2), writes: make(chan []byte, 2)}
	bridged := make(chan error, 1)
	go func() { bridged <- attachment.Bridge(context.Background(), client) }()
	client.reads <- clientRead{body: []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"file:///workspace","capabilities":{}}}`)}
	select {
	case body := <-client.writes:
		if !bytes.Contains(body, []byte(`"id":1`)) {
			t.Fatalf("worker response = %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker response was not bridged")
	}
	client.reads <- clientRead{err: io.EOF}
	select {
	case err := <-bridged:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not stop after client disconnect")
	}
	if !launcher.stopped(session.ID) {
		t.Fatal("systemd worker was not stopped after disconnect")
	}
	if _, err := manager.ClaimSession(context.Background(), session.ID, testOrigin, session.AttachTicket); lspFailure(err) != lsp.FailureSessionNotFound {
		t.Fatalf("reused ticket claim = %v", err)
	}
}

func TestManagerAttachTicketIsSessionOriginBoundAndConsumedOnce(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("production manager requires a non-root worker UID")
	}
	random := make([]byte, 0, 96)
	random = append(random, make([]byte, 16)...)
	random = append(random, bytes.Repeat([]byte{0x11}, 32)...)
	random = append(random, bytes.Repeat([]byte{0x22}, 16)...)
	random = append(random, bytes.Repeat([]byte{0x33}, 32)...)
	manager, _, cancelServe, served := startTestManager(t, 2, bytes.NewReader(random))
	defer func() { cancelServe(); _ = <-served }()
	first, err := manager.CreateSession(context.Background(), testOwnerID, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateSession(context.Background(), otherOwnerID, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		sessionID string
		origin    string
		ticket    string
	}{
		{name: "cross-session ticket", sessionID: first.ID, origin: testOrigin, ticket: second.AttachTicket},
		{name: "wrong origin", sessionID: first.ID, origin: otherOrigin, ticket: first.AttachTicket},
		{name: "malformed ticket", sessionID: first.ID, origin: testOrigin, ticket: "short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, claimErr := manager.ClaimSession(context.Background(), test.sessionID, test.origin, test.ticket); lspFailure(claimErr) != lsp.FailureSessionNotFound {
				t.Fatalf("claim error = %v", claimErr)
			}
		})
	}
	attachment, err := manager.ClaimSession(context.Background(), first.ID, testOrigin, first.AttachTicket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ClaimSession(context.Background(), first.ID, testOrigin, first.AttachTicket); lspFailure(err) != lsp.FailureSessionNotFound {
		t.Fatalf("repeated claim error = %v", err)
	}
	attachment.Close()
	if err := manager.CloseSession(context.Background(), otherOwnerID, second.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCapacityAndExplicitCancelAreBounded(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("production manager requires a non-root worker UID")
	}
	random := append(make([]byte, 48), bytes.Repeat([]byte{1}, 48)...)
	manager, launcher, cancelServe, served := startTestManager(t, 1, bytes.NewReader(random))
	defer func() { cancelServe(); _ = <-served }()
	session, err := manager.CreateSession(context.Background(), testOwnerID, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateSession(context.Background(), testOwnerID, testOrigin); lspFailure(err) != lsp.FailureCapacity {
		t.Fatalf("capacity error = %v", err)
	}
	if err := manager.CloseSession(context.Background(), otherOwnerID, session.ID); lspFailure(err) != lsp.FailureSessionOwner {
		t.Fatalf("wrong-owner close error = %v", err)
	}
	if err := manager.CloseSession(context.Background(), testOwnerID, session.ID); err != nil {
		t.Fatal(err)
	}
	if !launcher.stopped(session.ID) {
		t.Fatal("explicit cancel did not stop the worker")
	}
}

func TestManagerRejectsUnknownAndDuplicateWorkers(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("production manager requires a non-root worker UID")
	}
	manager, _, cancelServe, served := startTestManager(t, 1, bytes.NewReader(make([]byte, 16)))
	defer func() { cancelServe(); _ = <-served }()
	unknown, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: manager.config.SocketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lspwire.WriteHello(unknown, "22222222-2222-4222-8222-222222222222", manager.config.Policy); err != nil {
		t.Fatal(err)
	}
	_ = unknown.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := unknown.Read(buffer); err == nil {
		t.Fatal("unknown worker connection remained open")
	}
	_ = unknown.Close()
}

func startTestManager(t *testing.T, maximumSessions int, random io.Reader) (*Manager, *inProcessLauncher, context.CancelFunc, <-chan error) {
	t.Helper()
	root := t.TempDir()
	socket := filepath.Join(root, "lsp-control.sock")
	policy := lsp.DefaultPolicy()
	policy.MaximumSessionDuration = 5 * time.Second
	launcher := &inProcessLauncher{socket: socket, policy: policy, connections: make(map[string]*net.UnixConn)}
	manager, err := NewManager(launcher, Config{
		SocketPath: socket, ExpectedWorkerUID: uint32(os.Getuid()),
		MaximumSessions: maximumSessions, MaximumPendingHandshakes: 4,
		HandshakeTimeout: time.Second, StartupTimeout: 2 * time.Second, StopTimeout: time.Second,
		Random: random, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- manager.Serve(ctx) }()
	select {
	case <-manager.Ready():
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("manager did not become ready")
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0o660 {
		cancel()
		t.Fatalf("control socket mode = %v, error = %v", info, err)
	}
	return manager, launcher, cancel, served
}

func lspFailure(err error) lsp.FailureCode {
	var failure *lsp.Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}
