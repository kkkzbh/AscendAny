package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const syntheticContractArtifactSHA256 = "5182ed451d74a4e10d8384f3a4d9fcb2a8d2ad7d043e3721f2247e10c029bf58"

func TestRunRequiresExactVerifyCommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		nil,
		{"describe"},
		{"verify"},
		{"verify", "--model", "/tmp/model.json"},
		{"verify", "--sha256", strings.Repeat("a", 64)},
		{"verify", "--model", "/tmp/model.json", "--sha256", strings.Repeat("a", 64), "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("run(%v) = %d, stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunRejectsMissingModel(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "--model", "/definitely/missing/model.json", "--sha256", strings.Repeat("a", 64), "--expected-purpose", "production"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "model verification failed") {
		t.Fatalf("run = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunVerifiesSyntheticExternalContractArtifact(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate command test source")
	}
	fixturePath := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "recommendation", "fixtures", "synthetic-test-only.inference-model.v1.json"))
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(t.TempDir(), "synthetic-test-only.inference-model.v1.json")
	if err := os.WriteFile(modelPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(modelPath, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "--model", modelPath, "--sha256", syntheticContractArtifactSHA256, "--expected-purpose", "acceptance_test"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("run = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response struct {
		Schema              string `json:"schema"`
		ModelID             string `json:"modelId"`
		Purpose             string `json:"purpose"`
		ArtifactSHA256      string `json:"artifactSha256"`
		ArtifactSizeBytes   int64  `json:"artifactSizeBytes"`
		FeatureSchemaSHA256 string `json:"featureSchemaSha256"`
		ArtifactMode        uint32 `json:"artifactMode"`
		Algorithm           string `json:"algorithm"`
		InferenceContract   string `json:"inferenceContract"`
		TrainedAt           string `json:"trainedAt"`
		CatalogSHA256       string `json:"knowledgeCatalogSha256"`
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Schema != "ascendany.recommendation.inference-model.v1" ||
		response.ModelID != "00000000-0000-4000-8000-000000000001" ||
		response.Purpose != "acceptance_test" ||
		response.ArtifactSHA256 != syntheticContractArtifactSHA256 ||
		response.ArtifactSizeBytes != int64(len(raw)) ||
		response.FeatureSchemaSHA256 != "09c18717b8de4b3dba6c8bd9341fb237176c6d22ab7f99e2e937bf7b387a060f" ||
		response.CatalogSHA256 != "a58370ec66def22b13a0bd64acf195e9fa28530e81481e7ade2545aaaa9bfe3c" ||
		response.ArtifactMode != 0o644 || response.Algorithm != "knowledge_mirt_feature_v1" ||
		response.InferenceContract != "ascendany.recommendation.inference.v1" ||
		response.TrainedAt != "2000-01-01T00:00:00.123456Z" {
		t.Fatalf("verification response = %+v", response)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"verify", "--model", modelPath, "--sha256", syntheticContractArtifactSHA256, "--expected-purpose", "production"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "differs from expected purpose") {
		t.Fatalf("production verification = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
