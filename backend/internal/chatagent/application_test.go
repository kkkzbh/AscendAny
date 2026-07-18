package chatagent

import (
	"context"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type applicationVerifierStub struct {
	principal auth.AccessPrincipal
	token     string
}

func (stub *applicationVerifierStub) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	stub.token = token
	return stub.principal, nil
}

type applicationChatStub struct {
	enqueue     EnqueueInput
	autoEnqueue AutoAnalysisInput
}

func (*applicationChatStub) CreateThread(context.Context, auth.AccessPrincipal) (Thread, error) {
	return Thread{}, nil
}
func (*applicationChatStub) ListThreads(context.Context, ThreadQuery) (ThreadPage, error) {
	return ThreadPage{}, nil
}
func (*applicationChatStub) ListMessages(context.Context, MessageQuery) ([]Message, error) {
	return nil, nil
}
func (*applicationChatStub) GetRun(context.Context, RunQuery) (Run, bool, error) {
	return Run{}, false, nil
}
func (*applicationChatStub) ReadRunEvents(context.Context, EventQuery) (RunEventBatch, error) {
	return RunEventBatch{}, nil
}
func (stub *applicationChatStub) Enqueue(_ context.Context, input EnqueueInput) (EnqueueResult, error) {
	stub.enqueue = input
	return EnqueueResult{}, nil
}
func (stub *applicationChatStub) EnqueueAutoAnalysis(_ context.Context, input AutoAnalysisInput) (EnqueueResult, error) {
	stub.autoEnqueue = input
	return EnqueueResult{}, nil
}

func TestApplicationServiceDerivesAgentOwnershipFromAccessToken(t *testing.T) {
	t.Parallel()
	verifier := &applicationVerifierStub{principal: validPrincipal()}
	chat := &applicationChatStub{}
	service, err := NewApplicationService(verifier, chat)
	if err != nil {
		t.Fatal(err)
	}
	request := EnqueueRequest{
		ClientRequestID: testRequestID, Kind: RunReply, Content: "Explain this result.",
		PromptConfigurationKey: "agent.prompt.default", ModelConfigurationKey: "agent.model.default",
		ExpectedAnalyticsHeadRevision: chatAgentTestInt64Pointer(9),
	}
	if _, err := service.Enqueue(context.Background(), "signed-access", testThreadID, request); err != nil {
		t.Fatal(err)
	}
	if verifier.token != "signed-access" || chat.enqueue.Principal != verifier.principal || chat.enqueue.ThreadID != testThreadID ||
		chat.enqueue.ClientRequestID != request.ClientRequestID || chat.enqueue.Content != request.Content ||
		chat.enqueue.ExpectedAnalyticsHeadRevision == nil || request.ExpectedAnalyticsHeadRevision == nil ||
		*chat.enqueue.ExpectedAnalyticsHeadRevision != *request.ExpectedAnalyticsHeadRevision {
		t.Fatalf("token=%q enqueue=%#v", verifier.token, chat.enqueue)
	}
}

func TestApplicationServiceDerivesAutomaticAnalysisOwnershipFromAccessToken(t *testing.T) {
	t.Parallel()
	verifier := &applicationVerifierStub{principal: validPrincipal()}
	chat := &applicationChatStub{}
	service, err := NewApplicationService(verifier, chat)
	if err != nil {
		t.Fatal(err)
	}
	request := AutoAnalysisRequest{
		PromptConfigurationKey: "agent.prompt.default", ModelConfigurationKey: "agent.model.default",
		ExpectedAnalyticsHeadRevision: 9,
		FrontendContext:               testAutoAnalysisFrontendContext(),
	}
	request.Identity = AutoAnalysisIdentity{ExamID: request.FrontendContext.LatestExamID, RoleID: request.FrontendContext.RoleID}
	if _, err := service.EnqueueAutoAnalysis(context.Background(), "signed-access", request); err != nil {
		t.Fatal(err)
	}
	if verifier.token != "signed-access" || chat.autoEnqueue.Principal != verifier.principal ||
		chat.autoEnqueue.PromptConfigurationKey != request.PromptConfigurationKey ||
		chat.autoEnqueue.ModelConfigurationKey != request.ModelConfigurationKey ||
		chat.autoEnqueue.ExpectedAnalyticsHeadRevision != request.ExpectedAnalyticsHeadRevision || chat.autoEnqueue.Identity != request.Identity ||
		chat.autoEnqueue.FrontendContext != request.FrontendContext {
		t.Fatalf("token=%q auto=%#v", verifier.token, chat.autoEnqueue)
	}
}
