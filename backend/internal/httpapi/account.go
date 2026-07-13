package httpapi

import (
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type updateAccountProfileRequest struct {
	DisplayName string `json:"displayName"`
}

type accountSessionListResponse struct {
	Items []auth.ManagedSession `json:"items"`
}

func (handler *Handler) updateAccountProfile(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "query_not_allowed", "Query parameters are not allowed.")
		return
	}
	var payload updateAccountProfileRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	account, err := handler.accountManagement.UpdateProfile(request.Context(), access, auth.ProfileUpdateInput{
		DisplayName: payload.DisplayName,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, account)
}

func (handler *Handler) listAccountSessions(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "query_not_allowed", "Query parameters are not allowed.")
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
	sessions, err := handler.accountManagement.ListSessions(request.Context(), access)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, accountSessionListResponse{Items: sessions})
}

func (handler *Handler) revokeAccountSession(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "query_not_allowed", "Query parameters are not allowed.")
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
	current, err := handler.accountManagement.RevokeSession(
		request.Context(),
		access,
		request.PathValue("sessionId"),
	)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	if current {
		clearRefreshCookie(writer)
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusNoContent)
}
