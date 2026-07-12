package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/examcatalog"
)

func (handler *Handler) listExams(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	cursor, limit, err := parseCursorPageQuery(
		request.URL.RawQuery,
		request.URL.ForceQuery,
		examcatalog.DefaultPageSize,
		examcatalog.MaxPageSize,
		examcatalog.ValidPublicID,
	)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_page", fmt.Sprintf("cursor and limit must match the exam pagination contract; limit must be from 1 through %d.", examcatalog.MaxPageSize))
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, err := handler.examCatalog.List(request.Context(), access, cursor, limit)
	if err != nil {
		handler.handleExamCatalogError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) getExam(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	examID := request.PathValue("examId")
	if !examcatalog.ValidPublicID(examID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_id", "Exam ID must be a canonical UUIDv4.")
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "query_not_allowed", "Query parameters are not allowed.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	detail, found, err := handler.examCatalog.Get(request.Context(), access, examID)
	if err != nil {
		handler.handleExamCatalogError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "exam_not_found", "Exam does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (handler *Handler) handleExamCatalogError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch examcatalog.CodeOf(err) {
	case examcatalog.ErrorInvalidQuery:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_query", "Exam query is invalid.")
		return
	case examcatalog.ErrorCursorInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_exam_cursor", "Exam cursor does not identify an active exam.")
		return
	case examcatalog.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	case examcatalog.ErrorCanceled:
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "exam catalog HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", examcatalog.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}
