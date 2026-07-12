package chatagent

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type ChatAgent interface {
	CreateThread(context.Context, auth.AccessPrincipal) (Thread, error)
	ListThreads(context.Context, ThreadQuery) (ThreadPage, error)
	ListMessages(context.Context, MessageQuery) ([]Message, error)
	GetRun(context.Context, RunQuery) (Run, bool, error)
	ReadRunEvents(context.Context, EventQuery) (RunEventBatch, error)
	Enqueue(context.Context, EnqueueInput) (EnqueueResult, error)
	EnqueueAutoAnalysis(context.Context, AutoAnalysisInput) (EnqueueResult, error)
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	chat     ChatAgent
}

func NewApplicationService(verifier AccessPrincipalVerifier, chat ChatAgent) (*ApplicationService, error) {
	if verifier == nil || chat == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent application service", errors.New("principal verifier and chat agent service are required"))
	}
	return &ApplicationService{verifier: verifier, chat: chat}, nil
}

func (service *ApplicationService) CreateThread(ctx context.Context, token string) (Thread, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return Thread{}, err
	}
	return service.chat.CreateThread(ctx, principal)
}

func (service *ApplicationService) ListThreads(ctx context.Context, token string, cursor *string, limit int) (ThreadPage, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return ThreadPage{}, err
	}
	return service.chat.ListThreads(ctx, ThreadQuery{Principal: principal, Cursor: cursor, Limit: limit})
}

func (service *ApplicationService) ListMessages(
	ctx context.Context,
	token string,
	threadID string,
	afterSequence int64,
	limit int,
) ([]Message, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return nil, err
	}
	return service.chat.ListMessages(ctx, MessageQuery{
		Principal: principal, ThreadID: threadID, AfterSequence: afterSequence, Limit: limit,
	})
}

func (service *ApplicationService) GetRun(ctx context.Context, token, runID string) (Run, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return Run{}, false, err
	}
	return service.chat.GetRun(ctx, RunQuery{Principal: principal, RunID: runID})
}

func (service *ApplicationService) ReadRunEvents(
	ctx context.Context,
	token string,
	runID string,
	afterSequence int64,
	limit int,
) (RunEventBatch, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return RunEventBatch{}, err
	}
	return service.chat.ReadRunEvents(ctx, EventQuery{
		Principal: principal, RunID: runID, AfterSequence: afterSequence, Limit: limit,
	})
}

func (service *ApplicationService) Enqueue(
	ctx context.Context,
	token string,
	threadID string,
	request EnqueueRequest,
) (EnqueueResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return EnqueueResult{}, err
	}
	return service.chat.Enqueue(ctx, EnqueueInput{
		Principal:                     principal,
		ThreadID:                      threadID,
		ClientRequestID:               request.ClientRequestID,
		Kind:                          request.Kind,
		Content:                       request.Content,
		PromptConfigurationKey:        request.PromptConfigurationKey,
		ModelConfigurationKey:         request.ModelConfigurationKey,
		ExpectedAnalyticsHeadRevision: request.ExpectedAnalyticsHeadRevision,
	})
}

func (service *ApplicationService) EnqueueAutoAnalysis(
	ctx context.Context,
	token string,
	request AutoAnalysisRequest,
) (EnqueueResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return EnqueueResult{}, err
	}
	return service.chat.EnqueueAutoAnalysis(ctx, AutoAnalysisInput{
		Principal:                     principal,
		PromptConfigurationKey:        request.PromptConfigurationKey,
		ModelConfigurationKey:         request.ModelConfigurationKey,
		ExpectedAnalyticsHeadRevision: request.ExpectedAnalyticsHeadRevision,
	})
}
