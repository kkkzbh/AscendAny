package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

const (
	agentFrontendV1PromptConfigurationKey = "agent.prompt.default"
	agentFrontendV1ModelConfigurationKey  = "agent.model.default"
	agentFrontendV1ContextSchema          = chatagent.AgentFrontendV1ContextSchema
)

type agentFrontendV1ChatMessage struct {
	Role             string  `json:"role"`
	Content          string  `json:"content"`
	ReasoningContent *string `json:"reasoningContent,omitempty"`
}

type agentFrontendV1ChatReplyRequest struct {
	StudentID        *string                      `json:"studentId,omitempty"`
	PTANickname      *string                      `json:"ptaNickname,omitempty"`
	Messages         []agentFrontendV1ChatMessage `json:"messages"`
	Summary          *string                      `json:"summary"`
	RoleID           *string                      `json:"roleId,omitempty"`
	RoleName         *string                      `json:"roleName,omitempty"`
	RoleSystemPrompt *string                      `json:"roleSystemPrompt,omitempty"`
	Notes            *string                      `json:"notes,omitempty"`
	NotesTitle       *string                      `json:"notesTitle,omitempty"`
	NotesLocked      *bool                        `json:"notesLocked,omitempty"`
}

type agentFrontendV1AutoAnalysisRequest struct {
	StudentID        *string `json:"studentId,omitempty"`
	PTANickname      *string `json:"ptaNickname,omitempty"`
	RoleID           *string `json:"roleId,omitempty"`
	RoleName         *string `json:"roleName,omitempty"`
	RoleSystemPrompt *string `json:"roleSystemPrompt,omitempty"`
	LatestExamID     *string `json:"latestExamId,omitempty"`
	Notes            *string `json:"notes,omitempty"`
	NotesTitle       *string `json:"notesTitle,omitempty"`
	NotesLocked      *bool   `json:"notesLocked,omitempty"`
}

type agentFrontendV1ContextMessage struct {
	Content          string  `json:"content"`
	ReasoningContent *string `json:"reasoningContent"`
	Role             string  `json:"role"`
}

type agentFrontendV1RunContext struct {
	CurrentUser struct {
		Content      string `json:"content"`
		MessageIndex int    `json:"messageIndex"`
		PTANickname  string `json:"ptaNickname"`
		StudentID    string `json:"studentId"`
	} `json:"currentUser"`
	Messages []agentFrontendV1ContextMessage `json:"messages"`
	Notes    struct {
		Content string `json:"content"`
		Locked  bool   `json:"locked"`
		Title   string `json:"title"`
	} `json:"notes"`
	Role struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		SystemPrompt string `json:"systemPrompt"`
	} `json:"role"`
	Schema  string `json:"schema"`
	Summary string `json:"summary"`
}

type agentFrontendV1SSEEvent struct {
	Type            string          `json:"type"`
	Text            string          `json:"text,omitempty"`
	ActivityID      string          `json:"activityId,omitempty"`
	Label           string          `json:"label,omitempty"`
	Status          string          `json:"status,omitempty"`
	Reply           string          `json:"reply,omitempty"`
	Summary         *string         `json:"summary,omitempty"`
	RunID           string          `json:"runId,omitempty"`
	ThreadID        string          `json:"threadId,omitempty"`
	InputMessageID  string          `json:"inputMessageId,omitempty"`
	OutputMessageID string          `json:"outputMessageId,omitempty"`
	Created         *bool           `json:"created,omitempty"`
	Code            string          `json:"code,omitempty"`
	Message         string          `json:"message,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Previous        *string         `json:"previous,omitempty"`
	Next            *string         `json:"next,omitempty"`
	Patch           json.RawMessage `json:"patch,omitempty"`
	UpdatedNotes    *string         `json:"updatedNotes,omitempty"`
	Provider        string          `json:"provider,omitempty"`
	Model           string          `json:"model,omitempty"`
	RequestMode     string          `json:"requestMode,omitempty"`
}

type agentFrontendV1ReplyResponse struct {
	Reply        string  `json:"reply"`
	Summary      string  `json:"summary"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model,omitempty"`
	RequestMode  string  `json:"requestMode,omitempty"`
	UpdatedNotes *string `json:"updatedNotes,omitempty"`
}

type agentFrontendV1AutoAnalysisResponse struct {
	Reply        string  `json:"reply"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model,omitempty"`
	RequestMode  string  `json:"requestMode,omitempty"`
	UpdatedNotes *string `json:"updatedNotes,omitempty"`
}

type agentFrontendV1QueuedEventPayload struct {
	AnalyticsHeadRevision *int64            `json:"analyticsHeadRevision,omitempty"`
	AutoAnalysisExamID    string            `json:"autoAnalysisExamId,omitempty"`
	AutoAnalysisRoleID    string            `json:"autoAnalysisRoleId,omitempty"`
	MessageSequence       int64             `json:"messageSequence"`
	Model                 string            `json:"model,omitempty"`
	Provider              string            `json:"provider,omitempty"`
	RequestMode           string            `json:"requestMode,omitempty"`
	RunKind               chatagent.RunKind `json:"runKind"`
}

type agentFrontendV1ProviderMetadata struct {
	Provider    string
	Model       string
	RequestMode string
}

type agentFrontendV1ToolEventPayload struct {
	ToolCallKey  string `json:"toolCallKey"`
	ToolName     string `json:"toolName"`
	ToolSequence int64  `json:"toolSequence"`
}

type agentFrontendV1NotesUpdateEventPayload struct {
	Mode         string  `json:"mode"`
	Next         string  `json:"next"`
	Patch        *string `json:"patch"`
	Previous     string  `json:"previous"`
	ToolCallKey  string  `json:"toolCallKey"`
	ToolName     string  `json:"toolName"`
	ToolSequence int64   `json:"toolSequence"`
}

type agentFrontendV1ProjectionState struct {
	pendingNotesTool *agentFrontendV1NotesUpdateEventPayload
	updatedNotes     *string
	expectedNotes    string
	provider         *agentFrontendV1ProviderMetadata
	initialSummary   *string
	queuedObserved   bool
}

type agentFrontendV1CompletedEventPayload struct {
	MessageID       string `json:"messageId"`
	MessageSequence int64  `json:"messageSequence"`
}

type agentFrontendV1FailedEventPayload struct {
	ErrorCode string `json:"errorCode"`
}

func agentFrontendV1ChatRouteContracts(handler *Handler, bodyTimeout time.Duration) []routeContract {
	policy := func(scope string) *routePolicy {
		value := routePolicy{
			method:         http.MethodPost,
			requiresWrites: true,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   scope,
			bodyTimeout: bodyTimeout,
		}
		return &value
	}
	return []routeContract{
		{
			method:      http.MethodPost,
			pattern:     "/api/v1/chat/reply",
			examplePath: "/api/v1/chat/reply",
			handler:     handler.agentFrontendV1ChatReply,
			policy:      policy("agent.chat.reply"),
		},
		{
			method:      http.MethodPost,
			pattern:     "/api/v1/chat/reply/stream",
			examplePath: "/api/v1/chat/reply/stream",
			handler:     handler.agentFrontendV1StreamChatReply,
			policy:      policy("agent.chat.reply"),
		},
		{
			method:      http.MethodPost,
			pattern:     "/api/v1/chat/auto-analysis",
			examplePath: "/api/v1/chat/auto-analysis",
			handler:     handler.agentFrontendV1AutoAnalysis,
			policy:      policy("agent.chat.auto-analysis"),
		},
		{
			method:      http.MethodPost,
			pattern:     "/api/v1/chat/auto-analysis/stream",
			examplePath: "/api/v1/chat/auto-analysis/stream",
			handler:     handler.agentFrontendV1StreamAutoAnalysis,
			policy:      policy("agent.chat.auto-analysis"),
		},
	}
}

func (handler *Handler) agentFrontendV1StreamChatReply(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentFrontendV1ChatReplyRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentRunJSONBytes,
		"Chat request exceeds 524288 bytes.",
		"Chat request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	content, err := canonicalAgentFrontendV1RunInput(input)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_request", "Chat request is invalid.")
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

	revision, err := handler.agentFrontendV1ReplyAnalyticsRevision(request.Context(), access)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	result, err := handler.agentFrontendV1EnqueueFreshReply(request.Context(), access, content, revision)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	handler.agentFrontendV1StreamRun(writer, request, access, result, streamDeadline, valueOrEmpty(input.Notes), input.Summary)
}

func (handler *Handler) agentFrontendV1ChatReply(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentFrontendV1ChatReplyRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentRunJSONBytes,
		"Chat request exceeds 524288 bytes.",
		"Chat request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	content, err := canonicalAgentFrontendV1RunInput(input)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_chat_request", "Chat request is invalid.")
		return
	}
	revision, err := handler.agentFrontendV1ReplyAnalyticsRevision(request.Context(), access)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	result, err := handler.agentFrontendV1EnqueueFreshReply(request.Context(), access, content, revision)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	terminal, err := handler.agentFrontendV1AwaitRun(
		request.Context(), access, result, time.Now().Add(handler.sseMaxDuration), valueOrEmpty(input.Notes),
	)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	summary := *input.Summary
	if terminal.message.ContextSummary != nil {
		summary = *terminal.message.ContextSummary
	}
	writeJSON(writer, http.StatusOK, agentFrontendV1ReplyResponse{
		Reply: terminal.message.Content, Summary: summary,
		Provider: terminal.metadata.Provider, Model: terminal.metadata.Model,
		RequestMode: terminal.metadata.RequestMode, UpdatedNotes: terminal.updatedNotes,
	})
}

func (handler *Handler) agentFrontendV1StreamAutoAnalysis(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentFrontendV1AutoAnalysisRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentRunJSONBytes,
		"Automatic analysis request exceeds 524288 bytes.",
		"Automatic analysis request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if err := validateAgentFrontendV1AutoAnalysisRequest(input); err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_auto_analysis_request", "Automatic analysis request is invalid.")
		return
	}
	identity, analyticsHeadRevision, shouldRun, err := handler.agentFrontendV1AutoAnalysisDecision(request.Context(), access, input)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
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
	if !shouldRun {
		handler.writeAgentFrontendV1EmptySSE(writer)
		return
	}

	result, err := handler.chatAgent.EnqueueAutoAnalysis(request.Context(), access, chatagent.AutoAnalysisRequest{
		PromptConfigurationKey:        agentFrontendV1PromptConfigurationKey,
		ModelConfigurationKey:         agentFrontendV1ModelConfigurationKey,
		ExpectedAnalyticsHeadRevision: analyticsHeadRevision,
		Identity:                      identity,
		FrontendContext: chatagent.AutoAnalysisFrontendContext{
			StudentID:        valueOrEmpty(input.StudentID),
			PTANickname:      valueOrEmpty(input.PTANickname),
			RoleID:           identity.RoleID,
			RoleName:         valueOrEmpty(input.RoleName),
			RoleSystemPrompt: valueOrEmpty(input.RoleSystemPrompt),
			LatestExamID:     identity.ExamID,
			Notes:            valueOrEmpty(input.Notes),
			NotesTitle:       valueOrEmpty(input.NotesTitle),
			NotesLocked:      input.NotesLocked != nil && *input.NotesLocked,
		},
	})
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	if !result.Created {
		handler.writeAgentFrontendV1EmptySSE(writer)
		return
	}
	handler.agentFrontendV1StreamRun(writer, request, access, result, streamDeadline, valueOrEmpty(input.Notes), nil)
}

func (handler *Handler) agentFrontendV1AutoAnalysis(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentFrontendV1AutoAnalysisRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentRunJSONBytes,
		"Automatic analysis request exceeds 524288 bytes.",
		"Automatic analysis request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if err := validateAgentFrontendV1AutoAnalysisRequest(input); err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_auto_analysis_request", "Automatic analysis request is invalid.")
		return
	}
	identity, analyticsHeadRevision, shouldRun, err := handler.agentFrontendV1AutoAnalysisDecision(request.Context(), access, input)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	if !shouldRun {
		writeJSON(writer, http.StatusOK, agentFrontendV1AutoAnalysisResponse{Reply: "", Provider: "server_default"})
		return
	}
	result, err := handler.chatAgent.EnqueueAutoAnalysis(request.Context(), access, chatagent.AutoAnalysisRequest{
		PromptConfigurationKey:        agentFrontendV1PromptConfigurationKey,
		ModelConfigurationKey:         agentFrontendV1ModelConfigurationKey,
		ExpectedAnalyticsHeadRevision: analyticsHeadRevision,
		Identity:                      identity,
		FrontendContext: chatagent.AutoAnalysisFrontendContext{
			StudentID: valueOrEmpty(input.StudentID), PTANickname: valueOrEmpty(input.PTANickname),
			RoleID: identity.RoleID, RoleName: valueOrEmpty(input.RoleName),
			RoleSystemPrompt: valueOrEmpty(input.RoleSystemPrompt), LatestExamID: identity.ExamID,
			Notes: valueOrEmpty(input.Notes), NotesTitle: valueOrEmpty(input.NotesTitle),
			NotesLocked: input.NotesLocked != nil && *input.NotesLocked,
		},
	})
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	if !result.Created {
		writeJSON(writer, http.StatusOK, agentFrontendV1AutoAnalysisResponse{Reply: "", Provider: "server_default"})
		return
	}
	terminal, err := handler.agentFrontendV1AwaitRun(
		request.Context(), access, result, time.Now().Add(handler.sseMaxDuration), valueOrEmpty(input.Notes),
	)
	if err != nil {
		handler.handleChatAgentError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, agentFrontendV1AutoAnalysisResponse{
		Reply: terminal.message.Content, Provider: terminal.metadata.Provider, Model: terminal.metadata.Model,
		RequestMode: terminal.metadata.RequestMode, UpdatedNotes: terminal.updatedNotes,
	})
}

func (handler *Handler) agentFrontendV1AutoAnalysisDecision(
	ctx context.Context,
	access string,
	input agentFrontendV1AutoAnalysisRequest,
) (chatagent.AutoAnalysisIdentity, int64, bool, error) {
	analytics, err := handler.studentAnalytics.GetSelf(ctx, access, 1)
	if err != nil {
		return chatagent.AutoAnalysisIdentity{}, 0, false, err
	}
	if analytics.State != studentanalytics.StateReady || analytics.Ready == nil || analytics.HeadRevision < 1 ||
		len(analytics.Ready.ExamHistory) == 0 {
		return chatagent.AutoAnalysisIdentity{}, 0, false, nil
	}
	latestExamID := analytics.Ready.ExamHistory[len(analytics.Ready.ExamHistory)-1].ExamID
	requestedExamID := valueOrEmpty(input.LatestExamID)
	if requestedExamID != latestExamID {
		return chatagent.AutoAnalysisIdentity{}, 0, false, nil
	}
	identity, err := chatagent.NewAutoAnalysisIdentity(latestExamID, valueOrEmpty(input.RoleID))
	if err != nil {
		return chatagent.AutoAnalysisIdentity{}, 0, false, err
	}
	return identity, analytics.HeadRevision, true, nil
}

func canonicalAgentFrontendV1RunInput(input agentFrontendV1ChatReplyRequest) (string, error) {
	if len(input.Messages) == 0 || input.Summary == nil ||
		len(*input.Summary) > chatagent.MaxContextSummaryBytes || !utf8.ValidString(*input.Summary) {
		return "", errors.New("bounded messages and summary are required")
	}
	if input.Messages[len(input.Messages)-1].Role != "user" {
		return "", errors.New("the current message must be a user message")
	}
	for _, message := range input.Messages {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "system" {
			return "", errors.New("message role is invalid")
		}
		if len(message.Content) == 0 || len(message.Content) > chatagent.MaxMessageBytes || !utf8.ValidString(message.Content) ||
			strings.TrimSpace(message.Content) == "" || strings.ContainsRune(message.Content, '\x00') {
			return "", errors.New("message content is invalid")
		}
		if message.ReasoningContent != nil {
			if message.Role != "assistant" || len(*message.ReasoningContent) > chatagent.MaxReasoningBytes ||
				!utf8.ValidString(*message.ReasoningContent) || strings.ContainsRune(*message.ReasoningContent, '\x00') {
				return "", errors.New("message reasoning is invalid")
			}
		}
	}
	if !validAgentFrontendV1OptionalField(input.StudentID, 256) ||
		!validAgentFrontendV1OptionalField(input.PTANickname, 256) ||
		!validAgentFrontendV1OptionalField(input.RoleID, 256) ||
		!validAgentFrontendV1OptionalField(input.RoleName, 4096) ||
		!validAgentFrontendV1OptionalField(input.RoleSystemPrompt, chatagent.MaxMessageBytes) ||
		!validAgentFrontendV1OptionalField(input.Notes, chatagent.MaxFrontendNotesBytes) ||
		input.Notes != nil && utf8.RuneCountInString(*input.Notes) > chatagent.MaxFrontendNotesCharacters ||
		!validAgentFrontendV1OptionalField(input.NotesTitle, 4096) {
		return "", errors.New("context field is invalid")
	}
	var contextDocument agentFrontendV1RunContext
	contextDocument.Schema = agentFrontendV1ContextSchema
	contextDocument.Summary = *input.Summary
	contextDocument.CurrentUser.Content = input.Messages[len(input.Messages)-1].Content
	contextDocument.CurrentUser.MessageIndex = len(input.Messages) - 1
	contextDocument.CurrentUser.StudentID = valueOrEmpty(input.StudentID)
	contextDocument.CurrentUser.PTANickname = valueOrEmpty(input.PTANickname)
	contextDocument.Messages = make([]agentFrontendV1ContextMessage, len(input.Messages))
	for index, message := range input.Messages {
		contextDocument.Messages[index].Role = message.Role
		contextDocument.Messages[index].Content = message.Content
		if message.ReasoningContent != nil {
			reasoning := *message.ReasoningContent
			contextDocument.Messages[index].ReasoningContent = &reasoning
		}
	}
	contextDocument.Role.ID = valueOrEmpty(input.RoleID)
	contextDocument.Role.Name = valueOrEmpty(input.RoleName)
	contextDocument.Role.SystemPrompt = valueOrEmpty(input.RoleSystemPrompt)
	contextDocument.Notes.Content = valueOrEmpty(input.Notes)
	contextDocument.Notes.Title = valueOrEmpty(input.NotesTitle)
	contextDocument.Notes.Locked = input.NotesLocked != nil && *input.NotesLocked
	raw, err := json.Marshal(contextDocument)
	if err != nil {
		return "", err
	}
	canonical, _, err := canonicaljson.Object(raw, chatagent.MaxFrontendContextDocumentBytes)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func validateAgentFrontendV1AutoAnalysisRequest(input agentFrontendV1AutoAnalysisRequest) error {
	if !validAgentFrontendV1OptionalField(input.StudentID, 256) ||
		!validAgentFrontendV1OptionalField(input.PTANickname, 256) ||
		!validAgentFrontendV1OptionalField(input.RoleID, 256) ||
		!validAgentFrontendV1OptionalField(input.RoleName, 4096) ||
		!validAgentFrontendV1OptionalField(input.RoleSystemPrompt, chatagent.MaxMessageBytes) ||
		!validAgentFrontendV1OptionalField(input.LatestExamID, 4096) ||
		!validAgentFrontendV1OptionalField(input.Notes, chatagent.MaxFrontendNotesBytes) ||
		input.Notes != nil && utf8.RuneCountInString(*input.Notes) > chatagent.MaxFrontendNotesCharacters ||
		!validAgentFrontendV1OptionalField(input.NotesTitle, 4096) {
		return errors.New("automatic analysis context is invalid")
	}
	if input.LatestExamID != nil && *input.LatestExamID != "" && !chatagent.ValidPublicID(*input.LatestExamID) {
		return errors.New("automatic analysis exam identity is invalid")
	}
	if input.RoleID != nil && *input.RoleID != "" {
		if _, err := chatagent.NewAutoAnalysisIdentity("123e4567-e89b-42d3-a456-426614174000", *input.RoleID); err != nil {
			return err
		}
	}
	return nil
}

func validAgentFrontendV1OptionalField(value *string, maximumBytes int) bool {
	return value == nil || len(*value) <= maximumBytes && utf8.ValidString(*value) && !strings.ContainsRune(*value, '\x00')
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (handler *Handler) agentFrontendV1ReplyAnalyticsRevision(
	ctx context.Context,
	access string,
) (*int64, error) {
	analytics, err := handler.studentAnalytics.GetSelf(ctx, access, 1)
	if err != nil {
		return nil, err
	}
	switch analytics.State {
	case studentanalytics.StateNotGenerated, studentanalytics.StateNoObservations:
		return nil, nil
	case studentanalytics.StateReady:
		if analytics.Ready == nil || analytics.HeadRevision < 1 {
			return nil, errors.New("ready student analytics violates its publication contract")
		}
		revision := analytics.HeadRevision
		return &revision, nil
	default:
		return nil, errors.New("student analytics state is invalid")
	}
}

func (handler *Handler) agentFrontendV1EnqueueFreshReply(
	ctx context.Context,
	access string,
	content string,
	expectedAnalyticsHeadRevision *int64,
) (chatagent.EnqueueResult, error) {
	thread, err := handler.chatAgent.CreateThread(ctx, access)
	if err != nil {
		return chatagent.EnqueueResult{}, err
	}
	return handler.chatAgent.Enqueue(ctx, access, thread.ID, chatagent.EnqueueRequest{
		ClientRequestID:               handler.requestIDs.Next(),
		Kind:                          chatagent.RunReply,
		Content:                       content,
		PromptConfigurationKey:        agentFrontendV1PromptConfigurationKey,
		ModelConfigurationKey:         agentFrontendV1ModelConfigurationKey,
		ExpectedAnalyticsHeadRevision: expectedAnalyticsHeadRevision,
	})
}

func (handler *Handler) agentFrontendV1StreamRun(
	writer http.ResponseWriter,
	request *http.Request,
	access string,
	enqueued chatagent.EnqueueResult,
	streamDeadline time.Time,
	initialNotes string,
	initialSummary *string,
) {
	after := int64(0)
	batch, err := handler.chatAgent.ReadRunEvents(request.Context(), access, enqueued.Run.ID, after, agentEventBatchSize)
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
	projection := agentFrontendV1ProjectionState{expectedNotes: initialNotes, initialSummary: initialSummary}
	for {
		if len(batch.Events) > 0 {
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
		}
		for _, event := range batch.Events {
			after = event.Sequence
			terminal, projectionErr := handler.projectAgentFrontendV1RunEvent(writer, request, access, enqueued, batch, event, &projection)
			if projectionErr != nil {
				handler.logChatAgentStreamFailure(request, projectionErr)
				_ = writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
					Type: "error", Code: "agent_stream_invalid", Message: "Agent run stream is invalid.",
				})
				_ = controller.Flush()
				return
			}
			if terminal {
				_ = controller.Flush()
				return
			}
		}
		if len(batch.Events) > 0 {
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
		}
		if batch.Terminal && after == batch.LastSequence {
			_ = writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
				Type: "error", Code: "agent_terminal_event_missing", Message: "Agent run ended without a terminal event.",
			})
			_ = controller.Flush()
			return
		}
		if len(batch.Events) == agentEventBatchSize {
			batch, err = handler.chatAgent.ReadRunEvents(request.Context(), access, enqueued.Run.ID, after, agentEventBatchSize)
			if err != nil {
				handler.writeAgentFrontendV1StreamServiceError(writer, request, err)
				_ = controller.Flush()
				return
			}
			continue
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
		batch, err = handler.chatAgent.ReadRunEvents(request.Context(), access, enqueued.Run.ID, after, agentEventBatchSize)
		if err != nil {
			handler.writeAgentFrontendV1StreamServiceError(writer, request, err)
			_ = controller.Flush()
			return
		}
	}
}

func (handler *Handler) projectAgentFrontendV1RunEvent(
	writer http.ResponseWriter,
	request *http.Request,
	access string,
	enqueued chatagent.EnqueueResult,
	batch chatagent.RunEventBatch,
	event chatagent.RunEvent,
	projection *agentFrontendV1ProjectionState,
) (bool, error) {
	if projection == nil {
		return false, errors.New("Agent frontend projection state is absent")
	}
	switch event.Type {
	case "queued":
		metadata, err := decodeAgentFrontendV1QueuedMetadata(event.Payload, enqueued.Run.Kind, enqueued.Message.Sequence)
		if err != nil || projection.queuedObserved {
			return false, errors.New("queued event payload is invalid")
		}
		projection.queuedObserved = true
		projection.provider = metadata
		meta := agentFrontendV1SSEEvent{Type: "meta", Summary: projection.initialSummary}
		if metadata != nil {
			meta.Provider = metadata.Provider
			meta.Model = metadata.Model
			meta.RequestMode = metadata.RequestMode
		}
		return false, writeAgentFrontendV1SSEEvent(writer, meta)
	case "claimed", "reclaimed":
		return false, nil
	case "tool.succeeded", "tool.failed", "tool.denied":
		payload, err := applyAgentFrontendV1ToolEvent(event.Payload, event.Type, projection)
		if err != nil {
			return false, err
		}
		label := agentFrontendV1ToolActivityLabel(payload.ToolName)
		if payload.ToolName == chatagent.ToolUpdateNotes && event.Type == "tool.succeeded" {
			return false, writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
				Type: "tool_activity_done", ActivityID: payload.ToolCallKey, Label: label, Status: "done",
			})
		}
		if err := writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
			Type: "tool_activity_start", ActivityID: payload.ToolCallKey, Label: label, Status: "running",
		}); err != nil {
			return false, err
		}
		projectedType, status := "tool_activity_done", "done"
		if event.Type != "tool.succeeded" {
			projectedType, status = "tool_activity_error", "error"
		}
		return false, writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
			Type: projectedType, ActivityID: payload.ToolCallKey, Label: label, Status: status,
		})
	case "notes_update":
		payload, err := applyAgentFrontendV1NotesEvent(event.Payload, projection)
		if err != nil {
			return false, err
		}
		if err := writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
			Type: "tool_activity_start", ActivityID: payload.ToolCallKey,
			Label: agentFrontendV1ToolActivityLabel(payload.ToolName), Status: "running",
		}); err != nil {
			return false, err
		}
		patch, err := json.Marshal(payload.Patch)
		if err != nil {
			return false, err
		}
		previous, next := payload.Previous, payload.Next
		if err := writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
			Type: "notes_update", Mode: payload.Mode, Previous: &previous, Next: &next, Patch: patch,
		}); err != nil {
			return false, err
		}
		return false, nil
	case "completed":
		if projection.pendingNotesTool != nil {
			return false, errors.New("completed event follows an unfinished notes mutation activity")
		}
		if !batch.Terminal || event.Sequence != batch.LastSequence {
			return false, errors.New("completed event is not the durable terminal event")
		}
		var payload agentFrontendV1CompletedEventPayload
		if err := decodeAgentFrontendV1DurableEvent(event.Payload, &payload); err != nil ||
			!chatagent.ValidPublicID(payload.MessageID) || payload.MessageSequence < 1 {
			return false, errors.New("completed event payload is invalid")
		}
		run, message, err := handler.agentFrontendV1CompletedMessage(request.Context(), access, enqueued, payload)
		if err != nil {
			return false, err
		}
		if message.ReasoningContent != nil && *message.ReasoningContent != "" {
			if err := writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{Type: "reasoning_delta", Text: *message.ReasoningContent}); err != nil {
				return false, err
			}
		}
		if message.Content != "" {
			if err := writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{Type: "delta", Text: message.Content}); err != nil {
				return false, err
			}
		}
		created := enqueued.Created
		done := agentFrontendV1SSEEvent{
			Type:            "done",
			Reply:           message.Content,
			Summary:         message.ContextSummary,
			RunID:           run.ID,
			ThreadID:        run.ThreadID,
			InputMessageID:  run.InputMessageID,
			OutputMessageID: message.ID,
			Created:         &created,
			UpdatedNotes:    projection.updatedNotes,
		}
		if projection.provider != nil {
			done.Provider = projection.provider.Provider
			done.Model = projection.provider.Model
			done.RequestMode = projection.provider.RequestMode
		}
		return true, writeAgentFrontendV1SSEEvent(writer, done)
	case "failed":
		if projection.pendingNotesTool != nil {
			return false, errors.New("failed event follows an unfinished notes mutation activity")
		}
		if !batch.Terminal || event.Sequence != batch.LastSequence {
			return false, errors.New("failed event is not the durable terminal event")
		}
		var payload agentFrontendV1FailedEventPayload
		if err := decodeAgentFrontendV1DurableEvent(event.Payload, &payload); err != nil || payload.ErrorCode == "" {
			return false, errors.New("failed event payload is invalid")
		}
		run, found, err := handler.chatAgent.GetRun(request.Context(), access, enqueued.Run.ID)
		if err != nil || !found || run.Status != chatagent.RunFailed || run.ErrorCode == nil || *run.ErrorCode != payload.ErrorCode {
			return false, errors.New("failed event and run state disagree")
		}
		return true, writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
			Type: "error", Code: payload.ErrorCode, Message: "Agent run failed.",
		})
	case "superseded":
		if projection.pendingNotesTool != nil {
			return false, errors.New("superseded event follows an unfinished notes mutation activity")
		}
		if !batch.Terminal || event.Sequence != batch.LastSequence {
			return false, errors.New("superseded event is not the durable terminal event")
		}
		run, found, err := handler.chatAgent.GetRun(request.Context(), access, enqueued.Run.ID)
		if err != nil || !found || run.Status != chatagent.RunSuperseded {
			return false, errors.New("superseded event and run state disagree")
		}
		return true, writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{
			Type: "error", Code: "agent_run_superseded", Message: "Agent run was superseded.",
		})
	default:
		return false, errors.New("unknown durable run event")
	}
}

type agentFrontendV1TerminalResult struct {
	message      chatagent.Message
	metadata     agentFrontendV1ProviderMetadata
	updatedNotes *string
}

func decodeAgentFrontendV1QueuedMetadata(
	raw json.RawMessage,
	expectedKind chatagent.RunKind,
	expectedMessageSequence int64,
) (*agentFrontendV1ProviderMetadata, error) {
	var payload agentFrontendV1QueuedEventPayload
	if err := decodeAgentFrontendV1DurableEvent(raw, &payload); err != nil ||
		payload.MessageSequence != expectedMessageSequence || payload.RunKind != expectedKind {
		return nil, errors.New("queued event violates its durable shape")
	}
	switch expectedKind {
	case chatagent.RunReply:
		if payload.AutoAnalysisExamID != "" || payload.AutoAnalysisRoleID != "" ||
			payload.AnalyticsHeadRevision != nil && *payload.AnalyticsHeadRevision < 1 {
			return nil, errors.New("reply queued event has invalid analytics or automatic-analysis fields")
		}
	case chatagent.RunAutoAnalysis:
		if payload.AnalyticsHeadRevision == nil || *payload.AnalyticsHeadRevision < 1 ||
			!chatagent.ValidPublicID(payload.AutoAnalysisExamID) || payload.AutoAnalysisRoleID == "" ||
			payload.AutoAnalysisRoleID != strings.TrimSpace(payload.AutoAnalysisRoleID) ||
			len(payload.AutoAnalysisRoleID) > chatagent.MaxAutoAnalysisRoleIDBytes ||
			!utf8.ValidString(payload.AutoAnalysisRoleID) || strings.ContainsRune(payload.AutoAnalysisRoleID, '\x00') {
			return nil, errors.New("automatic-analysis queued event is missing immutable provenance")
		}
	default:
		return nil, errors.New("queued event run kind is invalid")
	}
	metadataCount := 0
	for _, value := range []string{payload.Provider, payload.Model, payload.RequestMode} {
		if value != "" {
			metadataCount++
		}
		if value != strings.TrimSpace(value) || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("queued provider metadata is invalid")
		}
	}
	if metadataCount == 0 {
		return nil, nil
	}
	if metadataCount != 3 {
		return nil, errors.New("queued provider metadata is incomplete")
	}
	return &agentFrontendV1ProviderMetadata{
		Provider: payload.Provider, Model: payload.Model, RequestMode: payload.RequestMode,
	}, nil
}

func applyAgentFrontendV1NotesEvent(
	raw json.RawMessage,
	projection *agentFrontendV1ProjectionState,
) (agentFrontendV1NotesUpdateEventPayload, error) {
	if projection == nil || projection.pendingNotesTool != nil {
		return agentFrontendV1NotesUpdateEventPayload{}, errors.New("durable notes mutations overlap")
	}
	var payload agentFrontendV1NotesUpdateEventPayload
	if err := decodeAgentFrontendV1DurableEvent(raw, &payload); err != nil ||
		payload.ToolName != chatagent.ToolUpdateNotes || payload.ToolCallKey == "" || payload.ToolSequence < 1 ||
		(payload.Mode != "patch" && payload.Mode != "replace") ||
		payload.Mode == "replace" && payload.Patch != nil || payload.Mode == "patch" && payload.Patch == nil ||
		!validAgentFrontendV1NotesEventContent(payload.Previous) || !validAgentFrontendV1NotesEventContent(payload.Next) ||
		payload.Previous != projection.expectedNotes {
		return agentFrontendV1NotesUpdateEventPayload{}, errors.New("notes_update event payload is invalid")
	}
	projection.pendingNotesTool = &payload
	next := payload.Next
	projection.updatedNotes = &next
	projection.expectedNotes = next
	return payload, nil
}

func validAgentFrontendV1NotesEventContent(value string) bool {
	return len(value) <= chatagent.MaxFrontendNotesBytes && utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= chatagent.MaxFrontendNotesCharacters && !strings.ContainsRune(value, '\x00')
}

func applyAgentFrontendV1ToolEvent(
	raw json.RawMessage,
	eventType string,
	projection *agentFrontendV1ProjectionState,
) (agentFrontendV1ToolEventPayload, error) {
	var payload agentFrontendV1ToolEventPayload
	if projection == nil || decodeAgentFrontendV1DurableEvent(raw, &payload) != nil ||
		payload.ToolCallKey == "" || payload.ToolName == "" || payload.ToolSequence < 1 {
		return agentFrontendV1ToolEventPayload{}, errors.New("tool event payload is invalid")
	}
	if payload.ToolName == chatagent.ToolUpdateNotes && eventType == "tool.succeeded" {
		pending := projection.pendingNotesTool
		if pending == nil || pending.ToolCallKey != payload.ToolCallKey || pending.ToolName != payload.ToolName ||
			pending.ToolSequence != payload.ToolSequence {
			return agentFrontendV1ToolEventPayload{}, errors.New("successful update_notes event lacks its preceding durable notes_update")
		}
		projection.pendingNotesTool = nil
		return payload, nil
	}
	if projection.pendingNotesTool != nil {
		return agentFrontendV1ToolEventPayload{}, errors.New("durable notes_update is not followed by its matching tool completion")
	}
	return payload, nil
}

func (handler *Handler) agentFrontendV1AwaitRun(
	ctx context.Context,
	access string,
	enqueued chatagent.EnqueueResult,
	deadline time.Time,
	initialNotes string,
) (agentFrontendV1TerminalResult, error) {
	waitContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	after := int64(0)
	projection := agentFrontendV1ProjectionState{expectedNotes: initialNotes}
	for {
		batch, err := handler.chatAgent.ReadRunEvents(waitContext, access, enqueued.Run.ID, after, agentEventBatchSize)
		if err != nil {
			return agentFrontendV1TerminalResult{}, err
		}
		for _, event := range batch.Events {
			after = event.Sequence
			switch event.Type {
			case "queued":
				metadata, err := decodeAgentFrontendV1QueuedMetadata(event.Payload, enqueued.Run.Kind, enqueued.Message.Sequence)
				if err != nil || projection.queuedObserved {
					return agentFrontendV1TerminalResult{}, errors.New("queued event payload is invalid")
				}
				projection.queuedObserved = true
				projection.provider = metadata
			case "claimed", "reclaimed":
			case "notes_update":
				if _, err := applyAgentFrontendV1NotesEvent(event.Payload, &projection); err != nil {
					return agentFrontendV1TerminalResult{}, err
				}
			case "tool.succeeded", "tool.failed", "tool.denied":
				if _, err := applyAgentFrontendV1ToolEvent(event.Payload, event.Type, &projection); err != nil {
					return agentFrontendV1TerminalResult{}, err
				}
			case "completed":
				if projection.pendingNotesTool != nil || !batch.Terminal || event.Sequence != batch.LastSequence {
					return agentFrontendV1TerminalResult{}, errors.New("completed event is not a valid durable terminal event")
				}
				var payload agentFrontendV1CompletedEventPayload
				if err := decodeAgentFrontendV1DurableEvent(event.Payload, &payload); err != nil ||
					!chatagent.ValidPublicID(payload.MessageID) || payload.MessageSequence < 1 {
					return agentFrontendV1TerminalResult{}, errors.New("completed event payload is invalid")
				}
				_, message, err := handler.agentFrontendV1CompletedMessage(waitContext, access, enqueued, payload)
				if err != nil {
					return agentFrontendV1TerminalResult{}, err
				}
				result := agentFrontendV1TerminalResult{message: message, updatedNotes: projection.updatedNotes}
				if projection.provider != nil {
					result.metadata = *projection.provider
				}
				return result, nil
			case "failed":
				var payload agentFrontendV1FailedEventPayload
				if decodeAgentFrontendV1DurableEvent(event.Payload, &payload) != nil || payload.ErrorCode == "" ||
					!batch.Terminal || event.Sequence != batch.LastSequence || projection.pendingNotesTool != nil {
					return agentFrontendV1TerminalResult{}, errors.New("failed event payload is invalid")
				}
				run, found, err := handler.chatAgent.GetRun(waitContext, access, enqueued.Run.ID)
				if err != nil || !found || run.Status != chatagent.RunFailed || run.ErrorCode == nil || *run.ErrorCode != payload.ErrorCode {
					return agentFrontendV1TerminalResult{}, errors.New("failed event and run state disagree")
				}
				return agentFrontendV1TerminalResult{}, &chatagent.Error{
					Code: chatagent.ErrorProvider, Permanent: true, Op: "await Agent frontend run",
					Cause: fmt.Errorf("agent run failed: %s", payload.ErrorCode),
				}
			case "superseded":
				if !batch.Terminal || event.Sequence != batch.LastSequence || projection.pendingNotesTool != nil {
					return agentFrontendV1TerminalResult{}, errors.New("superseded event payload is invalid")
				}
				run, found, err := handler.chatAgent.GetRun(waitContext, access, enqueued.Run.ID)
				if err != nil || !found || run.Status != chatagent.RunSuperseded {
					return agentFrontendV1TerminalResult{}, errors.New("superseded event and run state disagree")
				}
				return agentFrontendV1TerminalResult{}, &chatagent.Error{
					Code: chatagent.ErrorStoredDataInvalid, Permanent: true, Op: "await Agent frontend run",
					Cause: errors.New("agent run was superseded"),
				}
			default:
				return agentFrontendV1TerminalResult{}, errors.New("unknown durable run event")
			}
		}
		if batch.Terminal && after == batch.LastSequence {
			return agentFrontendV1TerminalResult{}, errors.New("agent run ended without a terminal event")
		}
		if len(batch.Events) == agentEventBatchSize {
			continue
		}
		timer := time.NewTimer(agentEventPollInterval)
		select {
		case <-waitContext.Done():
			timer.Stop()
			return agentFrontendV1TerminalResult{}, &chatagent.Error{
				Code: chatagent.ErrorCanceled, Permanent: false, Op: "await Agent frontend run", Cause: waitContext.Err(),
			}
		case <-timer.C:
		}
	}
}

func (handler *Handler) writeAgentFrontendV1EmptySSE(writer http.ResponseWriter) {
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	payload, _ := json.Marshal(struct {
		Type     string `json:"type"`
		Reply    string `json:"reply"`
		Provider string `json:"provider"`
	}{Type: "done", Reply: "", Provider: "none"})
	_, _ = fmt.Fprintf(writer, "event: done\ndata: %s\n\n", payload)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

func agentFrontendV1ToolActivityLabel(toolName string) string {
	if toolName == chatagent.ToolUpdateNotes {
		return "更新学习笔记"
	}
	return toolName
}

func (handler *Handler) agentFrontendV1CompletedMessage(
	ctx context.Context,
	access string,
	enqueued chatagent.EnqueueResult,
	payload agentFrontendV1CompletedEventPayload,
) (chatagent.Run, chatagent.Message, error) {
	run, found, err := handler.chatAgent.GetRun(ctx, access, enqueued.Run.ID)
	if err != nil {
		return chatagent.Run{}, chatagent.Message{}, err
	}
	if !found || run.ID != enqueued.Run.ID || run.Status != chatagent.RunSucceeded ||
		run.ThreadID != enqueued.Run.ThreadID || run.InputMessageID != enqueued.Run.InputMessageID ||
		enqueued.Message.ID != run.InputMessageID || enqueued.Message.ThreadID != run.ThreadID ||
		run.OutputMessageID == nil || *run.OutputMessageID != payload.MessageID {
		return chatagent.Run{}, chatagent.Message{}, errors.New("completed event and run state disagree")
	}
	messages, err := handler.chatAgent.ListMessages(ctx, access, run.ThreadID, payload.MessageSequence-1, 1)
	if err != nil {
		return chatagent.Run{}, chatagent.Message{}, err
	}
	if len(messages) != 1 || messages[0].ID != payload.MessageID || messages[0].Sequence != payload.MessageSequence ||
		messages[0].Kind != chatagent.MessageAssistant || messages[0].RunID == nil || *messages[0].RunID != run.ID {
		return chatagent.Run{}, chatagent.Message{}, errors.New("completed output message is invalid")
	}
	return run, messages[0], nil
}

func decodeAgentFrontendV1DurableEvent(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func writeAgentFrontendV1SSEEvent(writer http.ResponseWriter, event agentFrontendV1SSEEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, data)
	return err
}

func (handler *Handler) writeAgentFrontendV1StreamServiceError(writer http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	handler.logChatAgentStreamFailure(request, err)
	code := string(chatagent.CodeOf(err))
	message := "Agent run stream failed."
	if auth.ErrorCodeOf(err) != "" {
		code = "auth_authentication_rejected"
		message = "Authentication was rejected."
	}
	if code == "" {
		code = "internal_error"
	}
	_ = writeAgentFrontendV1SSEEvent(writer, agentFrontendV1SSEEvent{Type: "error", Code: code, Message: message})
}
