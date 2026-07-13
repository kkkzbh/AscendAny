package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" {
		fmt.Fprintln(stderr, "usage: ascendany-model verify --model /absolute/model.json --sha256 64_lower_hex --expected-purpose production|acceptance_test")
		return 2
	}
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	modelPath := flags.String("model", "", "absolute inference model artifact path")
	expectedSHA256 := flags.String("sha256", "", "expected artifact SHA-256")
	expectedPurposeValue := flags.String("expected-purpose", "", "required model deployment purpose")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *modelPath == "" || *expectedSHA256 == "" || *expectedPurposeValue == "" {
		if err == nil {
			fmt.Fprintln(stderr, "usage: ascendany-model verify --model /absolute/model.json --sha256 64_lower_hex --expected-purpose production|acceptance_test")
		}
		return 2
	}
	expectedPurpose, err := inferencemodel.ParsePurpose(*expectedPurposeValue)
	if err != nil {
		fmt.Fprintf(stderr, "model verification failed: expected purpose: %v\n", err)
		return 1
	}
	loaded, err := modelartifact.Load(*modelPath, *expectedSHA256)
	if err != nil {
		fmt.Fprintf(stderr, "model verification failed: %v\n", err)
		return 1
	}
	if err := recommendation.ValidateInferenceModel(loaded.Model, expectedPurpose); err != nil {
		fmt.Fprintf(stderr, "model verification failed: %v\n", err)
		return 1
	}
	manifest := loaded.Model.Manifest()
	trainedAt, err := time.Parse(time.RFC3339Nano, manifest.TrainedAt)
	if err != nil {
		fmt.Fprintln(stderr, "model verification failed: parsed model contains an invalid trainedAt")
		return 1
	}
	response := struct {
		Schema              string `json:"schema"`
		ModelID             string `json:"modelId"`
		Purpose             string `json:"purpose"`
		ArtifactSHA256      string `json:"artifactSha256"`
		ArtifactSizeBytes   int64  `json:"artifactSizeBytes"`
		ArtifactMode        uint32 `json:"artifactMode"`
		Algorithm           string `json:"algorithm"`
		InferenceContract   string `json:"inferenceContract"`
		TrainedAt           string `json:"trainedAt"`
		FeatureSchemaSHA256 string `json:"featureSchemaSha256"`
		CatalogSHA256       string `json:"knowledgeCatalogSha256"`
	}{
		Schema: inferencemodel.Schema, ModelID: manifest.ModelID, Purpose: string(manifest.Purpose),
		ArtifactSHA256: loaded.SHA256, ArtifactSizeBytes: loaded.Size,
		ArtifactMode: uint32(loaded.Mode.Perm()), Algorithm: manifest.Algorithm,
		InferenceContract: manifest.InferenceContract, TrainedAt: trainedAt.UTC().Format(time.RFC3339Nano),
		FeatureSchemaSHA256: manifest.FeatureSchemaSHA256, CatalogSHA256: manifest.KnowledgeCatalogSHA256,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintf(stderr, "model verification output failed: %v\n", err)
		return 1
	}
	return 0
}
