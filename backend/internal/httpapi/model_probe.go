package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/modelprobe"
)

func (handler *Handler) testModelConnection(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	key := request.PathValue("key")
	if !configuration.ValidKey(key) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_configuration_key", "Configuration key is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	result, err := handler.modelProbe.Test(request.Context(), access, key)
	if err != nil {
		handler.handleModelProbeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) handleModelProbeError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	if configuration.CodeOf(err) != "" {
		handler.handleConfigurationError(writer, request, err)
		return
	}
	switch modelprobe.CodeOf(err) {
	case modelprobe.ErrorInvalidInput:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_model_probe_request", "Model connection test request is invalid.")
		return
	case modelprobe.ErrorConfigurationMissing:
		handler.writeAPIError(writer, request, http.StatusNotFound, "model_configuration_not_found", "Model connection configuration does not exist.")
		return
	case modelprobe.ErrorConfigurationKind:
		handler.writeAPIError(writer, request, http.StatusConflict, "configuration_kind_conflict", "Configuration key is not a model connection.")
		return
	case modelprobe.ErrorProviderRejected:
		handler.writeModelProviderFailure(writer, request, err)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
		return
	}
	handler.logger.ErrorContext(request.Context(), "model connection test failed",
		"request_id", requestID(request.Context()),
		"code", modelprobe.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) writeModelProviderFailure(writer http.ResponseWriter, request *http.Request, err error) {
	var failure *chatagent.ProviderFailure
	if !errors.As(err, &failure) {
		handler.writeAPIError(writer, request, http.StatusBadGateway, "model_provider_rejected", "Model provider rejected the connection test.")
		return
	}
	status := http.StatusBadGateway
	switch failure.Code {
	case "provider_configuration_invalid", "provider_credential_invalid", "provider_request_rejected":
		status = http.StatusUnprocessableEntity
	case "provider_credential_unavailable", "provider_temporarily_unavailable":
		status = http.StatusServiceUnavailable
	case "provider_timeout":
		status = http.StatusGatewayTimeout
	}
	handler.writeAPIError(writer, request, status, failure.Code, "Model provider rejected the connection test.")
}
