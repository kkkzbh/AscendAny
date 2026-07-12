package examgeneration

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type Reader interface {
	GetCurrent(context.Context, CurrentQuery) (Generation, bool, error)
	ReadEvents(context.Context, EventQuery) (EventBatch, bool, error)
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	reader   Reader
}

func NewApplicationService(verifier AccessPrincipalVerifier, reader Reader) (*ApplicationService, error) {
	if verifier == nil || reader == nil {
		return nil, domainError(
			ErrorInvalidConfiguration,
			true,
			"construct exam generation application service",
			errors.New("principal verifier and generation reader are required"),
		)
	}
	return &ApplicationService{verifier: verifier, reader: reader}, nil
}

func (service *ApplicationService) GetCurrent(
	ctx context.Context,
	accessToken string,
	examID string,
) (Generation, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return Generation{}, false, err
	}
	return service.reader.GetCurrent(ctx, CurrentQuery{Principal: principal, ExamID: examID})
}

func (service *ApplicationService) ReadEvents(
	ctx context.Context,
	accessToken string,
	examID string,
	generationID string,
	afterSequence int64,
	limit int,
) (EventBatch, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return EventBatch{}, false, err
	}
	return service.reader.ReadEvents(ctx, EventQuery{
		Principal:     principal,
		ExamID:        examID,
		GenerationID:  generationID,
		AfterSequence: afterSequence,
		Limit:         limit,
	})
}
