package recommendation

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type RecommendationReader interface {
	ReadCurrent(context.Context, auth.AccessPrincipal) (CurrentRecommendation, error)
}

type RecommendationQueue interface {
	QueueTraining(context.Context, QueueInput) (QueueResult, error)
}

type ReaderApplicationService struct {
	verifier AccessPrincipalVerifier
	reader   RecommendationReader
}

func NewReaderApplicationService(
	verifier AccessPrincipalVerifier,
	reader RecommendationReader,
) (*ReaderApplicationService, error) {
	if verifier == nil || reader == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation reader application service", errors.New("principal verifier and recommendation reader are required"))
	}
	return &ReaderApplicationService{verifier: verifier, reader: reader}, nil
}

func (application *ReaderApplicationService) ReadCurrent(ctx context.Context, token string) (CurrentRecommendation, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return CurrentRecommendation{}, err
	}
	return application.reader.ReadCurrent(ctx, principal)
}

type QueueApplicationService struct {
	verifier AccessPrincipalVerifier
	queue    RecommendationQueue
}

type AdminReader interface {
	ReadReviewContext(context.Context, auth.AccessPrincipal) (ReviewContext, error)
	ReadTrainingRun(context.Context, auth.AccessPrincipal, string) (TrainingRunDetail, bool, error)
	ReadTrainingEvents(context.Context, auth.AccessPrincipal, string, int64, int) (TrainingEventPage, bool, error)
}

type AdminReaderApplicationService struct {
	verifier AccessPrincipalVerifier
	reader   AdminReader
}

func NewAdminReaderApplicationService(verifier AccessPrincipalVerifier, reader AdminReader) (*AdminReaderApplicationService, error) {
	if verifier == nil || reader == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation admin reader application", errors.New("principal verifier and admin reader are required"))
	}
	return &AdminReaderApplicationService{verifier: verifier, reader: reader}, nil
}

func (application *AdminReaderApplicationService) ReadReviewContext(ctx context.Context, token string) (ReviewContext, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return ReviewContext{}, err
	}
	return application.reader.ReadReviewContext(ctx, principal)
}

func (application *AdminReaderApplicationService) ReadTrainingRun(ctx context.Context, token, runID string) (TrainingRunDetail, bool, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return TrainingRunDetail{}, false, err
	}
	return application.reader.ReadTrainingRun(ctx, principal, runID)
}

func (application *AdminReaderApplicationService) ReadTrainingEvents(ctx context.Context, token, runID string, afterSequence int64, limit int) (TrainingEventPage, bool, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return TrainingEventPage{}, false, err
	}
	return application.reader.ReadTrainingEvents(ctx, principal, runID, afterSequence, limit)
}

func NewQueueApplicationService(
	verifier AccessPrincipalVerifier,
	queue RecommendationQueue,
) (*QueueApplicationService, error) {
	if verifier == nil || queue == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation queue application service", errors.New("principal verifier and recommendation queue are required"))
	}
	return &QueueApplicationService{verifier: verifier, queue: queue}, nil
}

func (application *QueueApplicationService) QueueTraining(ctx context.Context, token, configurationKey string, expectedGenerationID, expectedHeadRevision int64) (QueueResult, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return QueueResult{}, err
	}
	return application.queue.QueueTraining(ctx, QueueInput{
		Principal: principal, ConfigurationKey: configurationKey,
		ExpectedAnalyticsGenerationID: expectedGenerationID, ExpectedAnalyticsHeadRevision: expectedHeadRevision,
	})
}
