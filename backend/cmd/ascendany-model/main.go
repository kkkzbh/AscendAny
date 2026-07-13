package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/catalogartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "verify":
		return runVerifyModel(args[1:], stdout, stderr)
	case "verify-catalog":
		return runVerifyCatalog(args[1:], stdout, stderr)
	default:
		printUsage(stderr)
		return 2
	}
}

func runVerifyModel(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	modelPath := flags.String("model", "", "absolute inference model artifact path")
	expectedSHA256 := flags.String("sha256", "", "expected artifact SHA-256")
	expectedPurposeValue := flags.String("expected-purpose", "", "required model deployment purpose")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *modelPath == "" || *expectedSHA256 == "" || *expectedPurposeValue == "" {
		if err == nil {
			printVerifyModelUsage(stderr)
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

func runVerifyCatalog(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("verify-catalog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	catalogPath := flags.String("catalog", "", "absolute knowledge catalog artifact path")
	expectedCatalogSHA256 := flags.String("catalog-sha256", "", "expected catalog artifact SHA-256")
	modelPath := flags.String("model", "", "absolute inference model artifact path")
	expectedModelSHA256 := flags.String("model-sha256", "", "expected model artifact SHA-256")
	expectedPurposeValue := flags.String("expected-purpose", "", "required model deployment purpose")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *catalogPath == "" ||
		*expectedCatalogSHA256 == "" || *modelPath == "" || *expectedModelSHA256 == "" || *expectedPurposeValue == "" {
		if err == nil {
			printVerifyCatalogUsage(stderr)
		}
		return 2
	}
	expectedPurpose, err := inferencemodel.ParsePurpose(*expectedPurposeValue)
	if err != nil {
		fmt.Fprintf(stderr, "catalog verification failed: expected model purpose: %v\n", err)
		return 1
	}
	loadedModel, err := modelartifact.Load(*modelPath, *expectedModelSHA256)
	if err != nil {
		fmt.Fprintf(stderr, "catalog verification failed: model artifact: %v\n", err)
		return 1
	}
	if err := recommendation.ValidateInferenceModel(loadedModel.Model, expectedPurpose); err != nil {
		fmt.Fprintf(stderr, "catalog verification failed: model contract: %v\n", err)
		return 1
	}
	loadedCatalog, err := catalogartifact.Load(*catalogPath, *expectedCatalogSHA256, loadedModel.Model.Manifest())
	if err != nil {
		fmt.Fprintf(stderr, "catalog verification failed: %v\n", err)
		return 1
	}
	manifest := loadedModel.Model.Manifest()
	response := struct {
		Schema                 string   `json:"schema"`
		TaxonomyID             string   `json:"taxonomyId"`
		CatalogSHA256          string   `json:"catalogSha256"`
		ArtifactSizeBytes      int64    `json:"artifactSizeBytes"`
		ArtifactMode           uint32   `json:"artifactMode"`
		ModelID                string   `json:"modelId"`
		ModelArtifactSHA256    string   `json:"modelArtifactSha256"`
		KnowledgePointIDs      []string `json:"knowledgePointIds"`
		ProblemAssignmentCount int      `json:"problemAssignmentCount"`
	}{
		Schema: recommendation.KnowledgeCatalogSchemaV1, TaxonomyID: loadedCatalog.Artifact.TaxonomyID(),
		CatalogSHA256: loadedCatalog.SHA256, ArtifactSizeBytes: loadedCatalog.Size,
		ArtifactMode: uint32(loadedCatalog.Mode.Perm()), ModelID: manifest.ModelID,
		ModelArtifactSHA256:    loadedModel.SHA256,
		KnowledgePointIDs:      loadedCatalog.Artifact.KnowledgePointIDs(),
		ProblemAssignmentCount: loadedCatalog.Artifact.ProblemAssignmentCount(),
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintf(stderr, "catalog verification output failed: %v\n", err)
		return 1
	}
	return 0
}

func printUsage(stderr io.Writer) {
	printVerifyModelUsage(stderr)
	printVerifyCatalogUsage(stderr)
}

func printVerifyModelUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: ascendany-model verify --model /absolute/model.json --sha256 64_lower_hex --expected-purpose production|acceptance_test")
}

func printVerifyCatalogUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: ascendany-model verify-catalog --catalog /absolute/catalog.json --catalog-sha256 64_lower_hex --model /absolute/model.json --model-sha256 64_lower_hex --expected-purpose production|acceptance_test")
}
