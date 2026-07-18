package recommendation

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type ReaderRepository interface {
	ReadCurrent(context.Context, auth.AccessPrincipal) (CurrentRecommendation, error)
}

type currentRecommendationReadKey struct {
	accountID    string
	sessionID    string
	authRevision int64
	jwtID        string
}

type currentRecommendationReadCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  CurrentRecommendation
	err     error
}

type ReaderService struct {
	repository ReaderRepository
	mutex      sync.Mutex
	inFlight   map[currentRecommendationReadKey]*currentRecommendationReadCall
}

func NewReaderService(repository ReaderRepository) (*ReaderService, error) {
	if repository == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation reader service", errors.New("repository is required"))
	}
	return &ReaderService{
		repository: repository,
		inFlight:   make(map[currentRecommendationReadKey]*currentRecommendationReadCall),
	}, nil
}

func (service *ReaderService) ReadCurrent(ctx context.Context, principal auth.AccessPrincipal) (CurrentRecommendation, error) {
	if ctx == nil || principal.Role != auth.RoleStudent {
		return CurrentRecommendation{}, domainError(ErrorInvalidInput, true, "read current recommendation", errors.New("student principal and context are required"))
	}
	key := currentRecommendationKey(principal)
	service.mutex.Lock()
	call := service.inFlight[key]
	if call == nil {
		sharedContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
		call = &currentRecommendationReadCall{done: make(chan struct{}), cancel: cancel}
		service.inFlight[key] = call
		go service.runCurrentRecommendationRead(sharedContext, key, call, principal)
	}
	call.waiters++
	service.mutex.Unlock()

	select {
	case <-call.done:
		return cloneCurrentRecommendation(call.result), call.err
	case <-ctx.Done():
		service.releaseCurrentRecommendationWaiter(key, call)
		return CurrentRecommendation{}, ctx.Err()
	}
}

func cloneCurrentRecommendation(source CurrentRecommendation) CurrentRecommendation {
	result := source
	if source.UnavailableReason != nil {
		value := *source.UnavailableReason
		result.UnavailableReason = &value
	}
	if source.CurrentAnalyticsGenerationID != nil {
		value := *source.CurrentAnalyticsGenerationID
		result.CurrentAnalyticsGenerationID = &value
	}
	if source.Model != nil {
		value := *source.Model
		result.Model = &value
	}
	if source.Result != nil {
		value := *source.Result
		value.KnowledgeMastery = slices.Clone(source.Result.KnowledgeMastery)
		for index := range value.KnowledgeMastery {
			value.KnowledgeMastery[index].PrerequisiteIDs = slices.Clone(source.Result.KnowledgeMastery[index].PrerequisiteIDs)
		}
		value.LearningPath = slices.Clone(source.Result.LearningPath)
		for index := range value.LearningPath {
			value.LearningPath[index].PrerequisiteIDs = slices.Clone(source.Result.LearningPath[index].PrerequisiteIDs)
			value.LearningPath[index].RecommendedProblems = slices.Clone(source.Result.LearningPath[index].RecommendedProblems)
			for problemIndex := range value.LearningPath[index].RecommendedProblems {
				value.LearningPath[index].RecommendedProblems[problemIndex].SourceProblemSets = slices.Clone(
					source.Result.LearningPath[index].RecommendedProblems[problemIndex].SourceProblemSets,
				)
			}
		}
		if source.Result.Insufficiency != nil {
			insufficiency := *source.Result.Insufficiency
			insufficiency.BlockedKnowledgePointIDs = slices.Clone(source.Result.Insufficiency.BlockedKnowledgePointIDs)
			value.Insufficiency = &insufficiency
		}
		result.Result = &value
	}
	result.KnowledgeActivity = slices.Clone(source.KnowledgeActivity)
	for index := range result.KnowledgeActivity {
		if source.KnowledgeActivity[index].LastTriedAt != nil {
			value := *source.KnowledgeActivity[index].LastTriedAt
			result.KnowledgeActivity[index].LastTriedAt = &value
		}
		result.KnowledgeActivity[index].RecentSeries = slices.Clone(source.KnowledgeActivity[index].RecentSeries)
	}
	return result
}

func currentRecommendationKey(principal auth.AccessPrincipal) currentRecommendationReadKey {
	return currentRecommendationReadKey{
		accountID: principal.AccountID, sessionID: principal.SessionID,
		authRevision: principal.AuthRevision, jwtID: principal.JWTID,
	}
}

func (service *ReaderService) runCurrentRecommendationRead(
	ctx context.Context,
	key currentRecommendationReadKey,
	call *currentRecommendationReadCall,
	principal auth.AccessPrincipal,
) {
	result, err := service.repository.ReadCurrent(ctx, principal)
	call.cancel()
	service.mutex.Lock()
	call.result = result
	call.err = err
	if service.inFlight[key] == call {
		delete(service.inFlight, key)
	}
	close(call.done)
	service.mutex.Unlock()
}

func (service *ReaderService) releaseCurrentRecommendationWaiter(
	key currentRecommendationReadKey,
	call *currentRecommendationReadCall,
) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.inFlight[key] != call {
		return
	}
	call.waiters--
	if call.waiters == 0 {
		delete(service.inFlight, key)
		call.cancel()
	}
}

type AdminReaderRepository interface {
	ReadReviewContext(context.Context, auth.AccessPrincipal) (ReviewContext, error)
}

type AdminReaderService struct{ repository AdminReaderRepository }

func NewAdminReaderService(repository AdminReaderRepository) (*AdminReaderService, error) {
	if repository == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation admin reader service", errors.New("repository is required"))
	}
	return &AdminReaderService{repository: repository}, nil
}

func (service *AdminReaderService) ReadReviewContext(ctx context.Context, principal auth.AccessPrincipal) (ReviewContext, error) {
	if ctx == nil || principal.Role != auth.RoleAdmin {
		return ReviewContext{}, domainError(ErrorInvalidInput, true, "read recommendation review context", errors.New("administrator principal and context are required"))
	}
	return service.repository.ReadReviewContext(ctx, principal)
}
