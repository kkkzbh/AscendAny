package auth

import "context"

// AccountAuthenticator is the database-revalidated access-token boundary used
// by account application operations.
type AccountAuthenticator interface {
	Authenticate(context.Context, string) (AuthenticatedAccount, error)
}

type AccountManagement interface {
	UpdateProfile(context.Context, AuthenticatedAccount, ProfileUpdateInput) (Account, error)
	ListSessions(context.Context, AuthenticatedAccount) ([]ManagedSession, error)
	RevokeSession(context.Context, AuthenticatedAccount, string) error
}

// AccountApplicationService owns the complete authenticate-then-mutate flow so
// HTTP handlers never assemble or pass caller-controlled principal fields.
type AccountApplicationService struct {
	authenticator AccountAuthenticator
	management    AccountManagement
}

func NewAccountApplicationService(
	authenticator AccountAuthenticator,
	management AccountManagement,
) (*AccountApplicationService, error) {
	if authenticator == nil {
		return nil, authError(ErrorInvalidConfiguration, "Account authenticator is required.", nil)
	}
	if management == nil {
		return nil, authError(ErrorInvalidConfiguration, "Account management service is required.", nil)
	}
	return &AccountApplicationService{authenticator: authenticator, management: management}, nil
}

func (service *AccountApplicationService) UpdateProfile(
	ctx context.Context,
	accessToken string,
	input ProfileUpdateInput,
) (Account, error) {
	authenticated, err := service.authenticator.Authenticate(ctx, accessToken)
	if err != nil {
		return Account{}, err
	}
	return service.management.UpdateProfile(ctx, authenticated, input)
}

func (service *AccountApplicationService) ListSessions(
	ctx context.Context,
	accessToken string,
) ([]ManagedSession, error) {
	authenticated, err := service.authenticator.Authenticate(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return service.management.ListSessions(ctx, authenticated)
}

func (service *AccountApplicationService) RevokeSession(
	ctx context.Context,
	accessToken string,
	targetID string,
) (bool, error) {
	authenticated, err := service.authenticator.Authenticate(ctx, accessToken)
	if err != nil {
		return false, err
	}
	if err := service.management.RevokeSession(ctx, authenticated, targetID); err != nil {
		return false, err
	}
	return authenticated.Principal.SessionID == targetID, nil
}
