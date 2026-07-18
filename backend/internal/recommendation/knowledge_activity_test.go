package recommendation

import (
	"testing"
	"time"
)

func TestBuildKnowledgeActivityProjectsCatalogBoundSnapshotEvidence(t *testing.T) {
	t.Parallel()
	maxScore := "100"
	problem := problemRow{
		SnapshotID: 1, ProblemSetID: "set-1", ProblemSetProblemID: "problem-1",
		SourceURL: "https://pintia.cn/problem-sets/set-1", Platform: "pintia",
		ProblemID: "source-problem-1", Title: "Problem 1", MaxScore: &maxScore,
	}
	fact, err := buildProblemFact(problem)
	if err != nil {
		t.Fatal(err)
	}
	catalog := knowledgeCatalog{
		Points: []knowledgePoint{{ID: "arrays"}, {ID: "graphs"}},
		Assignments: []problemAssignment{{
			Platform: "pintia", ProblemID: problem.ProblemID, ProblemFactSHA256: fact.ProblemFactSHA256,
			Knowledge: []catalogWeight{{KnowledgePointID: "arrays", Weight: 1}},
		}},
	}
	first := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	latest := first.Add(24 * time.Hour)
	secondProblem := problem
	secondProblem.SnapshotID = 2
	facts, err := buildProblemFactIndex([]problemRow{problem, secondProblem})
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity := problemInstanceIdentity{SnapshotID: problem.SnapshotID, ProblemSetProblemID: problem.ProblemSetProblemID}
	secondIdentity := problemInstanceIdentity{SnapshotID: secondProblem.SnapshotID, ProblemSetProblemID: secondProblem.ProblemSetProblemID}
	activity, err := buildKnowledgeActivity(catalog, facts, []problemActivityRow{
		{Identity: firstIdentity, Correct: true, LastSubmittedAt: first},
		{Identity: secondIdentity, LastSubmittedAt: latest},
	}, []recentActivityRow{
		{Identity: firstIdentity, Date: "2026-07-17", Attempted: 2, Correct: 1},
		{Identity: secondIdentity, Date: "2026-07-17", Attempted: 1, Correct: 1},
		{Identity: secondIdentity, Date: "2026-07-18", Attempted: 3, Correct: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(activity) != 2 || activity[0].KnowledgePointID != "arrays" || activity[0].Attempted != 1 ||
		activity[0].Correct != 1 || activity[0].LastTriedAt == nil || !activity[0].LastTriedAt.Equal(latest) ||
		len(activity[0].RecentSeries) != 2 || activity[0].RecentSeries[0] != (RecommendationKnowledgeActivityDay{
		Date: "2026-07-17", Attempted: 3, Correct: 2,
	}) || activity[0].RecentSeries[1] != (RecommendationKnowledgeActivityDay{
		Date: "2026-07-18", Attempted: 3, Correct: 2,
	}) {
		t.Fatalf("arrays activity = %#v", activity)
	}
	if activity[1].KnowledgePointID != "graphs" || activity[1].Attempted != 0 ||
		activity[1].Correct != 0 || activity[1].LastTriedAt != nil || len(activity[1].RecentSeries) != 0 {
		t.Fatalf("graphs activity = %#v", activity[1])
	}
}

func TestBuildKnowledgeActivityRejectsInvalidRecentRows(t *testing.T) {
	t.Parallel()
	identity := problemInstanceIdentity{SnapshotID: 1, ProblemSetProblemID: "problem-1"}
	_, err := buildKnowledgeActivity(
		knowledgeCatalog{Points: []knowledgePoint{{ID: "arrays"}}},
		problemFactIndex{identity: {ProblemFactSHA256: "unused"}},
		nil,
		[]recentActivityRow{{Identity: identity, Date: "2026-7-1"}},
	)
	if err == nil {
		t.Fatal("invalid recent activity date was accepted")
	}
}

func TestBuildKnowledgeActivityRejectsUnknownProblemIdentity(t *testing.T) {
	t.Parallel()
	_, err := buildKnowledgeActivity(
		knowledgeCatalog{Points: []knowledgePoint{{ID: "arrays"}}},
		problemFactIndex{},
		[]problemActivityRow{{
			Identity:        problemInstanceIdentity{SnapshotID: 1, ProblemSetProblemID: "problem-1"},
			LastSubmittedAt: time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC),
		}},
		nil,
	)
	if err == nil {
		t.Fatal("unknown problem activity identity was accepted")
	}
}
