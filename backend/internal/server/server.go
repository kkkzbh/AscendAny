package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const maxHeaderBytes = 32 << 10

type Options struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration
}

func New(options Options, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              options.Address,
		Handler:           handler,
		ReadHeaderTimeout: options.ReadHeaderTimeout,
		// ReadTimeout is the global connection-level ceiling. Body-bearing
		// handlers install narrower route-specific read deadlines.
		ReadTimeout: options.ReadTimeout,
		// SSE handlers own their per-write and total stream deadlines.
		WriteTimeout:   0,
		IdleTimeout:    options.IdleTimeout,
		MaxHeaderBytes: maxHeaderBytes,
	}
}

func Run(ctx context.Context, httpServer *http.Server, shutdownTimeout time.Duration, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}
	return Serve(ctx, httpServer, listener, shutdownTimeout, logger)
}

func Serve(ctx context.Context, httpServer *http.Server, listener net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	if httpServer.BaseContext != nil {
		return errors.New("HTTP server BaseContext must be owned by Serve")
	}
	httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	logger.Info("HTTP server started", "address", listener.Addr().String())
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		logger.Info("HTTP server shutdown started")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdownContext)
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	serveErr := <-serveErrors
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
	}
	if shutdownErr != nil {
		return fmt.Errorf("gracefully shut down HTTP server: %w", shutdownErr)
	}
	logger.Info("HTTP server stopped")
	return nil
}
