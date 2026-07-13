package recommendation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func TestBuildActorFeaturesEncodesNullableMetricsExplicitly(t *testing.T) {
	t.Parallel()
	metrics := json.RawMessage(`{"protocol":"student_analytics_v1","referenceTime":"2026-07-13T12:00:00Z","current":{"knowledge":null,"accuracy":75,"quality":null,"flexibility":25,"proficiency":50},"examHistory":[{"examId":1,"snapshotId":11,"eventTime":"2026-07-12T12:00:00Z","values":{"knowledge":null,"accuracy":75,"quality":null,"flexibility":25,"proficiency":50}}],"ratingHistory":[{"examId":1,"snapshotId":11,"eventTime":"2026-07-12T12:00:00Z","rank":1,"oldRating":1500,"delta":0,"newRating":1500,"seed":1,"performance":1500}]}`)
	features, rating, err := buildActorFeatures("1500", metrics)
	if err != nil {
		t.Fatal(err)
	}
	if rating != 1500 || len(features) != len(actorFeatureIDs) || features[1].Value != 0 || features[2].Value != 0 ||
		features[3].Value != 75 || features[4].Value != 1 || features[5].Value != 0 || features[6].Value != 0 {
		t.Fatalf("features=%#v rating=%v", features, rating)
	}
}

func TestMaterializeInferenceBuildsDeterministicReadyPath(t *testing.T) {
	t.Parallel()
	catalogRaw := testCatalogDocument(t, []any{})
	catalog, _, digest, err := parseKnowledgeCatalog(catalogRaw)
	if err != nil {
		t.Fatal(err)
	}
	model, _ := testModel(t, digest)
	actor := make([]inferencemodel.FeatureValue, len(actorFeatureIDs))
	for index, id := range actorFeatureIDs {
		actor[index] = inferencemodel.FeatureValue{FeatureID: id}
	}
	candidates := make([]inferenceCandidate, 0, 6)
	for index := 0; index < 6; index++ {
		knowledgeID := "arrays"
		weights := []inferencemodel.KnowledgeWeight{{KnowledgePointID: "arrays", Weight: 1}, {KnowledgePointID: "graphs", Weight: 0}}
		if index >= 3 {
			knowledgeID = "graphs"
			weights = []inferencemodel.KnowledgeWeight{{KnowledgePointID: "arrays", Weight: 0}, {KnowledgePointID: "graphs", Weight: 1}}
		}
		problemFeatures := make([]inferencemodel.FeatureValue, len(problemFeatureIDs))
		for featureIndex, id := range problemFeatureIDs {
			problemFeatures[featureIndex] = inferencemodel.FeatureValue{FeatureID: id}
		}
		id := string(rune('a' + index))
		candidates = append(candidates, inferenceCandidate{
			ProblemKey: "pintia:" + id + ":" + strings.Repeat("a", 64), SourceProblemKey: "pintia:" + id,
			Platform: "pintia", ProblemID: id, Title: "Problem " + id,
			SourceProblemSets: []RecommendationSourceSet{{ProblemSetID: "1", SourceURL: "https://pintia.cn/problem-sets/1"}},
			Features:          problemFeatures, Weights: weights, weightByKnowledge: map[string]float64{knowledgeID: 1},
		})
	}
	evidence := observationEvidence{
		Evidence:        RecommendationInferenceEvidence{ObservationCount: 4, DistinctProblemCount: 3, PassedProblemCount: 1},
		KnowledgeCounts: map[string]int64{"arrays": 2, "graphs": 1}, PassedSources: map[string]struct{}{},
	}
	first, err := materializeInference(model, catalog, actor, 1500, candidates, evidence)
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializeInference(model, catalog, actor, 1500, candidates, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != RecommendationResultReady || len(first.LearningPath) != 2 ||
		len(first.LearningPath[0].RecommendedProblems) != 3 || len(first.LearningPath[1].RecommendedProblems) != 3 ||
		first.SHA256 == "" || first.SHA256 != second.SHA256 {
		t.Fatalf("first=%#v second hash=%s", first, second.SHA256)
	}
}

func TestKnowledgePathUsesFixedBounds(t *testing.T) {
	t.Parallel()
	points := []knowledgePoint{{ID: "arrays"}, {ID: "graphs"}}
	path, _, reason := buildKnowledgePath(points, []float64{0.9, 0.9})
	if reason != "mastery_target_satisfied" || len(path) != 0 {
		t.Fatalf("path=%v reason=%q", path, reason)
	}
	path, _, reason = buildKnowledgePath(points, []float64{0.1, 0.9})
	if reason != "path_below_minimum" || len(path) != 1 {
		t.Fatalf("path=%v reason=%q", path, reason)
	}
}

func TestRankPathProblemsReservesSharedCandidatesForGloballyFeasibleAssignment(t *testing.T) {
	t.Parallel()
	points := []knowledgePoint{{ID: "arrays", Label: "Arrays"}, {ID: "graphs", Label: "Graphs"}}
	mastery := []float64{0.1, 0.1}
	candidates := make([]inferenceCandidate, 0, 6)
	for index := 0; index < 3; index++ {
		id := fmt.Sprintf("shared-%d", index+1)
		candidates = append(candidates, inferenceCandidate{
			ProblemKey: "pintia:" + id + ":" + strings.Repeat("a", 64), SourceProblemKey: "pintia:" + id,
			Platform: "pintia", ProblemID: id, Title: id, probability: targetSuccessProbability,
			SourceProblemSets: []RecommendationSourceSet{{ProblemSetID: "1", SourceURL: "https://pintia.cn/problem-sets/1"}},
			weightByKnowledge: map[string]float64{"arrays": 0.5, "graphs": 0.5},
		})
	}
	for index := 0; index < 3; index++ {
		id := fmt.Sprintf("arrays-only-%d", index+1)
		candidates = append(candidates, inferenceCandidate{
			ProblemKey: "pintia:" + id + ":" + strings.Repeat("b", 64), SourceProblemKey: "pintia:" + id,
			Platform: "pintia", ProblemID: id, Title: id, probability: 0.1,
			SourceProblemSets: []RecommendationSourceSet{{ProblemSetID: "1", SourceURL: "https://pintia.cn/problem-sets/1"}},
			weightByKnowledge: map[string]float64{"arrays": 1},
		})
	}

	steps, blocked := rankPathProblems(points, mastery, candidates, []int{0, 1}, map[int]struct{}{0: {}, 1: {}})
	if len(blocked) != 0 || len(steps) != 2 {
		t.Fatalf("steps=%#v blocked=%v", steps, blocked)
	}
	for _, problem := range steps[0].RecommendedProblems {
		if !strings.Contains(problem.ProblemID, "arrays-only") {
			t.Fatalf("first step consumed shared candidate %q", problem.ProblemID)
		}
	}
	for _, problem := range steps[1].RecommendedProblems {
		if !strings.Contains(problem.ProblemID, "shared") {
			t.Fatalf("second step did not receive shared candidate %q", problem.ProblemID)
		}
	}
}
