package auth

import (
	"context"
	"errors"
	"testing"
)

type stubAccountAuthenticator struct {
	authenticated AuthenticatedAccount
	err           error
	token         string
	calls         int
}

func (stub *stubAccountAuthenticator) Authenticate(
	_ context.Context,
	token string,
) (AuthenticatedAccount, error) {
	stub.calls++
	stub.token = token
	return stub.authenticated, stub.err
}

type stubAccountManagement struct {
	updateAccount Account
	updateError   error
	sessions      []ManagedSession
	listError     error
	revokeError   error
	authenticated AuthenticatedAccount
	profile       ProfileUpdateInput
	targetID      string
	updateCalls   int
	listCalls     int
	revokeCalls   int
}

func (stub *stubAccountManagement) UpdateProfile(
	_ context.Context,
	authenticated AuthenticatedAccount,
	input ProfileUpdateInput,
) (Account, error) {
	stub.updateCalls++
	stub.authenticated = authenticated
	stub.profile = input
	return stub.updateAccount, stub.updateError
}

func (stub *stubAccountManagement) ListSessions(
	_ context.Context,
	authenticated AuthenticatedAccount,
) ([]ManagedSession, error) {
	stub.listCalls++
	stub.authenticated = authenticated
	return stub.sessions, stub.listError
}

func (stub *stubAccountManagement) RevokeSession(
	_ context.Context,
	authenticated AuthenticatedAccount,
	targetID string,
) error {
	stub.revokeCalls++
	stub.authenticated = authenticated
	stub.targetID = targetID
	return stub.revokeError
}

func TestAccountApplicationServiceOwnsAuthenticationBoundary(t *testing.T) {
	t.Parallel()
	authenticated := testAuthenticatedAccount()
	authenticator := &stubAccountAuthenticator{authenticated: authenticated}
	management := &stubAccountManagement{
		updateAccount: authenticated.Account,
		sessions:      []ManagedSession{{ID: authenticated.Principal.SessionID, Current: true}},
	}
	service, err := NewAccountApplicationService(authenticator, management)
	if err != nil {
		t.Fatal(err)
	}

	account, err := service.UpdateProfile(context.Background(), "access-token", ProfileUpdateInput{DisplayName: "New Name"})
	if err != nil || account.ID != authenticated.Account.ID || management.updateCalls != 1 ||
		management.authenticated != authenticated || management.profile.DisplayName != "New Name" {
		t.Fatalf("UpdateProfile() account=%#v management=%#v error=%v", account, management, err)
	}
	sessions, err := service.ListSessions(context.Background(), "access-token")
	if err != nil || len(sessions) != 1 || management.listCalls != 1 || management.authenticated != authenticated {
		t.Fatalf("ListSessions() sessions=%#v management=%#v error=%v", sessions, management, err)
	}
	current, err := service.RevokeSession(context.Background(), "access-token", testSessionID)
	if err != nil || !current ||
		management.revokeCalls != 1 || management.authenticated != authenticated || management.targetID != testSessionID {
		t.Fatalf("RevokeSession() management=%#v error=%v", management, err)
	}
	otherCurrent, err := service.RevokeSession(
		context.Background(),
		"access-token",
		"123e4567-e89b-42d3-a456-426614174088",
	)
	if err != nil || otherCurrent {
		t.Fatalf("RevokeSession(other) current=%t error=%v", otherCurrent, err)
	}
	if authenticator.calls != 4 || authenticator.token != "access-token" {
		t.Fatalf("authenticator=%#v", authenticator)
	}
}

func TestAccountApplicationServiceStopsOnAuthenticationFailure(t *testing.T) {
	t.Parallel()
	rejected := errors.New("rejected")
	authenticator := &stubAccountAuthenticator{err: rejected}
	management := &stubAccountManagement{}
	service, err := NewAccountApplicationService(authenticator, management)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.UpdateProfile(context.Background(), "bad", ProfileUpdateInput{DisplayName: "Name"}); !errors.Is(err, rejected) {
		t.Fatalf("UpdateProfile() error=%v", err)
	}
	if _, err := service.ListSessions(context.Background(), "bad"); !errors.Is(err, rejected) {
		t.Fatalf("ListSessions() error=%v", err)
	}
	if _, err := service.RevokeSession(context.Background(), "bad", testSessionID); !errors.Is(err, rejected) {
		t.Fatalf("RevokeSession() error=%v", err)
	}
	if management.updateCalls != 0 || management.listCalls != 0 || management.revokeCalls != 0 {
		t.Fatalf("management called after authentication failure: %#v", management)
	}
}

func TestAccountApplicationServiceRequiresBothOwners(t *testing.T) {
	t.Parallel()
	management := &stubAccountManagement{}
	authenticator := &stubAccountAuthenticator{}
	if _, err := NewAccountApplicationService(nil, management); ErrorCodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil authenticator error=%v", err)
	}
	if _, err := NewAccountApplicationService(authenticator, nil); ErrorCodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil management error=%v", err)
	}
}
