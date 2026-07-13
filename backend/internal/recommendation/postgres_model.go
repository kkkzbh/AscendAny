package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
)

func (repository *PostgresRepository) loadModelProvenance(ctx context.Context, tx recommendationTx) (ModelProvenance, error) {
	var provenance ModelProvenance
	var releaseID, headRevision int64
	var manifestText, manifestSHA256 string
	var trainedAt time.Time
	err := tx.QueryRow(ctx, `
SELECT head.current_release_id,
       head.head_revision,
       release.model_id::text,
       release.model_purpose,
       release.artifact_sha256,
       release.artifact_size_bytes,
       release.artifact_mode,
       release.model_schema,
       release.algorithm,
       release.inference_contract,
       release.trained_at,
       release.training_provenance_sha256,
       release.feature_schema_sha256,
       release.knowledge_catalog_sha256,
       release.parameter_sha256,
       release.golden_vectors_sha256,
       release.manifest::text,
       release.manifest_sha256,
       activation.application_version,
       activation.application_commit,
       activation.application_build_time
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS release
  ON release.recommendation_model_release_id = head.current_release_id
JOIN ascendany.recommendation_model_activation_events AS activation
  ON activation.head_revision = head.head_revision
 AND activation.recommendation_model_release_id = head.current_release_id
 AND activation.artifact_sha256 = release.artifact_sha256
WHERE head.singleton`).Scan(
		&releaseID, &headRevision, &provenance.ModelID, &provenance.Purpose, &provenance.ArtifactSHA256,
		&provenance.ArtifactSizeBytes, &provenance.ArtifactMode, &provenance.ModelSchema,
		&provenance.Algorithm, &provenance.InferenceContract, &trainedAt,
		&provenance.TrainingProvenanceSHA256, &provenance.FeatureSchemaSHA256,
		&provenance.KnowledgeCatalogSHA256, &provenance.ParameterSHA256,
		&provenance.GoldenVectorsSHA256, &manifestText, &manifestSHA256,
		&provenance.ApplicationVersion, &provenance.ApplicationCommit, &provenance.ApplicationBuildTime,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProvenance{}, domainError(ErrorStoredDataInvalid, true, "load recommendation model provenance", errors.New("active model head, release, or activation is missing"))
	}
	if err != nil {
		return ModelProvenance{}, databaseError("load recommendation model provenance", err)
	}
	manifest := repository.model.Manifest()
	canonicalManifest, digest, canonicalErr := canonicalObject(json.RawMessage(manifestText), manifestSHA256, maximumManifestBytes, "stored model manifest")
	if canonicalErr != nil || releaseID != repository.binding.ReleaseID || headRevision != repository.binding.HeadRevision ||
		digest != repository.binding.ManifestSHA256 || !slices.Equal(canonicalManifest, repository.binding.ManifestJSON) ||
		provenance.ModelID != manifest.ModelID || provenance.Purpose != string(manifest.Purpose) ||
		provenance.ArtifactSHA256 != repository.model.SHA256() ||
		provenance.ArtifactSizeBytes < 1 || provenance.ArtifactSizeBytes > inferencemodel.MaximumArtifactBytes || provenance.ArtifactMode != 420 ||
		provenance.ModelSchema != inferencemodel.Schema || provenance.Algorithm != inferencemodel.Algorithm ||
		provenance.InferenceContract != inferencemodel.InferenceContract || trainedAt.UTC().Format(time.RFC3339Nano) != manifest.TrainedAt ||
		provenance.TrainingProvenanceSHA256 != manifest.TrainingProvenanceSHA256 ||
		provenance.FeatureSchemaSHA256 != manifest.FeatureSchemaSHA256 || provenance.KnowledgeCatalogSHA256 != manifest.KnowledgeCatalogSHA256 ||
		provenance.ParameterSHA256 != manifest.ParameterSHA256 || provenance.GoldenVectorsSHA256 != manifest.GoldenVectorsSHA256 ||
		!validApplicationIdentity(provenance.ApplicationVersion) || !validApplicationIdentity(provenance.ApplicationCommit) ||
		!validApplicationIdentity(provenance.ApplicationBuildTime) {
		return ModelProvenance{}, domainError(ErrorStoredDataInvalid, true, "validate recommendation model provenance", errors.New("active database model differs from the process-bound artifact"))
	}
	activationErr := modelrelease.RequireCurrentActivationCatalog(ctx, tx, modelrelease.CurrentActivationExpectation{
		ReleaseID: releaseID, HeadRevision: headRevision,
		ArtifactSHA256: provenance.ArtifactSHA256, KnowledgeCatalogSHA256: provenance.KnowledgeCatalogSHA256,
		Application: modelrelease.ApplicationIdentity{
			Version: provenance.ApplicationVersion, Commit: provenance.ApplicationCommit, BuildTime: provenance.ApplicationBuildTime,
		},
	})
	if errors.Is(activationErr, modelrelease.ErrStoredDataInvalid) || errors.Is(activationErr, modelrelease.ErrInvalidConfiguration) {
		return ModelProvenance{}, domainError(ErrorStoredDataInvalid, true, "validate recommendation activation catalog binding", activationErr)
	}
	if activationErr != nil {
		return ModelProvenance{}, databaseError("validate recommendation activation catalog binding", activationErr)
	}
	provenance.TrainedAt = manifest.TrainedAt
	provenance.ModelHeadRevision = headRevision
	return provenance, nil
}

func validApplicationIdentity(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
}

func modelManifestKnowledgeIDs(model *inferencemodel.Model) []string {
	return model.Manifest().KnowledgePointIDs
}

func storedModelMismatch(format string, values ...any) error {
	return domainError(ErrorStoredDataInvalid, true, "validate recommendation model provenance", fmt.Errorf(format, values...))
}
