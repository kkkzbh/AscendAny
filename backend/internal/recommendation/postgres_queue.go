package recommendation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

func (repository *PostgresRepository) PrepareTraining(
	ctx context.Context,
	principal auth.AccessPrincipal,
	configurationKey string,
) (dataset TrainingDataset, resultErr error) {
	if !configurationKeyPattern.MatchString(configurationKey) {
		return TrainingDataset{}, domainError(ErrorInvalidInput, true, "prepare recommendation training", errors.New("canonical configuration key is required"))
	}
	resultErr = repository.transaction(ctx, "prepare recommendation training", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		if _, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin)); err != nil {
			return mapPrincipalError("authorize recommendation training", err)
		}
		loaded, err := loadCurrentTrainingDataset(ctx, tx, configurationKey, false)
		if err != nil {
			return err
		}
		dataset = loaded
		return nil
	})
	return dataset, resultErr
}

func (repository *PostgresRepository) QueueTraining(
	ctx context.Context,
	command QueueCommand,
) (result QueueResult, resultErr error) {
	if !canonicalUUIDv4Pattern.MatchString(command.RunPublicID) || command.MediaType != TrainingBundleMediaTypeV2 ||
		!validPrincipalShape(command.Principal, auth.RoleAdmin) || len(command.Bundle.CanonicalJSON) == 0 ||
		command.ExpectedAnalyticsGenerationID <= 0 || command.ExpectedAnalyticsHeadRevision <= 0 ||
		command.MaximumBundleBytes <= 0 || len(command.Bundle.CanonicalJSON) > command.MaximumBundleBytes {
		return QueueResult{}, domainError(ErrorInvalidInput, true, "queue recommendation training", errors.New("canonical queue command is required"))
	}
	if err := validateArtifact(command.Artifact, command.Bundle.SHA256, int64(len(command.Bundle.CanonicalJSON))); err != nil {
		return QueueResult{}, err
	}
	resultErr = repository.transaction(ctx, "queue recommendation training", pgx.TxOptions{}, func(tx recommendationTx) error {
		if _, err := principalguard.ResolveForUpdate(ctx, tx, command.Principal, principalguard.Roles(auth.RoleAdmin)); err != nil {
			return mapPrincipalError("authorize recommendation training queue", err)
		}
		current, err := loadCurrentTrainingDataset(ctx, tx, command.Dataset.Configuration.Key, true)
		if err != nil {
			return err
		}
		if current.Analytics.GenerationID != command.ExpectedAnalyticsGenerationID ||
			current.Analytics.HeadRevision != command.ExpectedAnalyticsHeadRevision {
			return domainError(ErrorStateConflict, false, "queue recommendation training", &AnalyticsHeadConflict{
				ExpectedGenerationID: command.ExpectedAnalyticsGenerationID,
				ExpectedHeadRevision: command.ExpectedAnalyticsHeadRevision,
				CurrentGenerationID:  current.Analytics.GenerationID,
				CurrentHeadRevision:  current.Analytics.HeadRevision,
			})
		}
		currentBundle, err := BuildInputBundle(current, command.MaximumBundleBytes)
		if err != nil {
			return err
		}
		if !sameDatasetIdentity(current, command.Dataset) ||
			!bytes.Equal(currentBundle.CanonicalJSON, command.Bundle.CanonicalJSON) ||
			!bytes.Equal(currentBundle.Manifest, command.Bundle.Manifest) ||
			currentBundle.SHA256 != command.Bundle.SHA256 ||
			currentBundle.ManifestSHA256 != command.Bundle.ManifestSHA256 ||
			!slices.Equal(currentBundle.ActorIDs, command.Bundle.ActorIDs) {
			return domainError(ErrorStateConflict, false, "queue recommendation training", errors.New("analytics head or active training configuration changed before queue commit"))
		}
		artifactID, err := ensureArtifact(ctx, tx, command.Artifact, command.MediaType)
		if err != nil {
			return err
		}

		inserted := true
		run := TrainingRun{}
		err = scanTrainingRun(tx.QueryRow(ctx, `
INSERT INTO ascendany.recommendation_training_runs (
    public_id,
    source_analytics_generation_id,
    source_analytics_head_revision,
    input_bundle_artifact_id,
    training_configuration_version_id,
	knowledge_catalog_version_id,
    bundle_protocol,
    input_manifest,
    input_manifest_sha256,
    status
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, 'queued')
ON CONFLICT (
    source_analytics_generation_id,
    input_manifest_sha256,
    training_configuration_version_id,
	knowledge_catalog_version_id
) DO NOTHING
RETURNING `+trainingRunReturningColumns,
			command.RunPublicID,
			current.Analytics.GenerationID,
			current.Analytics.HeadRevision,
			artifactID,
			current.Configuration.VersionID,
			current.KnowledgeCatalog.VersionID,
			TrainingBundleProtocolV2,
			string(currentBundle.Manifest),
			currentBundle.ManifestSHA256,
		), &run)
		if errors.Is(err, pgx.ErrNoRows) {
			inserted = false
			err = scanTrainingRun(tx.QueryRow(ctx, `
SELECT `+trainingRunReturningColumns+`
FROM ascendany.recommendation_training_runs
WHERE source_analytics_generation_id = $1
  AND input_manifest_sha256 = $2
	AND training_configuration_version_id = $3
	AND knowledge_catalog_version_id = $4`,
				current.Analytics.GenerationID,
				currentBundle.ManifestSHA256,
				current.Configuration.VersionID,
				current.KnowledgeCatalog.VersionID,
			), &run)
		}
		if err != nil {
			return databaseError("insert or load recommendation training run", err)
		}
		if run.SourceAnalyticsGenerationID != current.Analytics.GenerationID ||
			run.SourceAnalyticsHeadRevision != current.Analytics.HeadRevision ||
			run.TrainingConfigurationVersionID != current.Configuration.VersionID ||
			run.KnowledgeCatalogVersionID != current.KnowledgeCatalog.VersionID ||
			run.BundleProtocol != TrainingBundleProtocolV2 ||
			run.InputManifestSHA256 != currentBundle.ManifestSHA256 ||
			!bytes.Equal(run.InputManifest, currentBundle.Manifest) ||
			run.InputArtifact.Hash != command.Artifact.Hash ||
			run.InputArtifact.Size != command.Artifact.Size ||
			run.InputArtifact.StorageKey != command.Artifact.StorageKey {
			return domainError(ErrorStateConflict, false, "load idempotent recommendation training run", errors.New("existing run differs from deterministic queue content"))
		}
		if inserted {
			if err := appendTrainingEvent(ctx, tx, run.DatabaseID, "queued", map[string]any{
				"artifactSha256":              command.Artifact.Hash,
				"configurationVersionId":      current.Configuration.VersionID,
				"knowledgeCatalogVersionId":   current.KnowledgeCatalog.VersionID,
				"sourceAnalyticsGenerationId": current.Analytics.GenerationID,
				"sourceAnalyticsHeadRevision": current.Analytics.HeadRevision,
			}); err != nil {
				return err
			}
		}
		run.InputArtifact.Path = command.Artifact.Path
		result = QueueResult{Run: run, Created: inserted}
		return nil
	})
	return result, resultErr
}

func loadCurrentTrainingDataset(
	ctx context.Context,
	tx recommendationTx,
	configurationKey string,
	lockMutableHeads bool,
) (TrainingDataset, error) {
	dataset := TrainingDataset{}
	query := `
SELECT generation.analytics_generation_id,
       head.head_revision,
       generation.input_manifest::text,
       generation.input_manifest_sha256,
       generation.algorithm_version,
       generation.config_sha256,
       version.configuration_version_id,
       item.configuration_key,
       version.version_number,
       version.schema_id,
       version.document::text,
       version.document_sha256
FROM ascendany.analytics_head AS head
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
 AND generation.status = 'succeeded'
JOIN ascendany.configuration_items AS item
  ON item.configuration_key = $1
 AND item.configuration_kind = 'training'
 AND item.active_version_id IS NOT NULL
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = item.active_version_id
 AND version.configuration_item_id = item.configuration_item_id
 AND version.configuration_kind = 'training'
WHERE head.singleton`
	if lockMutableHeads {
		query += `
FOR SHARE OF head, item`
	}
	var analyticsManifest, configurationDocument string
	err := tx.QueryRow(ctx, query, configurationKey).Scan(
		&dataset.Analytics.GenerationID,
		&dataset.Analytics.HeadRevision,
		&analyticsManifest,
		&dataset.Analytics.InputManifestSHA256,
		&dataset.Analytics.AlgorithmVersion,
		&dataset.Analytics.ConfigurationSHA256,
		&dataset.Configuration.VersionID,
		&dataset.Configuration.Key,
		&dataset.Configuration.VersionNumber,
		&dataset.Configuration.SchemaID,
		&configurationDocument,
		&dataset.Configuration.DocumentSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var analyticsAvailable bool
		if err := tx.QueryRow(ctx, `
SELECT current_generation_id IS NOT NULL
FROM ascendany.analytics_head
WHERE singleton`).Scan(&analyticsAvailable); err != nil {
			return TrainingDataset{}, databaseError("inspect recommendation prerequisites", err)
		}
		if !analyticsAvailable {
			return TrainingDataset{}, domainError(ErrorAnalyticsUnavailable, true, "load recommendation training dataset", errors.New("current analytics head is unavailable"))
		}
		return TrainingDataset{}, domainError(ErrorTrainingConfigurationUnavailable, true, "load recommendation training dataset", fmt.Errorf("active training configuration %q is unavailable", configurationKey))
	}
	if err != nil {
		return TrainingDataset{}, databaseError("load recommendation training provenance", err)
	}
	dataset.Analytics.InputManifest = json.RawMessage(analyticsManifest)
	dataset.Configuration.Document = json.RawMessage(configurationDocument)
	canonicalConfiguration, err := canonicalStoredObject(
		configurationDocument,
		dataset.Configuration.DocumentSHA256,
		maxConfigurationBytes,
		"load recommendation training configuration",
	)
	if err != nil {
		return TrainingDataset{}, err
	}
	parsedConfiguration, err := parseTrainingConfiguration(canonicalConfiguration)
	if err != nil {
		return TrainingDataset{}, preflightFailure("training_configuration_invalid", nil)
	}
	var catalogDocument string
	err = tx.QueryRow(ctx, `
SELECT version.configuration_version_id,
       item.configuration_key,
       version.version_number,
       version.schema_id,
       version.document::text,
       version.document_sha256
FROM ascendany.configuration_versions AS version
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = version.configuration_item_id
 AND item.configuration_kind = 'knowledge_catalog'
WHERE version.configuration_version_id = $1
  AND version.configuration_kind = 'knowledge_catalog'`, parsedConfiguration.KnowledgeCatalogVersionID).Scan(
		&dataset.KnowledgeCatalog.VersionID,
		&dataset.KnowledgeCatalog.Key,
		&dataset.KnowledgeCatalog.VersionNumber,
		&dataset.KnowledgeCatalog.SchemaID,
		&catalogDocument,
		&dataset.KnowledgeCatalog.DocumentSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrainingDataset{}, preflightFailure("knowledge_catalog_reference_unavailable", nil)
	}
	if err != nil {
		return TrainingDataset{}, databaseError("load recommendation knowledge catalog", err)
	}
	dataset.KnowledgeCatalog.Document = json.RawMessage(catalogDocument)
	rows, err := tx.Query(ctx, `
SELECT analytics.actor_id,
       analytics.rating::text,
       analytics.metrics::text
FROM ascendany.student_analytics AS analytics
WHERE analytics.analytics_generation_id = $1
ORDER BY analytics.actor_id`, dataset.Analytics.GenerationID)
	if err != nil {
		return TrainingDataset{}, databaseError("load recommendation training students", err)
	}
	defer rows.Close()
	for rows.Next() {
		student := TrainingStudent{}
		var metrics string
		if err := rows.Scan(&student.ActorID, &student.Rating, &metrics); err != nil {
			return TrainingDataset{}, databaseError("scan recommendation training student", err)
		}
		student.Metrics = json.RawMessage(metrics)
		dataset.Students = append(dataset.Students, student)
	}
	if err := rows.Err(); err != nil {
		return TrainingDataset{}, databaseError("iterate recommendation training students", err)
	}
	rows.Close()
	problemRows, err := tx.Query(ctx, `
SELECT generation_snapshot.snapshot_id,
       exam.source_exam_id,
       problem.problem_set_problem_id,
       snapshot.source_url,
       exam.platform,
       problem.problem_id,
       problem.title,
       problem.content_html,
       problem.max_score::text,
       problem.time_limit_ms,
       problem.memory_limit_bytes
FROM ascendany.analytics_generation_snapshots AS generation_snapshot
JOIN ascendany.logical_exams AS exam
  ON exam.exam_id = generation_snapshot.exam_id
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = generation_snapshot.snapshot_id
 AND snapshot.exam_id = generation_snapshot.exam_id
JOIN ascendany.pintia_snapshot_problems AS problem
  ON problem.snapshot_id = generation_snapshot.snapshot_id
WHERE generation_snapshot.analytics_generation_id = $1
ORDER BY generation_snapshot.snapshot_id, problem.problem_set_problem_id`, dataset.Analytics.GenerationID)
	if err != nil {
		return TrainingDataset{}, databaseError("load recommendation training problems", err)
	}
	defer problemRows.Close()
	for problemRows.Next() {
		problem := TrainingProblem{}
		if err := problemRows.Scan(
			&problem.SnapshotID, &problem.ProblemSetID, &problem.ProblemSetProblemID,
			&problem.SourceURL, &problem.Platform, &problem.ProblemID, &problem.Title,
			&problem.ContentHTML, &problem.MaxScore, &problem.TimeLimitMS, &problem.MemoryLimitBytes,
		); err != nil {
			return TrainingDataset{}, databaseError("scan recommendation training problem", err)
		}
		dataset.Problems = append(dataset.Problems, problem)
	}
	if err := problemRows.Err(); err != nil {
		return TrainingDataset{}, databaseError("iterate recommendation training problems", err)
	}
	problemRows.Close()
	observationRows, err := tx.Query(ctx, `
SELECT result.snapshot_id,
       result.actor_id,
       result.problem_set_problem_id,
       result.score::text,
       problem.max_score::text,
       result.passed,
       result.valid_submission_count,
       count(submission.submission_identity_id)::bigint,
       min(identity.submitted_at),
       max(identity.submitted_at)
FROM ascendany.analytics_generation_snapshots AS generation_snapshot
JOIN ascendany.pintia_ranking_problem_results AS result
  ON result.snapshot_id = generation_snapshot.snapshot_id
JOIN ascendany.pintia_snapshot_problems AS problem
  ON problem.snapshot_id = result.snapshot_id
 AND problem.problem_set_problem_id = result.problem_set_problem_id
LEFT JOIN ascendany.pintia_snapshot_submissions AS submission
  ON submission.snapshot_id = result.snapshot_id
 AND submission.actor_id = result.actor_id
 AND submission.problem_set_problem_id = result.problem_set_problem_id
LEFT JOIN ascendany.pintia_submission_identities AS identity
  ON identity.submission_identity_id = submission.submission_identity_id
WHERE generation_snapshot.analytics_generation_id = $1
GROUP BY result.snapshot_id,
         result.actor_id,
         result.problem_set_problem_id,
         result.score,
         problem.max_score,
         result.passed,
         result.valid_submission_count
ORDER BY result.snapshot_id, result.actor_id, result.problem_set_problem_id`, dataset.Analytics.GenerationID)
	if err != nil {
		return TrainingDataset{}, databaseError("load recommendation training observations", err)
	}
	defer observationRows.Close()
	for observationRows.Next() {
		observation := TrainingObservation{}
		if err := observationRows.Scan(
			&observation.SnapshotID, &observation.ActorID, &observation.ProblemSetProblemID,
			&observation.Score, &observation.MaxScore, &observation.Passed,
			&observation.ValidSubmissionCount, &observation.SubmissionCount,
			&observation.FirstSubmittedAt, &observation.LastSubmittedAt,
		); err != nil {
			return TrainingDataset{}, databaseError("scan recommendation training observation", err)
		}
		dataset.Observations = append(dataset.Observations, observation)
	}
	if err := observationRows.Err(); err != nil {
		return TrainingDataset{}, databaseError("iterate recommendation training observations", err)
	}
	return dataset, nil
}

func sameDatasetIdentity(left, right TrainingDataset) bool {
	return left.Analytics.GenerationID == right.Analytics.GenerationID &&
		left.Analytics.HeadRevision == right.Analytics.HeadRevision &&
		left.Analytics.InputManifestSHA256 == right.Analytics.InputManifestSHA256 &&
		left.Analytics.AlgorithmVersion == right.Analytics.AlgorithmVersion &&
		left.Analytics.ConfigurationSHA256 == right.Analytics.ConfigurationSHA256 &&
		left.Configuration.VersionID == right.Configuration.VersionID &&
		left.Configuration.Key == right.Configuration.Key &&
		left.Configuration.VersionNumber == right.Configuration.VersionNumber &&
		left.Configuration.SchemaID == right.Configuration.SchemaID &&
		left.Configuration.DocumentSHA256 == right.Configuration.DocumentSHA256 &&
		left.KnowledgeCatalog.VersionID == right.KnowledgeCatalog.VersionID &&
		left.KnowledgeCatalog.Key == right.KnowledgeCatalog.Key &&
		left.KnowledgeCatalog.VersionNumber == right.KnowledgeCatalog.VersionNumber &&
		left.KnowledgeCatalog.SchemaID == right.KnowledgeCatalog.SchemaID &&
		left.KnowledgeCatalog.DocumentSHA256 == right.KnowledgeCatalog.DocumentSHA256
}

func ensureArtifact(ctx context.Context, tx recommendationTx, value artifact.Artifact, mediaType string) (int64, error) {
	var artifactID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (sha256) DO NOTHING
RETURNING artifact_id`, value.Hash, value.Size, mediaType, value.StorageKey).Scan(&artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		var size int64
		var storedMediaType, storageKey string
		if err := tx.QueryRow(ctx, `
SELECT artifact_id, size_bytes, media_type, storage_key
FROM ascendany.artifacts
WHERE sha256 = $1`, value.Hash).Scan(&artifactID, &size, &storedMediaType, &storageKey); err != nil {
			return 0, databaseError("load recommendation artifact", err)
		}
		if size != value.Size || storedMediaType != mediaType || storageKey != value.StorageKey {
			return 0, domainError(ErrorInvalidArtifact, true, "load recommendation artifact", errors.New("existing artifact metadata differs for its content hash"))
		}
	} else if err != nil {
		return 0, databaseError("insert recommendation artifact", err)
	}
	return artifactID, nil
}

const trainingRunReturningColumns = `
training_run_id,
public_id::text,
source_analytics_generation_id,
source_analytics_head_revision,
training_configuration_version_id,
knowledge_catalog_version_id,
bundle_protocol,
input_manifest::text,
input_manifest_sha256,
status,
attempt_count,
created_at,
started_at,
finished_at,
(SELECT sha256 FROM ascendany.artifacts WHERE artifact_id = input_bundle_artifact_id),
(SELECT size_bytes FROM ascendany.artifacts WHERE artifact_id = input_bundle_artifact_id),
(SELECT storage_key FROM ascendany.artifacts WHERE artifact_id = input_bundle_artifact_id)`

func scanTrainingRun(row pgx.Row, run *TrainingRun) error {
	var manifest string
	var status string
	if err := row.Scan(
		&run.DatabaseID,
		&run.ID,
		&run.SourceAnalyticsGenerationID,
		&run.SourceAnalyticsHeadRevision,
		&run.TrainingConfigurationVersionID,
		&run.KnowledgeCatalogVersionID,
		&run.BundleProtocol,
		&manifest,
		&run.InputManifestSHA256,
		&status,
		&run.AttemptCount,
		&run.CreatedAt,
		&run.StartedAt,
		&run.FinishedAt,
		&run.InputArtifact.Hash,
		&run.InputArtifact.Size,
		&run.InputArtifact.StorageKey,
	); err != nil {
		return err
	}
	canonicalManifest, err := canonicalStoredObject(manifest, run.InputManifestSHA256, maxManifestBytes, "scan recommendation training run")
	if err != nil {
		return err
	}
	run.InputManifest = canonicalManifest
	run.Status = RunStatus(status)
	run.CreatedAt = run.CreatedAt.UTC()
	if run.StartedAt != nil {
		value := run.StartedAt.UTC()
		run.StartedAt = &value
	}
	if run.FinishedAt != nil {
		value := run.FinishedAt.UTC()
		run.FinishedAt = &value
	}
	return nil
}

func appendTrainingEvent(ctx context.Context, tx recommendationTx, runID int64, eventType string, payload map[string]any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return domainError(ErrorStoredDataInvalid, true, "encode recommendation training event", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.recommendation_training_events (
    training_run_id,
    event_sequence,
    event_type,
    payload
)
SELECT $1,
       COALESCE(MAX(event_sequence), 0) + 1,
       $2,
       $3::jsonb
FROM ascendany.recommendation_training_events
WHERE training_run_id = $1`, runID, eventType, string(payloadJSON)); err != nil {
		return databaseError("append recommendation training event", err)
	}
	return nil
}
