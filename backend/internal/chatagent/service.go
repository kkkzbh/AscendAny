package chatagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

var (
	canonicalUUIDv4    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	configurationKey   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	identifierPattern  = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	toolCallKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	sha256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidPublicID(value string) bool {
	return canonicalUUIDv4.MatchString(value)
}

func ValidConfigurationKey(value string) bool {
	return configurationKey.MatchString(value)
}

type Repository interface {
	CreateThread(context.Context, CreateThreadCommand) (Thread, error)
	ListThreads(context.Context, ThreadQuery) ([]Thread, error)
	ListMessages(context.Context, MessageQuery) ([]Message, error)
	GetRun(context.Context, RunQuery) (Run, bool, error)
	ReadRunEvents(context.Context, EventQuery) (RunEventBatch, error)
	Enqueue(context.Context, EnqueueCommand) (EnqueueResult, error)
	EnqueueAutoAnalysis(context.Context, AutoAnalysisCommand) (EnqueueResult, error)
}

type UUIDGenerator func() (string, error)

type Service struct {
	repository Repository
	uuid       UUIDGenerator
}

func NewService(repository Repository) (*Service, error) {
	return newService(repository, randomUUIDv4)
}

func newService(repository Repository, uuid UUIDGenerator) (*Service, error) {
	if repository == nil || uuid == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent service", errors.New("repository and UUID generator are required"))
	}
	return &Service{repository: repository, uuid: uuid}, nil
}

func (service *Service) CreateThread(ctx context.Context, principal auth.AccessPrincipal) (Thread, error) {
	if err := validateStudentPrincipal(ctx, principal); err != nil {
		return Thread{}, err
	}
	threadID, err := service.uuid()
	if err != nil {
		return Thread{}, domainError(ErrorInvalidConfiguration, false, "generate chat thread ID", err)
	}
	thread, err := service.repository.CreateThread(ctx, CreateThreadCommand{
		Principal: principal,
		ThreadID:  threadID,
		Kind:      ThreadConversation,
	})
	if err != nil {
		return Thread{}, err
	}
	if err := validateThread(thread); err != nil {
		return Thread{}, domainError(ErrorStoredDataInvalid, true, "validate created chat thread", err)
	}
	if thread.ID != threadID {
		return Thread{}, domainError(ErrorStoredDataInvalid, true, "validate created chat thread", errors.New("repository returned a different thread ID"))
	}
	return thread, nil
}

func (service *Service) ListThreads(ctx context.Context, query ThreadQuery) (ThreadPage, error) {
	if err := validateStudentPrincipal(ctx, query.Principal); err != nil {
		return ThreadPage{}, err
	}
	if query.Limit < 1 || query.Limit > MaxPageSize || query.Cursor != nil && !canonicalUUIDv4.MatchString(*query.Cursor) {
		return ThreadPage{}, domainError(ErrorInvalidInput, true, "validate chat thread query", errors.New("canonical cursor and bounded limit are required"))
	}
	threads, err := service.repository.ListThreads(ctx, query)
	if err != nil {
		return ThreadPage{}, err
	}
	if len(threads) > query.Limit+1 {
		return ThreadPage{}, domainError(ErrorStoredDataInvalid, true, "validate chat thread page", errors.New("repository exceeded the requested lookahead limit"))
	}
	for _, thread := range threads {
		if err := validateThread(thread); err != nil {
			return ThreadPage{}, domainError(ErrorStoredDataInvalid, true, "validate chat thread page", err)
		}
	}
	page := ThreadPage{Items: threads}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		cursor := page.Items[len(page.Items)-1].ID
		page.NextCursor = &cursor
	}
	if page.Items == nil {
		page.Items = []Thread{}
	}
	return page, nil
}

func (service *Service) ListMessages(ctx context.Context, query MessageQuery) ([]Message, error) {
	if err := validateStudentPrincipal(ctx, query.Principal); err != nil {
		return nil, err
	}
	if !canonicalUUIDv4.MatchString(query.ThreadID) || query.AfterSequence < 0 || query.Limit < 1 || query.Limit > MaxPageSize {
		return nil, domainError(ErrorInvalidInput, true, "validate chat message query", errors.New("canonical thread ID, nonnegative cursor, and bounded limit are required"))
	}
	messages, err := service.repository.ListMessages(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(messages) > query.Limit {
		return nil, domainError(ErrorStoredDataInvalid, true, "validate chat message page", errors.New("repository exceeded the requested limit"))
	}
	previous := query.AfterSequence
	for _, message := range messages {
		if message.ThreadID != query.ThreadID || message.Sequence <= previous {
			return nil, domainError(ErrorStoredDataInvalid, true, "validate chat message page", errors.New("repository returned an unordered or foreign message"))
		}
		if err := validateMessage(message); err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "validate chat message page", err)
		}
		previous = message.Sequence
	}
	return messages, nil
}

func (service *Service) GetRun(ctx context.Context, query RunQuery) (Run, bool, error) {
	if err := validateStudentPrincipal(ctx, query.Principal); err != nil {
		return Run{}, false, err
	}
	if !canonicalUUIDv4.MatchString(query.RunID) {
		return Run{}, false, domainError(ErrorInvalidInput, true, "validate agent run query", errors.New("canonical run ID is required"))
	}
	run, found, err := service.repository.GetRun(ctx, query)
	if err != nil || !found {
		return Run{}, found, err
	}
	if run.ID != query.RunID {
		return Run{}, false, domainError(ErrorStoredDataInvalid, true, "validate agent run", errors.New("repository returned a foreign run"))
	}
	if err := validateRun(run); err != nil {
		return Run{}, false, domainError(ErrorStoredDataInvalid, true, "validate agent run", err)
	}
	return run, true, nil
}

func (service *Service) ReadRunEvents(ctx context.Context, query EventQuery) (RunEventBatch, error) {
	if err := validateStudentPrincipal(ctx, query.Principal); err != nil {
		return RunEventBatch{}, err
	}
	if !canonicalUUIDv4.MatchString(query.RunID) || query.AfterSequence < 0 || query.Limit < 1 || query.Limit > MaxPageSize {
		return RunEventBatch{}, domainError(ErrorInvalidInput, true, "validate agent run event query", errors.New("canonical run ID, nonnegative cursor, and bounded limit are required"))
	}
	batch, err := service.repository.ReadRunEvents(ctx, query)
	if err != nil {
		return RunEventBatch{}, err
	}
	if len(batch.Events) > query.Limit || batch.LastSequence < 1 || batch.LastSequence < query.AfterSequence {
		return RunEventBatch{}, domainError(ErrorStoredDataInvalid, true, "validate agent run event page", errors.New("repository returned an invalid event head or exceeded the requested limit"))
	}
	previous := query.AfterSequence
	for index := range batch.Events {
		event := batch.Events[index]
		if event.Sequence != previous+1 || !identifierPattern.MatchString(event.Type) || event.CreatedAt.IsZero() || !event.CreatedAt.Equal(event.CreatedAt.UTC()) {
			return RunEventBatch{}, domainError(ErrorStoredDataInvalid, true, "validate agent run event page", errors.New("repository returned an invalid event"))
		}
		canonical, _, err := canonicaljson.Object(event.Payload, MaxRunEventDocumentBytes)
		if err != nil {
			return RunEventBatch{}, domainError(ErrorStoredDataInvalid, true, "validate agent run event page", err)
		}
		batch.Events[index].Payload = canonical
		previous = event.Sequence
	}
	if previous > batch.LastSequence {
		return RunEventBatch{}, domainError(ErrorStoredDataInvalid, true, "validate agent run event page", errors.New("repository event page exceeds its durable head"))
	}
	if len(batch.Events) < query.Limit && previous != batch.LastSequence {
		return RunEventBatch{}, domainError(ErrorStoredDataInvalid, true, "validate agent run event page", errors.New("repository returned a partial event page before the durable head"))
	}
	if previous == batch.LastSequence && len(batch.Events) > 0 {
		terminalEvent := terminalRunEvent(batch.Events[len(batch.Events)-1].Type)
		if terminalEvent != batch.Terminal {
			return RunEventBatch{}, domainError(ErrorStoredDataInvalid, true, "validate agent run event page", errors.New("terminal run state and final event disagree"))
		}
	}
	return batch, nil
}

func terminalRunEvent(eventType string) bool {
	return eventType == "completed" || eventType == "failed" || eventType == "superseded"
}

func (service *Service) Enqueue(ctx context.Context, input EnqueueInput) (EnqueueResult, error) {
	if err := validateEnqueueInput(ctx, input); err != nil {
		return EnqueueResult{}, err
	}
	runID, err := service.uuid()
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate agent run ID", err)
	}
	messageID, err := service.uuid()
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate agent input message ID", err)
	}
	result, err := service.repository.Enqueue(ctx, EnqueueCommand{EnqueueInput: input, RunID: runID, MessageID: messageID})
	if err != nil {
		return EnqueueResult{}, err
	}
	if err := validateRun(result.Run); err != nil {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate enqueued agent run", err)
	}
	if err := validateMessage(result.Message); err != nil {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate enqueued agent message", err)
	}
	if result.Run.ThreadID != input.ThreadID || result.Run.ClientRequestID != input.ClientRequestID || result.Run.Kind != input.Kind ||
		result.Run.InputMessageID != result.Message.ID || result.Message.ThreadID != input.ThreadID || result.Message.Content != input.Content {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate enqueued agent run", errors.New("repository returned a run that differs from the request"))
	}
	expectedMessageKind := MessageUser
	if input.Kind == RunAutoAnalysis {
		expectedMessageKind = MessageAutoAnalysisRequest
	}
	if result.Message.Kind != expectedMessageKind || result.Created &&
		(result.Run.ID != runID || result.Message.ID != messageID || result.Run.Status != RunQueued) {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate enqueued agent run", errors.New("repository returned invalid creation identity or state"))
	}
	return result, nil
}

func (service *Service) EnqueueAutoAnalysis(ctx context.Context, input AutoAnalysisInput) (EnqueueResult, error) {
	if err := validateAutoAnalysisInput(ctx, input); err != nil {
		return EnqueueResult{}, err
	}
	inputContent, err := canonicalAutoAnalysisInputContent(input.FrontendContext)
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidInput, true, "encode auto-analysis frontend context", err)
	}
	threadID, err := service.uuid()
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate auto-analysis thread ID", err)
	}
	runID, err := service.uuid()
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate auto-analysis run ID", err)
	}
	messageID, err := service.uuid()
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate auto-analysis input message ID", err)
	}
	clientRequestID, err := service.uuid()
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate auto-analysis request ID", err)
	}
	command := AutoAnalysisCommand{
		AutoAnalysisInput: input,
		ThreadID:          threadID,
		RunID:             runID,
		MessageID:         messageID,
		ClientRequestID:   clientRequestID,
	}
	result, err := service.repository.EnqueueAutoAnalysis(ctx, command)
	if err != nil {
		return EnqueueResult{}, err
	}
	if err := validateRun(result.Run); err != nil {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate auto-analysis run", err)
	}
	if err := validateMessage(result.Message); err != nil {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate auto-analysis message", err)
	}
	if result.Run.Kind != RunAutoAnalysis || result.Run.InputMessageID != result.Message.ID ||
		result.Run.ThreadID != result.Message.ThreadID || result.Message.Kind != MessageAutoAnalysisRequest {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate auto-analysis run", errors.New("repository returned a run that violates the automatic analysis contract"))
	}
	if result.Created && result.Message.Content != inputContent {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate auto-analysis run", errors.New("repository returned newly created automatic analysis with different content"))
	}
	if result.Created && (result.Run.ID != runID || result.Run.InputMessageID != messageID ||
		result.Run.ClientRequestID != clientRequestID || result.Run.Status != RunQueued) {
		return EnqueueResult{}, domainError(ErrorStoredDataInvalid, true, "validate auto-analysis run", errors.New("repository returned invalid creation identity or state"))
	}
	return result, nil
}

func validateEnqueueInput(ctx context.Context, input EnqueueInput) error {
	if err := validateStudentPrincipal(ctx, input.Principal); err != nil {
		return err
	}
	if !canonicalUUIDv4.MatchString(input.ThreadID) || !canonicalUUIDv4.MatchString(input.ClientRequestID) ||
		!configurationKey.MatchString(input.PromptConfigurationKey) || !configurationKey.MatchString(input.ModelConfigurationKey) ||
		!validPersistedMessageContent(MessageUser, input.Content) {
		return domainError(ErrorInvalidInput, true, "validate agent enqueue", errors.New("canonical IDs, configuration keys, and bounded content are required"))
	}
	if input.Kind != RunReply || input.ExpectedAnalyticsHeadRevision != nil && *input.ExpectedAnalyticsHeadRevision < 1 {
		return domainError(ErrorInvalidInput, true, "validate agent enqueue", errors.New("conversation enqueue requires a reply run and an optional positive analytics head revision"))
	}
	return nil
}

func validateAutoAnalysisInput(ctx context.Context, input AutoAnalysisInput) error {
	if err := validateStudentPrincipal(ctx, input.Principal); err != nil {
		return err
	}
	if !configurationKey.MatchString(input.PromptConfigurationKey) ||
		!configurationKey.MatchString(input.ModelConfigurationKey) ||
		input.ExpectedAnalyticsHeadRevision < 1 {
		return domainError(ErrorInvalidInput, true, "validate auto-analysis enqueue", errors.New("configuration keys and a positive analytics head revision are required"))
	}
	if err := validateAutoAnalysisIdentity(input.Identity); err != nil {
		return domainError(ErrorInvalidInput, true, "validate auto-analysis identity", err)
	}
	if input.FrontendContext.LatestExamID != input.Identity.ExamID || input.FrontendContext.RoleID != input.Identity.RoleID {
		return domainError(ErrorInvalidInput, true, "validate auto-analysis enqueue", errors.New("automatic-analysis identity must equal the frozen frontend context"))
	}
	if _, err := canonicalAutoAnalysisInputContent(input.FrontendContext); err != nil {
		return domainError(ErrorInvalidInput, true, "validate auto-analysis frontend context", err)
	}
	return nil
}

func validateStudentPrincipal(ctx context.Context, principal auth.AccessPrincipal) error {
	if ctx == nil || !canonicalUUIDv4.MatchString(principal.AccountID) || !canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision < 1 {
		return domainError(ErrorInvalidInput, true, "validate student principal", errors.New("canonical access principal is required"))
	}
	if principal.Role != auth.RoleStudent {
		return domainError(ErrorPrincipalRejected, true, "authorize student principal", errors.New("student role is required"))
	}
	return nil
}

func validateThread(thread Thread) error {
	if !canonicalUUIDv4.MatchString(thread.ID) ||
		(thread.Kind != ThreadConversation && thread.Kind != ThreadAutoAnalysis) ||
		thread.HeadRevision < 0 || thread.CreatedAt.IsZero() || thread.UpdatedAt.Before(thread.CreatedAt) ||
		!thread.CreatedAt.Equal(thread.CreatedAt.UTC()) || !thread.UpdatedAt.Equal(thread.UpdatedAt.UTC()) {
		return errors.New("thread violates its persisted contract")
	}
	return nil
}

func validateMessage(message Message) error {
	if !canonicalUUIDv4.MatchString(message.ID) || !canonicalUUIDv4.MatchString(message.ThreadID) || message.Sequence < 1 ||
		!validPersistedMessageContent(message.Kind, message.Content) || message.CreatedAt.IsZero() || !message.CreatedAt.Equal(message.CreatedAt.UTC()) {
		return errors.New("message violates its persisted contract")
	}
	switch message.Kind {
	case MessageUser, MessageAutoAnalysisRequest:
		if message.RunID != nil || message.ReasoningContent != nil || message.ContextSummary != nil {
			return errors.New("input message has assistant-only fields")
		}
	case MessageAssistant:
		if message.RunID == nil || !canonicalUUIDv4.MatchString(*message.RunID) ||
			message.ReasoningContent != nil && (len(*message.ReasoningContent) > MaxReasoningBytes || !utf8.ValidString(*message.ReasoningContent) || strings.IndexByte(*message.ReasoningContent, 0) >= 0) ||
			message.ContextSummary != nil && (len(*message.ContextSummary) > MaxContextSummaryBytes || !utf8.ValidString(*message.ContextSummary) || strings.IndexByte(*message.ContextSummary, 0) >= 0) {
			return errors.New("assistant message fields are invalid")
		}
	default:
		return errors.New("message kind is invalid")
	}
	return nil
}

func validPersistedMessageContent(kind MessageKind, content string) bool {
	if len(content) < 1 || !utf8.ValidString(content) || strings.TrimSpace(content) == "" || strings.IndexByte(content, 0) >= 0 {
		return false
	}
	switch kind {
	case MessageUser:
		_, found, err := decodeReplyFrontendNotes(content)
		if found {
			return err == nil && len(content) <= MaxFrontendContextDocumentBytes
		}
		return len(content) <= MaxMessageBytes
	case MessageAutoAnalysisRequest:
		if len(content) > MaxFrontendContextDocumentBytes {
			return false
		}
		_, err := decodeAutoAnalysisInputContent(content)
		return err == nil
	case MessageAssistant:
		return len(content) <= MaxMessageBytes
	default:
		return false
	}
}

func validateRun(run Run) error {
	if !canonicalUUIDv4.MatchString(run.ID) || !canonicalUUIDv4.MatchString(run.ThreadID) || !canonicalUUIDv4.MatchString(run.ClientRequestID) ||
		!canonicalUUIDv4.MatchString(run.InputMessageID) || run.AttemptCount < 0 || run.CreatedAt.IsZero() || run.UpdatedAt.Before(run.CreatedAt) ||
		!run.CreatedAt.Equal(run.CreatedAt.UTC()) || !run.UpdatedAt.Equal(run.UpdatedAt.UTC()) {
		return errors.New("run violates its persisted contract")
	}
	if run.Kind != RunReply && run.Kind != RunAutoAnalysis {
		return errors.New("run kind is invalid")
	}
	switch run.Status {
	case RunQueued:
		if run.AttemptCount != 0 || run.OutputMessageID != nil || run.StartedAt != nil || run.FinishedAt != nil || run.ErrorCode != nil || run.ErrorDetail != nil {
			return errors.New("queued run state is invalid")
		}
	case RunRunning:
		if run.AttemptCount < 1 || run.OutputMessageID != nil || run.StartedAt == nil || run.FinishedAt != nil || run.ErrorCode != nil || run.ErrorDetail != nil {
			return errors.New("running run state is invalid")
		}
	case RunSucceeded:
		if run.AttemptCount < 1 || run.OutputMessageID == nil || !canonicalUUIDv4.MatchString(*run.OutputMessageID) || run.StartedAt == nil || run.FinishedAt == nil || run.ErrorCode != nil || run.ErrorDetail != nil {
			return errors.New("succeeded run state is invalid")
		}
	case RunFailed:
		if run.AttemptCount < 1 || run.OutputMessageID != nil || run.StartedAt == nil || run.FinishedAt == nil || run.ErrorCode == nil || !identifierPattern.MatchString(*run.ErrorCode) || run.ErrorDetail == nil || strings.TrimSpace(*run.ErrorDetail) == "" {
			return errors.New("failed run state is invalid")
		}
		if len(*run.ErrorDetail) > MaxFailureDetailBytes || !utf8.ValidString(*run.ErrorDetail) {
			return errors.New("failed run detail is invalid")
		}
	case RunSuperseded:
		if run.AttemptCount < 1 || run.OutputMessageID != nil || run.StartedAt == nil || run.FinishedAt == nil || run.ErrorCode != nil || run.ErrorDetail != nil {
			return errors.New("superseded run state is invalid")
		}
	default:
		return errors.New("run status is invalid")
	}
	return nil
}

func randomUUIDv4() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
