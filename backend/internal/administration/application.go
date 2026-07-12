package administration

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type Administration interface {
	ListAccounts(context.Context, AccountQuery) (AccountPage, error)
	ListStudents(context.Context, StudentQuery) (StudentPage, error)
	ListAudit(context.Context, AuditQuery) (AuditPage, error)
	SetAccountDisabled(context.Context, auth.AccessPrincipal, string, bool) (ManagedAccount, error)
}

type ApplicationService struct {
	verifier       AccessPrincipalVerifier
	administration Administration
}

func NewApplicationService(verifier AccessPrincipalVerifier, administration Administration) (*ApplicationService, error) {
	if verifier == nil || administration == nil {
		return nil, adminError(ErrorInvalidConfiguration, "construct administration application service", errors.New("principal verifier and administration service are required"))
	}
	return &ApplicationService{verifier: verifier, administration: administration}, nil
}

func (service *ApplicationService) ListAccounts(ctx context.Context, token string, cursor *string, limit int) (AccountPage, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return AccountPage{}, err
	}
	return service.administration.ListAccounts(ctx, AccountQuery{Principal: principal, Cursor: cursor, Limit: limit})
}

func (service *ApplicationService) ListStudents(ctx context.Context, token string, cursor *string, limit int) (StudentPage, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return StudentPage{}, err
	}
	return service.administration.ListStudents(ctx, StudentQuery{Principal: principal, Cursor: cursor, Limit: limit})
}

func (service *ApplicationService) ListAudit(ctx context.Context, token string, cursor *string, limit int) (AuditPage, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return AuditPage{}, err
	}
	return service.administration.ListAudit(ctx, AuditQuery{Principal: principal, Cursor: cursor, Limit: limit})
}

func (service *ApplicationService) SetAccountDisabled(ctx context.Context, token, targetID string, disabled bool) (ManagedAccount, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return ManagedAccount{}, err
	}
	return service.administration.SetAccountDisabled(ctx, principal, targetID, disabled)
}
