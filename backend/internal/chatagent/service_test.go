package chatagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func chatAgentTestInt64Pointer(value int64) *int64 { return &value }

const (
	testAccountID = "11111111-1111-4111-8111-111111111111"
	testSessionID = "22222222-2222-4222-8222-222222222222"
	testJWTID     = "33333333-3333-4333-8333-333333333333"
	testThreadID  = "44444444-4444-4444-8444-444444444444"
	testRequestID = "55555555-5555-4555-8555-555555555555"
	testRunID     = "66666666-6666-4666-8666-666666666666"
	testMessageID = "77777777-7777-4777-8777-777777777777"
)

type repositoryStub struct {
	command     EnqueueCommand
	result      EnqueueResult
	err         error
	threadQuery ThreadQuery
	threads     []Thread
	eventBatch  RunEventBatch
	eventErr    error
	autoCommand AutoAnalysisCommand
	autoResult  EnqueueResult
}

func (*repositoryStub) CreateThread(context.Context, CreateThreadCommand) (Thread, error) {
	return Thread{}, nil
}
func (repository *repositoryStub) ListThreads(_ context.Context, query ThreadQuery) ([]Thread, error) {
	repository.threadQuery = query
	return repository.threads, repository.err
}
func (*repositoryStub) ListMessages(context.Context, MessageQuery) ([]Message, error) {
	return nil, nil
}
func (*repositoryStub) GetRun(context.Context, RunQuery) (Run, bool, error) { return Run{}, false, nil }
func (repository *repositoryStub) ReadRunEvents(context.Context, EventQuery) (RunEventBatch, error) {
	return repository.eventBatch, repository.eventErr
}
func (repository *repositoryStub) Enqueue(_ context.Context, command EnqueueCommand) (EnqueueResult, error) {
	repository.command = command
	return repository.result, repository.err
}
func (repository *repositoryStub) EnqueueAutoAnalysis(_ context.Context, command AutoAnalysisCommand) (EnqueueResult, error) {
	repository.autoCommand = command
	return repository.autoResult, repository.err
}

func TestServiceEnqueueOwnsGeneratedIDsAndRequestValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	repository := &repositoryStub{result: EnqueueResult{
		Run: Run{
			ID: testRunID, ThreadID: testThreadID, ClientRequestID: testRequestID, Kind: RunReply,
			InputMessageID: testMessageID, Status: RunQueued, CreatedAt: now, UpdatedAt: now,
		},
		Message: Message{
			ID: testMessageID, ThreadID: testThreadID, Sequence: 1, Kind: MessageUser,
			Content: "Explain this result.", CreatedAt: now,
		},
		Created: true,
	}}
	identifiers := []string{testRunID, testMessageID}
	service, err := newService(repository, func() (string, error) {
		value := identifiers[0]
		identifiers = identifiers[1:]
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	input := validEnqueueInput()
	result, err := service.Enqueue(context.Background(), input)
	if err != nil || !result.Created {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if repository.command.RunID != testRunID || repository.command.MessageID != testMessageID || repository.command.EnqueueInput != input {
		t.Fatalf("command=%#v", repository.command)
	}
}

func TestServiceRequiresPositiveAnalyticsRevisionForReply(t *testing.T) {
	t.Parallel()
	service, err := NewService(&repositoryStub{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*EnqueueInput){
		"zero analytics revision": func(input *EnqueueInput) {
			input.ExpectedAnalyticsHeadRevision = chatAgentTestInt64Pointer(0)
		},
		"automatic analysis kind": func(input *EnqueueInput) {
			input.Kind = RunAutoAnalysis
		},
		"admin principal": func(input *EnqueueInput) {
			input.Principal.Role = auth.RoleAdmin
		},
		"nul content": func(input *EnqueueInput) {
			input.Content = "invalid\x00content"
		},
	} {
		name := name
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := validEnqueueInput()
			mutate(&input)
			if _, err := service.Enqueue(context.Background(), input); CodeOf(err) != ErrorInvalidInput && CodeOf(err) != ErrorPrincipalRejected {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
		})
	}
}

func TestServiceOwnsAllAutomaticAnalysisIdentityAndContent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	autoThreadID := "88888888-8888-4888-8888-888888888888"
	autoClientRequestID := "99999999-9999-4999-8999-999999999999"
	frontendContext := testAutoAnalysisFrontendContext()
	inputContent := mustAutoAnalysisInputContent(t, frontendContext)
	repository := &repositoryStub{autoResult: EnqueueResult{
		Run: Run{
			ID: testRunID, ThreadID: autoThreadID, ClientRequestID: autoClientRequestID, Kind: RunAutoAnalysis,
			InputMessageID: testMessageID, Status: RunQueued, CreatedAt: now, UpdatedAt: now,
		},
		Message: Message{
			ID: testMessageID, ThreadID: autoThreadID, Sequence: 1, Kind: MessageAutoAnalysisRequest,
			Content: inputContent, CreatedAt: now,
		},
		Created: true,
	}}
	identifiers := []string{autoThreadID, testRunID, testMessageID, autoClientRequestID}
	service, err := newService(repository, func() (string, error) {
		value := identifiers[0]
		identifiers = identifiers[1:]
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	input := AutoAnalysisInput{
		Principal: validPrincipal(), PromptConfigurationKey: "agent.prompt.default",
		ModelConfigurationKey: "agent.model.default", ExpectedAnalyticsHeadRevision: 7,
		Identity: AutoAnalysisIdentity{ExamID: frontendContext.LatestExamID, RoleID: frontendContext.RoleID}, FrontendContext: frontendContext,
	}
	result, err := service.EnqueueAutoAnalysis(context.Background(), input)
	if err != nil || !result.Created || result.AutoAnalysisContext == nil || *result.AutoAnalysisContext != frontendContext {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if repository.autoCommand.ThreadID != autoThreadID || repository.autoCommand.RunID != testRunID ||
		repository.autoCommand.MessageID != testMessageID || repository.autoCommand.ClientRequestID != autoClientRequestID ||
		repository.autoCommand.AutoAnalysisInput != input {
		t.Fatalf("command=%#v", repository.autoCommand)
	}
}

func TestServiceAcceptsStoredAutomaticAnalysisReplayWithChangedPresentationContext(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	storedContext := testAutoAnalysisFrontendContext()
	storedContent := mustAutoAnalysisInputContent(t, storedContext)
	autoThreadID := "88888888-8888-4888-8888-888888888888"
	autoClientRequestID := "99999999-9999-4999-8999-999999999999"
	repository := &repositoryStub{autoResult: EnqueueResult{
		Run: Run{
			ID: testRunID, ThreadID: autoThreadID, ClientRequestID: autoClientRequestID, Kind: RunAutoAnalysis,
			InputMessageID: testMessageID, Status: RunQueued, CreatedAt: now, UpdatedAt: now,
		},
		Message: Message{
			ID: testMessageID, ThreadID: autoThreadID, Sequence: 1, Kind: MessageAutoAnalysisRequest,
			Content: storedContent, CreatedAt: now,
		},
		Created: false,
	}}
	identifiers := []string{autoThreadID, testRunID, testMessageID, autoClientRequestID}
	service, err := newService(repository, func() (string, error) {
		value := identifiers[0]
		identifiers = identifiers[1:]
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	changedContext := storedContext
	changedContext.RoleName = "Changed presentation"
	changedContext.RoleSystemPrompt = "Changed prompt presentation."
	changedContext.Notes = "Changed local notes."
	input := AutoAnalysisInput{
		Principal: validPrincipal(), PromptConfigurationKey: "agent.prompt.changed",
		ModelConfigurationKey: "agent.model.changed", ExpectedAnalyticsHeadRevision: 99,
		Identity:        AutoAnalysisIdentity{ExamID: storedContext.LatestExamID, RoleID: storedContext.RoleID},
		FrontendContext: changedContext,
	}
	result, err := service.EnqueueAutoAnalysis(context.Background(), input)
	if err != nil || result.Created || result.Message.Content != storedContent ||
		result.AutoAnalysisContext == nil || *result.AutoAnalysisContext != storedContext {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestServiceRejectsInvalidAutomaticAnalysisInputBeforeGeneratingIDs(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service, err := newService(repository, func() (string, error) {
		t.Fatal("UUID generator was called")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := AutoAnalysisInput{
		Principal: validPrincipal(), PromptConfigurationKey: "agent.prompt.default",
		ModelConfigurationKey: "agent.model.default", ExpectedAnalyticsHeadRevision: 1,
		Identity:        AutoAnalysisIdentity{ExamID: "99999999-9999-4999-8999-999999999999", RoleID: "role-7"},
		FrontendContext: testAutoAnalysisFrontendContext(),
	}
	for name, mutate := range map[string]func(*AutoAnalysisInput){
		"zero revision":  func(input *AutoAnalysisInput) { input.ExpectedAnalyticsHeadRevision = 0 },
		"invalid prompt": func(input *AutoAnalysisInput) { input.PromptConfigurationKey = "UPPER" },
		"invalid model":  func(input *AutoAnalysisInput) { input.ModelConfigurationKey = "" },
		"invalid identity": func(input *AutoAnalysisInput) {
			input.Identity.ExamID = "exam-9"
		},
		"identity context mismatch": func(input *AutoAnalysisInput) {
			input.FrontendContext.RoleID = "other-role"
		},
		"invalid context": func(input *AutoAnalysisInput) {
			input.FrontendContext.Notes = strings.Repeat("n", MaxMessageBytes)
			input.FrontendContext.RoleSystemPrompt = "x"
		},
		"admin": func(input *AutoAnalysisInput) { input.Principal.Role = auth.RoleAdmin },
	} {
		name := name
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := valid
			mutate(&input)
			if _, err := service.EnqueueAutoAnalysis(context.Background(), input); CodeOf(err) != ErrorInvalidInput && CodeOf(err) != ErrorPrincipalRejected {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
		})
	}
}

func TestServiceBuildsExactThreadLookaheadPage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	thirdID := "88888888-8888-4888-8888-888888888888"
	repository := &repositoryStub{threads: []Thread{
		{ID: testThreadID, Kind: ThreadConversation, CreatedAt: now, UpdatedAt: now},
		{ID: testRunID, Kind: ThreadConversation, CreatedAt: now, UpdatedAt: now},
		{ID: thirdID, Kind: ThreadAutoAnalysis, CreatedAt: now, UpdatedAt: now},
	}}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListThreads(context.Background(), ThreadQuery{Principal: validPrincipal(), Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil || *page.NextCursor != testRunID || repository.threadQuery.Limit != 2 {
		t.Fatalf("page=%#v query=%#v error=%v", page, repository.threadQuery, err)
	}
	invalid := "NOT-A-UUID"
	if _, err := service.ListThreads(context.Background(), ThreadQuery{Principal: validPrincipal(), Cursor: &invalid, Limit: 2}); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("invalid cursor error=%v code=%q", err, CodeOf(err))
	}
}

func TestServiceValidatesClosedRunEventBatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	repository := &repositoryStub{eventBatch: RunEventBatch{
		Events: []RunEvent{
			{Sequence: 1, Type: "queued", Payload: []byte(`{"status":"queued"}`), CreatedAt: now},
			{Sequence: 2, Type: "completed", Payload: []byte(`{"status":"completed"}`), CreatedAt: now},
		},
		LastSequence: 2,
		Terminal:     true,
	}}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.ReadRunEvents(context.Background(), EventQuery{
		Principal: validPrincipal(), RunID: testRunID, Limit: 2,
	})
	if err != nil || len(batch.Events) != 2 || !batch.Terminal {
		t.Fatalf("batch=%#v error=%v", batch, err)
	}
	repository.eventBatch.Events[1].Sequence = 3
	if _, err := service.ReadRunEvents(context.Background(), EventQuery{
		Principal: validPrincipal(), RunID: testRunID, Limit: 2,
	}); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("gapped event error=%v code=%q", err, CodeOf(err))
	}
}

func validPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 1,
	}
}

func validEnqueueInput() EnqueueInput {
	return EnqueueInput{
		Principal: validPrincipal(), ThreadID: testThreadID, ClientRequestID: testRequestID,
		Kind: RunReply, Content: "Explain this result.", PromptConfigurationKey: "agent.prompt.default",
		ModelConfigurationKey: "agent.model.default", ExpectedAnalyticsHeadRevision: chatAgentTestInt64Pointer(7),
	}
}
