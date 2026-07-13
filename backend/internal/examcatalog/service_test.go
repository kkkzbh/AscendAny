package examcatalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID  = "11111111-1111-4111-8111-111111111111"
	testSessionID  = "22222222-2222-4222-8222-222222222222"
	testJWTID      = "33333333-3333-4333-8333-333333333333"
	testExamID     = "44444444-4444-4444-8444-444444444444"
	testSnapshotID = "55555555-5555-4555-8555-555555555555"
)

type repositoryStub struct {
	page        Page
	detail      Detail
	found       bool
	err         error
	listQuery   ListQuery
	detailQuery DetailQuery
	listCalls   int
	getCalls    int
}

func (stub *repositoryStub) LoadPage(_ context.Context, query ListQuery) (Page, error) {
	stub.listCalls++
	stub.listQuery = query
	return stub.page, stub.err
}

func (stub *repositoryStub) LoadDetail(_ context.Context, query DetailQuery) (Detail, bool, error) {
	stub.getCalls++
	stub.detailQuery = query
	return stub.detail, stub.found, stub.err
}

func TestServiceValidatesQueriesBeforeRepositoryRead(t *testing.T) {
	t.Parallel()
	validList := ListQuery{Principal: testPrincipal(), Limit: 20}
	invalid := []ListQuery{
		{Principal: testPrincipal(), Limit: 0},
		{Principal: testPrincipal(), Limit: MaxPageSize + 1},
		{Principal: withPrincipal(testPrincipal(), func(value *auth.AccessPrincipal) { value.JWTID = "invalid" }), Limit: 20},
		{Principal: withPrincipal(testPrincipal(), func(value *auth.AccessPrincipal) { value.Role = "guest" }), Limit: 20},
	}
	badCursor := "invalid"
	invalid = append(invalid, ListQuery{Principal: testPrincipal(), Cursor: &badCursor, Limit: 20})
	for _, query := range invalid {
		repository := &repositoryStub{}
		service, err := NewService(repository)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.List(context.Background(), query); CodeOf(err) != ErrorInvalidQuery || repository.listCalls != 0 {
			t.Fatalf("List(%#v) error=%v calls=%d", query, err, repository.listCalls)
		}
	}

	repository := &repositoryStub{page: Page{Items: []ExamSummary{}}}
	service, _ := NewService(repository)
	if _, err := service.List(context.Background(), validList); err != nil || repository.listCalls != 1 {
		t.Fatalf("valid List error=%v calls=%d", err, repository.listCalls)
	}
	if _, _, err := service.Get(context.Background(), DetailQuery{Principal: testPrincipal(), ExamID: "invalid"}); CodeOf(err) != ErrorInvalidQuery || repository.getCalls != 0 {
		t.Fatalf("invalid Get error=%v calls=%d", err, repository.getCalls)
	}
}

func TestServiceValidatesPageAndDetailShapes(t *testing.T) {
	t.Parallel()
	summary := validSummary()
	repository := &repositoryStub{
		page: Page{Items: []ExamSummary{summary}},
		detail: Detail{
			ExamSummary: summary,
			Problems: []Problem{{
				ID:                         "problem-set:problem-1",
				ProblemID:                  "problem:1",
				Label:                      stringPointer("7-1"),
				Title:                      "A+B",
				MaxScore:                   stringPointer("20.0"),
				SubmissionCount:            4,
				SubmittingParticipantCount: 2,
				PassedParticipantCount:     1,
			}},
		},
		found: true,
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.List(context.Background(), ListQuery{Principal: testPrincipal(), Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("List() page=%#v error=%v", page, err)
	}
	detail, found, err := service.Get(context.Background(), DetailQuery{Principal: testPrincipal(), ExamID: testExamID})
	if err != nil || !found || len(detail.Problems) != 1 {
		t.Fatalf("Get() detail=%#v found=%t error=%v", detail, found, err)
	}

	repository.page.Items[0].ProblemCount = 0
	if _, err := service.List(context.Background(), ListQuery{Principal: testPrincipal(), Limit: 20}); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("invalid page error=%v", err)
	}
	repository.detail.ExamSummary = summary
	repository.detail.Problems = nil
	if _, _, err := service.Get(context.Background(), DetailQuery{Principal: testPrincipal(), ExamID: testExamID}); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("invalid detail error=%v", err)
	}
	invalidSummary := summary
	invalidSummary.ProblemSetID = "problem/set"
	if err := validateSummary(invalidSummary); err == nil {
		t.Fatal("invalid Pintia problem set ID passed the public contract")
	}
	invalidProblem := Problem{ID: "problem/set", ProblemID: "problem:1", Title: "A+B"}
	if err := validateProblem(invalidProblem); err == nil {
		t.Fatal("invalid Pintia problem identity passed the public contract")
	}
}

func TestServicePropagatesRepositoryErrorsAndNotFound(t *testing.T) {
	t.Parallel()
	want := catalogError(ErrorDatabase, "test", errors.New("database unavailable"))
	repository := &repositoryStub{err: want}
	service, _ := NewService(repository)
	if _, err := service.List(context.Background(), ListQuery{Principal: testPrincipal(), Limit: 20}); !errors.Is(err, want) {
		t.Fatalf("List() error=%v", err)
	}
	repository.err = nil
	detail, found, err := service.Get(context.Background(), DetailQuery{Principal: testPrincipal(), ExamID: testExamID})
	if err != nil || found || detail.ID != "" {
		t.Fatalf("Get() detail=%#v found=%t error=%v", detail, found, err)
	}
}

func TestConstructorsRejectMissingDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewService(nil) error=%v", err)
	}
	if _, err := NewPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewPostgresRepository(nil) error=%v", err)
	}
	if _, err := newPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("newPostgresRepository(nil) error=%v", err)
	}
}

func testPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID:    testAccountID,
		SessionID:    testSessionID,
		Role:         auth.RoleStudent,
		AuthRevision: 3,
		JWTID:        testJWTID,
	}
}

func withPrincipal(value auth.AccessPrincipal, change func(*auth.AccessPrincipal)) auth.AccessPrincipal {
	change(&value)
	return value
}

func validSummary() ExamSummary {
	eventTime := time.Date(2026, 7, 10, 3, 4, 5, 0, time.UTC)
	return ExamSummary{
		ID:               testExamID,
		SnapshotID:       testSnapshotID,
		Platform:         "pintia",
		ProblemSetID:     "set:2039341868571590656",
		Title:            "集训",
		SourceURL:        "https://pintia.cn/problem-sets/set:2039341868571590656",
		TotalScore:       stringPointer("300.0"),
		ProblemCount:     1,
		ParticipantCount: 35,
		RankingCount:     35,
		SubmissionCount:  624,
		SnapshotSequence: 1,
		HeadRevision:     1,
		ExporterVersion:  "2.0.5",
		ExportedAt:       eventTime,
		UpdatedAt:        eventTime,
	}
}

func stringPointer(value string) *string { return &value }
