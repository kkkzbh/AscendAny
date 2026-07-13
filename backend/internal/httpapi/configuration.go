package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

const maxConfigurationJSONBytes int64 = 320 * 1024

func (handler *Handler) listConfigurations(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	kind, afterKey, limit, err := parseConfigurationListQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_configuration_page", fmt.Sprintf("kind, afterKey, and limit must match the configuration pagination contract; limit must be from 1 through %d.", configuration.MaxPageSize))
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, err := handler.configuration.List(request.Context(), access, kind, afterKey, limit)
	if err != nil {
		handler.handleConfigurationError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) getConfiguration(writer http.ResponseWriter, request *http.Request) {
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
	item, found, err := handler.configuration.Get(request.Context(), access, key)
	if err != nil {
		handler.handleConfigurationError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "configuration_not_found", "Configuration does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (handler *Handler) listConfigurationVersions(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	key := request.PathValue("key")
	if !configuration.ValidKey(key) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_configuration_key", "Configuration key is invalid.")
		return
	}
	beforeNumber, limit, err := parseConfigurationVersionsQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_configuration_version_page", fmt.Sprintf("beforeNumber and limit must match the configuration version pagination contract; limit must be from 1 through %d.", configuration.MaxPageSize))
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, found, err := handler.configuration.ListVersions(request.Context(), access, key, beforeNumber, limit)
	if err != nil {
		handler.handleConfigurationError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "configuration_not_found", "Configuration does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) createConfigurationVersion(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload configuration.CreateVersionInput
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&payload,
		maxConfigurationJSONBytes,
		"Configuration payload exceeds 327680 bytes.",
		"Configuration request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if payload.Kind == configuration.KindKnowledgeCatalog || payload.Key == configuration.KnowledgeCatalogKey {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_configuration_request", "Knowledge catalog publication requires the stopped-runtime release operator.")
		return
	}
	result, err := handler.configuration.CreateVersion(request.Context(), access, payload)
	if err != nil {
		handler.handleConfigurationError(writer, request, err)
		return
	}
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func parseConfigurationListQuery(rawQuery string, forceQuery bool) (*configuration.Kind, *string, int, error) {
	limit := configuration.DefaultPageSize
	if rawQuery == "" && !forceQuery {
		return nil, nil, limit, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{
		"kind": {}, "afterKey": {}, "limit": {},
	})
	if err != nil {
		return nil, nil, 0, err
	}
	var kind *configuration.Kind
	if raw, present := fields["kind"]; present {
		value := configuration.Kind(raw)
		if !configuration.ValidKind(value) {
			return nil, nil, 0, errors.New("configuration kind is invalid")
		}
		kind = &value
	}
	var afterKey *string
	if raw, present := fields["afterKey"]; present {
		if !configuration.ValidKey(raw) {
			return nil, nil, 0, errors.New("configuration cursor is invalid")
		}
		value := raw
		afterKey = &value
	}
	if raw, present := fields["limit"]; present {
		limit, err = parseCanonicalPositiveDecimal(raw, 1, configuration.MaxPageSize)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	return kind, afterKey, limit, nil
}

func parseConfigurationVersionsQuery(rawQuery string, forceQuery bool) (*int64, int, error) {
	limit := configuration.DefaultPageSize
	if rawQuery == "" && !forceQuery {
		return nil, limit, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{
		"beforeNumber": {}, "limit": {},
	})
	if err != nil {
		return nil, 0, err
	}
	var beforeNumber *int64
	if raw, present := fields["beforeNumber"]; present {
		value, parseErr := parseCanonicalPositiveDecimal64(raw, 2)
		if parseErr != nil {
			return nil, 0, parseErr
		}
		beforeNumber = &value
	}
	if raw, present := fields["limit"]; present {
		limit, err = parseCanonicalPositiveDecimal(raw, 1, configuration.MaxPageSize)
		if err != nil {
			return nil, 0, err
		}
	}
	return beforeNumber, limit, nil
}

func parseCanonicalQueryFields(rawQuery string, forceQuery bool, allowed map[string]struct{}) (map[string]string, error) {
	if rawQuery == "" || forceQuery && rawQuery == "" {
		return nil, errors.New("empty query marker is not canonical")
	}
	fields := make(map[string]string, len(allowed))
	for _, field := range strings.Split(rawQuery, "&") {
		name, value, found := strings.Cut(field, "=")
		if !found || name == "" || value == "" {
			return nil, errors.New("query field must contain one non-empty value")
		}
		if _, supported := allowed[name]; !supported {
			return nil, errors.New("query field is unknown")
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, errors.New("query field is duplicated")
		}
		fields[name] = value
	}
	return fields, nil
}

func parseCanonicalPositiveDecimal(raw string, minimum, maximum int) (int, error) {
	if raw == "" || raw[0] == '0' || len(raw) > len(strconv.Itoa(maximum)) {
		return 0, errors.New("decimal value is not canonical")
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, errors.New("decimal value is not canonical")
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("decimal value is outside the supported range")
	}
	return value, nil
}

func parseCanonicalPositiveDecimal64(raw string, minimum int64) (int64, error) {
	if raw == "" || raw[0] == '0' || len(raw) > 19 {
		return 0, errors.New("decimal value is not canonical")
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, errors.New("decimal value is not canonical")
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < minimum {
		return 0, errors.New("decimal value is outside the supported range")
	}
	return value, nil
}

func (handler *Handler) handleConfigurationError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch configuration.CodeOf(err) {
	case configuration.ErrorInvalidQuery:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_configuration_request", "Configuration request is invalid.")
		return
	case configuration.ErrorDocumentInvalid:
		handler.writeAPIError(writer, request, http.StatusUnprocessableEntity, "configuration_document_invalid", "Configuration document violates its semantic schema.")
		return
	case configuration.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case configuration.ErrorNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, "configuration_not_found", "Configuration does not exist.")
		return
	case configuration.ErrorHeadConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "configuration_head_conflict", "Configuration head revision changed concurrently.")
		return
	case configuration.ErrorReviewConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "recommendation_review_conflict", "Recommendation analytics review provenance is no longer current.")
		return
	case configuration.ErrorDocumentConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "configuration_document_conflict", "Configuration immutable identity conflicts with stored state.")
		return
	case configuration.ErrorCanceled:
		if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "configuration HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", configuration.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}
