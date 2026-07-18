package recommendation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPintiaV2IdentifiersDriveProblemAndCatalogKeys(t *testing.T) {
	t.Parallel()
	row := problemRow{
		SnapshotID: 1, ProblemSetID: "problem-set-100", ProblemSetProblemID: "problem-set:item:100",
		SourceURL: "https://pintia.cn/problem-sets/problem-set-100/problems/type/7", Platform: "pintia",
		ProblemID: "problem:100", Title: "Pintia identifier contract",
		MetricsJSON: json.RawMessage(`{"protocol":"problem_analytics_v1","participantCount":1,"submissionCount":1,"acceptedSubmissionCount":1,"attemptingActorCount":1,"acceptedActorCount":1,"submissionAcceptanceRate":1,"actorAcceptanceRate":1,"acceptedRuntimeMs":null,"acceptedMemoryBytes":null}`),
	}
	fact, err := buildProblemFact(row)
	if err != nil {
		t.Fatal(err)
	}
	expectedSourceKey := "pintia:problem:11:problem:100"
	if fact.SourceProblemKey != expectedSourceKey || fact.ProblemKey != expectedSourceKey+":"+fact.ProblemFactSHA256 {
		t.Fatalf("problem keys = source %q fact %q", fact.SourceProblemKey, fact.ProblemKey)
	}

	review, err := buildReviewCandidates([]problemRow{row})
	if err != nil {
		t.Fatal(err)
	}
	if len(review) != 1 || review[0].ProblemKey != fact.ProblemKey || len(review[0].SourceProblemSets) != 1 ||
		review[0].SourceProblemSets[0].ProblemSetID != row.ProblemSetID {
		t.Fatalf("review candidates = %#v", review)
	}

	catalog, _, _, err := parseKnowledgeCatalog(testCatalogDocument(t, []any{map[string]any{
		"platform": "pintia", "problemId": row.ProblemID, "problemFactSha256": fact.ProblemFactSHA256,
		"knowledge": []any{map[string]any{"knowledgePointId": "arrays", "weight": "1"}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := buildProblemFactIndex([]problemRow{row})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := buildCandidates([]problemRow{row}, facts, catalog, []string{"arrays", "graphs"}, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ProblemKey != fact.ProblemKey ||
		candidates[0].SourceProblemKey != fact.SourceProblemKey || candidates[0].ProblemID != row.ProblemID {
		t.Fatalf("inference candidates = %#v", candidates)
	}
}

func TestPintiaProblemKeyUsesLengthFramedIdentity(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	if got := pintiaProblemKey("a:b", digest); got != "pintia:problem:3:a:b:"+digest {
		t.Fatalf("problem key = %q", got)
	}
	if pintiaSourceProblemKey("a:b") == pintiaSourceProblemKey("a:b:") {
		t.Fatal("distinct Pintia IDs produced an equal source problem key")
	}
}

func TestBuildProblemFactIndexRejectsDuplicateSnapshotProblemIdentity(t *testing.T) {
	t.Parallel()
	row := problemRow{
		SnapshotID: 1, ProblemSetID: "problem-set-100", ProblemSetProblemID: "problem-set:item:100",
		SourceURL: "https://pintia.cn/problem-sets/problem-set-100/problems/type/7", Platform: "pintia",
		ProblemID: "problem:100", Title: "Pintia identifier contract",
	}
	if _, err := buildProblemFactIndex([]problemRow{row, row}); err == nil || !strings.Contains(err.Error(), "duplicates snapshot problem identity") {
		t.Fatalf("duplicate problem identity error = %v", err)
	}
}
