package httpapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
)

func TestRawHTTPSlowAuthenticationBodyReturns408(t *testing.T) {
	var authCalled atomic.Bool
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		authCalled.Store(true)
		return auth.AuthResult{}, nil
	}}
	options.AuthBodyTimeout = 75 * time.Millisecond
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	address, stop := startRawHTTPServer(t, handler)
	defer stop()

	connection := dialRawHTTP(t, address)
	defer connection.Close()
	started := time.Now()
	_, _ = fmt.Fprintf(connection, "POST /api/v2/auth/login HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", address, testWebOrigin)
	response := readRawHTTPResponse(t, connection, http.MethodPost)
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if !response.Close || time.Since(started) > time.Second {
		t.Fatalf("close=%t duration=%s", response.Close, time.Since(started))
	}
	if authCalled.Load() {
		t.Fatal("slow body reached authentication service")
	}
}

func TestRawHTTPSlowEnrollmentClaimBodyReturns408(t *testing.T) {
	var enrollmentCalled atomic.Bool
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Enrollment = stubEnrollmentService{claim: func(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
		enrollmentCalled.Store(true)
		return auth.AuthResult{}, nil
	}}
	options.AuthBodyTimeout = 75 * time.Millisecond
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	address, stop := startRawHTTPServer(t, handler)
	defer stop()

	connection := dialRawHTTP(t, address)
	defer connection.Close()
	started := time.Now()
	_, _ = fmt.Fprintf(connection, "POST /api/v2/auth/enrollment-claims/consume HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nContent-Length: 200\r\n\r\n{", address, testWebOrigin)
	response := readRawHTTPResponse(t, connection, http.MethodPost)
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if !response.Close || time.Since(started) > time.Second {
		t.Fatalf("close=%t duration=%s", response.Close, time.Since(started))
	}
	if enrollmentCalled.Load() {
		t.Fatal("slow body reached enrollment service")
	}
}

func TestRawHTTPEarlyRejectionsInterruptPartialBodies(t *testing.T) {
	var authCalled atomic.Bool
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		authCalled.Store(true)
		return auth.AuthResult{}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	address, stop := startRawHTTPServer(t, handler)
	defer stop()

	tests := []struct {
		name       string
		request    string
		method     string
		wantStatus int
	}{
		{
			name: "origin rejection",
			request: fmt.Sprintf(
				"POST /api/v2/auth/login HTTP/1.1\r\nHost: %s\r\nOrigin: https://attacker.example\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{",
				address,
			),
			method:     http.MethodPost,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "body on health route",
			request:    fmt.Sprintf("GET /livez HTTP/1.1\r\nHost: %s\r\nContent-Length: 1\r\n\r\n", address),
			method:     http.MethodGet,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := dialRawHTTP(t, address)
			defer connection.Close()
			started := time.Now()
			if _, err := io.WriteString(connection, test.request); err != nil {
				t.Fatal(err)
			}
			response := readRawHTTPResponse(t, connection, test.method)
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus || !response.Close {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d close=%t body=%s", response.StatusCode, response.Close, body)
			}
			if time.Since(started) > 500*time.Millisecond {
				t.Fatalf("rejection took %s", time.Since(started))
			}
			if authCalled.Load() {
				t.Fatal("rejected body reached authentication service")
			}
		})
	}
}

func TestRawHTTPOversizedPartialJSONReturns413AndCloses(t *testing.T) {
	var authCalled atomic.Bool
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		authCalled.Store(true)
		return auth.AuthResult{}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	address, stop := startRawHTTPServer(t, handler)
	defer stop()

	connection := dialRawHTTP(t, address)
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "POST /api/v2/auth/login HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nContent-Length: 9000\r\n\r\n", address, testWebOrigin)
	if _, err := io.WriteString(connection, strings.Repeat("x", int(maxAuthJSONBytes+1))); err != nil {
		t.Fatal(err)
	}
	response := readRawHTTPResponse(t, connection, http.MethodPost)
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge || !response.Close {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d close=%t body=%s", response.StatusCode, response.Close, body)
	}
	if authCalled.Load() {
		t.Fatal("oversized body reached authentication service")
	}
}

func TestRawHTTPSSESlowReaderHitsWriteDeadlineAndReleasesSlot(t *testing.T) {
	payload := []byte(`{"progress":"` + strings.Repeat("x", 128<<10) + `"}`)
	var terminal atomic.Bool
	reader := stubImportReader{events: func(_ context.Context, _ string, after int64, _ int) (importing.EventBatch, bool, error) {
		if terminal.Load() {
			return importing.EventBatch{Terminal: true}, true, nil
		}
		events := make([]importing.PublicEvent, importing.MaxEventBatchSize)
		for index := range events {
			events[index] = importing.PublicEvent{
				Sequence:   after + int64(index) + 1,
				Type:       "progress",
				OccurredAt: time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC),
				Payload:    payload,
			}
		}
		return importing.EventBatch{Events: events}, true, nil
	}}
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
	}}
	settings := defaultImportHandlerLifetimeSettings()
	settings.sseMaxDuration = 2 * time.Second
	settings.sseReauthInterval = 250 * time.Millisecond
	settings.sseWriteTimeout = 75 * time.Millisecond
	settings.maxActiveSSE = 1
	handler := newImportTestHandlerWithSettings(t, true, service, unusedArtifactPublisher{}, unusedImportQueue{}, reader, settings)

	firstDone := make(chan struct{})
	var requests atomic.Int32
	wrapped := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		first := requests.Add(1) == 1
		handler.ServeHTTP(writer, request)
		if first {
			close(firstDone)
		}
	})
	address, stop := startRawHTTPServerWithSmallBuffers(t, wrapped)
	defer stop()

	connection := dialRawHTTP(t, address)
	if tcpConnection, ok := connection.(*net.TCPConn); ok {
		_ = tcpConnection.SetReadBuffer(1024)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET /api/v2/imports/%s/events HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nAuthorization: Bearer admin-token\r\n\r\n", testImportJobID, address, testWebOrigin)
	buffered := bufio.NewReader(connection)
	request := &http.Request{Method: http.MethodGet}
	response, err := http.ReadResponse(buffered, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("slow SSE reader kept the handler past its write deadline")
	}

	terminal.Store(true)
	secondConnection := dialRawHTTP(t, address)
	defer secondConnection.Close()
	_, _ = fmt.Fprintf(secondConnection, "GET /api/v2/imports/%s/events HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nAuthorization: Bearer admin-token\r\n\r\n", testImportJobID, address, testWebOrigin)
	secondResponse := readRawHTTPResponse(t, secondConnection, http.MethodGet)
	defer secondResponse.Body.Close()
	if secondResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(secondResponse.Body)
		t.Fatalf("second stream status=%d body=%s", secondResponse.StatusCode, body)
	}
}

type smallWriteBufferListener struct {
	net.Listener
}

func (listener smallWriteBufferListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcpConnection, ok := connection.(*net.TCPConn); ok {
		if err := tcpConnection.SetWriteBuffer(1024); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	return connection, nil
}

func startRawHTTPServer(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	return startRawHTTPServerOnListener(t, handler, false)
}

func startRawHTTPServerWithSmallBuffers(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	return startRawHTTPServerOnListener(t, handler, true)
}

func startRawHTTPServerOnListener(t *testing.T, handler http.Handler, smallBuffers bool) (string, func()) {
	t.Helper()
	baseListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var listener net.Listener = baseListener
	if smallBuffers {
		listener = smallWriteBufferListener{Listener: baseListener}
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       time.Second,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = server.Close()
			err := <-serveDone
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("HTTP server error: %v", err)
			}
		})
	}
	return baseListener.Addr().String(), stop
}

func dialRawHTTP(t *testing.T, address string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	return connection
}

func readRawHTTPResponse(t *testing.T, connection net.Conn, method string) *http.Response {
	t.Helper()
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	return response
}
