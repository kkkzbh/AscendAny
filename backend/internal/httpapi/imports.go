package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
)

const (
	importEventPollInterval = 500 * time.Millisecond
	importEventHeartbeat    = 15 * time.Second
)

func (handler *Handler) listImportJobs(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	cursor, limit, err := parseImportJobPageQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_import_page", fmt.Sprintf("cursor and limit must match the import history pagination contract; limit must be from 1 through %d.", importing.MaxJobPageSize))
		return
	}
	if _, ok := handler.requireAdmin(writer, request); !ok {
		return
	}
	page, err := handler.importReader.ListJobs(request.Context(), cursor, limit)
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func parseImportJobPageQuery(rawQuery string, forceQuery bool) (*string, int, error) {
	return parseCursorPageQuery(
		rawQuery,
		forceQuery,
		importing.DefaultJobPageSize,
		importing.MaxJobPageSize,
		importing.ValidPublicID,
	)
}

func (handler *Handler) createPintiaImport(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.requireAdmin(writer, request); !ok {
		return
	}
	contentType, present, valid := singleHeader(request.Header, "Content-Type")
	if !valid || !present || contentType != importing.PintiaSnapshotV2MediaType {
		handler.writeAPIError(writer, request, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must match the Pintia snapshot v2 contract.")
		return
	}
	if len(request.Header.Values("Content-Encoding")) != 0 {
		handler.writeAPIError(writer, request, http.StatusUnsupportedMediaType, "unsupported_content_encoding", "Content-Encoding is not supported.")
		return
	}
	if request.ContentLength > handler.capabilities.MaxUploadBytes {
		handler.writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "payload_too_large", "Pintia snapshot exceeds the configured upload limit.")
		return
	}

	publication, err := handler.artifacts.Publish(requestBodyReadContext(request), request.Body)
	if err != nil {
		handler.handleArtifactError(writer, request, err)
		return
	}
	if err := finishRequestBodyRead(request); err != nil {
		if releaseErr := publication.Release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
		handler.handleRequestBodyError(writer, request, err, "Upload exceeded its duration limit.")
		return
	}
	queued, err := handler.imports.QueuePublication(request.Context(), publication, importing.PintiaSnapshotV2MediaType)
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}
	job, found, err := handler.importReader.GetJob(request.Context(), queued.Job.PublicID)
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}
	if !found {
		handler.logImportFailure(request, "queued_job_missing")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (handler *Handler) getImportJob(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if _, ok := handler.requireAdmin(writer, request); !ok {
		return
	}
	publicID := request.PathValue("jobId")
	if !importing.ValidPublicID(publicID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_job_id", "Import job ID must be a canonical UUIDv4.")
		return
	}
	job, found, err := handler.importReader.GetJob(request.Context(), publicID)
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "import_job_not_found", "Import job does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (handler *Handler) streamImportEvents(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	access, ok := handler.requireAdmin(writer, request)
	if !ok {
		return
	}
	publicID := request.PathValue("jobId")
	if !importing.ValidPublicID(publicID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_job_id", "Import job ID must be a canonical UUIDv4.")
		return
	}
	after, err := parseLastEventID(request.Header)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be one canonical non-negative decimal sequence.")
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

	batch, found, err := handler.importReader.ReadEvents(request.Context(), publicID, after, importing.MaxEventBatchSize)
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "import_job_not_found", "Import job does not exist.")
		return
	}
	if !handler.authorizeAdmin(writer, request, access) {
		return
	}

	controller := http.NewResponseController(writer)
	if err := clearSSEWriteDeadline(controller); err != nil {
		handler.logImportFailure(request, "stream_write_deadline_unsupported")
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
	heartbeat := time.NewTimer(importEventHeartbeat)
	defer heartbeat.Stop()
	reauthorize := time.NewTicker(handler.sseReauthInterval)
	defer reauthorize.Stop()

	for {
		if request.Context().Err() != nil {
			return
		}
		select {
		case <-reauthorize.C:
			if !handler.reauthorizeAdmin(request, access) {
				return
			}
		default:
		}
		if len(batch.Events) > 0 {
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			for _, event := range batch.Events {
				if err := writeImportEvent(writer, event); err != nil {
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
		if batch.Terminal {
			return
		}
		if len(batch.Events) == importing.MaxEventBatchSize {
			batch, found, err = handler.importReader.ReadEvents(request.Context(), publicID, after, importing.MaxEventBatchSize)
			if err != nil || !found {
				handler.logImportStreamFailure(request, err, found)
				return
			}
			continue
		}

		poll := time.NewTimer(importEventPollInterval)
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
			heartbeat.Reset(importEventHeartbeat)
		case <-reauthorize.C:
			poll.Stop()
			if !handler.reauthorizeAdmin(request, access) {
				return
			}
		case <-poll.C:
		}

		batch, found, err = handler.importReader.ReadEvents(request.Context(), publicID, after, importing.MaxEventBatchSize)
		if err != nil || !found {
			handler.logImportStreamFailure(request, err, found)
			return
		}
	}
}

func (handler *Handler) requireAdmin(writer http.ResponseWriter, request *http.Request) (string, bool) {
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return "", false
	}
	if !handler.authorizeAdmin(writer, request, access) {
		return "", false
	}
	return access, true
}

func (handler *Handler) authorizeAdmin(writer http.ResponseWriter, request *http.Request, access string) bool {
	account, err := handler.auth.Me(request.Context(), access)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return false
	}
	if account.Role != auth.RoleAdmin {
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return false
	}
	return true
}

func (handler *Handler) acquireSSE(writer http.ResponseWriter, request *http.Request) bool {
	select {
	case handler.sseSlots <- struct{}{}:
		return true
	default:
		writer.Header().Set("Retry-After", "1")
		handler.writeAPIError(writer, request, http.StatusTooManyRequests, "sse_capacity_exhausted", "Active event stream capacity is exhausted.")
		return false
	}
}

func (handler *Handler) releaseSSE() {
	<-handler.sseSlots
}

func (handler *Handler) reauthorizeAdmin(request *http.Request, access string) bool {
	account, err := handler.auth.Me(request.Context(), access)
	if err == nil && account.Role == auth.RoleAdmin {
		return true
	}
	code := auth.ErrorCodeOf(err)
	if err == nil {
		code = auth.ErrorForbidden
	}
	handler.logger.InfoContext(request.Context(), "import event stream authorization ended",
		"request_id", requestID(request.Context()),
		"code", code,
	)
	return false
}

func (handler *Handler) handleSSESetupError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Event stream setup exceeded its duration limit.")
		return
	}
	handler.logImportFailure(request, "stream_write_deadline")
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func parseLastEventID(header http.Header) (int64, error) {
	value, present, valid := singleHeader(header, "Last-Event-ID")
	if !valid {
		return 0, errors.New("Last-Event-ID must occur once")
	}
	if !present {
		return 0, nil
	}
	if value == "" {
		return 0, errors.New("Last-Event-ID must not be empty")
	}
	if value != "0" && (strings.HasPrefix(value, "0") || value[0] < '1' || value[0] > '9') {
		return 0, errors.New("Last-Event-ID is not canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("Last-Event-ID is not decimal")
		}
	}
	return strconv.ParseInt(value, 10, 64)
}

func writeImportEvent(writer http.ResponseWriter, event importing.PublicEvent) error {
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

func (handler *Handler) handleArtifactError(writer http.ResponseWriter, request *http.Request, err error) {
	code, owned := artifact.CodeOf(err)
	if owned {
		switch code {
		case artifact.ErrorEmptyArtifact, artifact.ErrorInvalidArgument:
			handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_snapshot", "Pintia snapshot body is invalid.")
			return
		case artifact.ErrorPayloadTooLarge:
			handler.writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "payload_too_large", "Pintia snapshot exceeds the configured upload limit.")
			return
		case artifact.ErrorCanceled:
			if errors.Is(err, context.DeadlineExceeded) {
				handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Upload exceeded its duration limit.")
				return
			}
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Upload was canceled.")
			return
		}
	}
	handler.logImportFailure(request, "artifact_"+string(code))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) handleRequestBodyError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
	deadlineMessage string,
) {
	if errors.Is(err, context.DeadlineExceeded) {
		handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", deadlineMessage)
		return
	}
	if errors.Is(err, context.Canceled) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
		return
	}
	handler.logImportFailure(request, "request_body_lifetime")
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) handleImportError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) {
		handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
		return
	}
	code, owned := importing.CodeOf(err)
	if owned {
		switch code {
		case importing.ErrorInvalidMediaType:
			handler.writeAPIError(writer, request, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must match the Pintia snapshot v2 contract.")
			return
		case importing.ErrorIdentityConflict, importing.ErrorSubmissionConflict, importing.ErrorHeadConflict:
			handler.writeAPIError(writer, request, http.StatusConflict, string(code), "Import conflicts with stored immutable identity.")
			return
		case importing.ErrorValidation:
			handler.writeAPIError(writer, request, http.StatusBadRequest, string(code), "Pintia snapshot is invalid.")
			return
		case importing.ErrorEventCursorAhead:
			handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_event_cursor", "Last-Event-ID exceeds the durable event head.")
			return
		case importing.ErrorJobCursorInvalid:
			handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_import_cursor", "Import history cursor is invalid.")
			return
		case importing.ErrorCanceled:
			if errors.Is(err, context.DeadlineExceeded) {
				handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
				return
			}
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logImportFailure(request, "import_"+string(code))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logImportFailure(request *http.Request, code string) {
	handler.logger.ErrorContext(request.Context(), "import HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", code,
	)
}

func (handler *Handler) logImportStreamFailure(request *http.Request, err error, found bool) {
	code := "job_disappeared"
	if err != nil {
		if importCode, ok := importing.CodeOf(err); ok {
			code = "import_" + string(importCode)
		} else {
			code = "unknown"
		}
	} else if found {
		code = "unknown"
	}
	handler.logImportFailure(request, "stream_"+code)
}
