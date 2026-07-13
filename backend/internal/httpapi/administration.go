package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type setManagedAccountStateRequest struct {
	Disabled bool `json:"disabled"`
}

func (handler *Handler) listManagedAccounts(writer http.ResponseWriter, request *http.Request) {
	handler.listAdministrationPage(
		writer,
		request,
		administration.ValidAccountID,
		"invalid_account_page",
		func(access string, cursor *string, limit int) (any, error) {
			return handler.administration.ListAccounts(request.Context(), access, cursor, limit)
		},
	)
}

func (handler *Handler) listManagedStudents(writer http.ResponseWriter, request *http.Request) {
	handler.listAdministrationPage(
		writer,
		request,
		administration.ValidStudentCursor,
		"invalid_student_page",
		func(access string, cursor *string, limit int) (any, error) {
			return handler.administration.ListStudents(request.Context(), access, cursor, limit)
		},
	)
}

func (handler *Handler) listAuditEvents(writer http.ResponseWriter, request *http.Request) {
	handler.listAdministrationPage(
		writer,
		request,
		administration.ValidAuditCursor,
		"invalid_audit_page",
		func(access string, cursor *string, limit int) (any, error) {
			return handler.administration.ListAudit(request.Context(), access, cursor, limit)
		},
	)
}

func (handler *Handler) listAdministrationPage(
	writer http.ResponseWriter,
	request *http.Request,
	validCursor func(string) bool,
	errorCode string,
	load func(string, *string, int) (any, error),
) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	cursor, limit, err := parseCursorPageQuery(
		request.URL.RawQuery,
		request.URL.ForceQuery,
		administration.DefaultPageSize,
		administration.MaxPageSize,
		validCursor,
	)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, errorCode, fmt.Sprintf("cursor and limit must match the administration pagination contract; limit must be from 1 through %d.", administration.MaxPageSize))
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, err := load(access, cursor, limit)
	if err != nil {
		handler.handleAdministrationError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) setManagedAccountState(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "query_not_allowed", "Query parameters are not allowed.")
		return
	}
	targetID := request.PathValue("accountId")
	if !administration.ValidAccountID(targetID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_account_id", "Account ID must be a canonical UUIDv4.")
		return
	}
	var payload setManagedAccountStateRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	account, err := handler.administration.SetAccountDisabled(request.Context(), access, targetID, payload.Disabled)
	if err != nil {
		handler.handleAdministrationError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, account)
}

func (handler *Handler) handleAdministrationError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch administration.CodeOf(err) {
	case administration.ErrorInvalidQuery:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_administration_request", "Administration request is invalid.")
		return
	case administration.ErrorCursorInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_administration_cursor", "Administration cursor does not identify an existing item.")
		return
	case administration.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	case administration.ErrorTargetNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, "account_not_found", "Account does not exist.")
		return
	case administration.ErrorSelfDisable:
		handler.writeAPIError(writer, request, http.StatusConflict, "active_admin_self_disable_rejected", "The active administrator account cannot be disabled.")
		return
	case administration.ErrorConcurrentMutation:
		handler.writeAPIError(writer, request, http.StatusConflict, "administration_concurrent_mutation", "Administration state changed concurrently; submit the operation again.")
		return
	case administration.ErrorCanceled:
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "administration HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", administration.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}
