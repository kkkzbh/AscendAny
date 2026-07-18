package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

type automaticAnalysisRequest struct {
	PromptConfigurationKey        string `json:"promptConfigurationKey"`
	ModelConfigurationKey         string `json:"modelConfigurationKey"`
	ExpectedAnalyticsHeadRevision int64  `json:"expectedAnalyticsHeadRevision"`
}

const (
	maxAgentRunJSONBytes   int64 = 512 << 10
	agentEventBatchSize          = chatagent.MaxPageSize
	agentEventPollInterval       = 500 * time.Millisecond
	agentEventHeartbeat          = 15 * time.Second
)

type chatMessagePage struct {
	Items        []chatagent.Message `json:"items"`
	LastSequence int64               `json:"lastSequence"`
}

func (handler *Handler) listChatThreads(writer http.ResponseWriter, request *http.Request) {
	cursor, limit, err := parseChatThreadQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_thread_query", "Chat thread query is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, err := handler.chatAgent.ListThreads(request.Context(), access, cursor, limit)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) createChatThread(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	thread, err := handler.chatAgent.CreateThread(request.Context(), access)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/students/me/chat/threads/"+thread.ID+"/messages")
	writeJSON(writer, http.StatusCreated, thread)
}

func (handler *Handler) listChatMessages(writer http.ResponseWriter, request *http.Request) {
	threadID := request.PathValue("threadId")
	if !chatagent.ValidPublicID(threadID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_thread_id", "Chat thread ID is invalid.")
		return
	}
	after, limit, err := parseChatMessageQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_message_query", "Chat message query is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	messages, err := handler.chatAgent.ListMessages(request.Context(), access, threadID, after, limit)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	if messages == nil {
		messages = []chatagent.Message{}
	}
	last := after
	if len(messages) > 0 {
		last = messages[len(messages)-1].Sequence
	}
	writeJSON(writer, http.StatusOK, chatMessagePage{Items: messages, LastSequence: last})
}

func (handler *Handler) enqueueAgentRun(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	threadID := request.PathValue("threadId")
	if !chatagent.ValidPublicID(threadID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_thread_id", "Chat thread ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input chatagent.EnqueueRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentRunJSONBytes,
		"Agent run payload exceeds 524288 bytes.",
		"Agent run request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	result, err := handler.chatAgent.Enqueue(request.Context(), access, threadID, input)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/students/me/agent-runs/"+result.Run.ID)
	status := http.StatusAccepted
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (handler *Handler) enqueueAutoAnalysis(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input automaticAnalysisRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentRunJSONBytes,
		"Automatic analysis payload exceeds 524288 bytes.",
		"Automatic analysis request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	analytics, err := handler.studentAnalytics.GetSelf(request.Context(), access, 1)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	if analytics.State != studentanalytics.StateReady || analytics.Ready == nil || analytics.HeadRevision < 1 ||
		len(analytics.Ready.ExamHistory) == 0 {
		handler.writeAPIError(writer, request, http.StatusConflict, "analytics_unavailable", "Published student analytics are unavailable.")
		return
	}
	latestExamID := analytics.Ready.ExamHistory[len(analytics.Ready.ExamHistory)-1].ExamID
	identity, err := chatagent.NewAutoAnalysisIdentity(latestExamID, "default")
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	result, err := handler.chatAgent.EnqueueAutoAnalysis(request.Context(), access, chatagent.AutoAnalysisRequest{
		PromptConfigurationKey:        input.PromptConfigurationKey,
		ModelConfigurationKey:         input.ModelConfigurationKey,
		ExpectedAnalyticsHeadRevision: input.ExpectedAnalyticsHeadRevision,
		Identity:                      identity,
		FrontendContext: chatagent.AutoAnalysisFrontendContext{
			LatestExamID: identity.ExamID,
			RoleID:       identity.RoleID,
		},
	})
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/students/me/agent-runs/"+result.Run.ID)
	status := http.StatusAccepted
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (handler *Handler) getAgentRun(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	runID := request.PathValue("runId")
	if !chatagent.ValidPublicID(runID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_run_id", "Agent run ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	run, found, err := handler.chatAgent.GetRun(request.Context(), access, runID)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "agent_run_not_found", "Agent run does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) streamAgentRunEvents(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	runID := request.PathValue("runId")
	if !chatagent.ValidPublicID(runID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_run_id", "Agent run ID is invalid.")
		return
	}
	after, err := parseLastEventID(request.Header)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be one canonical non-negative decimal sequence.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	if !handler.acquireSSE(writer, request) {
		return
	}
	defer handler.releaseSSE()
	streamDeadline := time.Now().Add(handler.sseMaxDuration)
	streamContext, cancelStream := context.WithDeadline(request.Context(), streamDeadline)
	defer cancelStream()
	request = request.WithContext(streamContext)
	batch, err := handler.chatAgent.ReadRunEvents(request.Context(), access, runID, after, agentEventBatchSize)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	controller := http.NewResponseController(writer)
	if err := clearSSEWriteDeadline(controller); err != nil {
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	stopWriteInterrupt := installSSEWriteInterrupt(request.Context(), controller)
	defer stopWriteInterrupt()
	if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
		handler.handleSSESetupError(writer, request, err)
		return
	}
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}
	if err := clearSSEWriteDeadline(controller); err != nil {
		return
	}
	heartbeat := time.NewTimer(agentEventHeartbeat)
	defer heartbeat.Stop()
	for {
		if len(batch.Events) > 0 {
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			for _, event := range batch.Events {
				if err := writeAgentRunEvent(writer, event); err != nil {
					return
				}
				after = event.Sequence
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
		}
		if len(batch.Events) == agentEventBatchSize {
			batch, err = handler.chatAgent.ReadRunEvents(request.Context(), access, runID, after, agentEventBatchSize)
			if err != nil {
				handler.logChatAgentStreamFailure(request, err)
				return
			}
			continue
		}
		if batch.Terminal && after == batch.LastSequence {
			return
		}
		poll := time.NewTimer(agentEventPollInterval)
		select {
		case <-request.Context().Done():
			poll.Stop()
			return
		case <-heartbeat.C:
			poll.Stop()
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			if _, err := fmt.Fprint(writer, ": keep-alive\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
			heartbeat.Reset(agentEventHeartbeat)
		case <-poll.C:
		}
		batch, err = handler.chatAgent.ReadRunEvents(request.Context(), access, runID, after, agentEventBatchSize)
		if err != nil {
			handler.logChatAgentStreamFailure(request, err)
			return
		}
	}
}

func parseChatThreadQuery(rawQuery string, forceQuery bool) (*string, int, error) {
	limit := chatagent.DefaultPageSize
	if rawQuery == "" && !forceQuery {
		return nil, limit, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{"cursor": {}, "limit": {}})
	if err != nil {
		return nil, 0, err
	}
	var cursor *string
	if value, present := fields["cursor"]; present {
		if !chatagent.ValidPublicID(value) {
			return nil, 0, errors.New("chat thread cursor is invalid")
		}
		cursor = &value
	}
	if value, present := fields["limit"]; present {
		parsed, parseErr := parseCanonicalPositiveDecimal(value, 1, chatagent.MaxPageSize)
		if parseErr != nil {
			return nil, 0, parseErr
		}
		limit = parsed
	}
	return cursor, limit, nil
}

func parseChatMessageQuery(rawQuery string, forceQuery bool) (int64, int, error) {
	after := int64(0)
	limit := chatagent.DefaultPageSize
	if rawQuery == "" && !forceQuery {
		return after, limit, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{
		"afterSequence": {}, "limit": {},
	})
	if err != nil {
		return 0, 0, err
	}
	if value, present := fields["afterSequence"]; present {
		after, err = parseCanonicalNonNegativeDecimal64(value)
		if err != nil {
			return 0, 0, err
		}
	}
	if value, present := fields["limit"]; present {
		limit, err = parseCanonicalPositiveDecimal(value, 1, chatagent.MaxPageSize)
		if err != nil {
			return 0, 0, err
		}
	}
	return after, limit, nil
}

func parseCanonicalNonNegativeDecimal64(value string) (int64, error) {
	if value == "0" {
		return 0, nil
	}
	if value == "" || value[0] == '0' || len(value) > 19 {
		return 0, errors.New("decimal value is not canonical")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("decimal value is not canonical")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return 0, errors.New("decimal value is outside the supported range")
	}
	return parsed, nil
}

func writeAgentRunEvent(writer http.ResponseWriter, event chatagent.RunEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, compact.Bytes())
	return err
}

func (handler *Handler) handleChatAgentError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch chatagent.CodeOf(err) {
	case chatagent.ErrorInvalidInput:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_agent_request", "Chat agent request is invalid.")
		return
	case chatagent.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case chatagent.ErrorNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, "chat_agent_not_found", "Chat agent resource does not exist.")
		return
	case chatagent.ErrorThreadCursorInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_thread_cursor", "Chat thread cursor is invalid.")
		return
	case chatagent.ErrorEventCursorInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_event_cursor", "Last-Event-ID exceeds the durable event head.")
		return
	case chatagent.ErrorIdempotencyConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "agent_run_idempotency_conflict", "Agent run request identity conflicts with stored input.")
		return
	case chatagent.ErrorThreadKindConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "chat_thread_kind_conflict", "Chat thread kind does not permit this run.")
		return
	case chatagent.ErrorAutoAnalysisConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "auto_analysis_configuration_conflict", "Automatic analysis for this analytics head owns different configuration keys.")
		return
	case chatagent.ErrorConfigurationMissing:
		handler.writeAPIError(writer, request, http.StatusConflict, "agent_configuration_missing", "Required agent configuration is unavailable.")
		return
	case chatagent.ErrorAnalyticsConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "analytics_head_conflict", "Published analytics head revision changed.")
		return
	case chatagent.ErrorCanceled:
		if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "chat agent HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", chatagent.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logChatAgentStreamFailure(request *http.Request, err error) {
	code := chatagent.CodeOf(err)
	if err == nil {
		code = chatagent.ErrorNotFound
	}
	handler.logger.ErrorContext(request.Context(), "chat agent event stream ended",
		"request_id", requestID(request.Context()),
		"code", code,
	)
}
