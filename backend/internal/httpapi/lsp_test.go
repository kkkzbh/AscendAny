package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
)

const (
	lspTestAccountID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	lspTestSessionID = "11111111-1111-4111-8111-111111111111"
	lspTestOrigin    = "https://ascendany.example"
	lspTestTicket    = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type lspAuthService struct{ unusedAuthService }

func (lspAuthService) Me(_ context.Context, token string) (auth.Account, error) {
	if token != "access-token" {
		return auth.Account{}, errors.New("rejected token")
	}
	return auth.Account{ID: lspTestAccountID, Username: "student", DisplayName: "Student", Role: auth.RoleStudent}, nil
}

type stubLSPService struct {
	mu sync.Mutex

	createdOwner     string
	createdOrigin    string
	checkedID        string
	claimedOrigin    string
	claimedTicket    string
	closedOwner      string
	closedID         string
	bridgedBody      []byte
	bridgeReads      int
	attachmentClosed bool
}

func (service *stubLSPService) CreateSession(_ context.Context, ownerID, origin string) (lsp.Session, error) {
	service.mu.Lock()
	service.createdOwner = ownerID
	service.createdOrigin = origin
	service.mu.Unlock()
	return lsp.Session{
		ID: lspTestSessionID, WorkspaceURI: lsp.PublicWorkspaceURI,
		WebSocketPath: "/api/v2/lsp/sessions/" + lspTestSessionID + "/websocket",
		AttachTicket:  lspTestTicket, ExpiresAt: time.Now().Add(time.Minute).UTC(),
	}, nil
}

func (service *stubLSPService) ClaimSession(_ context.Context, sessionID, origin, ticket string) (lsp.Attachment, error) {
	service.mu.Lock()
	service.checkedID = sessionID
	service.claimedOrigin = origin
	service.claimedTicket = ticket
	service.mu.Unlock()
	if sessionID != lspTestSessionID || origin != lspTestOrigin || ticket != lspTestTicket {
		return nil, &lsp.Failure{Code: lsp.FailureSessionNotFound, Err: errors.New("not found")}
	}
	return &stubLSPAttachment{service: service}, nil
}

type stubLSPAttachment struct {
	service *stubLSPService
}

func (attachment *stubLSPAttachment) Bridge(ctx context.Context, client lsp.Client) error {
	service := attachment.service
	body, err := client.ReadMessage(ctx)
	service.mu.Lock()
	service.bridgeReads++
	service.bridgedBody = append([]byte(nil), body...)
	service.mu.Unlock()
	if err != nil {
		return err
	}
	if err := client.WriteMessage(ctx, body); err != nil {
		return err
	}
	return nil
}

func (attachment *stubLSPAttachment) Close() {
	attachment.service.mu.Lock()
	attachment.service.attachmentClosed = true
	attachment.service.mu.Unlock()
}

func (service *stubLSPService) CloseSession(_ context.Context, ownerID, sessionID string) error {
	service.mu.Lock()
	service.closedOwner = ownerID
	service.closedID = sessionID
	service.mu.Unlock()
	return nil
}

func TestLSPCreateAndCloseRequireOriginBearerAndNoQuery(t *testing.T) {
	service := &stubLSPService{}
	handler := newLSPTestHandler(t, service)
	create := httptest.NewRequest(http.MethodPost, "/api/v2/lsp/sessions", nil)
	create.RemoteAddr = "192.0.2.1:1234"
	create.Header.Set("Origin", lspTestOrigin)
	create.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated || response.Header().Get("Location") != "/api/v2/lsp/sessions/"+lspTestSessionID || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"attachTicket":"`+lspTestTicket+`"`) {
		t.Fatalf("create status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
	service.mu.Lock()
	if service.createdOwner != lspTestAccountID || service.createdOrigin != lspTestOrigin {
		t.Fatalf("created owner/origin = %q/%q", service.createdOwner, service.createdOrigin)
	}
	service.mu.Unlock()

	closeRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/lsp/sessions/"+lspTestSessionID, nil)
	closeRequest.RemoteAddr = "192.0.2.1:1234"
	closeRequest.Header.Set("Origin", lspTestOrigin)
	closeRequest.Header.Set("Authorization", "Bearer access-token")
	closeResponse := httptest.NewRecorder()
	handler.ServeHTTP(closeResponse, closeRequest)
	if closeResponse.Code != http.StatusNoContent {
		t.Fatalf("close status=%d body=%s", closeResponse.Code, closeResponse.Body.String())
	}
	service.mu.Lock()
	if service.closedOwner != lspTestAccountID || service.closedID != lspTestSessionID {
		t.Fatalf("close owner/id = %q/%q", service.closedOwner, service.closedID)
	}
	service.mu.Unlock()

	for name, request := range map[string]*http.Request{
		"missing origin": func() *http.Request {
			value := httptest.NewRequest(http.MethodPost, "/api/v2/lsp/sessions", nil)
			value.Header.Set("Authorization", "Bearer access-token")
			return value
		}(),
		"query token": func() *http.Request {
			value := httptest.NewRequest(http.MethodPost, "/api/v2/lsp/sessions?access_token=stolen", nil)
			value.Header.Set("Origin", lspTestOrigin)
			value.Header.Set("Authorization", "Bearer access-token")
			return value
		}(),
		"missing bearer": func() *http.Request {
			value := httptest.NewRequest(http.MethodPost, "/api/v2/lsp/sessions", nil)
			value.Header.Set("Origin", lspTestOrigin)
			return value
		}(),
	} {
		request.RemoteAddr = "192.0.2.2:1234"
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if name == "missing origin" && result.Code != http.StatusForbidden {
			t.Errorf("%s status = %d", name, result.Code)
		}
		if name == "query token" && result.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", name, result.Code)
		}
		if name == "missing bearer" && result.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d", name, result.Code)
		}
	}
}

func TestLSPWebSocketUsesOneTimeTicketProtocolAndTextOnlyMessages(t *testing.T) {
	service := &stubLSPService{}
	server := httptest.NewServer(newLSPTestHandler(t, service))
	defer server.Close()
	webSocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/lsp/sessions/" + lspTestSessionID + "/websocket"
	header := http.Header{"Origin": []string{lspTestOrigin}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, webSocketURL, &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: lspTestSubprotocols(), CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil {
			t.Fatalf("dial: %v status=%d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	if err := connection.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	messageType, echoed, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText || string(echoed) != string(body) {
		t.Fatalf("echo type/body = %v/%s", messageType, echoed)
	}
	_, _, _ = connection.Read(ctx)
	service.mu.Lock()
	if service.checkedID != lspTestSessionID || service.claimedOrigin != lspTestOrigin || service.claimedTicket != lspTestTicket || string(service.bridgedBody) != string(body) {
		t.Fatalf("service claim/body = %q/%q/%q/%s", service.checkedID, service.claimedOrigin, service.claimedTicket, service.bridgedBody)
	}
	service.mu.Unlock()
	requireLSPAttachmentClosed(t, service)

	binaryService := &stubLSPService{}
	binaryServer := httptest.NewServer(newLSPTestHandler(t, binaryService))
	defer binaryServer.Close()
	binaryURL := "ws" + strings.TrimPrefix(binaryServer.URL, "http") + "/api/v2/lsp/sessions/" + lspTestSessionID + "/websocket"
	binaryConnection, _, err := websocket.Dial(ctx, binaryURL, &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: lspTestSubprotocols(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := binaryConnection.Write(ctx, websocket.MessageBinary, []byte("not LSP JSON")); err != nil {
		t.Fatal(err)
	}
	_, _, err = binaryConnection.Read(ctx)
	if status := websocket.CloseStatus(err); status != websocket.StatusUnsupportedData && status != websocket.StatusPolicyViolation {
		t.Fatalf("binary close status=%d error=%v", status, err)
	}
}

func TestLSPWebSocketRejectsQueryTokenAndWrongProtocolBeforeUpgrade(t *testing.T) {
	service := &stubLSPService{}
	server := httptest.NewServer(newLSPTestHandler(t, service))
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/lsp/sessions/" + lspTestSessionID + "/websocket"
	header := http.Header{"Origin": []string{lspTestOrigin}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for name, target := range map[string]string{
		"query token": base + "?access_token=stolen",
		"any query":   base + "?x=1",
	} {
		connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPHeader: header, Subprotocols: lspTestSubprotocols()})
		if connection != nil {
			_ = connection.CloseNow()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s error=%v response=%v", name, err, response)
		}
	}
	connection, response, err := websocket.Dial(ctx, base, &websocket.DialOptions{HTTPHeader: header, Subprotocols: []string{"bearer.stolen"}})
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong protocol error=%v response=%v", err, response)
	}
	authorizedHeader := header.Clone()
	authorizedHeader.Set("Authorization", "Bearer access-token")
	connection, response, err = websocket.Dial(ctx, base, &websocket.DialOptions{HTTPHeader: authorizedHeader, Subprotocols: lspTestSubprotocols()})
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("authorization header error=%v response=%v", err, response)
	}
	wrongOriginHeader := http.Header{"Origin": []string{"https://attacker.example"}}
	connection, response, err = websocket.Dial(ctx, base, &websocket.DialOptions{HTTPHeader: wrongOriginHeader, Subprotocols: lspTestSubprotocols()})
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin error=%v response=%v", err, response)
	}
}

func TestLSPWebSocketUpgradeFailureDestroysClaimedSession(t *testing.T) {
	service := &stubLSPService{}
	handler := newLSPTestHandler(t, service)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/lsp/sessions/"+lspTestSessionID+"/websocket", nil)
	request.RemoteAddr = "192.0.2.9:1234"
	request.Header.Set("Origin", lspTestOrigin)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Protocol", strings.Join(lspTestSubprotocols(), ", "))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.attachmentClosed || service.claimedOrigin != lspTestOrigin || service.claimedTicket != lspTestTicket {
		t.Fatalf("claim was not destroyed after upgrade failure: %#v", service)
	}
}

func lspTestSubprotocols() []string {
	return []string{lsp.WebSocketProtocolV1, lsp.WebSocketTicketPrefix + lspTestTicket}
}

func requireLSPAttachmentClosed(t *testing.T, service *stubLSPService) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.Lock()
		closed := service.attachmentClosed
		service.mu.Unlock()
		if closed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("claimed LSP attachment was not closed")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEnabledLSPRequiresSharedValidPolicy(t *testing.T) {
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.LSP = &stubLSPService{}
	if _, err := New(options); err == nil {
		t.Fatal("LSP service without a shared valid policy was accepted")
	}
}

func TestDisabledWritesRejectLSPDependencies(t *testing.T) {
	for name, configure := range map[string]func(*Options){
		"transport": func(options *Options) {
			options.LSP = &stubLSPService{}
			options.LSPPolicy = lsp.DefaultPolicy()
		},
		"policy": func(options *Options) {
			options.LSPPolicy = lsp.DefaultPolicy()
		},
	} {
		t.Run(name, func(t *testing.T) {
			options := disabledLSPTestOptions()
			configure(&options)
			if _, err := New(options); err == nil {
				t.Fatalf("disabled writes accepted LSP %s dependency", name)
			}
		})
	}
}

func TestDisabledWritesRejectEveryLSPRouteBeforeValidation(t *testing.T) {
	handler, err := New(disabledLSPTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/v2/lsp/sessions?unexpected=true", strings.NewReader("unexpected")),
		httptest.NewRequest(http.MethodDelete, "/api/v2/lsp/sessions/not-a-session", strings.NewReader("unexpected")),
		httptest.NewRequest(http.MethodGet, "/api/v2/lsp/sessions/not-a-session/websocket?unexpected=true", strings.NewReader("unexpected")),
	}
	for _, request := range requests {
		request.RemoteAddr = "192.0.2.9:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"writes_disabled"`) {
			t.Errorf("%s %s status=%d body=%s", request.Method, request.URL, response.Code, response.Body.String())
		}
	}
}

func disabledLSPTestOptions() Options {
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Artifacts = nil
	options.Imports = nil
	options.ModelProbe = nil
	options.Capabilities = testCapabilities(false)
	return options
}

func newLSPTestHandler(t *testing.T, service LSPService) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = lspAuthService{}
	options.LSP = service
	options.LSPPolicy = lsp.DefaultPolicy()
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
