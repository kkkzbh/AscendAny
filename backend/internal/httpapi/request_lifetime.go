package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

type requestBodyLifetimeContextKey struct{}

type requestBodyLifetimeState uint8

const (
	requestBodyActive requestBodyLifetimeState = iota
	requestBodyFinished
	requestBodyAborted
)

type requestBodyLifetime struct {
	mu            sync.Mutex
	request       *http.Request
	controller    *http.ResponseController
	readContext   context.Context
	cancel        context.CancelFunc
	deadline      time.Time
	state         requestBodyLifetimeState
	stopInterrupt func() bool
	interruptDone chan struct{}
	interruptOnce sync.Once
}

func beginRequestBodyLifetime(
	writer http.ResponseWriter,
	request *http.Request,
	timeout time.Duration,
) (*http.Request, *requestBodyLifetime, error) {
	deadline := time.Now().Add(timeout)
	controller := http.NewResponseController(writer)
	// Probe the connection capability without racing the context deadline.
	// The context transitions first; its callback then expires the socket read.
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		return request, nil, err
	}
	readContext, cancel := context.WithDeadline(request.Context(), deadline)
	lifetime := &requestBodyLifetime{
		controller:    controller,
		readContext:   readContext,
		cancel:        cancel,
		deadline:      deadline,
		state:         requestBodyActive,
		interruptDone: make(chan struct{}),
	}
	request = request.WithContext(context.WithValue(
		request.Context(),
		requestBodyLifetimeContextKey{},
		lifetime,
	))
	lifetime.request = request
	lifetime.stopInterrupt = context.AfterFunc(readContext, lifetime.interrupt)
	return request, lifetime, nil
}

func (lifetime *requestBodyLifetime) interrupt() {
	defer lifetime.signalInterruptDone()
	lifetime.mu.Lock()
	if lifetime.state != requestBodyActive {
		lifetime.mu.Unlock()
		return
	}
	lifetime.state = requestBodyAborted
	_ = lifetime.controller.SetReadDeadline(time.Now())
	lifetime.mu.Unlock()
	_ = lifetime.request.Body.Close()
}

func (lifetime *requestBodyLifetime) signalInterruptDone() {
	lifetime.interruptOnce.Do(func() { close(lifetime.interruptDone) })
}

func (lifetime *requestBodyLifetime) finish() error {
	lifetime.mu.Lock()
	if lifetime.state == requestBodyAborted {
		cause := context.Cause(lifetime.readContext)
		lifetime.mu.Unlock()
		lifetime.stopAndJoinInterrupt()
		lifetime.cancel()
		if cause == nil {
			return context.Canceled
		}
		return cause
	}
	if lifetime.state == requestBodyFinished {
		lifetime.mu.Unlock()
		return errors.New("request body lifetime was already finished")
	}
	if !time.Now().Before(lifetime.deadline) || context.Cause(lifetime.readContext) != nil {
		lifetime.state = requestBodyAborted
		cause := context.Cause(lifetime.readContext)
		if cause == nil {
			cause = context.DeadlineExceeded
		}
		_ = lifetime.controller.SetReadDeadline(time.Now())
		lifetime.request.Close = true
		lifetime.mu.Unlock()
		_ = lifetime.request.Body.Close()
		lifetime.stopAndJoinInterrupt()
		lifetime.cancel()
		return cause
	}
	lifetime.state = requestBodyFinished
	err := lifetime.controller.SetReadDeadline(time.Time{})
	lifetime.mu.Unlock()
	lifetime.stopAndJoinInterrupt()
	lifetime.cancel()
	return err
}

func (lifetime *requestBodyLifetime) abort() {
	lifetime.mu.Lock()
	if lifetime.state == requestBodyAborted {
		lifetime.mu.Unlock()
		lifetime.stopAndJoinInterrupt()
		lifetime.cancel()
		return
	}
	if lifetime.state == requestBodyFinished {
		lifetime.mu.Unlock()
		return
	}
	lifetime.state = requestBodyAborted
	_ = lifetime.controller.SetReadDeadline(time.Now())
	lifetime.request.Close = true
	lifetime.mu.Unlock()
	_ = lifetime.request.Body.Close()
	lifetime.stopAndJoinInterrupt()
	lifetime.cancel()
}

func (lifetime *requestBodyLifetime) abortIfActive() {
	lifetime.abort()
}

func (lifetime *requestBodyLifetime) stopAndJoinInterrupt() {
	if lifetime.stopInterrupt() {
		lifetime.signalInterruptDone()
	}
	<-lifetime.interruptDone
}

func requestBodyLifetimeFrom(request *http.Request) (*requestBodyLifetime, bool) {
	lifetime, ok := request.Context().Value(requestBodyLifetimeContextKey{}).(*requestBodyLifetime)
	return lifetime, ok && lifetime != nil
}

func requestBodyReadContext(request *http.Request) context.Context {
	if lifetime, ok := requestBodyLifetimeFrom(request); ok {
		return lifetime.readContext
	}
	return request.Context()
}

func finishRequestBodyRead(request *http.Request) error {
	lifetime, ok := requestBodyLifetimeFrom(request)
	if !ok {
		return errors.New("request body lifetime is missing")
	}
	return lifetime.finish()
}

func abortUnreadRequestBody(writer http.ResponseWriter, request *http.Request) {
	if requestBodyIsEmpty(request) {
		return
	}
	if lifetime, ok := requestBodyLifetimeFrom(request); ok {
		lifetime.mu.Lock()
		finished := lifetime.state == requestBodyFinished
		lifetime.mu.Unlock()
		if finished {
			return
		}
		writer.Header().Set("Connection", "close")
		request.Close = true
		lifetime.abort()
		return
	}
	writer.Header().Set("Connection", "close")
	request.Close = true
	_ = http.NewResponseController(writer).SetReadDeadline(time.Now())
	_ = request.Body.Close()
}

func unwrapResponseWriter(writer http.ResponseWriter) http.ResponseWriter {
	for {
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return writer
		}
		next := unwrapper.Unwrap()
		if next == nil || next == writer {
			return writer
		}
		writer = next
	}
}
