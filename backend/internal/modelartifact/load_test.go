package modelartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func TestLoadBindsReleaseFileAndDigest(t *testing.T) {
	t.Parallel()
	raw := testModelArtifact(t)
	digest := sha256.Sum256(raw)
	expected := hex.EncodeToString(digest[:])
	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, raw, RequiredMode); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path, expected)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Model == nil || loaded.SHA256 != expected || loaded.Size != int64(len(raw)) || loaded.Mode != RequiredMode {
		t.Fatalf("loaded = %#v", loaded)
	}
	if loaded.Model.Manifest().ModelID != "123e4567-e89b-42d3-a456-426614174000" {
		t.Fatalf("manifest = %#v", loaded.Model.Manifest())
	}
}

func TestLoadRejectsFileBoundaryDrift(t *testing.T) {
	t.Parallel()
	raw := testModelArtifact(t)
	digest := sha256.Sum256(raw)
	expected := hex.EncodeToString(digest[:])

	t.Run("relative path", func(t *testing.T) {
		if _, err := Load("model.json", expected); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("invalid digest", func(t *testing.T) {
		path := writeTestModel(t, raw, RequiredMode)
		if _, err := Load(path, strings.Repeat("f", 64)); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("unsafe mode", func(t *testing.T) {
		path := writeTestModel(t, raw, 0o600)
		if _, err := Load(path, expected); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("leaf symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.json")
		if err := os.WriteFile(target, raw, RequiredMode); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "model.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(link, expected); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("hard link", func(t *testing.T) {
		path := writeTestModel(t, raw, RequiredMode)
		if err := os.Link(path, path+".second"); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, expected); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
}

func writeTestModel(t *testing.T, raw []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func testModelArtifact(t *testing.T) []byte {
	t.Helper()
	featureDigest := strings.Repeat("2", 64)
	catalogDigest := strings.Repeat("3", 64)
	parameters := map[string]any{
		"actorNormalization": map[string]any{
			"means": []any{json.Number("10"), json.Number("0.5")}, "scales": []any{json.Number("2"), json.Number("0.25")},
		},
		"problemNormalization": map[string]any{
			"means": []any{json.Number("1"), json.Number("2")}, "scales": []any{json.Number("0.5"), json.Number("2")},
		},
		"knowledgeParameters": []any{
			map[string]any{"knowledgePointId": "arrays", "actorFeatureWeights": []any{json.Number("0.5"), json.Number("1")}, "bias": json.Number("0.1")},
			map[string]any{"knowledgePointId": "graphs", "actorFeatureWeights": []any{json.Number("-0.25"), json.Number("0.75")}, "bias": json.Number("-0.2")},
		},
		"problemFeatureWeights": []any{json.Number("1.2"), json.Number("-0.5")},
		"difficultyBias":        json.Number("0.3"), "discrimination": json.Number("1.5"),
	}
	golden := []any{map[string]any{
		"id": "primary",
		"input": map[string]any{
			"featureSchemaSha256": featureDigest, "knowledgeCatalogSha256": catalogDigest,
			"actorFeatures":    []any{map[string]any{"featureId": "practice", "value": json.Number("12")}, map[string]any{"featureId": "accuracy", "value": json.Number("0.75")}},
			"problemFeatures":  []any{map[string]any{"featureId": "difficulty", "value": json.Number("2")}, map[string]any{"featureId": "novelty", "value": json.Number("4")}},
			"knowledgeWeights": []any{map[string]any{"knowledgePointId": "arrays", "weight": json.Number("0.6")}, map[string]any{"knowledgePointId": "graphs", "weight": json.Number("0.4")}},
		},
		"expected": map[string]any{
			"probability":      json.Number("0.15709546888545273"),
			"knowledgeMastery": []any{map[string]any{"knowledgePointId": "arrays", "probability": json.Number("0.8320183851339245")}, map[string]any{"knowledgePointId": "graphs", "probability": json.Number("0.574442516811659")}},
		},
	}}
	root := map[string]any{
		"schema": inferencemodel.Schema,
		"manifest": map[string]any{
			"modelId": "123e4567-e89b-42d3-a456-426614174000", "purpose": string(inferencemodel.PurposeAcceptanceTest),
			"trainedAt": "2026-07-13T12:34:56Z",
			"algorithm": inferencemodel.Algorithm, "inferenceContract": inferencemodel.InferenceContract,
			"trainingProvenanceSha256": strings.Repeat("1", 64), "featureSchemaSha256": featureDigest,
			"knowledgeCatalogSha256": catalogDigest, "parameterSha256": "", "goldenVectorsSha256": "",
			"actorFeatureIds": []any{"practice", "accuracy"}, "problemFeatureIds": []any{"difficulty", "novelty"},
			"knowledgePointIds": []any{"arrays", "graphs"},
		},
		"parameters": parameters, "goldenVectors": golden,
	}
	manifest := root["manifest"].(map[string]any)
	manifest["parameterSha256"] = testCanonicalDigest(t, parameters)
	manifest["goldenVectorsSha256"] = testCanonicalDigest(t, golden)
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, inferencemodel.MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func testCanonicalDigest(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, inferencemodel.MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(canonical, &wrapper); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wrapper.Value)
	return hex.EncodeToString(digest[:])
}
