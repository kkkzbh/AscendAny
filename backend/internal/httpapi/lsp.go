package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
)

type LSPService interface {
	CreateSession(context.Context, string, string) (lsp.Session, error)
	ClaimSession(context.Context, string, string, string) (lsp.Attachment, error)
	CloseSession(context.Context, string, string) error
}

type lspWebSocketClient struct {
	connection *websocket.Conn
}

func (client *lspWebSocketClient) ReadMessage(ctx context.Context) ([]byte, error) {
	messageType, body, err := client.connection.Read(ctx)
	if err != nil {
		return nil, err
	}
	if messageType != websocket.MessageText {
		_ = client.connection.Close(websocket.StatusUnsupportedData, "text messages are required")
		return nil, errors.New("LSP WebSocket received a non-text message")
	}
	return body, nil
}

func (client *lspWebSocketClient) WriteMessage(ctx context.Context, body []byte) error {
	return client.connection.Write(ctx, websocket.MessageText, body)
}

func (handler *Handler) createLSPSession(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) {
		return
	}
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	account, origin, ok := handler.authenticateLSPRequest(writer, request)
	if !ok {
		return
	}
	if handler.lspService == nil {
		handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "lsp_unavailable", "LSP service is unavailable.")
		return
	}
	session, err := handler.lspService.CreateSession(request.Context(), account.ID, origin)
	if err != nil {
		handler.handleLSPError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Location", "/api/v2/lsp/sessions/"+session.ID)
	writeJSON(writer, http.StatusCreated, session)
}

func (handler *Handler) closeLSPSession(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) {
		return
	}
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	sessionID := request.PathValue("sessionId")
	if !lsp.ValidPublicID(sessionID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_lsp_session_id", "LSP session ID is invalid.")
		return
	}
	account, _, ok := handler.authenticateLSPRequest(writer, request)
	if !ok {
		return
	}
	if handler.lspService == nil {
		handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "lsp_unavailable", "LSP service is unavailable.")
		return
	}
	if err := handler.lspService.CloseSession(request.Context(), account.ID, sessionID); err != nil {
		handler.handleLSPError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) attachLSPSession(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) {
		return
	}
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	sessionID := request.PathValue("sessionId")
	if !lsp.ValidPublicID(sessionID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_lsp_session_id", "LSP session ID is invalid.")
		return
	}
	origin, ok := handler.requireLSPOrigin(writer, request)
	if !ok {
		return
	}
	if _, present, _ := singleHeader(request.Header, "Authorization"); present {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "lsp_websocket_authorization_not_allowed", "LSP WebSocket authorization must use its one-time attach ticket.")
		return
	}
	attachTicket, valid := lspWebSocketAttachTicket(request)
	if !valid {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_lsp_websocket_protocol", "LSP WebSocket protocol is invalid.")
		return
	}
	if handler.lspService == nil {
		handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "lsp_unavailable", "LSP service is unavailable.")
		return
	}
	attachment, err := handler.lspService.ClaimSession(request.Context(), sessionID, origin, attachTicket)
	if err != nil {
		handler.handleLSPError(writer, request, err)
		return
	}
	defer attachment.Close()
	origins := make([]string, 0, len(handler.allowedOrigins))
	for origin := range handler.allowedOrigins {
		origins = append(origins, origin)
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		Subprotocols: []string{lsp.WebSocketProtocolV1}, OriginPatterns: origins,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	connection.SetReadLimit(int64(handler.lspPolicy.MaximumBodyBytes))
	client := &lspWebSocketClient{connection: connection}
	bridgeErr := attachment.Bridge(context.Background(), client)
	if bridgeErr == nil || websocket.CloseStatus(bridgeErr) == websocket.StatusNormalClosure {
		_ = connection.Close(websocket.StatusNormalClosure, "session closed")
		return
	}
	handler.logger.WarnContext(request.Context(), "LSP WebSocket session ended",
		"request_id", requestID(request.Context()), "session_id", sessionID, "code", lspFailureCode(bridgeErr))
	_ = connection.Close(websocket.StatusPolicyViolation, "session terminated")
}

func (handler *Handler) authenticateLSPRequest(writer http.ResponseWriter, request *http.Request) (auth.Account, string, bool) {
	origin, ok := handler.requireLSPOrigin(writer, request)
	if !ok {
		return auth.Account{}, "", false
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return auth.Account{}, "", false
	}
	account, err := handler.auth.Me(request.Context(), access)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return auth.Account{}, "", false
	}
	return account, origin, true
}

func (handler *Handler) requireLSPOrigin(writer http.ResponseWriter, request *http.Request) (string, bool) {
	origin, present, valid := singleHeader(request.Header, "Origin")
	if !valid || !present || !handler.originAllowed(origin) {
		handler.writeAPIError(writer, request, http.StatusForbidden, "origin_rejected", "Request Origin was rejected.")
		return "", false
	}
	return origin, true
}

func lspWebSocketAttachTicket(request *http.Request) (string, bool) {
	value, present, valid := singleHeader(request.Header, "Sec-WebSocket-Protocol")
	if !present || !valid {
		return "", false
	}
	protocols := strings.Split(value, ",")
	if len(protocols) != 2 || strings.TrimSpace(protocols[0]) != lsp.WebSocketProtocolV1 {
		return "", false
	}
	ticketProtocol := strings.TrimSpace(protocols[1])
	if !strings.HasPrefix(ticketProtocol, lsp.WebSocketTicketPrefix) {
		return "", false
	}
	ticket := strings.TrimPrefix(ticketProtocol, lsp.WebSocketTicketPrefix)
	return ticket, lsp.ValidAttachTicket(ticket)
}

func (handler *Handler) handleLSPError(writer http.ResponseWriter, request *http.Request, err error) {
	var failure *lsp.Failure
	if errors.As(err, &failure) {
		switch failure.Code {
		case lsp.FailureCapacity:
			writer.Header().Set("Retry-After", "1")
			handler.writeAPIError(writer, request, http.StatusServiceUnavailable, string(failure.Code), "LSP session capacity is exhausted.")
			return
		case lsp.FailureStartup:
			handler.writeAPIError(writer, request, http.StatusServiceUnavailable, string(failure.Code), "LSP worker could not be started.")
			return
		case lsp.FailureSessionNotFound, lsp.FailureSessionOwner:
			handler.writeAPIError(writer, request, http.StatusNotFound, string(lsp.FailureSessionNotFound), "LSP session does not exist.")
			return
		case lsp.FailureAlreadyAttached:
			handler.writeAPIError(writer, request, http.StatusConflict, string(failure.Code), "LSP session already has a client.")
			return
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
		return
	}
	handler.logger.ErrorContext(request.Context(), "LSP operation failed",
		"request_id", requestID(request.Context()), "code", lspFailureCode(err))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func lspFailureCode(err error) string {
	var failure *lsp.Failure
	if errors.As(err, &failure) {
		return string(failure.Code)
	}
	if errors.Is(err, io.EOF) {
		return "lsp_disconnected"
	}
	return "lsp_internal_failure"
}
