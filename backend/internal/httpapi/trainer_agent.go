package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentserver"
)

const (
	maximumTrainerAgentSmallRequestBytes  int64 = 64 << 10
	maximumTrainerAgentResponseBytes            = 64 << 10
	maximumTrainerAgentClaimResponseBytes       = (1 << 30) + (1 << 20)
	maximumTrainerAgentOutputRequestBytes       = (1 << 30) + (1 << 20)
)

type trainerAgentRequestError struct {
	status int
	code   string
	detail string
}

func (value *trainerAgentRequestError) Error() string { return value.detail }

func (handler *Handler) claimRecommendationTraining(writer http.ResponseWriter, request *http.Request) {
	agentID, ok := handler.authenticateTrainerAgent(writer, request)
	if !ok {
		return
	}
	if !handler.allowTrainerAgentClaim(writer, request, agentID) {
		return
	}
	var input traineragentprotocol.ClaimRequestV1
	if _, err := decodeTrainerAgentRequest(
		writer, request, traineragentprotocol.ClaimMediaTypeV1, maximumTrainerAgentSmallRequestBytes, &input,
	); err != nil {
		handler.handleTrainerAgentRequestError(writer, request, err)
		return
	}
	result, err := handler.trainerAgent.Claim(request.Context(), agentID, input)
	if err != nil {
		handler.handleTrainerAgentServiceError(writer, request, err)
		return
	}
	if result == nil {
		writer.Header().Del("Content-Type")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	handler.writeTrainerAgentJSONBounded(
		writer, request, http.StatusOK, traineragentprotocol.ClaimMediaTypeV1, *result,
		handler.trainerAgentMaximumClaimResponseBytes,
	)
}

func (handler *Handler) heartbeatRecommendationTraining(writer http.ResponseWriter, request *http.Request) {
	agentID, ok := handler.authenticateTrainerAgent(writer, request)
	if !ok {
		return
	}
	var input traineragentprotocol.HeartbeatRequestV1
	if _, err := decodeTrainerAgentRequest(
		writer, request, traineragentprotocol.HeartbeatMediaTypeV1, maximumTrainerAgentSmallRequestBytes, &input,
	); err != nil {
		handler.handleTrainerAgentRequestError(writer, request, err)
		return
	}
	result, err := handler.trainerAgent.Heartbeat(request.Context(), request.PathValue("runId"), agentID, input)
	if err != nil {
		handler.handleTrainerAgentServiceError(writer, request, err)
		return
	}
	handler.writeTrainerAgentJSON(writer, request, http.StatusOK, traineragentprotocol.HeartbeatMediaTypeV1, result)
}

func (handler *Handler) publishRecommendationTrainingOutput(writer http.ResponseWriter, request *http.Request) {
	agentID, ok := handler.authenticateTrainerAgent(writer, request)
	if !ok {
		return
	}
	var input traineragentprotocol.OutputRequestV1
	requestSHA256, err := decodeTrainerAgentRequest(
		writer, request, traineragentprotocol.OutputMediaTypeV1, handler.trainerAgentMaximumOutputRequestBytes, &input,
	)
	if err != nil {
		handler.handleTrainerAgentRequestError(writer, request, err)
		return
	}
	result, err := handler.trainerAgent.Publish(
		request.Context(), request.PathValue("runId"), agentID, requestSHA256, input,
	)
	if err != nil {
		handler.handleTrainerAgentServiceError(writer, request, err)
		return
	}
	handler.writeTrainerAgentJSON(writer, request, http.StatusOK, traineragentprotocol.OutputMediaTypeV1, result)
}

func (handler *Handler) reportRecommendationTrainingFailure(writer http.ResponseWriter, request *http.Request) {
	agentID, ok := handler.authenticateTrainerAgent(writer, request)
	if !ok {
		return
	}
	var input traineragentprotocol.FailureRequestV1
	requestSHA256, err := decodeTrainerAgentRequest(
		writer, request, traineragentprotocol.FailureMediaTypeV1, maximumTrainerAgentSmallRequestBytes, &input,
	)
	if err != nil {
		handler.handleTrainerAgentRequestError(writer, request, err)
		return
	}
	result, err := handler.trainerAgent.ReportFailure(
		request.Context(), request.PathValue("runId"), agentID, requestSHA256, input,
	)
	if err != nil {
		handler.handleTrainerAgentServiceError(writer, request, err)
		return
	}
	handler.writeTrainerAgentJSON(writer, request, http.StatusOK, traineragentprotocol.FailureMediaTypeV1, result)
}

func (handler *Handler) authenticateTrainerAgent(writer http.ResponseWriter, request *http.Request) (string, bool) {
	presented := ""
	validHeader := false
	values := request.Header.Values("Authorization")
	if len(values) == 1 && strings.HasPrefix(values[0], "Bearer ") && len(values[0]) > len("Bearer ") &&
		strings.TrimSpace(values[0]) == values[0] && !strings.Contains(values[0][len("Bearer "):], " ") {
		presented = values[0][len("Bearer "):]
		validHeader = true
	}
	agentID, err := handler.trainerAgentVerifier.Verify(request.Context(), presented)
	if err != nil {
		var owned *traineragentserver.Error
		if errors.As(err, &owned) && owned.Code == traineragentserver.ErrorCredentialUnavailable {
			handler.writeTrainerAgentError(writer, request, http.StatusServiceUnavailable, string(owned.Code), owned.Detail, owned.Retryable)
			return "", false
		}
		handler.writeTrainerAgentError(writer, request, http.StatusUnauthorized, "authentication_rejected", "Authentication was rejected.", false)
		return "", false
	}
	if !validHeader || agentID == "" {
		handler.writeTrainerAgentError(writer, request, http.StatusUnauthorized, "authentication_rejected", "Authentication was rejected.", false)
		return "", false
	}
	return agentID, true
}

func (handler *Handler) allowTrainerAgentClaim(writer http.ResponseWriter, request *http.Request, agentID string) bool {
	decision := handler.rateLimiter.Allow("internal.recommendation.trainer-agent.claim.agent", agentID)
	if decision.Allowed {
		return true
	}
	setRetryAfter(writer.Header(), decision.RetryAfter)
	handler.writeTrainerAgentError(writer, request, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit was exceeded.", true)
	return false
}

func decodeTrainerAgentRequest(
	writer http.ResponseWriter,
	request *http.Request,
	mediaType string,
	maximumBytes int64,
	destination any,
) (string, error) {
	if request.URL.User != nil {
		return "", &trainerAgentRequestError{http.StatusBadRequest, "invalid_request", "URL credentials are forbidden."}
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		return "", &trainerAgentRequestError{http.StatusBadRequest, "invalid_request", "Query parameters are forbidden."}
	}
	contentTypes := request.Header.Values("Content-Type")
	accepts := request.Header.Values("Accept")
	if len(contentTypes) != 1 || contentTypes[0] != mediaType || len(accepts) != 1 || accepts[0] != mediaType {
		return "", &trainerAgentRequestError{http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type and Accept must match the operation media type."}
	}
	if len(request.Header.Values("Content-Encoding")) != 0 {
		return "", &trainerAgentRequestError{http.StatusUnsupportedMediaType, "unsupported_content_encoding", "Content-Encoding is forbidden."}
	}
	if maximumBytes <= 0 {
		return "", errors.New("trainer-agent request limit is invalid")
	}
	limited := http.MaxBytesReader(unwrapResponseWriter(writer), request.Body, maximumBytes)
	body, err := io.ReadAll(limited)
	if contextErr := context.Cause(requestBodyReadContext(request)); contextErr != nil {
		return "", &trainerAgentRequestError{http.StatusRequestTimeout, "request_timeout", "Request body exceeded its duration limit."}
	}
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return "", &trainerAgentRequestError{http.StatusRequestEntityTooLarge, "payload_too_large", "Request body exceeds the operation limit."}
		}
		return "", &trainerAgentRequestError{http.StatusBadRequest, "invalid_request", "Request body could not be read."}
	}
	if err := finishRequestBodyRead(request); err != nil {
		return "", err
	}
	canonical, digest, err := canonicaljson.Object(body, int(maximumBytes))
	if err != nil || !bytes.Equal(body, canonical) {
		return "", &trainerAgentRequestError{http.StatusBadRequest, "invalid_request", "Request body must be one canonical JSON object."}
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", &trainerAgentRequestError{http.StatusBadRequest, "invalid_request", "Request body does not match the operation contract."}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", &trainerAgentRequestError{http.StatusBadRequest, "invalid_request", "Request body must contain exactly one object."}
	}
	return digest, nil
}

func (handler *Handler) handleTrainerAgentRequestError(writer http.ResponseWriter, request *http.Request, err error) {
	var contract *trainerAgentRequestError
	if errors.As(err, &contract) {
		handler.writeTrainerAgentError(writer, request, contract.status, contract.code, contract.detail, false)
		return
	}
	handler.writeTrainerAgentError(writer, request, http.StatusInternalServerError, "service_unavailable", "Trainer-agent service is unavailable.", true)
}

func (handler *Handler) handleTrainerAgentServiceError(writer http.ResponseWriter, request *http.Request, err error) {
	var owned *traineragentserver.Error
	if !errors.As(err, &owned) {
		handler.writeTrainerAgentError(writer, request, http.StatusInternalServerError, "service_unavailable", "Trainer-agent service is unavailable.", true)
		return
	}
	status := http.StatusInternalServerError
	switch owned.Code {
	case traineragentserver.ErrorAuthenticationRejected:
		status = http.StatusUnauthorized
	case traineragentserver.ErrorInvalidRequest, traineragentserver.ErrorUnsupportedProtocol:
		status = http.StatusBadRequest
	case traineragentserver.ErrorLeaseLost:
		status = http.StatusConflict
	case traineragentserver.ErrorOutputRejected:
		status = http.StatusUnprocessableEntity
	case traineragentserver.ErrorCredentialUnavailable, traineragentserver.ErrorStorageUnavailable, traineragentserver.ErrorServiceUnavailable:
		status = http.StatusServiceUnavailable
	}
	handler.writeTrainerAgentError(writer, request, status, string(owned.Code), owned.Detail, owned.Retryable)
}

func (handler *Handler) writeTrainerAgentJSON(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	mediaType string,
	value any,
) {
	handler.writeTrainerAgentJSONBounded(
		writer, request, status, mediaType, value, maximumTrainerAgentResponseBytes,
	)
}

func (handler *Handler) writeTrainerAgentJSONBounded(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	mediaType string,
	value any,
	maximumBytes int64,
) {
	raw, err := json.Marshal(value)
	if err != nil {
		handler.writeTrainerAgentError(writer, request, http.StatusInternalServerError, "service_unavailable", "Trainer-agent service is unavailable.", true)
		return
	}
	canonical, _, err := canonicaljson.Object(raw, int(maximumBytes))
	if err != nil {
		handler.writeTrainerAgentError(writer, request, http.StatusInternalServerError, "service_unavailable", "Trainer-agent service is unavailable.", true)
		return
	}
	writer.Header().Set("Content-Type", mediaType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(canonical)
}

func (handler *Handler) writeTrainerAgentError(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code, detail string,
	retryable bool,
) {
	abortUnreadRequestBody(writer, request)
	response := traineragentprotocol.ErrorResponseV1{
		Protocol: traineragentprotocol.ErrorResponseProtocolV1, Code: code, Detail: detail, Retryable: retryable,
	}
	raw, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		panic(fmt.Sprintf("marshal trainer-agent error: %v", marshalErr))
	}
	canonical, _, canonicalErr := canonicaljson.Object(raw, maximumTrainerAgentResponseBytes)
	if canonicalErr != nil {
		panic(fmt.Sprintf("canonicalize trainer-agent error: %v", canonicalErr))
	}
	writer.Header().Set("Content-Type", traineragentprotocol.ErrorMediaTypeV1)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(canonical)
}

func setRetryAfter(header http.Header, duration time.Duration) {
	seconds := int64((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	header.Set("Retry-After", strconv.FormatInt(seconds, 10))
}
