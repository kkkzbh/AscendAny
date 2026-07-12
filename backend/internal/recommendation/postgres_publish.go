package recommendation

import (
	"bytes"
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func (repository *PostgresRepository) PublishTrainingOutput(
	ctx context.Context,
	command PublishCommand,
) (published PublishResult, resultErr error) {
	if err := validateClaim(command.Claim); err != nil {
		return PublishResult{}, err
	}
	if !canonicalUUIDv4Pattern.MatchString(command.ModelPublicID) || command.MediaType != TrainingOutputMediaTypeV2 ||
		len(command.Input.CanonicalJSON) == 0 || len(command.Output.CanonicalJSON) == 0 {
		return PublishResult{}, domainError(ErrorInvalidInput, true, "publish recommendation training output", errors.New("canonical model ID, output, and media type are required"))
	}
	if err := validateArtifact(command.Artifact, command.Output.SHA256, int64(len(command.Output.CanonicalJSON))); err != nil {
		return PublishResult{}, err
	}
	if command.Receipt != nil {
		if err := validateTrainerAgentReceiptReference(
			command.Receipt.Attempt, command.Receipt.Operation, command.Receipt.RequestSHA256,
		); err != nil {
			return PublishResult{}, err
		}
		if command.Receipt.Operation != TrainerAgentOutputOperation ||
			command.Receipt.Attempt != trainerAgentAttemptFromClaim(command.Claim) {
			return PublishResult{}, domainError(ErrorInvalidInput, true, "publish recommendation trainer-agent output", errors.New("terminal receipt does not bind the claimed output attempt"))
		}
	}
	resultErr = repository.transaction(ctx, "publish recommendation training output", pgx.TxOptions{}, func(tx recommendationTx) error {
		if command.Receipt != nil {
			replayed, err := lookupTrainerAgentTerminalReceipt(
				ctx, tx, command.Receipt.Attempt, command.Receipt.Operation, command.Receipt.RequestSHA256,
			)
			if err != nil {
				return err
			}
			if replayed != nil {
				var err error
				published, err = publishResultFromTrainerAgentReceipt(*replayed)
				return err
			}
		}
		stored, err := lockTrainingClaim(ctx, tx, command.Claim)
		if err != nil {
			if command.Receipt != nil && CodeOf(err) == ErrorLeaseLost {
				replayed, replayErr := lookupTrainerAgentTerminalReceipt(
					ctx, tx, command.Receipt.Attempt, command.Receipt.Operation, command.Receipt.RequestSHA256,
				)
				if replayErr != nil {
					return replayErr
				}
				if replayed != nil {
					published, replayErr = publishResultFromTrainerAgentReceipt(*replayed)
					return replayErr
				}
			}
			return err
		}
		canonicalInput, inputSHA256, err := canonicaljson.Object(
			command.Input.CanonicalJSON, len(command.Input.CanonicalJSON),
		)
		if err != nil || !bytes.Equal(canonicalInput, command.Input.CanonicalJSON) ||
			inputSHA256 != stored.InputArtifact.Hash || int64(len(canonicalInput)) != stored.InputArtifact.Size {
			return domainError(ErrorInvalidBundle, true, "publish recommendation training output", errors.New("input bundle differs from the immutable training artifact"))
		}
		parsedInput, err := ParseInputBundle(canonicalInput, len(canonicalInput), stored)
		if err != nil {
			return err
		}
		parsed, err := ParseOutputBundle(
			command.Output.CanonicalJSON,
			len(command.Output.CanonicalJSON),
			parsedInput,
		)
		if err != nil {
			return err
		}
		if !sameParsedOutput(parsed, command.Output) {
			return domainError(ErrorInvalidBundle, true, "publish recommendation training output", errors.New("parsed output differs from the publish command"))
		}
		artifactID, err := ensureArtifact(ctx, tx, command.Artifact, command.MediaType)
		if err != nil {
			return err
		}

		var analyticsGenerationID *int64
		var analyticsHeadRevision int64
		if err := tx.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton
FOR UPDATE`).Scan(&analyticsGenerationID, &analyticsHeadRevision); err != nil {
			return databaseError("lock analytics head for recommendation publication", err)
		}
		var currentModelID *int64
		var currentSourceRevision *int64
		var recommendationHeadRevision int64
		if err := tx.QueryRow(ctx, `
SELECT current_model_id, source_analytics_head_revision, head_revision
FROM ascendany.recommendation_head
WHERE singleton
FOR UPDATE`).Scan(&currentModelID, &currentSourceRevision, &recommendationHeadRevision); err != nil {
			return databaseError("lock recommendation head", err)
		}
		if recommendationHeadRevision == math.MaxInt64 {
			return domainError(ErrorStateConflict, false, "advance recommendation head", errors.New("recommendation head revision is exhausted"))
		}
		activate := analyticsGenerationID != nil && *analyticsGenerationID == stored.SourceAnalyticsGenerationID &&
			analyticsHeadRevision == stored.SourceAnalyticsHeadRevision &&
			(currentSourceRevision == nil || stored.SourceAnalyticsHeadRevision > *currentSourceRevision)
		outcome := string(RunSuperseded)
		disposition := PublishSuperseded
		if activate {
			outcome = string(RunSucceeded)
			disposition = PublishActivated
		}
		commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.recommendation_training_runs
SET output_bundle_artifact_id = $6,
    status = $7,
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = NULL,
    error_detail = NULL,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE training_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`,
			stored.DatabaseID, stored.ID, command.Claim.AttemptCount, command.Claim.AttemptToken,
			command.Claim.LeaseOwner, artifactID, outcome)
		if err != nil {
			return databaseError("complete recommendation training run", err)
		}
		if commandTag.RowsAffected() != 1 {
			return domainError(ErrorLeaseLost, false, "complete recommendation training run", errors.New("training attempt is no longer active"))
		}

		var modelDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.recommendation_models (
    public_id,
    training_run_id,
    source_analytics_generation_id,
    source_analytics_head_revision,
    output_bundle_artifact_id,
    training_outcome,
    model_schema,
    model_manifest,
    model_manifest_sha256,
    metrics
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10::jsonb)
RETURNING recommendation_model_id`,
			command.ModelPublicID,
			stored.DatabaseID,
			stored.SourceAnalyticsGenerationID,
			stored.SourceAnalyticsHeadRevision,
			artifactID,
			outcome,
			parsed.Model.Schema,
			string(parsed.Model.Manifest),
			parsed.Model.ManifestSHA256,
			string(parsed.Model.Metrics),
		).Scan(&modelDatabaseID); err != nil {
			return databaseError("insert recommendation model", err)
		}
		for _, result := range parsed.Results {
			if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.student_recommendation_results (
    recommendation_model_id,
    actor_id,
    result_schema,
    result,
    result_sha256
)
VALUES ($1, $2, $3, $4::jsonb, $5)`,
				modelDatabaseID,
				result.ActorID,
				result.Schema,
				string(result.Result),
				result.ResultSHA256,
			); err != nil {
				return databaseError("insert student recommendation result", err)
			}
		}
		var activatedHeadRevision *int64
		if activate {
			nextRevision := recommendationHeadRevision + 1
			commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.recommendation_head
SET current_model_id = $1,
    source_analytics_generation_id = $2,
    source_analytics_head_revision = $3,
    current_model_outcome = 'succeeded',
    head_revision = $4,
    updated_at = clock_timestamp()
WHERE singleton
  AND current_model_id IS NOT DISTINCT FROM $5
  AND head_revision = $6`,
				modelDatabaseID,
				stored.SourceAnalyticsGenerationID,
				stored.SourceAnalyticsHeadRevision,
				nextRevision,
				currentModelID,
				recommendationHeadRevision,
			)
			if err != nil {
				return databaseError("advance recommendation head", err)
			}
			if commandTag.RowsAffected() != 1 {
				return domainError(ErrorStateConflict, false, "advance recommendation head", errors.New("recommendation head CAS failed while locked"))
			}
			activatedHeadRevision = &nextRevision
		}
		eventType := "superseded"
		if activate {
			eventType = "activated"
		}
		if err := appendTrainingEvent(ctx, tx, stored.DatabaseID, eventType, map[string]any{
			"modelId":                     command.ModelPublicID,
			"outputArtifactSha256":        command.Artifact.Hash,
			"sourceAnalyticsGenerationId": stored.SourceAnalyticsGenerationID,
			"sourceAnalyticsHeadRevision": stored.SourceAnalyticsHeadRevision,
		}); err != nil {
			return err
		}
		published = PublishResult{
			Disposition:                disposition,
			ModelID:                    command.ModelPublicID,
			RecommendationHeadRevision: activatedHeadRevision,
		}
		if command.Receipt != nil {
			modelID := command.ModelPublicID
			runtimeConstructionSHA256 := parsed.Model.RuntimeConstructionSHA256
			runtimeProvenanceSHA256 := parsed.Model.RuntimeProvenanceSHA256
			runtimeTreeSHA256 := parsed.Model.RuntimeTreeSHA256
			hostCapabilitySHA256 := parsed.Model.HostCapabilitySHA256
			runtimeAttestationSHA256 := parsed.Model.RuntimeAttestationSHA256
			result := TrainerAgentSuperseded
			if disposition == PublishActivated {
				result = TrainerAgentActivated
			}
			receipt := TrainerAgentTerminalReceipt{
				Operation: TrainerAgentOutputOperation, RequestSHA256: command.Receipt.RequestSHA256,
				Result: result, ModelID: &modelID,
				RuntimeConstructionSHA256: &runtimeConstructionSHA256,
				RuntimeProvenanceSHA256:   &runtimeProvenanceSHA256,
				RuntimeTreeSHA256:         &runtimeTreeSHA256,
				HostCapabilitySHA256:      &hostCapabilitySHA256,
				RuntimeAttestationSHA256:  &runtimeAttestationSHA256,
			}
			if err := insertTrainerAgentTerminalReceipt(
				ctx, tx, stored.DatabaseID, command.Receipt.Attempt, receipt,
			); err != nil {
				return err
			}
		}
		return nil
	})
	return published, resultErr
}

func publishResultFromTrainerAgentReceipt(receipt TrainerAgentTerminalReceipt) (PublishResult, error) {
	if err := validateTrainerAgentTerminalReceipt(receipt); err != nil {
		return PublishResult{}, domainError(ErrorStoredDataInvalid, true, "replay recommendation trainer-agent output", err)
	}
	if receipt.ModelID == nil {
		return PublishResult{}, domainError(ErrorLeaseLost, false, "replay recommendation trainer-agent output", errors.New("attempt completed without an output publication"))
	}
	disposition := PublishSuperseded
	if receipt.Result == TrainerAgentActivated {
		disposition = PublishActivated
	} else if receipt.Result != TrainerAgentSuperseded {
		return PublishResult{}, domainError(ErrorLeaseLost, false, "replay recommendation trainer-agent output", errors.New("attempt completed with a different terminal operation"))
	}
	return PublishResult{Disposition: disposition, ModelID: *receipt.ModelID}, nil
}

func lockTrainingClaim(ctx context.Context, tx recommendationTx, claim Claim) (TrainingRun, error) {
	stored := TrainingRun{}
	var manifest, status string
	err := tx.QueryRow(ctx, `
SELECT run.training_run_id,
       run.public_id::text,
       run.source_analytics_generation_id,
	       run.source_analytics_head_revision,
	       run.training_configuration_version_id,
	       run.knowledge_catalog_version_id,
	       run.bundle_protocol,
       run.input_manifest::text,
       run.input_manifest_sha256,
       run.status,
       run.attempt_count,
       run.created_at,
       run.started_at,
       run.finished_at,
       artifact.sha256,
       artifact.size_bytes,
       artifact.storage_key
FROM ascendany.recommendation_training_runs AS run
JOIN ascendany.artifacts AS artifact
  ON artifact.artifact_id = run.input_bundle_artifact_id
WHERE run.training_run_id = $1
  AND run.public_id = $2::uuid
  AND run.status = 'running'
  AND run.attempt_count = $3
  AND run.attempt_token = $4::uuid
  AND run.lease_owner = $5
  AND run.lease_expires_at > clock_timestamp()
FOR UPDATE OF run`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner).Scan(
		&stored.DatabaseID,
		&stored.ID,
		&stored.SourceAnalyticsGenerationID,
		&stored.SourceAnalyticsHeadRevision,
		&stored.TrainingConfigurationVersionID,
		&stored.KnowledgeCatalogVersionID,
		&stored.BundleProtocol,
		&manifest,
		&stored.InputManifestSHA256,
		&status,
		&stored.AttemptCount,
		&stored.CreatedAt,
		&stored.StartedAt,
		&stored.FinishedAt,
		&stored.InputArtifact.Hash,
		&stored.InputArtifact.Size,
		&stored.InputArtifact.StorageKey,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrainingRun{}, domainError(ErrorLeaseLost, false, "lock recommendation training claim", errors.New("training attempt is no longer active"))
	}
	if err != nil {
		return TrainingRun{}, databaseError("lock recommendation training claim", err)
	}
	stored.Status = RunStatus(status)
	canonicalManifest, err := canonicalStoredObject(manifest, stored.InputManifestSHA256, maxManifestBytes, "lock recommendation training claim")
	if err != nil {
		return TrainingRun{}, err
	}
	stored.InputManifest = canonicalManifest
	if stored.SourceAnalyticsGenerationID != claim.SourceAnalyticsGenerationID ||
		stored.SourceAnalyticsHeadRevision != claim.SourceAnalyticsHeadRevision ||
		stored.TrainingConfigurationVersionID != claim.TrainingConfigurationVersionID ||
		stored.KnowledgeCatalogVersionID != claim.KnowledgeCatalogVersionID ||
		stored.BundleProtocol != claim.BundleProtocol ||
		stored.InputManifestSHA256 != claim.InputManifestSHA256 ||
		!bytes.Equal(stored.InputManifest, claim.InputManifest) ||
		stored.InputArtifact.Hash != claim.InputArtifact.Hash ||
		stored.InputArtifact.Size != claim.InputArtifact.Size ||
		stored.InputArtifact.StorageKey != claim.InputArtifact.StorageKey {
		return TrainingRun{}, domainError(ErrorStateConflict, false, "lock recommendation training claim", errors.New("claimed immutable provenance columns changed"))
	}
	return stored, nil
}

func sameParsedOutput(left, right ParsedOutputBundle) bool {
	if left.SHA256 != right.SHA256 || left.InputManifestSHA256 != right.InputManifestSHA256 ||
		!bytes.Equal(left.CanonicalJSON, right.CanonicalJSON) || left.Model.Schema != right.Model.Schema ||
		left.Model.ManifestSHA256 != right.Model.ManifestSHA256 ||
		left.Model.RuntimeConstructionSHA256 != right.Model.RuntimeConstructionSHA256 ||
		left.Model.RuntimeProvenanceSHA256 != right.Model.RuntimeProvenanceSHA256 ||
		left.Model.RuntimeTreeSHA256 != right.Model.RuntimeTreeSHA256 ||
		left.Model.HostCapabilitySHA256 != right.Model.HostCapabilitySHA256 ||
		left.Model.RuntimeAttestationSHA256 != right.Model.RuntimeAttestationSHA256 ||
		!bytes.Equal(left.Model.Manifest, right.Model.Manifest) || !bytes.Equal(left.Model.Metrics, right.Model.Metrics) ||
		len(left.Results) != len(right.Results) {
		return false
	}
	for index := range left.Results {
		if left.Results[index].ActorID != right.Results[index].ActorID ||
			left.Results[index].Schema != right.Results[index].Schema ||
			left.Results[index].ResultSHA256 != right.Results[index].ResultSHA256 ||
			!bytes.Equal(left.Results[index].Result, right.Results[index].Result) {
			return false
		}
	}
	return true
}
