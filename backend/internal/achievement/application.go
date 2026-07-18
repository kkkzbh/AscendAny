package achievement

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type Reader interface {
	GetSelf(context.Context, SelfQuery) (Result, error)
	GetByStudentNumber(context.Context, StudentNumberQuery) (Result, error)
	GetByStudentIdentity(context.Context, StudentIdentityQuery) (Result, error)
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	reader   Reader
}

func NewApplicationService(verifier AccessPrincipalVerifier, reader Reader) (*ApplicationService, error) {
	if verifier == nil || reader == nil {
		return nil, achievementError(
			ErrorInvalidConfiguration,
			"construct achievement application service",
			errors.New("principal verifier and achievement reader are required"),
		)
	}
	return &ApplicationService{verifier: verifier, reader: reader}, nil
}

func (service *ApplicationService) GetSelf(ctx context.Context, accessToken string) (Result, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return Result{}, err
	}
	return service.reader.GetSelf(ctx, SelfQuery{Principal: principal})
}

func (service *ApplicationService) GetByStudentNumber(ctx context.Context, studentNumber string) (Result, error) {
	return service.reader.GetByStudentNumber(ctx, StudentNumberQuery{StudentNumber: studentNumber})
}

func (service *ApplicationService) GetByStudentIdentity(ctx context.Context, studentNumber, ptaNickname string) (Result, error) {
	return service.reader.GetByStudentIdentity(ctx, StudentIdentityQuery{
		StudentNumber: studentNumber,
		PTANickname:   ptaNickname,
	})
}
