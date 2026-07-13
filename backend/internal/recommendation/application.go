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

type ReaderApplicationService struct {
	verifier AccessPrincipalVerifier
	reader   RecommendationReader
}

func NewReaderApplicationService(verifier AccessPrincipalVerifier, reader RecommendationReader) (*ReaderApplicationService, error) {
	if verifier == nil || reader == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation reader application service", errors.New("principal verifier and reader are required"))
	}
	return &ReaderApplicationService{verifier: verifier, reader: reader}, nil
}

func (service *ReaderApplicationService) ReadCurrent(ctx context.Context, token string) (CurrentRecommendation, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return CurrentRecommendation{}, err
	}
	return service.reader.ReadCurrent(ctx, principal)
}

type AdminReader interface {
	ReadReviewContext(context.Context, auth.AccessPrincipal) (ReviewContext, error)
}

type AdminReaderApplicationService struct {
	verifier AccessPrincipalVerifier
	reader   AdminReader
}

func NewAdminReaderApplicationService(verifier AccessPrincipalVerifier, reader AdminReader) (*AdminReaderApplicationService, error) {
	if verifier == nil || reader == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation admin reader application service", errors.New("principal verifier and admin reader are required"))
	}
	return &AdminReaderApplicationService{verifier: verifier, reader: reader}, nil
}

func (service *AdminReaderApplicationService) ReadReviewContext(ctx context.Context, token string) (ReviewContext, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return ReviewContext{}, err
	}
	return service.reader.ReadReviewContext(ctx, principal)
}
