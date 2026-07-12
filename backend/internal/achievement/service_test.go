package achievement

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID = "11111111-1111-4111-8111-111111111111"
	testSessionID = "22222222-2222-4222-8222-222222222222"
	testJWTID     = "33333333-3333-4333-8333-333333333333"
)

type fakeRepository struct {
	snapshot RepositorySnapshot
	err      error
	calls    int
	query    SelfQuery
}

func (repository *fakeRepository) LoadSelf(_ context.Context, query SelfQuery) (RepositorySnapshot, error) {
	repository.calls++
	repository.query = query
	return repository.snapshot, repository.err
}

func TestServiceDerivesEverySupportedProgressValue(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{snapshot: RepositorySnapshot{
		RuleSetVersion: 1, RuleHeadRevision: 1, AnalyticsHeadRevision: 4,
		Rules: testRules(), Metrics: testMetrics(), AIDialogueCount: 15,
	}}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetSelf(context.Background(), testQuery())
	if err != nil {
		t.Fatalf("GetSelf() error = %v", err)
	}
	if result.State != StateReady || result.AnalyticsHeadRevision != 4 || result.RuleSetVersion != 1 || result.RuleHeadRevision != 1 {
		t.Fatalf("result metadata = %#v", result)
	}
	want := map[ProgressKey]float64{
		ProgressExamCount: 2, ProgressPositiveDeltaCount: 2, ProgressBestPositiveStreak: 2,
		ProgressKnowledgeMax: 75, ProgressAccuracyMax: 65, ProgressQualityMax: 85,
		ProgressFlexibilityMax: 80, ProgressProficiencyMax: 95,
		ProgressMaxRating: 1030, ProgressMaxRatingDelta: 20,
		ProgressTop10Count: 2, ProgressTop3Count: 1, ProgressRank1Count: 1,
		ProgressMaxExamMinMetric: 55, ProgressCurrentMinMetric: 56,
		ProgressAIDialogueCount: 15,
	}
	if len(result.Items) != len(want) || result.Summary.Total != len(want) {
		t.Fatalf("items/summary = %d/%#v", len(result.Items), result.Summary)
	}
	for _, item := range result.Items {
		if item.Progress != want[item.ProgressKey] {
			t.Errorf("%s progress = %v, want %v", item.ProgressKey, item.Progress, want[item.ProgressKey])
		}
		if item.Tier != evaluateTier(item.Progress, Rule{BronzeTarget: 1, SilverTarget: 2, GoldTarget: 3}) {
			t.Errorf("%s tier = %d", item.ProgressKey, item.Tier)
		}
	}
	if repository.calls != 1 || repository.query != testQuery() {
		t.Fatalf("repository = %#v", repository)
	}
}

func TestServiceReturnsAllRulesAndLiveDialogueForEmptyAnalyticsStates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		headRevision int64
		state        State
	}{
		{name: "not generated", state: StateNotGenerated},
		{name: "no observations", headRevision: 5, state: StateNoObservations},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeRepository{snapshot: RepositorySnapshot{
				RuleSetVersion: 1, RuleHeadRevision: 2, AnalyticsHeadRevision: test.headRevision,
				Rules: []Rule{
					{Code: "exam", Title: "Exam", Description: "Exam progress", ProgressKey: ProgressExamCount, BronzeTarget: 1, SilverTarget: 2, GoldTarget: 3, SortOrder: 1},
					{Code: "dialogue", Title: "Dialogue", Description: "Dialogue progress", ProgressKey: ProgressAIDialogueCount, BronzeTarget: 1, SilverTarget: 2, GoldTarget: 3, SortOrder: 2},
				},
				AIDialogueCount: 2,
			}}
			service, err := NewService(repository)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.GetSelf(context.Background(), testQuery())
			if err != nil {
				t.Fatalf("GetSelf() error = %v", err)
			}
			if result.State != test.state || len(result.Items) != 2 || result.Items[0].Progress != 0 || result.Items[1].Progress != 2 || result.Items[1].Tier != 2 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestServiceRejectsInvalidQueryBeforeRepository(t *testing.T) {
	t.Parallel()

	valid := testQuery()
	tests := []struct {
		name  string
		ctx   context.Context
		query SelfQuery
		code  ErrorCode
	}{
		{name: "nil context", query: valid, code: ErrorInvalidQuery},
		{name: "account", ctx: context.Background(), query: withQuery(valid, func(query *SelfQuery) { query.Principal.AccountID = "bad" }), code: ErrorInvalidQuery},
		{name: "session", ctx: context.Background(), query: withQuery(valid, func(query *SelfQuery) { query.Principal.SessionID = "bad" }), code: ErrorInvalidQuery},
		{name: "jwt", ctx: context.Background(), query: withQuery(valid, func(query *SelfQuery) { query.Principal.JWTID = "bad" }), code: ErrorInvalidQuery},
		{name: "revision", ctx: context.Background(), query: withQuery(valid, func(query *SelfQuery) { query.Principal.AuthRevision = 0 }), code: ErrorInvalidQuery},
		{name: "admin", ctx: context.Background(), query: withQuery(valid, func(query *SelfQuery) { query.Principal.Role = auth.RoleAdmin }), code: ErrorForbidden},
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
				t.Fatalf("error/calls = %v/%d", err, repository.calls)
			}
		})
	}
}

func TestServiceRejectsCorruptRepositorySnapshots(t *testing.T) {
	t.Parallel()

	base := RepositorySnapshot{RuleSetVersion: 1, RuleHeadRevision: 1, AnalyticsHeadRevision: 1, Rules: testRules(), Metrics: testMetrics()}
	tests := []struct {
		name   string
		mutate func(*RepositorySnapshot)
	}{
		{name: "empty rules", mutate: func(snapshot *RepositorySnapshot) { snapshot.Rules = nil }},
		{name: "unknown key", mutate: func(snapshot *RepositorySnapshot) { snapshot.Rules[0].ProgressKey = "unknown" }},
		{name: "duplicate order", mutate: func(snapshot *RepositorySnapshot) { snapshot.Rules[1].SortOrder = snapshot.Rules[0].SortOrder }},
		{name: "nonfinite target", mutate: func(snapshot *RepositorySnapshot) { snapshot.Rules[0].GoldTarget = math.Inf(1) }},
		{name: "metrics without head", mutate: func(snapshot *RepositorySnapshot) { snapshot.AnalyticsHeadRevision = 0 }},
		{name: "misaligned metrics", mutate: func(snapshot *RepositorySnapshot) { snapshot.Metrics.RatingHistory = nil }},
		{name: "negative dialogue", mutate: func(snapshot *RepositorySnapshot) { snapshot.AIDialogueCount = -1 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := cloneSnapshot(base)
			test.mutate(&snapshot)
			service, err := NewService(&fakeRepository{snapshot: snapshot})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.GetSelf(context.Background(), testQuery()); CodeOf(err) != ErrorStoredDataInvalid {
				t.Fatalf("GetSelf() error = %v (%q)", err, CodeOf(err))
			}
		})
	}
}

func TestServicePropagatesRepositoryFailure(t *testing.T) {
	t.Parallel()

	want := achievementError(ErrorDatabase, "test", errors.New("offline"))
	service, err := NewService(&fakeRepository{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if _, got := service.GetSelf(context.Background(), testQuery()); !errors.Is(got, want) {
		t.Fatalf("GetSelf() error = %v", got)
	}
}

func testQuery() SelfQuery {
	return SelfQuery{Principal: auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 3,
	}}
}

func withQuery(query SelfQuery, mutate func(*SelfQuery)) SelfQuery {
	mutate(&query)
	return query
}

func testRules() []Rule {
	keys := []ProgressKey{
		ProgressExamCount, ProgressPositiveDeltaCount, ProgressBestPositiveStreak,
		ProgressKnowledgeMax, ProgressAccuracyMax, ProgressQualityMax,
		ProgressFlexibilityMax, ProgressProficiencyMax, ProgressMaxRating,
		ProgressMaxRatingDelta, ProgressTop10Count, ProgressTop3Count,
		ProgressRank1Count, ProgressMaxExamMinMetric, ProgressCurrentMinMetric,
		ProgressAIDialogueCount,
	}
	rules := make([]Rule, 0, len(keys))
	for index, key := range keys {
		rules = append(rules, Rule{
			Code: "rule_" + string(key), Title: "Rule", Description: "Rule description", ProgressKey: key,
			BronzeTarget: 1, SilverTarget: 2, GoldTarget: 3, SortOrder: int64(index + 1),
		})
	}
	return rules
}

func testMetrics() *analytics.StudentMetrics {
	reference := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	event1 := reference.Add(-2 * time.Hour)
	event2 := reference.Add(-time.Hour)
	metric := func(value float64) *float64 { return &value }
	return &analytics.StudentMetrics{
		Protocol:      analytics.StudentMetricsProtocolV1,
		ReferenceTime: reference,
		Current: analytics.MetricValues{
			Knowledge: metric(76), Accuracy: metric(66), Quality: metric(86), Flexibility: metric(56), Proficiency: metric(96),
		},
		ExamHistory: []analytics.ExamMetricPoint{
			{ExamID: 1, SnapshotID: 11, EventTime: event1, Values: analytics.MetricValues{Knowledge: metric(50), Accuracy: metric(60), Quality: metric(70), Flexibility: metric(80), Proficiency: metric(90)}},
			{ExamID: 2, SnapshotID: 22, EventTime: event2, Values: analytics.MetricValues{Knowledge: metric(75), Accuracy: metric(65), Quality: metric(85), Flexibility: metric(55), Proficiency: metric(95)}},
		},
		RatingHistory: []analytics.RatingHistoryPoint{
			{ExamID: 1, SnapshotID: 11, EventTime: event1, Rank: 10, OldRating: 1000, Delta: 10, NewRating: 1010, Seed: 1, Performance: 1010},
			{ExamID: 2, SnapshotID: 22, EventTime: event2, Rank: 1, OldRating: 1010, Delta: 20, NewRating: 1030, Seed: 1, Performance: 1030},
		},
	}
}

func cloneSnapshot(snapshot RepositorySnapshot) RepositorySnapshot {
	result := snapshot
	result.Rules = append([]Rule(nil), snapshot.Rules...)
	if snapshot.Metrics != nil {
		metrics := *snapshot.Metrics
		metrics.ExamHistory = append([]analytics.ExamMetricPoint(nil), snapshot.Metrics.ExamHistory...)
		metrics.RatingHistory = append([]analytics.RatingHistoryPoint(nil), snapshot.Metrics.RatingHistory...)
		result.Metrics = &metrics
	}
	return result
}
