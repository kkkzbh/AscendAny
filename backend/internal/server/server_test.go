package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewSetsGlobalReadCeilingAndLeavesSSEWritesToHandler(t *testing.T) {
	t.Parallel()

	server := New(Options{
		Address:           "127.0.0.1:0",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       time.Minute,
	}, http.NotFoundHandler())
	if server.ReadTimeout != 2*time.Minute || server.WriteTimeout != 0 {
		t.Fatalf("streaming deadlines = read %s, write %s", server.ReadTimeout, server.WriteTimeout)
	}
	if server.MaxHeaderBytes != maxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, maxHeaderBytes)
	}
}

func TestServeCancelsActiveRequestBeforeGracefulShutdown(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requestStarted := make(chan struct{})
	requestStopped := make(chan struct{})
	httpServer := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
		close(requestStopped)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		serveDone <- Serve(ctx, httpServer, listener, time.Second, logger)
	}()

	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			_ = response.Body.Close()
		}
		requestDone <- requestErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case <-requestStopped:
	case <-time.After(time.Second):
		t.Fatal("active request context was not canceled")
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	<-requestDone
}

func TestServeStopsGracefullyWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-releaseRequest
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		serveDone <- Serve(ctx, httpServer, listener, time.Second, logger)
	}()

	requestDone := make(chan error, 1)
	go func() {
		request, requestErr := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String(), nil)
		if requestErr != nil {
			requestDone <- requestErr
			return
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
		}
		requestDone <- requestErr
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	close(releaseRequest)

	if err := <-requestDone; err != nil {
		t.Fatalf("HTTP request error = %v", err)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}
