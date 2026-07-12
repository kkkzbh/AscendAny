package examcatalog

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type Catalog interface {
	List(context.Context, ListQuery) (Page, error)
	Get(context.Context, DetailQuery) (Detail, bool, error)
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	catalog  Catalog
}

func NewApplicationService(verifier AccessPrincipalVerifier, catalog Catalog) (*ApplicationService, error) {
	if verifier == nil || catalog == nil {
		return nil, catalogError(ErrorInvalidConfiguration, "construct exam catalog application service", errors.New("principal verifier and catalog are required"))
	}
	return &ApplicationService{verifier: verifier, catalog: catalog}, nil
}

func (service *ApplicationService) List(
	ctx context.Context,
	accessToken string,
	cursor *string,
	limit int,
) (Page, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return Page{}, err
	}
	return service.catalog.List(ctx, ListQuery{Principal: principal, Cursor: cursor, Limit: limit})
}

func (service *ApplicationService) Get(
	ctx context.Context,
	accessToken string,
	examID string,
) (Detail, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return Detail{}, false, err
	}
	return service.catalog.Get(ctx, DetailQuery{Principal: principal, ExamID: examID})
}
