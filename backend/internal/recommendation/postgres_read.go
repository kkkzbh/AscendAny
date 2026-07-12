package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

func (repository *PostgresRepository) ReadCurrent(
	ctx context.Context,
	principal auth.AccessPrincipal,
) (result CurrentRecommendation, resultErr error) {
	resultErr = repository.transaction(ctx, "read current recommendation", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		resolved, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize current recommendation", err)
		}
		if resolved.ActorID == nil {
			return domainError(ErrorStoredDataInvalid, true, "authorize current recommendation", errors.New("student principal has no actor"))
		}
		var analyticsGenerationID *int64
		if err := tx.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton`).Scan(&analyticsGenerationID, &result.CurrentAnalyticsHeadRevision); err != nil {
			return databaseError("load current analytics head for recommendation", err)
		}
		if analyticsGenerationID != nil {
			value := strconv.FormatInt(*analyticsGenerationID, 10)
			result.CurrentAnalyticsGenerationID = &value
		}
		var currentModelID *int64
		var headSourceGenerationID *int64
		var headSourceRevision *int64
		if err := tx.QueryRow(ctx, `
SELECT current_model_id,
       source_analytics_generation_id,
       source_analytics_head_revision,
       head_revision
FROM ascendany.recommendation_head
WHERE singleton`).Scan(
			&currentModelID,
			&headSourceGenerationID,
			&headSourceRevision,
			&result.RecommendationHeadRevision,
		); err != nil {
			return databaseError("load recommendation head", err)
		}
		if currentModelID == nil {
			reason := "no_active_model"
			result.State = RecommendationUnavailable
			result.UnavailableReason = &reason
			return nil
		}
		if headSourceGenerationID == nil || headSourceRevision == nil || analyticsGenerationID == nil {
			return domainError(ErrorStoredDataInvalid, true, "load recommendation head", errors.New("active recommendation or analytics head provenance is incomplete"))
		}

		provenance := ModelProvenance{}
		var sourceGenerationID, configurationVersionID, catalogVersionID int64
		var manifestJSON, metricsJSON string
		var resultSchema, resultJSON, resultSHA256 *string
		err = tx.QueryRow(ctx, `
SELECT model.public_id::text,
       run.public_id::text,
       model.source_analytics_generation_id,
       model.source_analytics_head_revision,
       run.training_configuration_version_id,
       run.input_manifest_sha256,
       configuration_item.configuration_key,
       configuration.version_number,
	       configuration.schema_id,
	       configuration.document_sha256,
	       run.knowledge_catalog_version_id,
	       catalog_item.configuration_key,
	       catalog.version_number,
	       catalog.schema_id,
	       catalog.document_sha256,
	       output_artifact.sha256,
       model.model_schema,
       model.model_manifest::text,
       model.model_manifest_sha256,
       model.metrics::text,
       model.created_at,
       student_result.result_schema,
       student_result.result::text,
       student_result.result_sha256
FROM ascendany.recommendation_models AS model
JOIN ascendany.recommendation_training_runs AS run
  ON run.training_run_id = model.training_run_id
 AND run.status = 'succeeded'
 AND run.output_bundle_artifact_id = model.output_bundle_artifact_id
JOIN ascendany.artifacts AS output_artifact
  ON output_artifact.artifact_id = model.output_bundle_artifact_id
JOIN ascendany.configuration_versions AS configuration
  ON configuration.configuration_version_id = run.training_configuration_version_id
 AND configuration.configuration_kind = 'training'
JOIN ascendany.configuration_items AS configuration_item
  ON configuration_item.configuration_item_id = configuration.configuration_item_id
 AND configuration_item.configuration_kind = 'training'
JOIN ascendany.configuration_versions AS catalog
  ON catalog.configuration_version_id = run.knowledge_catalog_version_id
 AND catalog.configuration_kind = 'knowledge_catalog'
JOIN ascendany.configuration_items AS catalog_item
  ON catalog_item.configuration_item_id = catalog.configuration_item_id
 AND catalog_item.configuration_kind = 'knowledge_catalog'
LEFT JOIN ascendany.student_recommendation_results AS student_result
  ON student_result.recommendation_model_id = model.recommendation_model_id
 AND student_result.actor_id = $2
WHERE model.recommendation_model_id = $1
  AND model.training_outcome = 'succeeded'`, *currentModelID, *resolved.ActorID).Scan(
			&provenance.ModelID,
			&provenance.TrainingRunID,
			&sourceGenerationID,
			&provenance.AnalyticsHeadRevision,
			&configurationVersionID,
			&provenance.InputManifestSHA256,
			&provenance.TrainingConfigurationKey,
			&provenance.TrainingConfigurationVersion,
			&provenance.TrainingConfigurationSchema,
			&provenance.TrainingConfigurationSHA256,
			&catalogVersionID,
			&provenance.KnowledgeCatalogKey,
			&provenance.KnowledgeCatalogVersion,
			&provenance.KnowledgeCatalogSchema,
			&provenance.KnowledgeCatalogSHA256,
			&provenance.OutputArtifactSHA256,
			&provenance.ModelSchema,
			&manifestJSON,
			&provenance.ModelManifestSHA256,
			&metricsJSON,
			&provenance.CreatedAt,
			&resultSchema,
			&resultJSON,
			&resultSHA256,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorStoredDataInvalid, true, "load active recommendation model", errors.New("recommendation head target is missing or non-active"))
		}
		if err != nil {
			return databaseError("load active recommendation model", err)
		}
		if sourceGenerationID != *headSourceGenerationID || provenance.AnalyticsHeadRevision != *headSourceRevision ||
			!canonicalUUIDv4Pattern.MatchString(provenance.ModelID) ||
			!canonicalUUIDv4Pattern.MatchString(provenance.TrainingRunID) ||
			provenance.ModelSchema != ModelSchemaV2 ||
			!lowercaseSHA256Pattern.MatchString(provenance.InputManifestSHA256) ||
			!configurationKeyPattern.MatchString(provenance.TrainingConfigurationKey) ||
			provenance.TrainingConfigurationVersion <= 0 ||
			!schemaIDPattern.MatchString(provenance.TrainingConfigurationSchema) ||
			!lowercaseSHA256Pattern.MatchString(provenance.TrainingConfigurationSHA256) ||
			catalogVersionID <= 0 || !configurationKeyPattern.MatchString(provenance.KnowledgeCatalogKey) ||
			provenance.KnowledgeCatalogVersion <= 0 || provenance.KnowledgeCatalogSchema != knowledgeCatalogSchemaV1 ||
			!lowercaseSHA256Pattern.MatchString(provenance.KnowledgeCatalogSHA256) ||
			!lowercaseSHA256Pattern.MatchString(provenance.OutputArtifactSHA256) ||
			!lowercaseSHA256Pattern.MatchString(provenance.ModelManifestSHA256) {
			return domainError(ErrorStoredDataInvalid, true, "validate active recommendation model", errors.New("active model provenance violates its persisted contract"))
		}
		manifest, manifestSHA256, err := canonicaljson.Object(json.RawMessage(manifestJSON), maxModelManifestBytes)
		if err != nil || manifestSHA256 != provenance.ModelManifestSHA256 {
			return domainError(ErrorStoredDataInvalid, true, "validate active recommendation model", errors.New("model manifest hash differs"))
		}
		metrics, _, err := canonicaljson.Object(json.RawMessage(metricsJSON), maxMetricsBytes)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate active recommendation model", errors.New("model metrics are invalid"))
		}
		provenance.AnalyticsGenerationID = strconv.FormatInt(sourceGenerationID, 10)
		provenance.TrainingConfigurationVersionID = strconv.FormatInt(configurationVersionID, 10)
		provenance.KnowledgeCatalogVersionID = strconv.FormatInt(catalogVersionID, 10)
		provenance.ModelManifest = manifest
		provenance.Metrics = metrics
		provenance.CreatedAt = provenance.CreatedAt.UTC()
		result.Model = &provenance

		if resultSchema == nil || resultJSON == nil || resultSHA256 == nil {
			if resultSchema != nil || resultJSON != nil || resultSHA256 != nil {
				return domainError(ErrorStoredDataInvalid, true, "validate active student recommendation", errors.New("student result columns are partially null"))
			}
			reason := "actor_not_in_active_model"
			result.State = RecommendationUnavailable
			result.UnavailableReason = &reason
			return nil
		}
		if *resultSchema != ResultSchemaV2 || !lowercaseSHA256Pattern.MatchString(*resultSHA256) {
			return domainError(ErrorStoredDataInvalid, true, "validate active student recommendation", errors.New("student result schema or hash is invalid"))
		}
		studentResult, studentResultSHA256, err := canonicaljson.Object(json.RawMessage(*resultJSON), maxStudentResultBytes)
		if err != nil || studentResultSHA256 != *resultSHA256 {
			return domainError(ErrorStoredDataInvalid, true, "validate active student recommendation", errors.New("student result hash differs"))
		}
		typedResult, err := parseStudentRecommendationResultV2(studentResult, studentResultSHA256)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate active student recommendation", err)
		}
		result.Result = &typedResult
		if *analyticsGenerationID == sourceGenerationID && result.CurrentAnalyticsHeadRevision == provenance.AnalyticsHeadRevision {
			result.State = RecommendationFresh
		} else {
			result.State = RecommendationStale
		}
		return nil
	})
	return result, resultErr
}
