package chatagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type existingEnqueue struct {
	result                EnqueueResult
	promptKey             string
	modelKey              string
	analyticsID           *int64
	analyticsHeadRevision *int64
	analyticsBaseRevision *int64
	analyticsStatus       *string
}

func (repository *PostgresRepository) Enqueue(ctx context.Context, command EnqueueCommand) (result EnqueueResult, resultErr error) {
	if err := validateEnqueueInput(ctx, command.EnqueueInput); err != nil {
		return EnqueueResult{}, err
	}
	if !canonicalUUIDv4.MatchString(command.RunID) || !canonicalUUIDv4.MatchString(command.MessageID) {
		return EnqueueResult{}, domainError(ErrorInvalidInput, true, "enqueue agent run", errors.New("canonical generated run and message IDs are required"))
	}
	resultErr = repository.transaction(ctx, "enqueue agent run", pgx.TxOptions{}, func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, command.Principal, true)
		if err != nil {
			return err
		}
		existing, found, err := loadExistingEnqueue(ctx, tx, resolved.AccountDatabaseID, command.ClientRequestID)
		if err != nil {
			return err
		}
		if found {
			if existing.result.Run.ThreadID != command.ThreadID || existing.result.Run.Kind != command.Kind ||
				existing.result.Message.Content != command.Content || existing.promptKey != command.PromptConfigurationKey ||
				existing.modelKey != command.ModelConfigurationKey ||
				(existing.analyticsHeadRevision == nil) != (command.ExpectedAnalyticsHeadRevision == nil) ||
				command.ExpectedAnalyticsHeadRevision != nil && *existing.analyticsHeadRevision != *command.ExpectedAnalyticsHeadRevision {
				return domainError(ErrorIdempotencyConflict, true, "replay agent enqueue", errors.New("client request ID already owns a different immutable request"))
			}
			if command.ExpectedAnalyticsHeadRevision == nil {
				if existing.analyticsID != nil || existing.analyticsBaseRevision != nil || existing.analyticsStatus != nil {
					return domainError(ErrorStoredDataInvalid, true, "validate stored agent enqueue", errors.New("unbound reply run unexpectedly references analytics provenance"))
				}
			} else if existing.analyticsID == nil || existing.analyticsBaseRevision == nil || existing.analyticsStatus == nil ||
				*existing.analyticsStatus != "succeeded" || *existing.analyticsBaseRevision+1 != *existing.analyticsHeadRevision {
				return domainError(ErrorStoredDataInvalid, true, "validate stored agent enqueue", errors.New("stored analytics binding violates its immutable publication contract"))
			}
			result = existing.result
			result.Created = false
			return nil
		}
		var analyticsGenerationID *int64
		if command.ExpectedAnalyticsHeadRevision != nil {
			resolvedGenerationID, err := resolveAnalyticsSnapshot(ctx, tx, *command.ExpectedAnalyticsHeadRevision)
			if err != nil {
				return err
			}
			analyticsGenerationID = &resolvedGenerationID
		}

		var threadDatabaseID, headRevision int64
		var threadKind ThreadKind
		if err := tx.QueryRow(ctx, `
SELECT chat_thread_id, thread_kind, head_revision
FROM ascendany.chat_threads
WHERE public_id = $1::uuid
  AND owner_account_id = $2
FOR UPDATE`, command.ThreadID, resolved.AccountDatabaseID).Scan(&threadDatabaseID, &threadKind, &headRevision); errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorNotFound, true, "resolve agent run thread", errors.New("chat thread was not found"))
		} else if err != nil {
			return databaseFailure("resolve agent run thread", err)
		}
		if threadKind != ThreadConversation {
			return domainError(ErrorThreadKindConflict, true, "resolve agent run thread", errors.New("reply runs require a conversation thread"))
		}

		promptVersionID, err := resolveActiveConfiguration(ctx, tx, command.PromptConfigurationKey, "prompt")
		if err != nil {
			return err
		}
		modelVersionID, err := resolveActiveConfiguration(ctx, tx, command.ModelConfigurationKey, "model_connection")
		if err != nil {
			return err
		}
		providerMetadata, hasProviderMetadata, err := loadFrontendProviderMetadata(ctx, tx, modelVersionID)
		if err != nil {
			return err
		}

		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read agent enqueue time", err)
		}
		mutationTime = mutationTime.UTC()
		messageKind := MessageUser
		messageSequence := headRevision + 1
		var messageDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.chat_messages (
    public_id,
    chat_thread_id,
    owner_account_id,
    message_sequence,
    message_kind,
    content,
    author_session_id,
    created_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
RETURNING chat_message_id`, command.MessageID, threadDatabaseID, resolved.AccountDatabaseID, messageSequence,
			string(messageKind), command.Content, resolved.SessionDatabaseID, mutationTime).Scan(&messageDatabaseID); err != nil {
			return databaseFailure("insert agent input message", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.chat_threads
SET head_revision = head_revision + 1,
    updated_at = $3
WHERE chat_thread_id = $1
  AND head_revision = $2`, threadDatabaseID, headRevision, mutationTime)
		if err != nil {
			return databaseFailure("advance chat thread head", err)
		}
		if tag.RowsAffected() != 1 {
			return domainError(ErrorStoredDataInvalid, true, "advance chat thread head", errors.New("locked thread head did not advance"))
		}

		var runDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.agent_runs (
    public_id,
    chat_thread_id,
    owner_account_id,
    request_session_id,
    client_request_id,
    run_kind,
    input_message_id,
    input_message_kind,
    prompt_configuration_version_id,
    model_configuration_version_id,
    analytics_generation_id,
    status,
    created_at,
    updated_at
)
VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6, $7, $8, $9, $10, $11, 'queued', $12, $12)
RETURNING agent_run_id`, command.RunID, threadDatabaseID, resolved.AccountDatabaseID, resolved.SessionDatabaseID,
			command.ClientRequestID, string(command.Kind), messageDatabaseID, string(messageKind), promptVersionID,
			modelVersionID, analyticsGenerationID, mutationTime).Scan(&runDatabaseID); err != nil {
			return databaseFailure("insert agent run", err)
		}
		queuedPayload := map[string]any{
			"messageSequence": messageSequence,
			"runKind":         command.Kind,
		}
		if command.ExpectedAnalyticsHeadRevision != nil {
			queuedPayload["analyticsHeadRevision"] = *command.ExpectedAnalyticsHeadRevision
		}
		if hasProviderMetadata {
			addFrontendProviderMetadata(queuedPayload, providerMetadata)
		}
		if err := appendRunEvent(ctx, tx, runDatabaseID, "queued", queuedPayload); err != nil {
			return err
		}
		result = EnqueueResult{
			Run: Run{
				ID: command.RunID, ThreadID: command.ThreadID, ClientRequestID: command.ClientRequestID,
				Kind: command.Kind, InputMessageID: command.MessageID, Status: RunQueued,
				CreatedAt: mutationTime, UpdatedAt: mutationTime,
			},
			Message: Message{
				ID: command.MessageID, ThreadID: command.ThreadID, Sequence: messageSequence,
				Kind: messageKind, Content: command.Content, CreatedAt: mutationTime,
			},
			Created: true,
		}
		return nil
	})
	return result, resultErr
}

func resolveActiveConfiguration(ctx context.Context, tx postgresTx, key, kind string) (int64, error) {
	var versionID int64
	err := tx.QueryRow(ctx, `
SELECT version.configuration_version_id
FROM ascendany.configuration_items AS item
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = item.active_version_id
 AND version.configuration_item_id = item.configuration_item_id
 AND version.configuration_kind = item.configuration_kind
WHERE item.configuration_key = $1
  AND item.configuration_kind = $2
FOR SHARE OF item`, key, kind).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domainError(ErrorConfigurationMissing, true, "resolve active agent configuration", fmt.Errorf("active %s configuration %q is unavailable", kind, key))
	}
	if err != nil {
		return 0, databaseFailure("resolve active agent configuration", err)
	}
	return versionID, nil
}

func resolveAnalyticsSnapshot(ctx context.Context, tx postgresTx, expectedRevision int64) (int64, error) {
	var generationID, headRevision int64
	var status string
	err := tx.QueryRow(ctx, `
SELECT generation.analytics_generation_id,
       head.head_revision,
       generation.status
FROM ascendany.analytics_head AS head
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
WHERE head.singleton
FOR SHARE OF head, generation`).Scan(&generationID, &headRevision, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domainError(ErrorAnalyticsConflict, true, "resolve analytics snapshot", errors.New("published analytics head is unavailable"))
	}
	if err != nil {
		return 0, databaseFailure("resolve analytics snapshot", err)
	}
	if headRevision != expectedRevision {
		return 0, domainError(ErrorAnalyticsConflict, true, "resolve analytics snapshot", fmt.Errorf("expected analytics head revision %d, found %d", expectedRevision, headRevision))
	}
	if status != "succeeded" {
		return 0, domainError(ErrorStoredDataInvalid, true, "resolve analytics snapshot", fmt.Errorf("published analytics generation has status %q", status))
	}
	return generationID, nil
}

func loadExistingEnqueue(ctx context.Context, tx postgresTx, ownerDatabaseID int64, clientRequestID string) (existingEnqueue, bool, error) {
	var existing existingEnqueue
	var inputKind MessageKind
	var createdAt time.Time
	row := tx.QueryRow(ctx, `
SELECT run.public_id::text,
       thread.public_id::text,
       run.client_request_id::text,
       run.run_kind,
       input.public_id::text,
       output.public_id::text,
       run.status,
       run.attempt_count,
       run.error_code,
       run.error_detail,
       run.created_at,
       run.started_at,
       run.finished_at,
       run.updated_at,
       input.message_sequence,
       input.message_kind,
       input.content,
       input.created_at,
       prompt_item.configuration_key,
	       model_item.configuration_key,
	       run.analytics_generation_id,
	       NULLIF(queued_event.payload ->> 'analyticsHeadRevision', '')::bigint,
	       generation.base_head_revision,
	       generation.status
FROM ascendany.agent_runs AS run
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = run.chat_thread_id
JOIN ascendany.chat_messages AS input
  ON input.chat_message_id = run.input_message_id
LEFT JOIN ascendany.chat_messages AS output
  ON output.chat_message_id = run.output_message_id
JOIN ascendany.configuration_versions AS prompt_version
  ON prompt_version.configuration_version_id = run.prompt_configuration_version_id
JOIN ascendany.configuration_items AS prompt_item
  ON prompt_item.configuration_item_id = prompt_version.configuration_item_id
JOIN ascendany.configuration_versions AS model_version
  ON model_version.configuration_version_id = run.model_configuration_version_id
JOIN ascendany.configuration_items AS model_item
  ON model_item.configuration_item_id = model_version.configuration_item_id
JOIN ascendany.agent_run_events AS queued_event
  ON queued_event.agent_run_id = run.agent_run_id
 AND queued_event.event_sequence = 1
 AND queued_event.event_type = 'queued'
LEFT JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = run.analytics_generation_id
WHERE run.owner_account_id = $1
  AND run.client_request_id = $2::uuid`, ownerDatabaseID, clientRequestID)
	err := row.Scan(
		&existing.result.Run.ID,
		&existing.result.Run.ThreadID,
		&existing.result.Run.ClientRequestID,
		&existing.result.Run.Kind,
		&existing.result.Run.InputMessageID,
		&existing.result.Run.OutputMessageID,
		&existing.result.Run.Status,
		&existing.result.Run.AttemptCount,
		&existing.result.Run.ErrorCode,
		&existing.result.Run.ErrorDetail,
		&existing.result.Run.CreatedAt,
		&existing.result.Run.StartedAt,
		&existing.result.Run.FinishedAt,
		&existing.result.Run.UpdatedAt,
		&existing.result.Message.Sequence,
		&inputKind,
		&existing.result.Message.Content,
		&createdAt,
		&existing.promptKey,
		&existing.modelKey,
		&existing.analyticsID,
		&existing.analyticsHeadRevision,
		&existing.analyticsBaseRevision,
		&existing.analyticsStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return existingEnqueue{}, false, nil
	}
	if err != nil {
		return existingEnqueue{}, false, databaseFailure("load existing agent enqueue", err)
	}
	normalizeRunTimes(&existing.result.Run)
	existing.result.Message.ID = existing.result.Run.InputMessageID
	existing.result.Message.ThreadID = existing.result.Run.ThreadID
	existing.result.Message.Kind = inputKind
	existing.result.Message.CreatedAt = createdAt.UTC()
	return existing, true, nil
}
