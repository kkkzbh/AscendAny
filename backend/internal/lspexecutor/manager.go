package lspexecutor

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspunix"
	"github.com/kkkzbh/AscendAny/backend/internal/lspwire"
)

type Launcher interface {
	Start(context.Context, string) error
	Stop(context.Context, string) error
}

type Config struct {
	SocketPath               string
	ExpectedWorkerUID        uint32
	MaximumSessions          int
	MaximumPendingHandshakes int
	HandshakeTimeout         time.Duration
	StartupTimeout           time.Duration
	StopTimeout              time.Duration
	Random                   io.Reader
	Policy                   lsp.Policy
}

type Manager struct {
	launcher Launcher
	config   Config

	mu       sync.Mutex
	listener *net.UnixListener
	sessions map[string]*sessionState
	ready    chan struct{}
	closed   bool
	sem      chan struct{}
}

type sessionState struct {
	id           string
	expiresAt    time.Time
	ownerID      string
	origin       string
	ticketDigest [sha256.Size]byte
	ready        chan struct{}
	conn         *net.UnixConn
	reader       *lspwire.Reader
	claimed      bool
	once         sync.Once
}

type sessionAttachment struct {
	manager *Manager
	state   *sessionState

	mu      sync.Mutex
	bridged bool
}

func NewManager(launcher Launcher, config Config) (*Manager, error) {
	if launcher == nil || config.ExpectedWorkerUID == 0 || config.Random == nil || !lsp.ValidPolicy(config.Policy) {
		return nil, errors.New("LSP manager requires launcher, non-root worker UID, random source, and valid policy")
	}
	if config.SocketPath == "" || !filepath.IsAbs(config.SocketPath) || filepath.Clean(config.SocketPath) != config.SocketPath || len(config.SocketPath) > 107 {
		return nil, errors.New("LSP control socket path must be canonical, absolute, and Unix-compatible")
	}
	if config.MaximumSessions < 1 || config.MaximumSessions > 10000 || config.MaximumPendingHandshakes < 1 || config.MaximumPendingHandshakes > 1024 {
		return nil, errors.New("LSP manager capacities are invalid")
	}
	if config.HandshakeTimeout < time.Second || config.HandshakeTimeout > time.Minute || config.StartupTimeout < time.Second || config.StartupTimeout > time.Minute || config.StopTimeout < time.Second || config.StopTimeout > time.Minute {
		return nil, errors.New("LSP manager timeouts are invalid")
	}
	return &Manager{
		launcher: launcher, config: config, sessions: make(map[string]*sessionState),
		ready: make(chan struct{}), sem: make(chan struct{}, config.MaximumPendingHandshakes),
	}, nil
}

func (manager *Manager) Ready() <-chan struct{} { return manager.ready }

func (manager *Manager) Serve(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errors.New("LSP manager context is required")
	}
	if err := lspunix.EnsureRealDirectory(filepath.Dir(manager.config.SocketPath)); err != nil {
		return fmt.Errorf("validate LSP control directory: %w", err)
	}
	if _, err := os.Lstat(manager.config.SocketPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("LSP control socket path already exists")
		}
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: manager.config.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on LSP control socket: %w", err)
	}
	listener.SetUnlinkOnClose(true)
	defer func() {
		if closeErr := listener.Close(); resultErr == nil && closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			resultErr = closeErr
		}
		manager.stopAll()
	}()
	if err := os.Chmod(manager.config.SocketPath, 0o660); err != nil {
		return fmt.Errorf("set LSP control socket mode: %w", err)
	}
	manager.mu.Lock()
	if manager.closed || manager.listener != nil {
		manager.mu.Unlock()
		return errors.New("LSP manager can only be served once")
	}
	manager.listener = listener
	close(manager.ready)
	manager.mu.Unlock()
	stopAccept := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopAccept()
	for {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if context.Cause(ctx) != nil || errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept LSP worker: %w", acceptErr)
		}
		select {
		case manager.sem <- struct{}{}:
			go func() {
				defer func() { <-manager.sem }()
				manager.acceptWorker(connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (manager *Manager) CreateSession(ctx context.Context, ownerID, origin string) (lsp.Session, error) {
	if ctx == nil || !lsp.ValidPublicID(ownerID) || !validOriginBinding(origin) {
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("canonical LSP session owner and origin are required")}
	}
	sessionID, err := lsp.NewPublicID(manager.config.Random)
	if err != nil {
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("generate LSP session ID")}
	}
	attachTicket, err := lsp.NewAttachTicket(manager.config.Random)
	if err != nil {
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("generate LSP attach ticket")}
	}
	expiresAt := time.Now().Add(manager.config.Policy.MaximumSessionDuration).UTC()
	state := &sessionState{
		id: sessionID, expiresAt: expiresAt, ownerID: ownerID, origin: origin,
		ticketDigest: attachTicketDigest(ownerID, sessionID, origin, attachTicket), ready: make(chan struct{}),
	}
	manager.mu.Lock()
	if manager.listener == nil || manager.closed {
		manager.mu.Unlock()
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("LSP control manager is not serving")}
	}
	if len(manager.sessions) >= manager.config.MaximumSessions {
		manager.mu.Unlock()
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureCapacity, Err: errors.New("LSP session capacity is exhausted")}
	}
	if _, duplicate := manager.sessions[sessionID]; duplicate {
		manager.mu.Unlock()
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("generated LSP session ID is not unique")}
	}
	manager.sessions[sessionID] = state
	manager.mu.Unlock()

	if err := manager.launcher.Start(ctx, sessionID); err != nil {
		manager.cleanup(state)
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("LSP worker could not be started")}
	}
	startupTimer := time.NewTimer(manager.config.StartupTimeout)
	defer startupTimer.Stop()
	select {
	case <-ctx.Done():
		manager.cleanup(state)
		return lsp.Session{}, context.Cause(ctx)
	case <-startupTimer.C:
		manager.cleanup(state)
		return lsp.Session{}, &lsp.Failure{Code: lsp.FailureStartup, Err: errors.New("LSP worker did not connect before the startup deadline")}
	case <-state.ready:
	}
	go manager.expire(state)
	return lsp.Session{
		ID: sessionID, WorkspaceURI: lsp.PublicWorkspaceURI,
		WebSocketPath: "/api/v2/lsp/sessions/" + sessionID + "/websocket",
		AttachTicket:  attachTicket, ExpiresAt: expiresAt,
	}, nil
}

func (manager *Manager) ClaimSession(ctx context.Context, sessionID, origin, attachTicket string) (lsp.Attachment, error) {
	if ctx == nil || !lsp.ValidPublicID(sessionID) || !validOriginBinding(origin) || !lsp.ValidAttachTicket(attachTicket) {
		return nil, &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("LSP attach capability is invalid")}
	}
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	default:
	}
	manager.mu.Lock()
	state, found := manager.sessions[sessionID]
	if !found || state.conn == nil || state.claimed || state.origin != origin {
		manager.mu.Unlock()
		return nil, &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("LSP session does not exist")}
	}
	digest := attachTicketDigest(state.ownerID, sessionID, origin, attachTicket)
	if subtle.ConstantTimeCompare(state.ticketDigest[:], digest[:]) != 1 {
		manager.mu.Unlock()
		return nil, &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("LSP session does not exist")}
	}
	state.claimed = true
	state.ticketDigest = [sha256.Size]byte{}
	manager.mu.Unlock()
	return &sessionAttachment{manager: manager, state: state}, nil
}

func (attachment *sessionAttachment) Bridge(ctx context.Context, client lsp.Client) error {
	if attachment == nil || attachment.manager == nil || attachment.state == nil || ctx == nil || client == nil {
		return &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("claimed LSP attachment is required")}
	}
	attachment.mu.Lock()
	if attachment.bridged {
		attachment.mu.Unlock()
		return &lsp.Failure{Code: lsp.FailureAlreadyAttached, Err: errors.New("LSP attachment was already used")}
	}
	attachment.bridged = true
	attachment.mu.Unlock()
	return attachment.manager.bridgeClaimed(ctx, attachment.state, client)
}

func (attachment *sessionAttachment) Close() {
	if attachment != nil && attachment.manager != nil {
		attachment.manager.cleanup(attachment.state)
	}
}

func (manager *Manager) bridgeClaimed(ctx context.Context, state *sessionState, client lsp.Client) error {
	manager.mu.Lock()
	current, found := manager.sessions[state.id]
	ready := found && current == state && state.claimed && state.conn != nil
	manager.mu.Unlock()
	if !ready {
		return &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("LSP session does not exist")}
	}
	defer manager.cleanup(state)

	writer, err := lspwire.NewWriter(state.conn, manager.config.Policy)
	if err != nil {
		return err
	}
	deadline := state.expiresAt
	sessionContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	bridgeErrors := make(chan error, 2)
	go func() {
		for {
			body, readErr := client.ReadMessage(sessionContext)
			if readErr != nil {
				bridgeErrors <- readErr
				return
			}
			if len(body) > manager.config.Policy.MaximumBodyBytes {
				bridgeErrors <- errors.New("LSP WebSocket message exceeds the byte limit")
				return
			}
			if validateErr := lspwire.ValidateBody(body); validateErr != nil {
				bridgeErrors <- validateErr
				return
			}
			if validateErr := lsp.ValidateClientMessage(body); validateErr != nil {
				bridgeErrors <- validateErr
				return
			}
			if writeErr := writer.Write(body); writeErr != nil {
				bridgeErrors <- writeErr
				return
			}
		}
	}()
	go func() {
		for {
			body, readErr := state.reader.Read()
			if readErr != nil {
				bridgeErrors <- readErr
				return
			}
			if writeErr := client.WriteMessage(sessionContext, body); writeErr != nil {
				bridgeErrors <- writeErr
				return
			}
		}
	}()
	select {
	case <-sessionContext.Done():
		if errors.Is(context.Cause(sessionContext), context.Canceled) {
			return nil
		}
		return context.Cause(sessionContext)
	case err := <-bridgeErrors:
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
			return nil
		}
		return &lsp.Failure{Code: lsp.FailureProtocol, Err: err}
	}
}

func (manager *Manager) CloseSession(ctx context.Context, ownerID, sessionID string) error {
	if ctx == nil || !lsp.ValidPublicID(ownerID) || !lsp.ValidPublicID(sessionID) {
		return &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("canonical LSP owner and session IDs are required")}
	}
	manager.mu.Lock()
	state, found := manager.sessions[sessionID]
	if !found {
		manager.mu.Unlock()
		return &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("LSP session does not exist")}
	}
	if state.ownerID != ownerID {
		manager.mu.Unlock()
		return &lsp.Failure{Code: lsp.FailureSessionOwner, Err: errors.New("LSP session belongs to another account")}
	}
	manager.mu.Unlock()
	manager.cleanup(state)
	return nil
}

func (manager *Manager) acceptWorker(connection *net.UnixConn) {
	claimed := false
	defer func() {
		if !claimed {
			_ = connection.Close()
		}
	}()
	_ = connection.SetDeadline(time.Now().Add(manager.config.HandshakeTimeout))
	if err := lspunix.RequirePeerUID(connection, manager.config.ExpectedWorkerUID); err != nil {
		return
	}
	reader, err := lspwire.NewReader(connection, manager.config.Policy)
	if err != nil {
		return
	}
	hello, err := lspwire.ReadHello(reader)
	if err != nil {
		return
	}
	manager.mu.Lock()
	state, exists := manager.sessions[hello.SessionID]
	if !exists || state.conn != nil || time.Now().After(state.expiresAt) {
		manager.mu.Unlock()
		return
	}
	state.conn = connection
	state.reader = reader
	close(state.ready)
	manager.mu.Unlock()
	_ = connection.SetDeadline(time.Time{})
	claimed = true
}

func (manager *Manager) expire(state *sessionState) {
	timer := time.NewTimer(time.Until(state.expiresAt))
	defer timer.Stop()
	<-timer.C
	manager.cleanup(state)
}

func (manager *Manager) cleanup(state *sessionState) {
	if state == nil {
		return
	}
	state.once.Do(func() {
		manager.mu.Lock()
		delete(manager.sessions, state.id)
		connection := state.conn
		manager.mu.Unlock()
		if connection != nil {
			_ = connection.Close()
		}
		stopContext, cancel := context.WithTimeout(context.Background(), manager.config.StopTimeout)
		defer cancel()
		_ = manager.launcher.Stop(stopContext, state.id)
	})
}

func validOriginBinding(value string) bool {
	if value == "" || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return false
	}
	return parsed.Opaque == "" && parsed.Path == "" && parsed.RawPath == "" && parsed.RawQuery == "" &&
		!parsed.ForceQuery && parsed.Fragment == "" && parsed.String() == value
}

func attachTicketDigest(ownerID, sessionID, origin, attachTicket string) [sha256.Size]byte {
	return sha256.Sum256([]byte(ownerID + "\x00" + sessionID + "\x00" + origin + "\x00" + attachTicket))
}

func (manager *Manager) stopAll() {
	manager.mu.Lock()
	manager.closed = true
	states := make([]*sessionState, 0, len(manager.sessions))
	for _, state := range manager.sessions {
		states = append(states, state)
	}
	manager.mu.Unlock()
	for _, state := range states {
		manager.cleanup(state)
	}
}
