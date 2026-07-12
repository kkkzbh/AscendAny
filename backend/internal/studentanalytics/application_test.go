package studentanalytics

import (
	"context"
	"errors"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type principalVerifierStub struct {
	principal auth.AccessPrincipal
	err       error
	token     string
	calls     int
}

func (stub *principalVerifierStub) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	stub.calls++
	stub.token = token
	return stub.principal, stub.err
}

type selfReaderStub struct {
	result Result
	err    error
	query  SelfQuery
	calls  int
}

func (stub *selfReaderStub) GetSelf(_ context.Context, query SelfQuery) (Result, error) {
	stub.calls++
	stub.query = query
	return stub.result, stub.err
}

type leaderboardReaderStub struct {
	result LeaderboardResult
	err    error
	query  LeaderboardQuery
	calls  int
}

func (stub *leaderboardReaderStub) Get(
	_ context.Context,
	query LeaderboardQuery,
) (LeaderboardResult, error) {
	stub.calls++
	stub.query = query
	return stub.result, stub.err
}

func TestApplicationServicePassesExactSignedPrincipalToSnapshotReaders(t *testing.T) {
	t.Parallel()
	principal := auth.AccessPrincipal{
		AccountID:    testAccountID,
		SessionID:    testSessionID,
		Role:         auth.RoleStudent,
		AuthRevision: 7,
		JWTID:        "99999999-9999-4999-8999-999999999999",
	}
	verifier := &principalVerifierStub{principal: principal}
	self := &selfReaderStub{result: Result{State: StateNotGenerated}}
	leaderboard := &leaderboardReaderStub{result: LeaderboardResult{State: StateNotGenerated}}
	service, err := NewApplicationService(verifier, self, leaderboard)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.GetSelf(context.Background(), "access-token", 50); err != nil {
		t.Fatal(err)
	}
	wantSelf := SelfQuery{
		AccountID:            principal.AccountID,
		SessionID:            principal.SessionID,
		ExpectedAuthRevision: principal.AuthRevision,
		ExpectedRole:         principal.Role,
		HistoryLimit:         50,
	}
	if self.calls != 1 || self.query != wantSelf {
		t.Fatalf("self reader=%#v", self)
	}
	if _, err := service.GetLeaderboard(context.Background(), "access-token", 100); err != nil {
		t.Fatal(err)
	}
	wantLeaderboard := LeaderboardQuery{
		AccountID:            principal.AccountID,
		SessionID:            principal.SessionID,
		ExpectedAuthRevision: principal.AuthRevision,
		ExpectedRole:         principal.Role,
		Limit:                100,
	}
	if leaderboard.calls != 1 || leaderboard.query != wantLeaderboard || verifier.calls != 2 || verifier.token != "access-token" {
		t.Fatalf("verifier=%#v leaderboard=%#v", verifier, leaderboard)
	}
}

func TestApplicationServiceStopsBeforeDataReadWhenTokenVerificationFails(t *testing.T) {
	t.Parallel()
	rejected := errors.New("token rejected")
	verifier := &principalVerifierStub{err: rejected}
	self := &selfReaderStub{}
	leaderboard := &leaderboardReaderStub{}
	service, err := NewApplicationService(verifier, self, leaderboard)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.GetSelf(context.Background(), "bad", 50); !errors.Is(err, rejected) {
		t.Fatalf("GetSelf() error=%v", err)
	}
	if _, err := service.GetLeaderboard(context.Background(), "bad", 50); !errors.Is(err, rejected) {
		t.Fatalf("GetLeaderboard() error=%v", err)
	}
	if self.calls != 0 || leaderboard.calls != 0 {
		t.Fatalf("readers called after token rejection: self=%d leaderboard=%d", self.calls, leaderboard.calls)
	}
}

func TestApplicationServiceRequiresEveryOwner(t *testing.T) {
	t.Parallel()
	verifier := &principalVerifierStub{}
	self := &selfReaderStub{}
	leaderboard := &leaderboardReaderStub{}
	for _, dependencies := range []struct {
		verifier    AccessPrincipalVerifier
		self        SelfReader
		leaderboard LeaderboardReader
	}{
		{self: self, leaderboard: leaderboard},
		{verifier: verifier, leaderboard: leaderboard},
		{verifier: verifier, self: self},
	} {
		if _, err := NewApplicationService(dependencies.verifier, dependencies.self, dependencies.leaderboard); CodeOf(err) != ErrorInvalidConfiguration {
			t.Fatalf("dependencies=%#v error=%v", dependencies, err)
		}
	}
}
