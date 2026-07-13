package recommendation

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type ReaderRepository interface {
	ReadCurrent(context.Context, auth.AccessPrincipal) (CurrentRecommendation, error)
}

type ReaderService struct{ repository ReaderRepository }

func NewReaderService(repository ReaderRepository) (*ReaderService, error) {
	if repository == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation reader service", errors.New("repository is required"))
	}
	return &ReaderService{repository: repository}, nil
}

func (service *ReaderService) ReadCurrent(ctx context.Context, principal auth.AccessPrincipal) (CurrentRecommendation, error) {
	if ctx == nil || principal.Role != auth.RoleStudent {
		return CurrentRecommendation{}, domainError(ErrorInvalidInput, true, "read current recommendation", errors.New("student principal and context are required"))
	}
	return service.repository.ReadCurrent(ctx, principal)
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
