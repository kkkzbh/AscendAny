package studentanalytics

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID = "11111111-1111-4111-8111-111111111111"
	testSessionID = "77777777-7777-4777-8777-777777777777"
)

type fakeRepository struct {
	result Result
	err    error
	calls  int
	query  SelfQuery
}

func (repository *fakeRepository) LoadSelf(_ context.Context, query SelfQuery) (Result, error) {
	repository.calls++
	repository.query = query
	return repository.result, repository.err
}

func TestServiceValidatesPrincipalInputBeforeRepository(t *testing.T) {
	t.Parallel()

	valid := SelfQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 3,
		ExpectedRole:         auth.RoleStudent,
		HistoryLimit:         10,
	}
	tests := []struct {
		name  string
		ctx   context.Context
		query SelfQuery
		code  ErrorCode
	}{
		{name: "nil context", query: valid, code: ErrorInvalidQuery},
		{name: "non canonical account", ctx: context.Background(), query: withQuery(valid, func(value *SelfQuery) { value.AccountID = "NOT-A-UUID" }), code: ErrorInvalidQuery},
		{name: "non canonical session", ctx: context.Background(), query: withQuery(valid, func(value *SelfQuery) { value.SessionID = "NOT-A-UUID" }), code: ErrorInvalidQuery},
		{name: "zero revision", ctx: context.Background(), query: withQuery(valid, func(value *SelfQuery) { value.ExpectedAuthRevision = 0 }), code: ErrorInvalidQuery},
		{name: "admin", ctx: context.Background(), query: withQuery(valid, func(value *SelfQuery) { value.ExpectedRole = auth.RoleAdmin }), code: ErrorForbidden},
		{name: "zero history", ctx: context.Background(), query: withQuery(valid, func(value *SelfQuery) { value.HistoryLimit = 0 }), code: ErrorInvalidQuery},
		{name: "oversized history", ctx: context.Background(), query: withQuery(valid, func(value *SelfQuery) { value.HistoryLimit = MaxHistoryLimit + 1 }), code: ErrorInvalidQuery},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeRepository{}
			service, err := NewService(repository)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.GetSelf(test.ctx, test.query)
			if CodeOf(err) != test.code || repository.calls != 0 {
				t.Fatalf("GetSelf() error = %v (%q), calls = %d", err, CodeOf(err), repository.calls)
			}
		})
	}
}

func TestServiceReturnsValidatedRepositoryUnion(t *testing.T) {
	t.Parallel()

	query := SelfQuery{AccountID: testAccountID, SessionID: testSessionID, ExpectedAuthRevision: 3, ExpectedRole: auth.RoleStudent, HistoryLimit: 2}
	repository := &fakeRepository{result: validReadyResult()}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetSelf(context.Background(), query)
	if err != nil {
		t.Fatalf("GetSelf() error = %v", err)
	}
	if result.State != StateReady || result.Ready == nil || repository.calls != 1 || repository.query != query {
		t.Fatalf("result = %#v, repository = %#v", result, repository)
	}

	repository.result = Result{State: StateReady, HeadRevision: 1}
	_, err = service.GetSelf(context.Background(), query)
	if CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("invalid union error = %v (%q)", err, CodeOf(err))
	}
}

func TestServicePropagatesOwnedRepositoryFailure(t *testing.T) {
	t.Parallel()

	want := studentAnalyticsError(ErrorDatabase, "test", errors.New("database unavailable"))
	repository := &fakeRepository{err: want}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	_, got := service.GetSelf(context.Background(), SelfQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 1,
		ExpectedRole:         auth.RoleStudent,
		HistoryLimit:         1,
	})
	if !errors.Is(got, want) {
		t.Fatalf("GetSelf() error = %v, want %v", got, want)
	}
}

func TestServiceRejectsInvalidRepositoryReadyPayload(t *testing.T) {
	t.Parallel()

	query := SelfQuery{AccountID: testAccountID, SessionID: testSessionID, ExpectedAuthRevision: 3, ExpectedRole: auth.RoleStudent, HistoryLimit: 2}
	tests := []struct {
		name   string
		mutate func(*Result)
	}{
		{name: "current metric out of range", mutate: func(result *Result) { value := 101.0; result.Ready.Current.Knowledge = &value }},
		{name: "history metric non finite", mutate: func(result *Result) { value := math.NaN(); result.Ready.ExamHistory[0].Values.Accuracy = &value }},
		{name: "non UTC event", mutate: func(result *Result) {
			event := time.Date(2026, 1, 2, 11, 4, 5, 0, time.FixedZone("+08", 8*60*60))
			result.Ready.ExamHistory[0].EventTime = event
			result.Ready.RatingHistory[0].EventTime = event
		}},
		{name: "zero rank", mutate: func(result *Result) { result.Ready.RatingHistory[0].Rank = 0 }},
		{name: "wrong delta", mutate: func(result *Result) { result.Ready.RatingHistory[0].Delta++ }},
		{name: "non finite performance", mutate: func(result *Result) { result.Ready.RatingHistory[0].Performance = math.Inf(1) }},
		{name: "canonical rating mismatch", mutate: func(result *Result) { result.Ready.Rating++ }},
		{name: "duplicate observation", mutate: func(result *Result) {
			result.Ready.ExamHistory = append(result.Ready.ExamHistory, result.Ready.ExamHistory[0])
			next := result.Ready.RatingHistory[0]
			next.OldRating = next.NewRating
			result.Ready.RatingHistory = append(result.Ready.RatingHistory, next)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := validReadyResult()
			test.mutate(&result)
			repository := &fakeRepository{result: result}
			service, err := NewService(repository)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.GetSelf(context.Background(), query); CodeOf(err) != ErrorStoredDataInvalid {
				t.Fatalf("GetSelf() error = %v (%q)", err, CodeOf(err))
			}
		})
	}
}

func TestConstructorsRejectNilDependencies(t *testing.T) {
	t.Parallel()

	if _, err := NewService(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewService(nil) error = %v", err)
	}
	if _, err := NewPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewPostgresRepository(nil) error = %v", err)
	}
	if _, err := newPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("newPostgresRepository(nil) error = %v", err)
	}
}

func withQuery(value SelfQuery, change func(*SelfQuery)) SelfQuery {
	change(&value)
	return value
}

func validReadyResult() Result {
	eventTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return Result{
		State:        StateReady,
		HeadRevision: 2,
		Ready: &ReadyResult{
			ReferenceTime: eventTime,
			Rating:        1500,
			ExamHistory: []ExamHistoryPoint{{
				ExamID:     "22222222-2222-4222-8222-222222222222",
				SnapshotID: "33333333-3333-4333-8333-333333333333",
				Title:      "Exam",
				EventTime:  eventTime,
			}},
			RatingHistory: []RatingHistoryPoint{{
				ExamID:      "22222222-2222-4222-8222-222222222222",
				SnapshotID:  "33333333-3333-4333-8333-333333333333",
				Title:       "Exam",
				EventTime:   eventTime,
				Rank:        1,
				OldRating:   1500,
				Delta:       0,
				NewRating:   1500,
				Seed:        1,
				Performance: 1500,
			}},
		},
	}
}
