package backup

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func testRecommendationModelDescriptor(t *testing.T) RecommendationModelDescriptor {
	t.Helper()
	trainedAt := time.Date(2026, 7, 10, 12, 34, 56, 123456000, time.UTC)
	digests := []string{
		strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64),
		strings.Repeat("4", 64), strings.Repeat("5", 64),
	}
	raw, err := json.Marshal(map[string]any{
		"schema": inferencemodel.Schema, "modelId": "123e4567-e89b-42d3-a456-426614174000",
		"purpose":   string(inferencemodel.PurposeAcceptanceTest),
		"trainedAt": trainedAt.Format(time.RFC3339Nano), "algorithm": inferencemodel.Algorithm,
		"inferenceContract":        inferencemodel.InferenceContract,
		"trainingProvenanceSha256": digests[0], "featureSchemaSha256": digests[1],
		"knowledgeCatalogSha256": digests[2], "parameterSha256": digests[3],
		"goldenVectorsSha256": digests[4], "actorFeatureIds": []string{"actor"},
		"problemFeatureIds": []string{"problem"}, "knowledgePointIds": []string{"arrays"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, manifestSHA256, err := canonicaljson.Object(raw, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return RecommendationModelDescriptor{
		ReleaseID: 1, HeadRevision: 2, ModelID: "123e4567-e89b-42d3-a456-426614174000",
		ModelPurpose:   string(inferencemodel.PurposeAcceptanceTest),
		ArtifactSHA256: strings.Repeat("a", 64), ArtifactSizeBytes: 4096, ArtifactMode: 0o644,
		ModelSchema: inferencemodel.Schema, Algorithm: inferencemodel.Algorithm,
		InferenceContract: inferencemodel.InferenceContract, TrainedAt: trainedAt,
		TrainingProvenanceSHA256: digests[0], FeatureSchemaSHA256: digests[1],
		KnowledgeCatalogSHA256: digests[2], ParameterSHA256: digests[3], GoldenVectorsSHA256: digests[4],
		Manifest: manifest, ManifestSHA256: manifestSHA256,
		ReleaseCreatedAt:   time.Date(2026, 7, 10, 13, 0, 0, 0, time.UTC),
		ApplicationVersion: "2.0.0", ApplicationCommit: strings.Repeat("b", 40),
		ApplicationBuildTime: "2026-07-10T13:00:00Z",
		ActivatedAt:          time.Date(2026, 7, 10, 13, 1, 0, 0, time.UTC),
		HeadUpdatedAt:        time.Date(2026, 7, 10, 13, 1, 0, 0, time.UTC),
	}
}

func TestValidateRecommendationModelDescriptorRejectsColumnDrift(t *testing.T) {
	t.Parallel()
	value := testRecommendationModelDescriptor(t)
	if err := validateRecommendationModelDescriptor(value); err != nil {
		t.Fatal(err)
	}
	value.FeatureSchemaSHA256 = strings.Repeat("f", 64)
	if err := validateRecommendationModelDescriptor(value); err == nil {
		t.Fatal("manifest/column drift was accepted")
	}
	value = testRecommendationModelDescriptor(t)
	value.ModelPurpose = string(inferencemodel.PurposeProduction)
	if err := validateRecommendationModelDescriptor(value); err == nil {
		t.Fatal("manifest/purpose drift was accepted")
	}
}

func TestNormalizeRecommendationModelTimestampsUsesUTC(t *testing.T) {
	t.Parallel()
	value := testRecommendationModelDescriptor(t)
	location := time.FixedZone("database-session", 8*60*60)
	wantTrainedAt := value.TrainedAt
	wantReleaseCreatedAt := value.ReleaseCreatedAt
	wantActivatedAt := value.ActivatedAt
	wantHeadUpdatedAt := value.HeadUpdatedAt
	value.TrainedAt = value.TrainedAt.In(location)
	value.ReleaseCreatedAt = value.ReleaseCreatedAt.In(location)
	value.ActivatedAt = value.ActivatedAt.In(location)
	value.HeadUpdatedAt = value.HeadUpdatedAt.In(location)

	normalizeRecommendationModelTimestamps(&value)

	for label, pair := range map[string][2]time.Time{
		"trainedAt":        {value.TrainedAt, wantTrainedAt},
		"releaseCreatedAt": {value.ReleaseCreatedAt, wantReleaseCreatedAt},
		"activatedAt":      {value.ActivatedAt, wantActivatedAt},
		"headUpdatedAt":    {value.HeadUpdatedAt, wantHeadUpdatedAt},
	} {
		if pair[0].Location() != time.UTC || !pair[0].Equal(pair[1]) {
			t.Fatalf("%s = %s (%s), want UTC instant %s", label, pair[0], pair[0].Location(), pair[1])
		}
	}
}
