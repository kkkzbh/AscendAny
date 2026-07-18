package recommendation

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
)

func testAnalyticsConfig(t *testing.T) analytics.ParsedConfig {
	t.Helper()
	configuration, err := analytics.ParseConfig([]byte(`{
  "algorithmVersion": "ascendany_analytics_v1",
  "acceptedVerdicts": ["ACCEPTED"],
  "winsor": {"low": 0.05, "high": 0.95},
  "halfLifeDays": {"knowledge": 45, "accuracy": 21, "quality": 45, "flexibility": 21, "proficiency": 21},
  "rating": {"initial": 800, "binarySearchMin": -2000, "binarySearchMax": 8000, "binarySearchSteps": 30}
}`))
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func testCatalogDocument(t *testing.T, assignments []any) json.RawMessage {
	t.Helper()
	return testCanonical(t, map[string]any{
		"taxonomyId": "core",
		"knowledgePoints": []any{
			map[string]any{"id": "arrays", "label": "Arrays", "description": "Array fundamentals", "prerequisiteIds": []any{}},
			map[string]any{"id": "graphs", "label": "Graphs", "description": "Graph fundamentals", "prerequisiteIds": []any{}},
		},
		"problemAssignments": assignments,
	})
}

func testModel(t *testing.T, catalogDigest string) (*inferencemodel.Model, modelrelease.Binding) {
	t.Helper()
	zeroActor := numberVector(len(actorFeatureIDs), "0")
	oneActor := numberVector(len(actorFeatureIDs), "1")
	zeroProblem := numberVector(len(problemFeatureIDs), "0")
	oneProblem := numberVector(len(problemFeatureIDs), "1")
	parameters := map[string]any{
		"actorNormalization":   map[string]any{"means": zeroActor, "scales": oneActor},
		"problemNormalization": map[string]any{"means": zeroProblem, "scales": oneProblem},
		"knowledgeParameters": []any{
			map[string]any{"knowledgePointId": "arrays", "actorFeatureWeights": zeroActor, "bias": json.Number("-1")},
			map[string]any{"knowledgePointId": "graphs", "actorFeatureWeights": zeroActor, "bias": json.Number("-2")},
		},
		"problemFeatureWeights": zeroProblem,
		"difficultyBias":        json.Number("0"),
		"discrimination":        json.Number("1"),
	}
	actorValues := make([]any, len(actorFeatureIDs))
	for index, id := range actorFeatureIDs {
		actorValues[index] = map[string]any{"featureId": id, "value": json.Number("0")}
	}
	problemValues := make([]any, len(problemFeatureIDs))
	for index, id := range problemFeatureIDs {
		problemValues[index] = map[string]any{"featureId": id, "value": json.Number("0")}
	}
	golden := []any{map[string]any{
		"id": "runtime",
		"input": map[string]any{
			"featureSchemaSha256": FeatureSchemaSHA256(), "knowledgeCatalogSha256": catalogDigest,
			"actorFeatures": actorValues, "problemFeatures": problemValues,
			"knowledgeWeights": []any{
				map[string]any{"knowledgePointId": "arrays", "weight": "0.5"},
				map[string]any{"knowledgePointId": "graphs", "weight": "0.5"},
			},
		},
		"expected": map[string]any{
			"probability": json.Number(floatText(sigmoidTest(-1.5))),
			"knowledgeMastery": []any{
				map[string]any{"knowledgePointId": "arrays", "probability": json.Number(floatText(sigmoidTest(-1)))},
				map[string]any{"knowledgePointId": "graphs", "probability": json.Number(floatText(sigmoidTest(-2)))},
			},
		},
	}}
	parameterRaw := testCanonicalValue(t, parameters)
	goldenRaw := testCanonicalValue(t, golden)
	manifest := map[string]any{
		"modelId": "123e4567-e89b-42d3-a456-426614174000", "purpose": string(inferencemodel.PurposeAcceptanceTest),
		"trainedAt": "2026-07-13T12:34:56.123456Z",
		"algorithm": inferencemodel.Algorithm, "inferenceContract": inferencemodel.InferenceContract,
		"trainingProvenanceSha256": strings.Repeat("1", 64), "featureSchemaSha256": FeatureSchemaSHA256(),
		"knowledgeCatalogSha256": catalogDigest, "parameterSha256": sha256Bytes(parameterRaw),
		"goldenVectorsSha256": sha256Bytes(goldenRaw), "actorFeatureIds": stringAny(actorFeatureIDs),
		"problemFeatureIds": stringAny(problemFeatureIDs), "knowledgePointIds": []any{"arrays", "graphs"},
	}
	artifact := testCanonical(t, map[string]any{
		"schema": inferencemodel.Schema, "manifest": manifest, "parameters": parameters, "goldenVectors": golden,
	})
	model, err := inferencemodel.Parse(artifact)
	if err != nil {
		t.Fatal(err)
	}
	releaseManifest := testCanonical(t, map[string]any{
		"schema":  inferencemodel.Schema,
		"modelId": manifest["modelId"], "purpose": manifest["purpose"],
		"trainedAt": manifest["trainedAt"], "algorithm": manifest["algorithm"],
		"inferenceContract": manifest["inferenceContract"], "trainingProvenanceSha256": manifest["trainingProvenanceSha256"],
		"featureSchemaSha256": manifest["featureSchemaSha256"], "knowledgeCatalogSha256": manifest["knowledgeCatalogSha256"],
		"parameterSha256": manifest["parameterSha256"], "goldenVectorsSha256": manifest["goldenVectorsSha256"],
		"actorFeatureIds": manifest["actorFeatureIds"], "problemFeatureIds": manifest["problemFeatureIds"],
		"knowledgePointIds": manifest["knowledgePointIds"],
	})
	return model, modelrelease.Binding{
		ReleaseID: 7, HeadRevision: 3, ModelPurpose: inferencemodel.PurposeAcceptanceTest,
		ManifestJSON: releaseManifest, ManifestSHA256: sha256Bytes(releaseManifest),
	}
}

func testCanonical(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, inferencemodel.MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func testCanonicalValue(t *testing.T, value any) json.RawMessage {
	t.Helper()
	container := testCanonical(t, map[string]any{"value": value})
	var wire struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(container, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Value
}

func numberVector(count int, value string) []any {
	result := make([]any, count)
	for index := range result {
		result[index] = json.Number(value)
	}
	return result
}

func stringAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func sigmoidTest(value float64) float64 {
	if value >= 0 {
		return 1 / (1 + math.Exp(-value))
	}
	exponential := math.Exp(value)
	return exponential / (1 + exponential)
}

func floatText(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
