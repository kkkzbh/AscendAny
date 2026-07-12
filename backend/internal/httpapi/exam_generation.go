package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/examgeneration"
)

const (
	examGenerationEventBatchSize    = examgeneration.MaxEventPageSize
	examGenerationEventPollInterval = 500 * time.Millisecond
	examGenerationEventHeartbeat    = 15 * time.Second
)

func (handler *Handler) getCurrentExamGeneration(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	examID := request.PathValue("examId")
	if !examgeneration.ValidPublicID(examID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_id", "Exam ID must be a canonical UUIDv4.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	generation, found, err := handler.examGeneration.GetCurrent(request.Context(), access, examID)
	if err != nil {
		handler.handleExamGenerationError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "exam_generation_not_found", "The exam has no current analysis generation.")
		return
	}
	writeJSON(writer, http.StatusOK, generation)
}

func (handler *Handler) streamExamGenerationEvents(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	examID := request.PathValue("examId")
	if !examgeneration.ValidPublicID(examID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_id", "Exam ID must be a canonical UUIDv4.")
		return
	}
	generationID := request.PathValue("generationId")
	if !examgeneration.ValidGenerationID(generationID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_generation_id", "Generation ID must be a canonical positive decimal int64.")
		return
	}
	after, err := parseLastEventID(request.Header)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be one canonical non-negative decimal sequence.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	if !handler.acquireSSE(writer, request) {
		return
	}
	defer handler.releaseSSE()

	streamDeadline := time.Now().Add(handler.sseMaxDuration)
	streamContext, cancelStream := context.WithDeadline(request.Context(), streamDeadline)
	defer cancelStream()
	request = request.WithContext(streamContext)

	batch, found, err := handler.examGeneration.ReadEvents(
		request.Context(), access, examID, generationID, after, examGenerationEventBatchSize,
	)
	if err != nil {
		handler.handleExamGenerationError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "exam_generation_not_found", "The requested analysis generation does not belong to this exam.")
		return
	}
	if batch.GenerationID != generationID {
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}

	controller := http.NewResponseController(writer)
	if err := clearSSEWriteDeadline(controller); err != nil {
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	stopWriteInterrupt := installSSEWriteInterrupt(request.Context(), controller)
	defer stopWriteInterrupt()
	if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
		handler.handleSSESetupError(writer, request, err)
		return
	}
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}
	if err := clearSSEWriteDeadline(controller); err != nil {
		return
	}

	heartbeat := time.NewTimer(examGenerationEventHeartbeat)
	defer heartbeat.Stop()
	reauthorize := time.NewTicker(handler.sseReauthInterval)
	defer reauthorize.Stop()
	for {
		if len(batch.Events) > 0 {
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			for _, event := range batch.Events {
				if err := writeExamGenerationEvent(writer, event); err != nil {
					return
				}
				after = event.Sequence
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
		}
		if batch.Terminal && after == batch.EventHead {
			return
		}
		if len(batch.Events) == examGenerationEventBatchSize {
			batch, found, err = handler.examGeneration.ReadEvents(
				request.Context(), access, examID, generationID, after, examGenerationEventBatchSize,
			)
			if err != nil || !found || batch.GenerationID != generationID {
				handler.logExamGenerationStreamFailure(
					request, err, found, err == nil && found && batch.GenerationID != generationID,
				)
				return
			}
			continue
		}

		poll := time.NewTimer(examGenerationEventPollInterval)
		select {
		case <-request.Context().Done():
			poll.Stop()
			return
		case <-heartbeat.C:
			poll.Stop()
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			if _, err := fmt.Fprint(writer, ": keep-alive\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
			heartbeat.Reset(examGenerationEventHeartbeat)
		case <-reauthorize.C:
			poll.Stop()
		case <-poll.C:
		}
		batch, found, err = handler.examGeneration.ReadEvents(
			request.Context(), access, examID, generationID, after, examGenerationEventBatchSize,
		)
		if err != nil || !found || batch.GenerationID != generationID {
			handler.logExamGenerationStreamFailure(
				request, err, found, err == nil && found && batch.GenerationID != generationID,
			)
			return
		}
	}
}

func writeExamGenerationEvent(writer http.ResponseWriter, event examgeneration.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, compact.Bytes())
	return err
}

func (handler *Handler) handleExamGenerationError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch examgeneration.CodeOf(err) {
	case examgeneration.ErrorInvalidInput:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_generation_request", "Exam analysis generation request is invalid.")
		return
	case examgeneration.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case examgeneration.ErrorEventCursorInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_event_cursor", "Last-Event-ID exceeds the durable event head.")
		return
	case examgeneration.ErrorCanceled:
		if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "exam analysis generation HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", examgeneration.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logExamGenerationStreamFailure(
	request *http.Request,
	err error,
	found bool,
	generationMismatch bool,
) {
	code := string(examgeneration.CodeOf(err))
	if !found {
		code = "exam_generation_not_found"
	}
	if generationMismatch {
		code = "exam_generation_mismatch"
	}
	if code == "" {
		code = "exam_generation_stream_failure"
	}
	handler.logger.ErrorContext(request.Context(), "exam analysis generation event stream ended",
		"request_id", requestID(request.Context()),
		"code", code,
	)
}
