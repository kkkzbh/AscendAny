package auth

import (
	"context"
	"errors"
	"time"
)

const MaxListedSessions = 100

type ProfileUpdateInput struct {
	DisplayName string
}

type ManagedSession struct {
	ID               string     `json:"id"`
	CreatedAt        time.Time  `json:"createdAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	LastSeenAt       time.Time  `json:"lastSeenAt"`
	RevokedAt        *time.Time `json:"revokedAt"`
	RevocationReason *string    `json:"revocationReason"`
	Current          bool       `json:"current"`
	Active           bool       `json:"active"`
}

type UpdateProfileCommand struct {
	Authenticated AuthenticatedAccount
	DisplayName   string
	Now           time.Time
}

type ListSessionsQuery struct {
	Authenticated AuthenticatedAccount
	Now           time.Time
	Limit         int
}

type RevokeSessionCommand struct {
	Authenticated AuthenticatedAccount
	TargetID      string
	Now           time.Time
}

type AccountMutationStatus uint8

const (
	AccountMutationApplied AccountMutationStatus = iota + 1
	AccountMutationPrincipalRejected
	AccountMutationTargetMissing
)

type UpdateProfileResult struct {
	Status  AccountMutationStatus
	Account Account
}

type ListSessionsResult struct {
	Status   AccountMutationStatus
	Sessions []ManagedSession
}

type AccountManagementRepository interface {
	UpdateProfile(context.Context, UpdateProfileCommand) (UpdateProfileResult, error)
	ListSessions(context.Context, ListSessionsQuery) (ListSessionsResult, error)
	RevokeSession(context.Context, RevokeSessionCommand) (AccountMutationStatus, error)
}

type AccountManager struct {
	repository AccountManagementRepository
	clock      Clock
}

func NewAccountManager(repository AccountManagementRepository, clock Clock) (*AccountManager, error) {
	if repository == nil {
		return nil, authError(ErrorInvalidConfiguration, "Account-management repository is required.", nil)
	}
	if clock == nil {
		return nil, authError(ErrorInvalidConfiguration, "Account-management clock is required.", nil)
	}
	return &AccountManager{repository: repository, clock: clock}, nil
}

func NewProductionAccountManager(repository AccountManagementRepository) (*AccountManager, error) {
	return NewAccountManager(repository, systemClock{})
}

func (manager *AccountManager) UpdateProfile(
	ctx context.Context,
	authenticated AuthenticatedAccount,
	input ProfileUpdateInput,
) (Account, error) {
	if err := validateAuthenticatedAccount(authenticated); err != nil {
		return Account{}, err
	}
	displayName, err := validateTrimmedField("Display name", input.DisplayName, MinDisplayNameBytes, MaxDisplayNameBytes)
	if err != nil {
		return Account{}, err
	}
	result, err := manager.repository.UpdateProfile(ctx, UpdateProfileCommand{
		Authenticated: authenticated,
		DisplayName:   displayName,
		Now:           manager.clock.Now().UTC(),
	})
	if err != nil {
		return Account{}, err
	}
	switch result.Status {
	case AccountMutationApplied:
		return result.Account, nil
	case AccountMutationPrincipalRejected:
		return Account{}, authenticationRejected(nil)
	default:
		return Account{}, authError(ErrorInternal, "Profile update returned an invalid state.", nil)
	}
}

func (manager *AccountManager) ListSessions(
	ctx context.Context,
	authenticated AuthenticatedAccount,
) ([]ManagedSession, error) {
	if err := validateAuthenticatedAccount(authenticated); err != nil {
		return nil, err
	}
	result, err := manager.repository.ListSessions(ctx, ListSessionsQuery{
		Authenticated: authenticated,
		Now:           manager.clock.Now().UTC(),
		Limit:         MaxListedSessions,
	})
	if err != nil {
		return nil, err
	}
	if result.Status == AccountMutationPrincipalRejected {
		return nil, authenticationRejected(nil)
	}
	if result.Status != AccountMutationApplied {
		return nil, authError(ErrorInternal, "Session list returned an invalid state.", nil)
	}
	return result.Sessions, nil
}

func (manager *AccountManager) RevokeSession(
	ctx context.Context,
	authenticated AuthenticatedAccount,
	targetID string,
) error {
	if err := validateAuthenticatedAccount(authenticated); err != nil {
		return err
	}
	if _, err := parseUUIDv4(targetID); err != nil {
		return authError(ErrorInvalidInput, "Session ID must be a canonical UUIDv4.", err)
	}
	status, err := manager.repository.RevokeSession(ctx, RevokeSessionCommand{
		Authenticated: authenticated,
		TargetID:      targetID,
		Now:           manager.clock.Now().UTC(),
	})
	if err != nil {
		return err
	}
	switch status {
	case AccountMutationApplied:
		return nil
	case AccountMutationPrincipalRejected:
		return authenticationRejected(nil)
	case AccountMutationTargetMissing:
		return sessionNotFound()
	default:
		return authError(ErrorInternal, "Session revocation returned an invalid state.", nil)
	}
}

func validateAuthenticatedAccount(authenticated AuthenticatedAccount) error {
	account := authenticated.Account
	principal := authenticated.Principal
	if _, err := parseUUIDv4(account.ID); err != nil {
		return authError(ErrorInternal, "Authenticated account ID is invalid.", err)
	}
	if _, err := parseUUIDv4(principal.SessionID); err != nil {
		return authError(ErrorInternal, "Authenticated session ID is invalid.", err)
	}
	if _, err := parseUUIDv4(principal.JWTID); err != nil {
		return authError(ErrorInternal, "Authenticated JWT ID is invalid.", err)
	}
	if principal.AccountID != account.ID || principal.Role != account.Role ||
		principal.AuthRevision != account.AuthRevision || !validRole(account.Role) || account.AuthRevision < 1 {
		return authError(ErrorInternal, "Authenticated account and principal differ.", errors.New("identity binding mismatch"))
	}
	return nil
}
