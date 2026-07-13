package inferencemodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const (
	testFeatureSchemaSHA256    = "2222222222222222222222222222222222222222222222222222222222222222"
	testKnowledgeCatalogSHA256 = "3333333333333333333333333333333333333333333333333333333333333333"
)

func TestParseSHA256AndEvaluateGoldenModel(t *testing.T) {
	t.Parallel()
	raw := validArtifact(t)
	model, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	wantDigest := hex.EncodeToString(digest[:])
	if model.SHA256() != wantDigest {
		t.Fatalf("Model.SHA256() = %q, want %q", model.SHA256(), wantDigest)
	}
	gotDigest, err := SHA256(raw)
	if err != nil || gotDigest != wantDigest {
		t.Fatalf("SHA256() = %q, %v, want %q", gotDigest, err, wantDigest)
	}

	manifest := model.Manifest()
	if manifest.ModelID != "123e4567-e89b-42d3-a456-426614174000" ||
		manifest.Algorithm != Algorithm || manifest.InferenceContract != InferenceContract {
		t.Fatalf("Manifest() = %#v", manifest)
	}
	manifest.ActorFeatureIDs[0] = "mutated"
	if model.Manifest().ActorFeatureIDs[0] != "practice" {
		t.Fatal("Manifest returned mutable model-owned identities")
	}

	result, err := Evaluate(model, validInput())
	if err != nil {
		t.Fatal(err)
	}
	assertClose(t, "probability", result.Probability, 0.15709546888545273, 1e-15)
	if len(result.KnowledgeMastery) != 2 || result.KnowledgeMastery[0].KnowledgePointID != "arrays" ||
		result.KnowledgeMastery[1].KnowledgePointID != "graphs" {
		t.Fatalf("KnowledgeMastery = %#v", result.KnowledgeMastery)
	}
	assertClose(t, "arrays mastery", result.KnowledgeMastery[0].Probability, 0.8320183851339245, 1e-15)
	assertClose(t, "graphs mastery", result.KnowledgeMastery[1].Probability, 0.574442516811659, 1e-15)
}

func TestParseRejectsNonCanonicalClosedOrOversizedArtifacts(t *testing.T) {
	t.Parallel()
	valid := validArtifact(t)
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "trailing newline", raw: append(append([]byte(nil), valid...), '\n')},
		{name: "duplicate key", raw: []byte(`{"schema":"a","schema":"b"}`)},
		{name: "root array", raw: []byte(`[]`)},
		{name: "oversized", raw: bytes.Repeat([]byte{' '}, MaximumArtifactBytes+1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(test.raw); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Parse() error = %v", err)
			}
		})
	}
}

func TestParseRejectsManifestAndDigestViolations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
		rebind bool
	}{
		{name: "unknown root field", mutate: func(root map[string]any) { root["unexpected"] = true }},
		{name: "missing manifest field", mutate: func(root map[string]any) { delete(manifestMap(root), "featureSchemaSha256") }},
		{name: "missing purpose", mutate: func(root map[string]any) { delete(manifestMap(root), "purpose") }},
		{name: "invalid purpose", mutate: func(root map[string]any) { manifestMap(root)["purpose"] = "test" }},
		{name: "invalid UUID", mutate: func(root map[string]any) { manifestMap(root)["modelId"] = "123e4567-e89b-12d3-a456-426614174000" }},
		{name: "non-UTC timestamp", mutate: func(root map[string]any) { manifestMap(root)["trainedAt"] = "2026-07-13T12:34:56+00:00" }},
		{name: "timestamp outside PostgreSQL common-era range", mutate: func(root map[string]any) { manifestMap(root)["trainedAt"] = "0000-01-01T00:00:00Z" }},
		{name: "noncanonical timestamp", mutate: func(root map[string]any) { manifestMap(root)["trainedAt"] = "2026-07-13T12:34:56.000Z" }},
		{name: "timestamp exceeds PostgreSQL precision", mutate: func(root map[string]any) { manifestMap(root)["trainedAt"] = "2026-07-13T12:34:56.1234567Z" }},
		{name: "timestamp has nanosecond precision", mutate: func(root map[string]any) { manifestMap(root)["trainedAt"] = "2026-07-13T12:34:56.123456789Z" }},
		{name: "wrong algorithm", mutate: func(root map[string]any) { manifestMap(root)["algorithm"] = "other" }},
		{name: "wrong inference contract", mutate: func(root map[string]any) {
			manifestMap(root)["inferenceContract"] = "ascendany.recommendation.inference.v2"
		}},
		{name: "uppercase digest", mutate: func(root map[string]any) { manifestMap(root)["featureSchemaSha256"] = strings.Repeat("A", 64) }},
		{name: "duplicate feature identity", mutate: func(root map[string]any) { manifestMap(root)["actorFeatureIds"] = []any{"practice", "practice"} }},
		{name: "invalid identity", mutate: func(root map[string]any) { manifestMap(root)["knowledgePointIds"] = []any{"arrays", "bad identity"} }},
		{name: "uppercase knowledge identity", mutate: func(root map[string]any) { manifestMap(root)["knowledgePointIds"] = []any{"Arrays", "graphs"} }},
		{name: "unsorted knowledge identities", mutate: func(root map[string]any) { manifestMap(root)["knowledgePointIds"] = []any{"graphs", "arrays"} }},
		{name: "parameter digest mismatch", mutate: func(root map[string]any) { parametersMap(root)["difficultyBias"] = json.Number("0.4") }},
		{name: "golden digest mismatch", mutate: func(root map[string]any) { goldenExpected(root)["probability"] = json.Number("0.2") }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := decodeArtifact(t, validArtifact(t))
			test.mutate(root)
			if test.rebind {
				rebindDigests(t, root)
			}
			if _, err := Parse(canonicalArtifact(t, root)); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Parse() error = %v", err)
			}
		})
	}
}

func TestParseRejectsKnowledgePointCountAboveCatalogContract(t *testing.T) {
	t.Parallel()
	root := decodeArtifact(t, validArtifact(t))
	identities := make([]any, MaximumKnowledgePoints+1)
	for index := range identities {
		identities[index] = fmt.Sprintf("k%04d", index)
	}
	manifestMap(root)["knowledgePointIds"] = identities
	if _, err := Parse(canonicalArtifact(t, root)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseAcceptsCanonicalPostgreSQLTimestampPrecision(t *testing.T) {
	t.Parallel()
	for _, trainedAt := range []string{
		"2026-07-13T12:34:56Z",
		"2026-07-13T12:34:56.1Z",
		"2026-07-13T12:34:56.123456Z",
	} {
		trainedAt := trainedAt
		t.Run(trainedAt, func(t *testing.T) {
			t.Parallel()
			root := decodeArtifact(t, validArtifact(t))
			manifestMap(root)["trainedAt"] = trainedAt
			model, err := Parse(canonicalArtifact(t, root))
			if err != nil {
				t.Fatal(err)
			}
			if model.Manifest().TrainedAt != trainedAt {
				t.Fatalf("trainedAt = %q", model.Manifest().TrainedAt)
			}
		})
	}
}

func TestParseRejectsParameterShapeAndBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "actor residual compatibility field",
			mutate: func(root map[string]any) {
				parametersMap(root)["actorResiduals"] = []any{}
			},
		},
		{
			name: "unknown nested field",
			mutate: func(root map[string]any) {
				parametersMap(root)["actorNormalization"].(map[string]any)["fallbackScale"] = json.Number("1")
			},
		},
		{
			name: "normalization count",
			mutate: func(root map[string]any) {
				parametersMap(root)["actorNormalization"].(map[string]any)["means"] = []any{json.Number("10")}
			},
		},
		{
			name: "zero scale",
			mutate: func(root map[string]any) {
				parametersMap(root)["actorNormalization"].(map[string]any)["scales"].([]any)[0] = json.Number("0")
			},
		},
		{
			name: "negative scale",
			mutate: func(root map[string]any) {
				parametersMap(root)["problemNormalization"].(map[string]any)["scales"].([]any)[0] = json.Number("-0.5")
			},
		},
		{
			name: "parameter over absolute bound",
			mutate: func(root map[string]any) {
				parametersMap(root)["problemFeatureWeights"].([]any)[0] = json.Number("100.0000000001")
			},
		},
		{
			name:   "positive discrimination",
			mutate: func(root map[string]any) { parametersMap(root)["discrimination"] = json.Number("0") },
		},
		{
			name: "knowledge count",
			mutate: func(root map[string]any) {
				parametersMap(root)["knowledgeParameters"] = parametersMap(root)["knowledgeParameters"].([]any)[:1]
			},
		},
		{
			name: "knowledge order",
			mutate: func(root map[string]any) {
				parametersMap(root)["knowledgeParameters"].([]any)[0].(map[string]any)["knowledgePointId"] = "graphs"
			},
		},
		{
			name: "problem weight count",
			mutate: func(root map[string]any) {
				parametersMap(root)["problemFeatureWeights"] = []any{json.Number("1.2")}
			},
		},
		{
			name:   "float64 underflow",
			mutate: func(root map[string]any) { parametersMap(root)["difficultyBias"] = json.Number("1e-400") },
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := decodeArtifact(t, validArtifact(t))
			test.mutate(root)
			rebindDigests(t, root)
			if _, err := Parse(canonicalArtifact(t, root)); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Parse() error = %v", err)
			}
		})
	}
}

func TestParseRejectsGoldenShapeIdentityAndSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "empty vectors", mutate: func(root map[string]any) { root["goldenVectors"] = []any{} }},
		{name: "unknown field", mutate: func(root map[string]any) { goldenInput(root)["unexpected"] = true }},
		{name: "feature schema digest", mutate: func(root map[string]any) { goldenInput(root)["featureSchemaSha256"] = strings.Repeat("4", 64) }},
		{name: "catalog digest", mutate: func(root map[string]any) { goldenInput(root)["knowledgeCatalogSha256"] = strings.Repeat("5", 64) }},
		{name: "actor feature order", mutate: func(root map[string]any) {
			goldenInput(root)["actorFeatures"].([]any)[0].(map[string]any)["featureId"] = "accuracy"
		}},
		{name: "problem feature count", mutate: func(root map[string]any) {
			goldenInput(root)["problemFeatures"] = goldenInput(root)["problemFeatures"].([]any)[:1]
		}},
		{name: "knowledge order", mutate: func(root map[string]any) {
			goldenInput(root)["knowledgeWeights"].([]any)[0].(map[string]any)["knowledgePointId"] = "graphs"
		}},
		{name: "negative knowledge weight", mutate: func(root map[string]any) {
			goldenInput(root)["knowledgeWeights"].([]any)[0].(map[string]any)["weight"] = json.Number("-0.1")
		}},
		{name: "knowledge weight sum", mutate: func(root map[string]any) {
			goldenInput(root)["knowledgeWeights"].([]any)[0].(map[string]any)["weight"] = json.Number("0.5")
		}},
		{name: "mastery order", mutate: func(root map[string]any) {
			goldenExpected(root)["knowledgeMastery"].([]any)[0].(map[string]any)["knowledgePointId"] = "graphs"
		}},
		{name: "expected probability mismatch", mutate: func(root map[string]any) { goldenExpected(root)["probability"] = json.Number("0.2") }},
		{name: "expected mastery mismatch", mutate: func(root map[string]any) {
			goldenExpected(root)["knowledgeMastery"].([]any)[0].(map[string]any)["probability"] = json.Number("0.8")
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := decodeArtifact(t, validArtifact(t))
			test.mutate(root)
			rebindDigests(t, root)
			if _, err := Parse(canonicalArtifact(t, root)); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Parse() error = %v", err)
			}
		})
	}
}

func TestParseUsesFixedGoldenTolerance(t *testing.T) {
	t.Parallel()
	within := decodeArtifact(t, validArtifact(t))
	goldenExpected(within)["probability"] = json.Number("0.15709546888595273")
	rebindDigests(t, within)
	if _, err := Parse(canonicalArtifact(t, within)); err != nil {
		t.Fatalf("difference within tolerance was rejected: %v", err)
	}

	outside := decodeArtifact(t, validArtifact(t))
	goldenExpected(outside)["probability"] = json.Number("0.15709546888745273")
	rebindDigests(t, outside)
	if _, err := Parse(canonicalArtifact(t, outside)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("difference outside tolerance error = %v", err)
	}
}

func TestEvaluateRejectsInvalidRuntimeInput(t *testing.T) {
	t.Parallel()
	model, err := Parse(validArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Input)
	}{
		{name: "feature schema digest", mutate: func(input *Input) { input.FeatureSchemaSHA256 = strings.Repeat("4", 64) }},
		{name: "catalog digest", mutate: func(input *Input) { input.KnowledgeCatalogSHA256 = strings.Repeat("5", 64) }},
		{name: "actor count", mutate: func(input *Input) { input.ActorFeatures = input.ActorFeatures[:1] }},
		{name: "actor order", mutate: func(input *Input) { input.ActorFeatures[0].FeatureID = "accuracy" }},
		{name: "actor NaN", mutate: func(input *Input) { input.ActorFeatures[0].Value = math.NaN() }},
		{name: "problem infinity", mutate: func(input *Input) { input.ProblemFeatures[0].Value = math.Inf(1) }},
		{name: "knowledge identity", mutate: func(input *Input) { input.KnowledgeWeights[0].KnowledgePointID = "graphs" }},
		{name: "negative weight", mutate: func(input *Input) { input.KnowledgeWeights[0].Weight = -0.1 }},
		{name: "weight over one", mutate: func(input *Input) { input.KnowledgeWeights[0].Weight = 1.1 }},
		{name: "weight sum", mutate: func(input *Input) { input.KnowledgeWeights[0].Weight = 0.5 }},
		{name: "normalization overflow", mutate: func(input *Input) { input.ProblemFeatures[0].Value = math.MaxFloat64 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			test.mutate(&input)
			if _, err := model.Evaluate(input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Evaluate() error = %v", err)
			}
		})
	}
	if _, err := Evaluate(nil, validInput()); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Evaluate(nil) error = %v", err)
	}
}

func TestValidateNumericEnvelopeCoversDomainsOutsideGoldenVectors(t *testing.T) {
	t.Parallel()
	model, err := Parse(validArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	actor := []FeatureDomain{
		{FeatureID: "practice", Minimum: 0, Maximum: 100},
		{FeatureID: "accuracy", Minimum: 0, Maximum: 1},
	}
	problem := []FeatureDomain{
		{FeatureID: "difficulty", Minimum: 0, Maximum: 100},
		{FeatureID: "novelty", Minimum: 0, Maximum: 100},
	}
	if err := model.ValidateNumericEnvelope(actor, problem); err != nil {
		t.Fatalf("bounded production-like envelope was rejected: %v", err)
	}
	actor[0].Maximum = math.MaxFloat64
	if err := model.ValidateNumericEnvelope(actor, problem); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("overflowing envelope error = %v", err)
	}
}

func TestValidateNumericEnvelopeRejectsUnsafeModelOperations(t *testing.T) {
	t.Parallel()
	actorRanges := []FeatureDomain{
		{FeatureID: "practice", Minimum: 0, Maximum: 100},
		{FeatureID: "accuracy", Minimum: 0, Maximum: 1},
	}
	problemRanges := []FeatureDomain{
		{FeatureID: "difficulty", Minimum: 0, Maximum: 100},
		{FeatureID: "novelty", Minimum: 0, Maximum: 100},
	}
	safe, err := Parse(validArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := safe.ValidateNumericEnvelope(actorRanges, problemRanges); err != nil {
		t.Fatalf("safe model envelope: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Model)
	}{
		{
			name: "normalization",
			mutate: func(model *Model) {
				model.actorNormalization.scales[0] = math.SmallestNonzeroFloat64
			},
		},
		{
			name: "dot product",
			mutate: func(model *Model) {
				model.actorNormalization.scales[0] = 1e-305
				for index := range model.knowledge {
					model.knowledge[index].actorFeatureWeights[0] = 100
				}
			},
		},
		{
			name: "final logit",
			mutate: func(model *Model) {
				model.actorNormalization.scales[0] = 1e-303
				model.discrimination = 100
				for index := range model.knowledge {
					model.knowledge[index].actorFeatureWeights[0] = 100
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, err := Parse(validArtifact(t))
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(model)
			if err := model.ValidateNumericEnvelope(actorRanges, problemRanges); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("ValidateNumericEnvelope() error = %v", err)
			}
		})
	}
}

func TestValidateNumericEnvelopeRejectsDomainContractDrift(t *testing.T) {
	t.Parallel()
	model, err := Parse(validArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	actorRanges := []FeatureDomain{
		{FeatureID: "wrong", Minimum: 0, Maximum: 100},
		{FeatureID: "accuracy", Minimum: 0, Maximum: 1},
	}
	problemRanges := []FeatureDomain{
		{FeatureID: "difficulty", Minimum: 0, Maximum: 100},
		{FeatureID: "novelty", Minimum: 1, Maximum: 0},
	}
	if err := model.ValidateNumericEnvelope(actorRanges, problemRanges); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("ValidateNumericEnvelope() error = %v", err)
	}
}

func validInput() Input {
	return Input{
		FeatureSchemaSHA256:    testFeatureSchemaSHA256,
		KnowledgeCatalogSHA256: testKnowledgeCatalogSHA256,
		ActorFeatures: []FeatureValue{
			{FeatureID: "practice", Value: 12},
			{FeatureID: "accuracy", Value: 0.75},
		},
		ProblemFeatures: []FeatureValue{
			{FeatureID: "difficulty", Value: 2},
			{FeatureID: "novelty", Value: 4},
		},
		KnowledgeWeights: []KnowledgeWeight{
			{KnowledgePointID: "arrays", Weight: 0.6},
			{KnowledgePointID: "graphs", Weight: 0.4},
		},
	}
}

func validArtifact(t *testing.T) []byte {
	t.Helper()
	parameters := map[string]any{
		"actorNormalization": map[string]any{
			"means":  []any{json.Number("10"), json.Number("0.5")},
			"scales": []any{json.Number("2"), json.Number("0.25")},
		},
		"problemNormalization": map[string]any{
			"means":  []any{json.Number("1"), json.Number("2")},
			"scales": []any{json.Number("0.5"), json.Number("2")},
		},
		"knowledgeParameters": []any{
			map[string]any{"knowledgePointId": "arrays", "actorFeatureWeights": []any{json.Number("0.5"), json.Number("1")}, "bias": json.Number("0.1")},
			map[string]any{"knowledgePointId": "graphs", "actorFeatureWeights": []any{json.Number("-0.25"), json.Number("0.75")}, "bias": json.Number("-0.2")},
		},
		"problemFeatureWeights": []any{json.Number("1.2"), json.Number("-0.5")},
		"difficultyBias":        json.Number("0.3"),
		"discrimination":        json.Number("1.5"),
	}
	goldenVectors := []any{
		map[string]any{
			"id": "primary",
			"input": map[string]any{
				"featureSchemaSha256":    testFeatureSchemaSHA256,
				"knowledgeCatalogSha256": testKnowledgeCatalogSHA256,
				"actorFeatures": []any{
					map[string]any{"featureId": "practice", "value": json.Number("12")},
					map[string]any{"featureId": "accuracy", "value": json.Number("0.75")},
				},
				"problemFeatures": []any{
					map[string]any{"featureId": "difficulty", "value": json.Number("2")},
					map[string]any{"featureId": "novelty", "value": json.Number("4")},
				},
				"knowledgeWeights": []any{
					map[string]any{"knowledgePointId": "arrays", "weight": json.Number("0.6")},
					map[string]any{"knowledgePointId": "graphs", "weight": json.Number("0.4")},
				},
			},
			"expected": map[string]any{
				"probability": json.Number("0.15709546888545273"),
				"knowledgeMastery": []any{
					map[string]any{"knowledgePointId": "arrays", "probability": json.Number("0.8320183851339245")},
					map[string]any{"knowledgePointId": "graphs", "probability": json.Number("0.574442516811659")},
				},
			},
		},
	}
	root := map[string]any{
		"schema": Schema,
		"manifest": map[string]any{
			"modelId":                  "123e4567-e89b-42d3-a456-426614174000",
			"purpose":                  string(PurposeAcceptanceTest),
			"trainedAt":                "2026-07-13T12:34:56Z",
			"algorithm":                Algorithm,
			"inferenceContract":        InferenceContract,
			"trainingProvenanceSha256": strings.Repeat("1", 64),
			"featureSchemaSha256":      testFeatureSchemaSHA256,
			"knowledgeCatalogSha256":   testKnowledgeCatalogSHA256,
			"parameterSha256":          "",
			"goldenVectorsSha256":      "",
			"actorFeatureIds":          []any{"practice", "accuracy"},
			"problemFeatureIds":        []any{"difficulty", "novelty"},
			"knowledgePointIds":        []any{"arrays", "graphs"},
		},
		"parameters":    parameters,
		"goldenVectors": goldenVectors,
	}
	rebindDigests(t, root)
	return canonicalArtifact(t, root)
}

func decodeArtifact(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	return root
}

func canonicalArtifact(t *testing.T, value map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func canonicalValue(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(canonical, &wrapper); err != nil {
		t.Fatal(err)
	}
	return wrapper.Value
}

func rebindDigests(t *testing.T, root map[string]any) {
	t.Helper()
	manifest := manifestMap(root)
	manifest["parameterSha256"] = digestBytes(canonicalValue(t, root["parameters"]))
	manifest["goldenVectorsSha256"] = digestBytes(canonicalValue(t, root["goldenVectors"]))
}

func manifestMap(root map[string]any) map[string]any {
	return root["manifest"].(map[string]any)
}

func parametersMap(root map[string]any) map[string]any {
	return root["parameters"].(map[string]any)
}

func goldenInput(root map[string]any) map[string]any {
	return root["goldenVectors"].([]any)[0].(map[string]any)["input"].(map[string]any)
}

func goldenExpected(root map[string]any) map[string]any {
	return root["goldenVectors"].([]any)[0].(map[string]any)["expected"].(map[string]any)
}

func assertClose(t *testing.T, label string, actual, expected, tolerance float64) {
	t.Helper()
	if math.Abs(actual-expected) > tolerance {
		t.Fatalf("%s = %.17g, want %.17g within %.0e", label, actual, expected, tolerance)
	}
}
