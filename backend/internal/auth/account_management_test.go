package auth

import (
	"context"
	"testing"
	"time"
)

type accountManagementRepositoryStub struct {
	updateResult UpdateProfileResult
	listResult   ListSessionsResult
	revokeStatus AccountMutationStatus
	err          error
	update       UpdateProfileCommand
	list         ListSessionsQuery
	revoke       RevokeSessionCommand
}

func (stub *accountManagementRepositoryStub) UpdateProfile(
	_ context.Context,
	command UpdateProfileCommand,
) (UpdateProfileResult, error) {
	stub.update = command
	return stub.updateResult, stub.err
}

func (stub *accountManagementRepositoryStub) ListSessions(
	_ context.Context,
	query ListSessionsQuery,
) (ListSessionsResult, error) {
	stub.list = query
	return stub.listResult, stub.err
}

func (stub *accountManagementRepositoryStub) RevokeSession(
	_ context.Context,
	command RevokeSessionCommand,
) (AccountMutationStatus, error) {
	stub.revoke = command
	return stub.revokeStatus, stub.err
}

func testAuthenticatedAccount() AuthenticatedAccount {
	studentNumber := "20260001"
	return AuthenticatedAccount{
		Account: Account{
			ID:            testAccountID,
			Username:      "student_1",
			DisplayName:   "Student One",
			StudentNumber: &studentNumber,
			Role:          RoleStudent,
			AuthRevision:  3,
		},
		Principal: AccessPrincipal{
			AccountID:    testAccountID,
			SessionID:    testSessionID,
			Role:         RoleStudent,
			AuthRevision: 3,
			JWTID:        "123e4567-e89b-42d3-a456-426614174099",
		},
	}
}

func TestAccountManagerUsesVerifiedPrincipalAndCanonicalProfileInput(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 4, 0, 0, 0, time.FixedZone("source", 8*60*60))
	authenticated := testAuthenticatedAccount()
	updated := authenticated.Account
	updated.DisplayName = "Updated Student"
	repository := &accountManagementRepositoryStub{
		updateResult: UpdateProfileResult{Status: AccountMutationApplied, Account: updated},
	}
	manager, err := NewAccountManager(repository, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}

	account, err := manager.UpdateProfile(
		context.Background(),
		authenticated,
		ProfileUpdateInput{DisplayName: "  Updated Student  "},
	)
	if err != nil || account.DisplayName != "Updated Student" {
		t.Fatalf("UpdateProfile() account=%#v error=%v", account, err)
	}
	if repository.update.DisplayName != "Updated Student" ||
		repository.update.Authenticated != authenticated ||
		repository.update.Now.Location() != time.UTC ||
		!repository.update.Now.Equal(now) {
		t.Fatalf("profile command is not canonical: %#v", repository.update)
	}
}

func TestAccountManagerMapsPrincipalAndTargetStatesWithoutFallback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 4, 0, 0, 0, time.UTC)
	authenticated := testAuthenticatedAccount()
	tests := []struct {
		name   string
		status AccountMutationStatus
		code   ErrorCode
	}{
		{name: "principal", status: AccountMutationPrincipalRejected, code: ErrorAuthentication},
		{name: "missing", status: AccountMutationTargetMissing, code: ErrorSessionNotFound},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &accountManagementRepositoryStub{revokeStatus: test.status}
			manager, err := NewAccountManager(repository, fixedClock{now: now})
			if err != nil {
				t.Fatal(err)
			}
			err = manager.RevokeSession(
				context.Background(),
				authenticated,
				"123e4567-e89b-42d3-a456-426614174088",
			)
			if ErrorCodeOf(err) != test.code {
				t.Fatalf("RevokeSession() error=%v code=%q", err, ErrorCodeOf(err))
			}
		})
	}
}

func TestAccountManagerListsBoundedSessionsAndRejectsIdentityMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 4, 0, 0, 0, time.UTC)
	authenticated := testAuthenticatedAccount()
	repository := &accountManagementRepositoryStub{
		listResult: ListSessionsResult{
			Status:   AccountMutationApplied,
			Sessions: []ManagedSession{{ID: testSessionID, Current: true, Active: true}},
		},
	}
	manager, err := NewAccountManager(repository, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background(), authenticated)
	if err != nil || len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("ListSessions() sessions=%#v error=%v", sessions, err)
	}
	if repository.list.Limit != MaxListedSessions || repository.list.Authenticated != authenticated {
		t.Fatalf("session-list query is not bound: %#v", repository.list)
	}

	authenticated.Principal.AuthRevision++
	if _, err := manager.ListSessions(context.Background(), authenticated); ErrorCodeOf(err) != ErrorInternal {
		t.Fatalf("identity mismatch error=%v code=%q", err, ErrorCodeOf(err))
	}
}

func TestAccountManagerRejectsInvalidSessionIDBeforeRepository(t *testing.T) {
	t.Parallel()
	repository := &accountManagementRepositoryStub{revokeStatus: AccountMutationApplied}
	manager, err := NewAccountManager(repository, fixedClock{now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.RevokeSession(context.Background(), testAuthenticatedAccount(), "not-a-uuid")
	if ErrorCodeOf(err) != ErrorInvalidInput || repository.revoke.TargetID != "" {
		t.Fatalf("invalid target reached repository: command=%#v error=%v", repository.revoke, err)
	}
}
