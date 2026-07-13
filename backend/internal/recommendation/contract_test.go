package recommendation

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func TestFeatureSchemaIsCanonicalAndClosed(t *testing.T) {
	t.Parallel()
	if inferencemodel.MaximumKnowledgePoints != maximumKnowledgePoints {
		t.Fatalf("knowledge point maximum differs: model=%d catalog=%d", inferencemodel.MaximumKnowledgePoints, maximumKnowledgePoints)
	}
	document := FeatureSchemaDocument()
	canonical, digest, err := canonicaljson.Object(document, maximumManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(document) || digest != FeatureSchemaSHA256() ||
		!slices.Equal(ActorFeatureIDs(), actorFeatureIDs) || !slices.Equal(ProblemFeatureIDs(), problemFeatureIDs) {
		t.Fatalf("feature schema=%s digest=%s", document, digest)
	}
	var decoded featureSchemaWire
	if err := decodeClosed(document, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != FeatureSchemaV1 || !reflect.DeepEqual(decoded.ActorFeatures, actorFeatureDefinitions) ||
		!reflect.DeepEqual(decoded.ProblemFeatures, problemFeatureDefinitions) {
		t.Fatalf("decoded feature schema differs: %#v", decoded)
	}
	validateFeatureDefinitions(t, decoded.ActorFeatures, actorFeatureIDs)
	validateFeatureDefinitions(t, decoded.ProblemFeatures, problemFeatureIDs)
	ActorFeatureIDs()[0] = "mutated"
	if actorFeatureIDs[0] != "log1p_rating" {
		t.Fatal("feature IDs escaped as mutable state")
	}
}

func validateFeatureDefinitions(t *testing.T, definitions []featureDefinitionWire, expectedIDs []string) {
	t.Helper()
	byID := make(map[string]featureDefinitionWire, len(definitions))
	for index, definition := range definitions {
		if definition.ID != expectedIDs[index] || definition.Source.Protocol == "" || len(definition.Source.Fields) == 0 ||
			definition.Aggregation.Scope == "" || definition.Aggregation.Operation == "" || definition.Missing.Policy == "" ||
			definition.Transform.Operation == "" || definition.Domain.SourceType == "" || definition.Domain.OutputType == "" {
			t.Fatalf("feature definition %d is incomplete: %#v", index, definition)
		}
		if definition.Transform.Operation == "ratio" && definition.Domain.DenominatorZero != "return_zero" ||
			definition.Transform.Operation != "ratio" && definition.Domain.DenominatorZero != "not_applicable" {
			t.Fatalf("feature %q denominator-zero rule is inconsistent", definition.ID)
		}
		validateFeatureOutputDomain(t, definition)
		if _, duplicate := byID[definition.ID]; duplicate {
			t.Fatalf("feature %q is duplicated", definition.ID)
		}
		byID[definition.ID] = definition
	}
	for _, definition := range definitions {
		if definition.Missing.PairedFeatureID == nil {
			continue
		}
		paired, exists := byID[*definition.Missing.PairedFeatureID]
		if !exists || paired.Missing.PairedFeatureID == nil || *paired.Missing.PairedFeatureID != definition.ID {
			t.Fatalf("feature %q has an invalid missing-value pair", definition.ID)
		}
	}
}

func validateFeatureOutputDomain(t *testing.T, definition featureDefinitionWire) {
	t.Helper()
	if definition.Domain.OutputMinimum == nil || definition.Domain.OutputMaximum == nil {
		t.Fatalf("feature %q does not expose a closed output domain", definition.ID)
	}
	outputMinimum, minimumErr := strconv.ParseFloat(*definition.Domain.OutputMinimum, 64)
	outputMaximum, maximumErr := strconv.ParseFloat(*definition.Domain.OutputMaximum, 64)
	if minimumErr != nil || maximumErr != nil || !finite(outputMinimum) || !finite(outputMaximum) || outputMinimum > outputMaximum {
		t.Fatalf("feature %q output domain is invalid: %#v", definition.ID, definition.Domain)
	}
	switch definition.Transform.Operation {
	case "identity":
		assertFeatureBoundsFromSource(t, definition, func(value float64) float64 { return value })
	case "is_present":
		if outputMinimum != 0 || outputMaximum != 1 {
			t.Fatalf("feature %q presence domain = [%g,%g]", definition.ID, outputMinimum, outputMaximum)
		}
	case "ratio":
		if outputMinimum != 0 || outputMaximum != 1 {
			t.Fatalf("feature %q ratio domain = [%g,%g]", definition.ID, outputMinimum, outputMaximum)
		}
	case "log1p":
		assertFeatureBoundsFromSource(t, definition, math.Log1p)
	default:
		t.Fatalf("feature %q has unsupported transform %q", definition.ID, definition.Transform.Operation)
	}
}

func assertFeatureBoundsFromSource(t *testing.T, definition featureDefinitionWire, transform func(float64) float64) {
	t.Helper()
	if definition.Domain.SourceMinimum == nil || definition.Domain.SourceMaximum == nil {
		t.Fatalf("feature %q does not expose a closed source domain", definition.ID)
	}
	sourceMinimum, minimumErr := strconv.ParseFloat(*definition.Domain.SourceMinimum, 64)
	sourceMaximum, maximumErr := strconv.ParseFloat(*definition.Domain.SourceMaximum, 64)
	outputMinimum, outputMinimumErr := strconv.ParseFloat(*definition.Domain.OutputMinimum, 64)
	outputMaximum, outputMaximumErr := strconv.ParseFloat(*definition.Domain.OutputMaximum, 64)
	if minimumErr != nil || maximumErr != nil || outputMinimumErr != nil || outputMaximumErr != nil ||
		outputMinimum != transform(sourceMinimum) || outputMaximum != transform(sourceMaximum) {
		t.Fatalf("feature %q output domain does not equal its transformed source extrema", definition.ID)
	}
}

type featureExtractionVectors struct {
	Schema     string `json:"schema"`
	ActorCases []struct {
		ID    string `json:"id"`
		Input struct {
			Rating         string          `json:"rating"`
			StudentMetrics json.RawMessage `json:"studentMetrics"`
		} `json:"input"`
		Expected []featureVectorValue `json:"expected"`
	} `json:"actorCases"`
	ProblemCases []struct {
		ID       string               `json:"id"`
		Rows     []problemVectorRow   `json:"rows"`
		Expected []featureVectorValue `json:"expected"`
	} `json:"problemCases"`
}

type featureVectorValue struct {
	FeatureID string  `json:"featureId"`
	Value     float64 `json:"value"`
}

type problemVectorRow struct {
	SnapshotID          int64           `json:"snapshotId"`
	ProblemSetID        string          `json:"problemSetId"`
	ProblemSetProblemID string          `json:"problemSetProblemId"`
	SourceURL           string          `json:"sourceUrl"`
	Platform            string          `json:"platform"`
	ProblemID           string          `json:"problemId"`
	Title               string          `json:"title"`
	ContentHTML         *string         `json:"contentHtml"`
	MaxScore            *string         `json:"maxScore"`
	TimeLimitMS         *int64          `json:"timeLimitMs"`
	MemoryLimitBytes    *int64          `json:"memoryLimitBytes"`
	Metrics             json.RawMessage `json:"metrics"`
}

func TestFeatureExtractionVectorsMatchImplementation(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(recommendationContractDirectory(t), "vectors", "ascendany.recommendation.feature-extraction-vectors.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, maximumManifestBytes)
	if err != nil || string(canonical) != string(raw) {
		t.Fatalf("feature extraction vectors are not canonical: %v", err)
	}
	var vectors featureExtractionVectors
	if err := decodeClosed(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Schema != "ascendany.recommendation.feature-extraction-vectors.v1" || len(vectors.ActorCases) == 0 || len(vectors.ProblemCases) < 2 {
		t.Fatalf("feature extraction vector envelope is incomplete: %#v", vectors)
	}
	for _, test := range vectors.ActorCases {
		test := test
		t.Run("actor/"+test.ID, func(t *testing.T) {
			features, _, err := buildActorFeatures(test.Input.Rating, test.Input.StudentMetrics)
			if err != nil {
				t.Fatal(err)
			}
			assertFeatureVector(t, features, test.Expected)
		})
	}
	for _, test := range vectors.ProblemCases {
		test := test
		t.Run("problem/"+test.ID, func(t *testing.T) {
			rows := make([]problemRow, len(test.Rows))
			for index, encoded := range test.Rows {
				rows[index] = problemRow{
					SnapshotID: encoded.SnapshotID, ProblemSetID: encoded.ProblemSetID, ProblemSetProblemID: encoded.ProblemSetProblemID,
					SourceURL: encoded.SourceURL, Platform: encoded.Platform, ProblemID: encoded.ProblemID, Title: encoded.Title,
					ContentHTML: encoded.ContentHTML, MaxScore: encoded.MaxScore, TimeLimitMS: encoded.TimeLimitMS,
					MemoryLimitBytes: encoded.MemoryLimitBytes, MetricsJSON: encoded.Metrics,
				}
			}
			fact, err := buildProblemFact(rows[0])
			if err != nil {
				t.Fatal(err)
			}
			catalog := knowledgeCatalog{
				Points: []knowledgePoint{{ID: "arrays"}},
				Assignments: []problemAssignment{{
					Platform: rows[0].Platform, ProblemID: rows[0].ProblemID, ProblemFactSHA256: fact.ProblemFactSHA256,
					Knowledge: []catalogWeight{{KnowledgePointID: "arrays", Weight: 1, raw: "1"}},
				}},
			}
			candidates, err := buildCandidates(rows, catalog, []string{"arrays"}, map[string]struct{}{})
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 1 {
				t.Fatalf("candidate count = %d", len(candidates))
			}
			assertFeatureVector(t, candidates[0].Features, test.Expected)
		})
	}
}

func assertFeatureVector(t *testing.T, actual []inferencemodel.FeatureValue, expected []featureVectorValue) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("feature count = %d, want %d", len(actual), len(expected))
	}
	for index := range actual {
		if actual[index].FeatureID != expected[index].FeatureID || math.Abs(actual[index].Value-expected[index].Value) > 1e-15 {
			t.Fatalf("feature %d = %#v, want %#v", index, actual[index], expected[index])
		}
	}
}

func recommendationContractDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate recommendation contract test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "recommendation"))
}

func TestConfigurationPublicationContractAcceptsOnlyStrictCatalog(t *testing.T) {
	t.Parallel()
	validator := ConfigurationPublicationContract{}
	valid := testCatalogDocument(t, []any{})
	if err := validator.ValidateRecommendationDocument(configuration.KindKnowledgeCatalog, KnowledgeCatalogSchemaV1, valid); err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateRecommendationDocument(configuration.KindPrompt, KnowledgeCatalogSchemaV1, valid); err == nil {
		t.Fatal("non-catalog kind was accepted")
	}
	var root map[string]any
	if err := json.Unmarshal(valid, &root); err != nil {
		t.Fatal(err)
	}
	root["unknown"] = true
	if _, _, _, err := parseKnowledgeCatalog(testCanonical(t, root)); err == nil {
		t.Fatal("unknown catalog field was accepted")
	}
}

func TestCatalogCoverageDifferenceRequiresExactProblemFactSet(t *testing.T) {
	t.Parallel()
	currentHash := strings.Repeat("a", 64)
	changedHash := strings.Repeat("b", 64)
	document := testCatalogDocument(t, []any{map[string]any{
		"platform": "pintia", "problemId": "123", "problemFactSha256": changedHash,
		"knowledge": []any{map[string]any{"knowledgePointId": "arrays", "weight": "1"}},
	}})
	catalog, _, _, err := parseKnowledgeCatalog(document)
	if err != nil {
		t.Fatal(err)
	}
	currentKey := pintiaProblemKey("123", currentHash)
	changedKey := pintiaProblemKey("123", changedHash)
	missing, dangling := catalogCoverageDifference(catalog, []ReviewProblemCandidate{{
		ProblemKey: currentKey, Platform: "pintia", ProblemID: "123", ProblemFactSHA256: currentHash,
	}})
	if !slices.Equal(missing, []string{currentKey}) || !slices.Equal(dangling, []string{changedKey}) {
		t.Fatalf("missing=%v dangling=%v", missing, dangling)
	}
}

func TestKnowledgeCatalogRejectsCyclesAndInexactWeightSum(t *testing.T) {
	t.Parallel()
	cycle := map[string]any{
		"taxonomyId": "core",
		"knowledgePoints": []any{
			map[string]any{"id": "arrays", "label": "Arrays", "description": "A", "prerequisiteIds": []any{"graphs"}},
			map[string]any{"id": "graphs", "label": "Graphs", "description": "G", "prerequisiteIds": []any{"arrays"}},
		},
		"problemAssignments": []any{},
	}
	if _, _, _, err := parseKnowledgeCatalog(testCanonical(t, cycle)); err == nil {
		t.Fatal("cyclic knowledge catalog was accepted")
	}
	badWeight := []any{map[string]any{
		"platform": "pintia", "problemId": "123", "problemFactSha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"knowledge": []any{
			map[string]any{"knowledgePointId": "arrays", "weight": "0.4"},
			map[string]any{"knowledgePointId": "graphs", "weight": "0.5"},
		},
	}}
	if _, _, _, err := parseKnowledgeCatalog(testCatalogDocument(t, badWeight)); err == nil {
		t.Fatal("inexact knowledge weight sum was accepted")
	}
}

func TestKnowledgeCatalogPreservesCanonicalDecimalWeightContract(t *testing.T) {
	t.Parallel()
	assignments := []any{map[string]any{
		"platform": "pintia", "problemId": "123", "problemFactSha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"knowledge": []any{
			map[string]any{"knowledgePointId": "arrays", "weight": "0.3333333333333333333333333333333333"},
			map[string]any{"knowledgePointId": "graphs", "weight": "0.6666666666666666666666666666666667"},
		},
	}}
	catalog, _, _, err := parseKnowledgeCatalog(testCatalogDocument(t, assignments))
	if err != nil {
		t.Fatal(err)
	}
	weights := catalog.Assignments[0].Knowledge
	if weights[0].raw != "0.3333333333333333333333333333333333" ||
		weights[1].raw != "0.6666666666666666666666666666666667" {
		t.Fatalf("canonical weight strings changed: %#v", weights)
	}

	for _, invalid := range []any{json.Number("1"), "0", "0.50", "1e0", "1.0"} {
		assignments[0].(map[string]any)["knowledge"] = []any{
			map[string]any{"knowledgePointId": "arrays", "weight": invalid},
		}
		if _, _, _, parseErr := parseKnowledgeCatalog(testCatalogDocument(t, assignments)); parseErr == nil {
			t.Fatalf("noncanonical catalog weight %#v was accepted", invalid)
		}
	}
}

func TestFullE2ECatalogAssignmentsMatchSanitizedExporterFacts(t *testing.T) {
	t.Parallel()
	type programmingConfig struct {
		TimeLimit   int64 `json:"timeLimit"`
		MemoryLimit int64 `json:"memoryLimit"`
	}
	type sourceProblem struct {
		ID            string      `json:"id"`
		ProblemID     string      `json:"problemId"`
		ProblemSetID  string      `json:"problemSetId"`
		Title         string      `json:"title"`
		Content       string      `json:"content"`
		Score         json.Number `json:"score"`
		ProblemConfig struct {
			Programming programmingConfig `json:"programmingProblemConfig"`
		} `json:"problemConfig"`
	}
	var source struct {
		ProblemSetID string `json:"problemSetId"`
		SourceURL    string `json:"sourceUrl"`
		Problems     struct {
			Items []sourceProblem `json:"items"`
		} `json:"problems"`
	}
	repositoryRoot := filepath.Clean(filepath.Join(recommendationContractDirectory(t), "..", ".."))
	sourceBytes, err := os.ReadFile(filepath.Join(repositoryRoot, "tools", "pintia-exporter-extension", "tests", "fixtures", "sanitized-source-shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sourceBytes, &source); err != nil {
		t.Fatal(err)
	}
	catalogBytes, err := os.ReadFile(filepath.Join(recommendationContractDirectory(t), "fixtures", "e2e-test-only.knowledge-catalog.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, canonical, _, err := parseKnowledgeCatalog(catalogBytes)
	if err != nil || string(canonical) != string(catalogBytes) {
		t.Fatalf("parse full E2E catalog: canonical=%s err=%v", canonical, err)
	}
	if len(source.Problems.Items) != 2 || len(catalog.Assignments) != len(source.Problems.Items) {
		t.Fatalf("source problems=%d catalog assignments=%d", len(source.Problems.Items), len(catalog.Assignments))
	}
	for index, item := range source.Problems.Items {
		maxScore := string(item.Score)
		content := item.Content
		timeLimit := item.ProblemConfig.Programming.TimeLimit
		memoryLimit := item.ProblemConfig.Programming.MemoryLimit * 1024
		fact, err := buildProblemFact(problemRow{
			SnapshotID: 1, ProblemSetID: source.ProblemSetID, ProblemSetProblemID: item.ID,
			SourceURL: source.SourceURL, Platform: "pintia", ProblemID: item.ProblemID, Title: item.Title,
			ContentHTML: &content, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit,
		})
		if err != nil {
			t.Fatalf("build sanitized problem fact %q: %v", item.ProblemID, err)
		}
		assignment := catalog.Assignments[index]
		if assignment.Platform != "pintia" || assignment.ProblemID != item.ProblemID ||
			assignment.ProblemFactSHA256 != fact.ProblemFactSHA256 {
			t.Fatalf("catalog assignment %d does not bind sanitized fact %+v: %+v", index, fact, assignment)
		}
	}
}
