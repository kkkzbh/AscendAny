package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
)

const maxFeedbackJSONBytes int64 = 256 * 1024

type submitFeedbackRequest struct {
	ClientRequestID string  `json:"clientRequestId"`
	Title           string  `json:"title"`
	Content         string  `json:"content"`
	Platform        *string `json:"platform,omitempty"`
	AppVersion      *string `json:"appVersion,omitempty"`
	UserAgent       *string `json:"userAgent,omitempty"`
}

func (handler *Handler) submitFeedback(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload submitFeedbackRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&payload,
		maxFeedbackJSONBytes,
		"Feedback payload exceeds 262144 bytes.",
		"Feedback request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	result, err := handler.feedback.SubmitAuthenticated(request.Context(), access, feedback.ApplicationInput{
		ClientRequestID: payload.ClientRequestID,
		Title:           payload.Title,
		Content:         payload.Content,
		Platform:        payload.Platform,
		AppVersion:      payload.AppVersion,
		UserAgent:       payload.UserAgent,
	})
	if err != nil {
		handler.handleFeedbackError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func (handler *Handler) handleFeedbackError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch feedback.CodeOf(err) {
	case feedback.ErrorInvalidInput:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_feedback_request", "Feedback request is invalid.")
		return
	case feedback.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case feedback.ErrorRateLimited:
		writer.Header().Set("Retry-After", "1")
		handler.writeAPIError(writer, request, http.StatusTooManyRequests, "feedback_rate_limited", "Feedback submission rate limit was exceeded.")
		return
	case feedback.ErrorIdempotencyConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "feedback_idempotency_conflict", "Feedback request identity conflicts with the stored submission.")
		return
	case feedback.ErrorDeliveryUnavailable:
		handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "feedback_delivery_unavailable", "Feedback delivery configuration is unavailable.")
		return
	case feedback.ErrorCanceled:
		if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "feedback HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", feedback.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}
