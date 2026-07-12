package studentanalytics

import (
	"context"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type fakeLeaderboardRepository struct {
	result LeaderboardResult
	err    error
	calls  int
	query  LeaderboardQuery
}

func (repository *fakeLeaderboardRepository) LoadLeaderboard(
	_ context.Context,
	query LeaderboardQuery,
) (LeaderboardResult, error) {
	repository.calls++
	repository.query = query
	return repository.result, repository.err
}

func TestLeaderboardServiceValidatesViewerAndLimitBeforeRepository(t *testing.T) {
	t.Parallel()
	valid := LeaderboardQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 3,
		ExpectedRole:         auth.RoleStudent,
		Limit:                50,
	}
	tests := []struct {
		name  string
		ctx   context.Context
		query LeaderboardQuery
		code  ErrorCode
	}{
		{name: "nil context", query: valid, code: ErrorInvalidQuery},
		{name: "account", ctx: context.Background(), query: withLeaderboardQuery(valid, func(query *LeaderboardQuery) { query.AccountID = "invalid" }), code: ErrorInvalidQuery},
		{name: "session", ctx: context.Background(), query: withLeaderboardQuery(valid, func(query *LeaderboardQuery) { query.SessionID = "invalid" }), code: ErrorInvalidQuery},
		{name: "revision", ctx: context.Background(), query: withLeaderboardQuery(valid, func(query *LeaderboardQuery) { query.ExpectedAuthRevision = 0 }), code: ErrorInvalidQuery},
		{name: "role", ctx: context.Background(), query: withLeaderboardQuery(valid, func(query *LeaderboardQuery) { query.ExpectedRole = auth.RoleAdmin }), code: ErrorForbidden},
		{name: "zero limit", ctx: context.Background(), query: withLeaderboardQuery(valid, func(query *LeaderboardQuery) { query.Limit = 0 }), code: ErrorInvalidQuery},
		{name: "large limit", ctx: context.Background(), query: withLeaderboardQuery(valid, func(query *LeaderboardQuery) { query.Limit = MaxLeaderboardLimit + 1 }), code: ErrorInvalidQuery},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeLeaderboardRepository{}
			service, err := NewLeaderboardService(repository)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Get(test.ctx, test.query)
			if CodeOf(err) != test.code || repository.calls != 0 {
				t.Fatalf("Get() error=%v code=%q calls=%d", err, CodeOf(err), repository.calls)
			}
		})
	}
}

func TestLeaderboardServiceReturnsOnlyCanonicalRepositoryShapes(t *testing.T) {
	t.Parallel()
	query := LeaderboardQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 3,
		ExpectedRole:         auth.RoleStudent,
		Limit:                2,
	}
	result := validLeaderboardResult()
	repository := &fakeLeaderboardRepository{result: result}
	service, err := NewLeaderboardService(repository)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), query)
	if err != nil || got.State != StateReady || repository.calls != 1 || repository.query != query {
		t.Fatalf("Get() result=%#v repository=%#v error=%v", got, repository, err)
	}

	invalid := validLeaderboardResult()
	invalid.Items[1].Rank = 2
	repository.result = invalid
	if _, err := service.Get(context.Background(), query); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("broken tie error=%v code=%q", err, CodeOf(err))
	}
}

func TestLeaderboardServiceAcceptsExplicitEmptyStates(t *testing.T) {
	t.Parallel()
	query := LeaderboardQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 3,
		ExpectedRole:         auth.RoleStudent,
		Limit:                2,
	}
	for _, result := range []LeaderboardResult{
		{State: StateNotGenerated},
		{State: StateNoObservations, HeadRevision: 2},
	} {
		repository := &fakeLeaderboardRepository{result: result}
		service, err := NewLeaderboardService(repository)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := service.Get(context.Background(), query); err != nil || got.State != result.State {
			t.Fatalf("Get() result=%#v error=%v", got, err)
		}
	}
}

func withLeaderboardQuery(value LeaderboardQuery, change func(*LeaderboardQuery)) LeaderboardQuery {
	change(&value)
	return value
}

func validLeaderboardResult() LeaderboardResult {
	firstName := "Student One"
	secondName := "Student Two"
	return LeaderboardResult{
		State:        StateReady,
		HeadRevision: 2,
		Population:   3,
		Items: []LeaderboardItem{
			{Rank: 1, StudentNumber: "20260001", DisplayName: &firstName, Rating: 1506},
			{Rank: 1, StudentNumber: "20260002", DisplayName: &secondName, Rating: 1506},
		},
	}
}
