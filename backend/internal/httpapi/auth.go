package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (handler *Handler) login(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) {
		return
	}
	var payload loginRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if auth.IsCanonicalUsername(payload.Username) &&
		!handler.allowRate(writer, request, "auth.login.username", payload.Username) {
		return
	}
	result, err := handler.auth.Login(request.Context(), auth.LoginInput{
		Username: payload.Username,
		Password: payload.Password,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	setRefreshCookie(writer, result.RefreshCookieValue)
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) refresh(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) {
		return
	}
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	refresh, ok := refreshCookie(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	csrf, ok := csrfToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusForbidden, "csrf_rejected", "CSRF token was rejected.")
		return
	}
	result, err := handler.auth.Refresh(request.Context(), auth.RefreshInput{
		RefreshToken: refresh,
		CSRFToken:    csrf,
	})
	if err != nil {
		if auth.ErrorCodeOf(err) == auth.ErrorRefreshReuse {
			clearRefreshCookie(writer)
		}
		handler.handleAuthError(writer, request, err)
		return
	}
	setRefreshCookie(writer, result.RefreshCookieValue)
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) logout(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) {
		return
	}
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	refresh, ok := refreshCookie(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	csrf, ok := csrfToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusForbidden, "csrf_rejected", "CSRF token was rejected.")
		return
	}
	if err := handler.auth.Logout(request.Context(), auth.LogoutInput{
		AccessToken:  access,
		RefreshToken: refresh,
		CSRFToken:    csrf,
	}); err != nil {
		if auth.ErrorCodeOf(err) == auth.ErrorRefreshReuse {
			clearRefreshCookie(writer)
		}
		handler.handleAuthError(writer, request, err)
		return
	}
	clearRefreshCookie(writer)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) requireWritesEnabled(writer http.ResponseWriter, request *http.Request) bool {
	if handler.capabilities.WritesEnabled {
		return true
	}
	handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "writes_disabled", "Write operations are disabled.")
	return false
}

func (handler *Handler) me(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	account, err := handler.auth.Me(request.Context(), access)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, account)
}

func (handler *Handler) handleRequestContractError(writer http.ResponseWriter, request *http.Request, err error) {
	var contractError *requestContractError
	if errors.As(err, &contractError) {
		handler.writeAPIError(writer, request, contractError.status, contractError.code, contractError.message)
		return
	}
	handler.logger.ErrorContext(request.Context(), "request contract processing failed",
		"request_id", requestID(request.Context()),
		"code", "request_contract_internal_failure",
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) handleAuthError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) {
		handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
		return
	}
	code := auth.ErrorCodeOf(err)
	switch code {
	case auth.ErrorInvalidInput:
		var owned *auth.Error
		if errors.As(err, &owned) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, string(code), owned.Message)
			return
		}
	case auth.ErrorAuthentication, auth.ErrorRefreshReuse:
		handler.writeAPIError(writer, request, http.StatusUnauthorized, string(auth.ErrorAuthentication), "Authentication was rejected.")
		return
	case auth.ErrorEnrollmentRejected:
		handler.writeAPIError(writer, request, http.StatusUnauthorized, string(code), "Enrollment was rejected.")
		return
	case auth.ErrorPasswordWorkSaturated:
		writer.Header().Set("Retry-After", "1")
		handler.writeAPIError(writer, request, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit was exceeded.")
		return
	case auth.ErrorForbidden:
		handler.writeAPIError(writer, request, http.StatusForbidden, string(code), "Authorization was rejected.")
		return
	case auth.ErrorEnrollmentIdentity:
		handler.writeAPIError(writer, request, http.StatusConflict, string(code), "Enrollment identity is unavailable.")
		return
	case auth.ErrorEnrollmentNotRevocable:
		handler.writeAPIError(writer, request, http.StatusConflict, string(code), "Enrollment grant cannot be revoked.")
		return
	case auth.ErrorSessionNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, string(code), "The account session does not exist.")
		return
	}
	handler.logger.ErrorContext(request.Context(), "authentication operation failed",
		"request_id", requestID(request.Context()),
		"code", code,
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func setRefreshCookie(writer http.ResponseWriter, value string) {
	http.SetCookie(writer, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteNoneMode,
	})
}

func clearRefreshCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteNoneMode,
	})
}
