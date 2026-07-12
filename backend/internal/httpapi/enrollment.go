package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type enrollmentIssueRequest struct {
	Username      string    `json:"username"`
	DisplayName   string    `json:"displayName"`
	StudentNumber string    `json:"studentNumber"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type enrollmentClaimRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (handler *Handler) issueEnrollment(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) || !handler.requireNoQuery(writer, request) {
		return
	}
	accessToken, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload enrollmentIssueRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	issued, err := handler.enrollment.IssueEnrollment(request.Context(), accessToken, auth.EnrollmentIssueInput{
		Username:      payload.Username,
		DisplayName:   payload.DisplayName,
		StudentNumber: payload.StudentNumber,
		ExpiresAt:     payload.ExpiresAt,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, issued)
}

func (handler *Handler) revokeEnrollment(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) || !handler.requireNoQuery(writer, request) {
		return
	}
	accessToken, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	if err := handler.enrollment.RevokeEnrollment(request.Context(), accessToken, request.PathValue("grantId")); err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) claimEnrollment(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) || !handler.requireNoQuery(writer, request) {
		return
	}
	var payload enrollmentClaimRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	tokenDigest := sha256.Sum256([]byte(payload.Token))
	if !handler.allowRate(writer, request, "auth.enrollment.claim.token", hex.EncodeToString(tokenDigest[:])) {
		return
	}
	result, err := handler.enrollment.ClaimEnrollment(request.Context(), auth.EnrollmentClaimInput{
		Token:    payload.Token,
		Password: payload.Password,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	setRefreshCookie(writer, result.RefreshCookieValue)
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) requireNoQuery(writer http.ResponseWriter, request *http.Request) bool {
	if request.URL.RawQuery == "" && !request.URL.ForceQuery {
		return true
	}
	handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_query", "Query parameters are not supported.")
	return false
}
