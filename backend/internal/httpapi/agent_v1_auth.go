package httpapi

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type agentV1AuthPolicyResponse struct {
	SignupPolicy string `json:"signupPolicy"`
	RequirePhone bool   `json:"requirePhone"`
	RequireEmail bool   `json:"requireEmail"`
}

type agentV1LoginRequest struct {
	Username     string  `json:"username"`
	Password     string  `json:"password"`
	PasswordMode *string `json:"passwordMode,omitempty"`
	DeviceID     *string `json:"deviceId,omitempty"`
}

type agentV1RegisterRequest struct {
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	StudentID   string  `json:"studentId"`
	PTANickname string  `json:"ptaNickname"`
	Phone       *string `json:"phone,omitempty"`
	Email       *string `json:"email,omitempty"`
	DeviceID    *string `json:"deviceId,omitempty"`
}

type agentV1SSOExchangeRequest struct {
	Token string `json:"token"`
}

type agentV1RefreshRequest struct {
	RefreshToken string  `json:"refreshToken"`
	DeviceID     *string `json:"deviceId,omitempty"`
}

type agentV1LogoutRequest struct {
	RefreshToken *string `json:"refreshToken,omitempty"`
}

type agentV1LocalPasswordBootstrapRequest struct {
	NewPassword string `json:"newPassword"`
}

type agentV1AccountResponse struct {
	AccountID            string  `json:"accountId"`
	Username             string  `json:"username"`
	DisplayName          string  `json:"displayName"`
	IsAdmin              bool    `json:"isAdmin"`
	StudentID            *string `json:"studentId"`
	PTANickname          *string `json:"ptaNickname"`
	ProvisionSource      string  `json:"provisionSource"`
	LocalPasswordEnabled bool    `json:"localPasswordEnabled"`
}

type agentV1TokensResponse struct {
	AccessToken           string                 `json:"accessToken"`
	AccessTokenExpiresAt  time.Time              `json:"accessTokenExpiresAt"`
	RefreshToken          string                 `json:"refreshToken"`
	RefreshTokenExpiresAt time.Time              `json:"refreshTokenExpiresAt"`
	Account               agentV1AccountResponse `json:"account"`
}

type agentV1MeResponse struct {
	Account agentV1AccountResponse `json:"account"`
}

type agentV1ProfileRequest struct {
	DisplayName *string `json:"displayName"`
	StudentID   *string `json:"studentId"`
	PTANickname *string `json:"ptaNickname"`
}

type agentV1ProfileResponse struct {
	DisplayName string  `json:"displayName"`
	StudentID   *string `json:"studentId"`
	PTANickname *string `json:"ptaNickname"`
}

func (handler *Handler) agentV1AuthPolicy(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	writeJSON(writer, http.StatusOK, agentV1AuthPolicyResponse{
		SignupPolicy: "username_password_only",
		RequirePhone: false,
		RequireEmail: false,
	})
}

func (handler *Handler) agentV1Login(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	var payload agentV1LoginRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if payload.PasswordMode != nil && *payload.PasswordMode != "plain" {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "auth_password_mode_unsupported", "Only plain password login is supported.")
		return
	}
	if !validAgentV1DeviceID(payload.DeviceID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "auth_device_id_invalid", "Device ID is invalid.")
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
	response, err := handler.agentV1TokensResponse(result, time.Time{})
	if err != nil {
		handler.logAgentV1Failure(request, "seal_initial_refresh_envelope", err)
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) agentV1Register(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	var payload agentV1RegisterRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if !validAgentV1OptionalContact(payload.Phone, 32) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "auth_phone_invalid", "Phone is invalid.")
		return
	}
	if !validAgentV1OptionalContact(payload.Email, 320) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "auth_email_invalid", "Email is invalid.")
		return
	}
	if !validAgentV1DeviceID(payload.DeviceID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "auth_device_id_invalid", "Device ID is invalid.")
		return
	}
	if auth.IsCanonicalUsername(payload.Username) &&
		!handler.allowRate(writer, request, "auth.register.username", payload.Username) {
		return
	}
	result, err := handler.auth.Register(request.Context(), auth.RegistrationInput{
		Username:      payload.Username,
		Password:      payload.Password,
		StudentNumber: payload.StudentID,
		PTANickname:   payload.PTANickname,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	response, err := handler.agentV1TokensResponse(result, time.Time{})
	if err != nil {
		handler.logAgentV1Failure(request, "seal_registration_refresh_envelope", err)
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (handler *Handler) agentV1SSOExchange(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	var payload agentV1SSOExchangeRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	result, err := handler.auth.ExchangeSSO(request.Context(), auth.SSOExchangeInput{Token: payload.Token})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	response, err := handler.agentV1TokensResponse(result, time.Time{})
	if err != nil {
		handler.logAgentV1Failure(request, "seal_sso_refresh_envelope", err)
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) agentV1Refresh(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	var payload agentV1RefreshRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if !validAgentV1DeviceID(payload.DeviceID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "auth_device_id_invalid", "Device ID is invalid.")
		return
	}
	credentials, ok := handler.openAgentV1RefreshToken(writer, request, payload.RefreshToken)
	if !ok {
		return
	}
	result, err := handler.auth.Refresh(request.Context(), auth.RefreshInput{
		RefreshToken: credentials.RefreshCookieValue,
		CSRFToken:    credentials.CSRFToken,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	response, err := handler.agentV1TokensResponse(result, credentials.ExpiresAt)
	if err != nil {
		handler.logAgentV1Failure(request, "seal_rotated_refresh_envelope", err)
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) agentV1Logout(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload agentV1LogoutRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if payload.RefreshToken == nil {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	credentials, ok := handler.openAgentV1RefreshToken(writer, request, *payload.RefreshToken)
	if !ok {
		return
	}
	if err := handler.auth.Logout(request.Context(), auth.LogoutInput{
		AccessToken:  access,
		RefreshToken: credentials.RefreshCookieValue,
		CSRFToken:    credentials.CSRFToken,
	}); err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (handler *Handler) agentV1Me(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
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
	writeJSON(writer, http.StatusOK, agentV1MeResponse{Account: mapAgentV1Account(account)})
}

func (handler *Handler) agentV1UpdateProfile(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload agentV1ProfileRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	current, err := handler.auth.Me(request.Context(), access)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	if !agentV1ProfileIdentityUnchanged(payload, current) {
		handler.writeAPIError(writer, request, http.StatusUnprocessableEntity, "auth_profile_identity_immutable", "Student identity fields cannot be changed by this backend.")
		return
	}
	displayName := current.DisplayName
	if payload.DisplayName != nil {
		displayName = *payload.DisplayName
	}
	updated, err := handler.accountManagement.UpdateProfile(request.Context(), access, auth.ProfileUpdateInput{
		DisplayName: displayName,
	})
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, agentV1ProfileResponse{
		DisplayName: updated.DisplayName,
		StudentID:   updated.StudentNumber,
		PTANickname: current.PTANickname,
	})
}

func (handler *Handler) agentV1BootstrapLocalPassword(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload agentV1LocalPasswordBootstrapRequest
	if err := decodeStrictJSON(writer, request, &payload); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if err := handler.auth.BootstrapLocalPassword(request.Context(), auth.LocalPasswordBootstrapInput{
		AccessToken: access,
		NewPassword: payload.NewPassword,
	}); err != nil {
		handler.handleAuthError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (handler *Handler) agentV1TokensResponse(
	result auth.AuthResult,
	refreshExpiresAt time.Time,
) (agentV1TokensResponse, error) {
	if refreshExpiresAt.IsZero() {
		var err error
		refreshExpiresAt, err = handler.agentV1Tokens.initialExpiry(result.ExpiresAt)
		if err != nil {
			return agentV1TokensResponse{}, err
		}
	}
	serialized, err := handler.agentV1Tokens.seal(agentV1RefreshCredentials{
		RefreshCookieValue: result.RefreshCookieValue,
		CSRFToken:          result.CSRFToken,
		ExpiresAt:          refreshExpiresAt,
	})
	if err != nil {
		return agentV1TokensResponse{}, err
	}
	return agentV1TokensResponse{
		AccessToken:           result.AccessToken,
		AccessTokenExpiresAt:  result.ExpiresAt.UTC(),
		RefreshToken:          serialized,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Account:               mapAgentV1Account(result.Account),
	}, nil
}

func (handler *Handler) openAgentV1RefreshToken(
	writer http.ResponseWriter,
	request *http.Request,
	serialized string,
) (agentV1RefreshCredentials, bool) {
	credentials, err := handler.agentV1Tokens.open(serialized)
	if err != nil || !time.Now().UTC().Before(credentials.ExpiresAt) {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return agentV1RefreshCredentials{}, false
	}
	return credentials, true
}

func mapAgentV1Account(account auth.Account) agentV1AccountResponse {
	return agentV1AccountResponse{
		AccountID:            account.ID,
		Username:             account.Username,
		DisplayName:          account.DisplayName,
		IsAdmin:              account.Role == auth.RoleAdmin,
		StudentID:            account.StudentNumber,
		PTANickname:          account.PTANickname,
		ProvisionSource:      "local",
		LocalPasswordEnabled: true,
	}
}

func agentV1ProfileIdentityUnchanged(payload agentV1ProfileRequest, current auth.Account) bool {
	if payload.PTANickname != nil && *payload.PTANickname != "" &&
		(current.PTANickname == nil || *payload.PTANickname != *current.PTANickname) {
		return false
	}
	if payload.StudentID == nil || *payload.StudentID == "" {
		return true
	}
	return current.StudentNumber != nil && *payload.StudentID == *current.StudentNumber
}

func validAgentV1OptionalContact(value *string, maximum int) bool {
	if value == nil {
		return true
	}
	return *value != "" && len(*value) <= maximum && utf8.ValidString(*value) &&
		strings.TrimSpace(*value) == *value && strings.IndexByte(*value, 0) < 0
}

func validAgentV1DeviceID(value *string) bool {
	if value == nil {
		return true
	}
	return *value != "" && len(*value) <= 256 && strings.TrimSpace(*value) == *value &&
		strings.IndexByte(*value, 0) < 0
}

func (handler *Handler) logAgentV1Failure(request *http.Request, operation string, err error) {
	handler.logger.ErrorContext(request.Context(), "Agent API v1 operation failed",
		"request_id", requestID(request.Context()),
		"operation", operation,
		"error", err,
	)
}
