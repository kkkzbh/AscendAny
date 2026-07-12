package chatagent

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type existingAutoAnalysis struct {
	result     EnqueueResult
	promptKey  string
	modelKey   string
	threadKind ThreadKind
}

func (repository *PostgresRepository) EnqueueAutoAnalysis(
	ctx context.Context,
	command AutoAnalysisCommand,
) (result EnqueueResult, resultErr error) {
	if err := validateAutoAnalysisInput(ctx, command.AutoAnalysisInput); err != nil {
		return EnqueueResult{}, err
	}
	if !canonicalUUIDv4.MatchString(command.ThreadID) || !canonicalUUIDv4.MatchString(command.RunID) ||
		!canonicalUUIDv4.MatchString(command.MessageID) || !canonicalUUIDv4.MatchString(command.ClientRequestID) {
		return EnqueueResult{}, domainError(ErrorInvalidInput, true, "enqueue automatic analysis", errors.New("canonical generated IDs are required"))
	}

	resultErr = repository.transaction(ctx, "enqueue automatic analysis", pgx.TxOptions{}, func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, command.Principal, true)
		if err != nil {
			return err
		}
		analyticsGenerationID, err := resolveAnalyticsSnapshot(ctx, tx, command.ExpectedAnalyticsHeadRevision)
		if err != nil {
			return err
		}
		existing, found, err := loadExistingAutoAnalysis(ctx, tx, resolved.AccountDatabaseID, analyticsGenerationID)
		if err != nil {
			return err
		}
		if found {
			if existing.threadKind != ThreadAutoAnalysis || existing.result.Message.Content != AutoAnalysisInputContent {
				return domainError(ErrorStoredDataInvalid, true, "validate stored automatic analysis", errors.New("stored automatic analysis violates its dedicated thread contract"))
			}
			if existing.promptKey != command.PromptConfigurationKey || existing.modelKey != command.ModelConfigurationKey {
				return domainError(ErrorAutoAnalysisConflict, true, "replay automatic analysis", errors.New("analytics generation already owns different configuration keys"))
			}
			result = existing.result
			result.Created = false
			return nil
		}

		promptVersionID, err := resolveActiveConfiguration(ctx, tx, command.PromptConfigurationKey, "prompt")
		if err != nil {
			return err
		}
		modelVersionID, err := resolveActiveConfiguration(ctx, tx, command.ModelConfigurationKey, "model_connection")
		if err != nil {
			return err
		}

		var threadDatabaseID, headRevision int64
		err = tx.QueryRow(ctx, `
SELECT chat_thread_id, head_revision
FROM ascendany.chat_threads
WHERE owner_account_id = $1
  AND thread_kind = 'auto_analysis'
FOR UPDATE`, resolved.AccountDatabaseID).Scan(&threadDatabaseID, &headRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
VALUES ($1::uuid, $2, 'auto_analysis')
RETURNING chat_thread_id, head_revision`, command.ThreadID, resolved.AccountDatabaseID).Scan(&threadDatabaseID, &headRevision)
			if err != nil {
				return databaseFailure("insert automatic analysis thread", err)
			}
		} else if err != nil {
			return databaseFailure("resolve automatic analysis thread", err)
		}

		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read automatic analysis enqueue time", err)
		}
		mutationTime = mutationTime.UTC()
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
VALUES ($1::uuid, $2, $3, $4, 'auto_analysis_request', $5, $6, $7)
RETURNING chat_message_id`, command.MessageID, threadDatabaseID, resolved.AccountDatabaseID, messageSequence,
			AutoAnalysisInputContent, resolved.SessionDatabaseID, mutationTime).Scan(&messageDatabaseID); err != nil {
			return databaseFailure("insert automatic analysis message", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.chat_threads
SET head_revision = head_revision + 1,
    updated_at = $3
WHERE chat_thread_id = $1
  AND head_revision = $2`, threadDatabaseID, headRevision, mutationTime)
		if err != nil {
			return databaseFailure("advance automatic analysis thread", err)
		}
		if tag.RowsAffected() != 1 {
			return domainError(ErrorStoredDataInvalid, true, "advance automatic analysis thread", errors.New("locked thread head did not advance"))
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
VALUES ($1::uuid, $2, $3, $4, $5::uuid, 'auto_analysis', $6, 'auto_analysis_request', $7, $8, $9, 'queued', $10, $10)
RETURNING agent_run_id`, command.RunID, threadDatabaseID, resolved.AccountDatabaseID, resolved.SessionDatabaseID,
			command.ClientRequestID, messageDatabaseID, promptVersionID, modelVersionID, analyticsGenerationID, mutationTime).Scan(&runDatabaseID); err != nil {
			return databaseFailure("insert automatic analysis run", err)
		}
		if err := appendRunEvent(ctx, tx, runDatabaseID, "queued", map[string]any{
			"analyticsHeadRevision": command.ExpectedAnalyticsHeadRevision,
			"messageSequence":       messageSequence,
			"runKind":               RunAutoAnalysis,
		}); err != nil {
			return err
		}

		var threadPublicID string
		if err := tx.QueryRow(ctx, `
SELECT public_id::text
FROM ascendany.chat_threads
WHERE chat_thread_id = $1`, threadDatabaseID).Scan(&threadPublicID); err != nil {
			return databaseFailure("read automatic analysis thread ID", err)
		}
		result = EnqueueResult{
			Run: Run{
				ID: command.RunID, ThreadID: threadPublicID, ClientRequestID: command.ClientRequestID,
				Kind: RunAutoAnalysis, InputMessageID: command.MessageID, Status: RunQueued,
				CreatedAt: mutationTime, UpdatedAt: mutationTime,
			},
			Message: Message{
				ID: command.MessageID, ThreadID: threadPublicID, Sequence: messageSequence,
				Kind: MessageAutoAnalysisRequest, Content: AutoAnalysisInputContent, CreatedAt: mutationTime,
			},
			Created: true,
		}
		return nil
	})
	return result, resultErr
}

func loadExistingAutoAnalysis(
	ctx context.Context,
	tx postgresTx,
	ownerDatabaseID int64,
	analyticsGenerationID int64,
) (existingAutoAnalysis, bool, error) {
	var existing existingAutoAnalysis
	var inputKind MessageKind
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
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
       thread.thread_kind
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
WHERE run.owner_account_id = $1
  AND run.analytics_generation_id = $2
  AND run.run_kind = 'auto_analysis'`, ownerDatabaseID, analyticsGenerationID).Scan(
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
		&existing.threadKind,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return existingAutoAnalysis{}, false, nil
	}
	if err != nil {
		return existingAutoAnalysis{}, false, databaseFailure("load existing automatic analysis", err)
	}
	normalizeRunTimes(&existing.result.Run)
	existing.result.Message.ID = existing.result.Run.InputMessageID
	existing.result.Message.ThreadID = existing.result.Run.ThreadID
	existing.result.Message.Kind = inputKind
	existing.result.Message.CreatedAt = createdAt.UTC()
	return existing, true, nil
}
