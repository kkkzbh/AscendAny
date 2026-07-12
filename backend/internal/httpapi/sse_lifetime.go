package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

func installSSEWriteInterrupt(
	ctx context.Context,
	controller *http.ResponseController,
) func() {
	done := make(chan struct{})
	var doneOnce sync.Once
	signalDone := func() { doneOnce.Do(func() { close(done) }) }
	stop := context.AfterFunc(ctx, func() {
		_ = controller.SetWriteDeadline(time.Now())
		signalDone()
	})
	return func() {
		if stop() {
			signalDone()
		}
		<-done
	}
}

func (handler *Handler) setSSEWriteDeadline(
	controller *http.ResponseController,
	streamDeadline time.Time,
) error {
	deadline := time.Now().Add(handler.sseWriteTimeout)
	if streamDeadline.Before(deadline) {
		deadline = streamDeadline
	}
	if !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return controller.SetWriteDeadline(deadline)
}

func clearSSEWriteDeadline(controller *http.ResponseController) error {
	return controller.SetWriteDeadline(time.Time{})
}
