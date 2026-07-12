package recommendation

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func TestBuildInputBundleIsDeterministicAndRoundTrips(t *testing.T) {
	t.Parallel()
	dataset := testTrainingDataset(t)
	first, err := BuildInputBundle(dataset, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(dataset.Students)
	slices.Reverse(dataset.Problems)
	slices.Reverse(dataset.Observations)
	second, err := BuildInputBundle(dataset, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CanonicalJSON, second.CanonicalJSON) || first.SHA256 != second.SHA256 ||
		first.ManifestSHA256 != second.ManifestSHA256 || !slices.Equal(first.ActorIDs, []int64{11, 29}) {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	parsed, err := ParseInputBundle(first.CanonicalJSON, 1<<20, testTrainingRun(first))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ManifestSHA256 != first.ManifestSHA256 || !slices.Equal(parsed.ActorIDs, first.ActorIDs) {
		t.Fatalf("parsed=%#v", parsed)
	}
	if len(parsed.Actors) != 2 || len(parsed.Problems) != 2 || len(parsed.Interactions) != 6 ||
		len(parsed.Actors[0].Features) != 4 || len(parsed.Problems[0].Features) != 3 {
		t.Fatalf("parsed typed input=%#v", parsed)
	}
	if bytes.Contains(first.CanonicalJSON, []byte(`"userId"`)) || bytes.Contains(first.CanonicalJSON, []byte(`"studentNumber"`)) {
		t.Fatalf("training bundle contains direct identity fields: %s", first.CanonicalJSON)
	}
	for _, interaction := range parsed.Interactions {
		if interaction.ActorID == "11" && interaction.SnapshotID == "102" && interaction.Split != "validation" {
			t.Fatalf("latest actor interaction split=%q", interaction.Split)
		}
	}
}

func TestInputBundleRejectsChangedProvenanceAndNoncanonicalBytes(t *testing.T) {
	t.Parallel()
	bundle, err := BuildInputBundle(testTrainingDataset(t), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	run := testTrainingRun(bundle)
	run.SourceAnalyticsHeadRevision++
	if _, err := ParseInputBundle(bundle.CanonicalJSON, 1<<20, run); CodeOf(err) != ErrorInvalidBundle {
		t.Fatalf("changed provenance error=%v code=%q", err, CodeOf(err))
	}
	padded := append(slices.Clone(bundle.CanonicalJSON), '\n')
	if _, err := ParseInputBundle(padded, 1<<20, testTrainingRun(bundle)); CodeOf(err) != ErrorInvalidBundle {
		t.Fatalf("noncanonical input error=%v code=%q", err, CodeOf(err))
	}
	var negativeRating map[string]any
	decoder := json.NewDecoder(bytes.NewReader(bundle.CanonicalJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&negativeRating); err != nil {
		t.Fatal(err)
	}
	negativeRating["actors"].([]any)[0].(map[string]any)["currentRating"] = json.Number("-1")
	if _, err := ParseInputBundle(canonicalObject(t, negativeRating), 1<<20, testTrainingRun(bundle)); CodeOf(err) != ErrorInvalidBundle {
		t.Fatalf("negative rating error=%v code=%q", err, CodeOf(err))
	}
}

func TestBuildInputBundleRejectsRatingOutsideAnalyticsContract(t *testing.T) {
	t.Parallel()
	for _, rating := range []string{"-1", "1000000.0000001"} {
		dataset := testTrainingDataset(t)
		dataset.Students[0].Rating = rating
		if _, err := BuildInputBundle(dataset, 4<<20); CodeOf(err) != ErrorStoredDataInvalid {
			t.Fatalf("rating %q error=%v code=%q", rating, err, CodeOf(err))
		}
	}
}

func TestInputBundleRejectsUnreviewedProblemAndContradictoryHistoricalResult(t *testing.T) {
	t.Parallel()
	dataset := testTrainingDataset(t)
	var catalog map[string]any
	if err := json.Unmarshal(dataset.KnowledgeCatalog.Document, &catalog); err != nil {
		t.Fatal(err)
	}
	catalog["problemAssignments"] = catalog["problemAssignments"].([]any)[:1]
	dataset.KnowledgeCatalog.Document, dataset.KnowledgeCatalog.DocumentSHA256 = canonicalRaw(t, catalog)
	if _, err := BuildInputBundle(dataset, 4<<20); CodeOf(err) != ErrorPreflightFailed {
		t.Fatalf("missing mapping error=%v code=%q", err, CodeOf(err))
	}

	dataset = testTrainingDataset(t)
	contradictoryScore := "99"
	passed := true
	dataset.Observations[0].Score = &contradictoryScore
	dataset.Observations[0].Passed = &passed
	if _, err := BuildInputBundle(dataset, 4<<20); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("contradictory result error=%v code=%q", err, CodeOf(err))
	}
}

func TestInputBundleRejectsUnknownTrainingConfigurationField(t *testing.T) {
	t.Parallel()
	dataset := testTrainingDataset(t)
	var configuration map[string]any
	if err := json.Unmarshal(dataset.Configuration.Document, &configuration); err != nil {
		t.Fatal(err)
	}
	configuration["unexpected"] = true
	dataset.Configuration.Document, dataset.Configuration.DocumentSHA256 = canonicalRaw(t, configuration)
	if _, err := BuildInputBundle(dataset, 4<<20); CodeOf(err) != ErrorPreflightFailed {
		t.Fatalf("unknown config field error=%v code=%q", err, CodeOf(err))
	}
}

func TestInputBundleRejectsFloat64UnderflowInTrainingConfiguration(t *testing.T) {
	t.Parallel()
	dataset := testTrainingDataset(t)
	var configuration map[string]any
	decoder := json.NewDecoder(bytes.NewReader(dataset.Configuration.Document))
	decoder.UseNumber()
	if err := decoder.Decode(&configuration); err != nil {
		t.Fatal(err)
	}
	configuration["learningRate"] = json.Number("1e-400")
	dataset.Configuration.Document, dataset.Configuration.DocumentSHA256 = canonicalRaw(t, configuration)
	if _, err := BuildInputBundle(dataset, 4<<20); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("underflowing configuration error=%v code=%q", err, CodeOf(err))
	}
}

func testTrainingDataset(t *testing.T) TrainingDataset {
	t.Helper()
	analyticsManifest, analyticsHash := canonicalRaw(t, map[string]any{"generation": "source", "snapshots": []any{"a", "b"}})
	problemOneHTML := "<p>Compute <strong>A</strong>.</p>"
	problemTwoHTML := "<p>Traverse a graph.</p>"
	maxScore := "100"
	timeLimit := int64(1000)
	memoryLimit := int64(67_108_864)
	problemOneHash := testProblemFactHash(t, "501", "Problem A", &problemOneHTML, maxScore, &timeLimit, &memoryLimit)
	problemTwoHash := testProblemFactHash(t, "502", "Problem B", &problemTwoHTML, maxScore, &timeLimit, &memoryLimit)
	catalog, catalogHash := canonicalRaw(t, map[string]any{
		"taxonomyId": "recommendation.default",
		"knowledgePoints": []any{
			map[string]any{"id": "arrays", "label": "Arrays", "description": "Array fundamentals", "prerequisiteIds": []any{}},
			map[string]any{"id": "graphs", "label": "Graphs", "description": "Graph traversal", "prerequisiteIds": []any{"arrays"}},
		},
		"problemAssignments": []any{
			map[string]any{"platform": "pintia", "problemId": "501", "problemFactSha256": problemOneHash, "knowledge": []any{map[string]any{"knowledgePointId": "arrays", "weight": 1}}},
			map[string]any{"platform": "pintia", "problemId": "502", "problemFactSha256": problemTwoHash, "knowledge": []any{map[string]any{"knowledgePointId": "graphs", "weight": 1}}},
		},
	})
	configuration, configurationHash := canonicalRaw(t, map[string]any{
		"algorithm": "knowledge_mirt_v1", "knowledgeCatalogVersionId": "52", "accelerator": "cuda",
		"seed": 17, "epochs": 4, "patience": 2, "batchSize": 2, "learningRate": 0.01,
		"weightDecay": 0.001, "minTrainInteractions": 2, "minActorInteractions": 3, "minProblemInteractions": 1,
		"validation":     map[string]any{"minActors": 2, "minInteractions": 2, "minRelativeLogLossImprovement": 0},
		"pathPolicy":     map[string]any{"targetMastery": 0.8, "maxKnowledgeTargets": 2, "minSteps": 2, "maxSteps": 4, "problemsPerStep": 2, "targetSuccessProbability": 0.7},
		"rankingWeights": map[string]any{"knowledgeGap": 1, "successDistance": 1},
	})
	baseTime := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	return TrainingDataset{
		Analytics: AnalyticsProvenance{
			GenerationID: 73, HeadRevision: 9, InputManifest: analyticsManifest,
			InputManifestSHA256: analyticsHash, AlgorithmVersion: "analytics_v2",
			ConfigurationSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Configuration: TrainingConfiguration{
			VersionID: 41, Key: "recommendation.training.default", VersionNumber: 3,
			SchemaID: trainingConfigurationSchemaV2, Document: configuration,
			DocumentSHA256: configurationHash,
		},
		KnowledgeCatalog: KnowledgeCatalogConfiguration{
			VersionID: 52, Key: "recommendation.knowledge.default", VersionNumber: 2,
			SchemaID: knowledgeCatalogSchemaV1, Document: catalog, DocumentSHA256: catalogHash,
		},
		Students: []TrainingStudent{
			{ActorID: 29, Rating: "1299.5000", Metrics: json.RawMessage(`{"solved":7}`)},
			{ActorID: 11, Rating: "1000", Metrics: json.RawMessage(`{"solved":3}`)},
		},
		Problems: []TrainingProblem{
			{SnapshotID: 101, ProblemSetID: "1001", ProblemSetProblemID: "2001", SourceURL: "https://pintia.cn/problem-sets/1001", Platform: "pintia", ProblemID: "501", Title: "Problem A", ContentHTML: &problemOneHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
			{SnapshotID: 101, ProblemSetID: "1001", ProblemSetProblemID: "2002", SourceURL: "https://pintia.cn/problem-sets/1001", Platform: "pintia", ProblemID: "502", Title: "Problem B", ContentHTML: &problemTwoHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
			{SnapshotID: 102, ProblemSetID: "1002", ProblemSetProblemID: "3001", SourceURL: "https://pintia.cn/problem-sets/1002", Platform: "pintia", ProblemID: "501", Title: "Problem A", ContentHTML: &problemOneHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
			{SnapshotID: 102, ProblemSetID: "1002", ProblemSetProblemID: "3002", SourceURL: "https://pintia.cn/problem-sets/1002", Platform: "pintia", ProblemID: "502", Title: "Problem B", ContentHTML: &problemTwoHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
		},
		Observations: []TrainingObservation{
			testObservation(101, 11, "2001", "80", false, 2, baseTime),
			testObservation(101, 11, "2002", "100", true, 1, baseTime.Add(time.Minute)),
			testObservation(102, 11, "3001", "100", true, 3, baseTime.Add(2*time.Minute)),
			testObservation(101, 29, "2001", "100", true, 1, baseTime.Add(3*time.Minute)),
			testObservation(102, 29, "3002", "70", false, 2, baseTime.Add(4*time.Minute)),
			testObservation(102, 29, "3001", "90", false, 2, baseTime.Add(5*time.Minute)),
		},
	}
}

func testProblemFactHash(t *testing.T, problemID, title string, contentHTML *string, maxScore string, timeLimit, memoryLimit *int64) string {
	t.Helper()
	maxScoreJSON, err := canonicalNumber(maxScore)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashCanonicalValue(problemFactWire{
		Platform: "pintia", ProblemID: problemID, Title: title, ContentHTML: contentHTML,
		MaxScore: maxScoreJSON, TimeLimitMS: timeLimit, MemoryLimitBytes: memoryLimit,
	}, 5<<20, "test problem fact")
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func testObservation(snapshotID, actorID int64, problemSetProblemID, score string, passed bool, submissions int64, submittedAt time.Time) TrainingObservation {
	maxScore := "100"
	validSubmissions := submissions
	first := submittedAt.Add(-time.Duration(submissions-1) * time.Second)
	last := submittedAt
	return TrainingObservation{
		SnapshotID: snapshotID, ActorID: actorID, ProblemSetProblemID: problemSetProblemID,
		Score: &score, MaxScore: &maxScore, Passed: &passed, ValidSubmissionCount: &validSubmissions,
		SubmissionCount: submissions, FirstSubmittedAt: &first, LastSubmittedAt: &last,
	}
}

func testTrainingRun(bundle BuiltInputBundle) TrainingRun {
	return TrainingRun{
		DatabaseID:                     5,
		ID:                             "123e4567-e89b-42d3-a456-426614174100",
		SourceAnalyticsGenerationID:    73,
		SourceAnalyticsHeadRevision:    9,
		TrainingConfigurationVersionID: 41,
		KnowledgeCatalogVersionID:      52,
		BundleProtocol:                 TrainingBundleProtocolV2,
		InputManifest:                  bundle.Manifest,
		InputManifestSHA256:            bundle.ManifestSHA256,
		Status:                         RunRunning,
		AttemptCount:                   1,
	}
}

func canonicalRaw(t *testing.T, value any) (json.RawMessage, string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, digest, err := canonicaljson.Object(raw, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, digest
}

func canonicalObject(t *testing.T, value any) json.RawMessage {
	t.Helper()
	canonical, _ := canonicalRaw(t, value)
	return canonical
}
