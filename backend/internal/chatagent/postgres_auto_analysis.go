package chatagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type existingAutoAnalysis struct {
	result     EnqueueResult
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
	inputContent, err := canonicalAutoAnalysisInputContent(command.FrontendContext)
	if err != nil {
		return EnqueueResult{}, domainError(ErrorInvalidInput, true, "encode automatic analysis frontend context", err)
	}

	resultErr = repository.transaction(ctx, "enqueue automatic analysis", pgx.TxOptions{}, func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, command.Principal, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			autoAnalysisLockKey(resolved.AccountDatabaseID, command.Identity.ExamID, command.Identity.RoleID)); err != nil {
			return databaseFailure("lock automatic analysis identity", err)
		}
		existing, found, err := loadExistingAutoAnalysis(
			ctx,
			tx,
			resolved.AccountDatabaseID,
			command.Identity.ExamID,
			command.Identity.RoleID,
		)
		if err != nil {
			return err
		}
		if found {
			if existing.threadKind != ThreadAutoAnalysis {
				return domainError(ErrorStoredDataInvalid, true, "validate stored automatic analysis", errors.New("stored automatic analysis violates its dedicated thread contract"))
			}
			storedContext, err := decodeAutoAnalysisInputContent(existing.result.Message.Content)
			if err != nil {
				return domainError(ErrorStoredDataInvalid, true, "validate stored automatic analysis frontend context", err)
			}
			if storedContext.LatestExamID != command.Identity.ExamID || storedContext.RoleID != command.Identity.RoleID {
				return domainError(ErrorStoredDataInvalid, true, "validate stored automatic analysis identity", errors.New("stored automatic-analysis input disagrees with its durable identity"))
			}
			result = existing.result
			result.Created = false
			return nil
		}
		analyticsGenerationID, err := resolveAnalyticsSnapshot(ctx, tx, command.ExpectedAnalyticsHeadRevision)
		if err != nil {
			return err
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
			inputContent, resolved.SessionDatabaseID, mutationTime).Scan(&messageDatabaseID); err != nil {
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
	    auto_analysis_exam_id,
	    auto_analysis_role_id,
	    status,
    created_at,
    updated_at
)
VALUES ($1::uuid, $2, $3, $4, $5::uuid, 'auto_analysis', $6, 'auto_analysis_request', $7, $8, $9, $10::uuid, $11, 'queued', $12, $12)
RETURNING agent_run_id`, command.RunID, threadDatabaseID, resolved.AccountDatabaseID, resolved.SessionDatabaseID,
			command.ClientRequestID, messageDatabaseID, promptVersionID, modelVersionID, analyticsGenerationID,
			command.Identity.ExamID, command.Identity.RoleID, mutationTime).Scan(&runDatabaseID); err != nil {
			return databaseFailure("insert automatic analysis run", err)
		}
		queuedPayload := map[string]any{
			"analyticsHeadRevision": command.ExpectedAnalyticsHeadRevision,
			"autoAnalysisExamId":    command.Identity.ExamID,
			"autoAnalysisRoleId":    command.Identity.RoleID,
			"messageSequence":       messageSequence,
			"runKind":               RunAutoAnalysis,
		}
		if hasProviderMetadata {
			addFrontendProviderMetadata(queuedPayload, providerMetadata)
		}
		if err := appendRunEvent(ctx, tx, runDatabaseID, "queued", queuedPayload); err != nil {
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
				Kind: MessageAutoAnalysisRequest, Content: inputContent, CreatedAt: mutationTime,
			},
			Created: true,
		}
		return nil
	})
	return result, resultErr
}

func autoAnalysisLockKey(accountDatabaseID int64, examID, roleID string) string {
	return fmt.Sprintf("auto-analysis:%d:%d:%s:%d:%s", accountDatabaseID, len(examID), examID, len(roleID), roleID)
}

func loadExistingAutoAnalysis(
	ctx context.Context,
	tx postgresTx,
	ownerDatabaseID int64,
	examID string,
	roleID string,
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
	       thread.thread_kind
FROM ascendany.agent_runs AS run
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = run.chat_thread_id
JOIN ascendany.chat_messages AS input
  ON input.chat_message_id = run.input_message_id
LEFT JOIN ascendany.chat_messages AS output
  ON output.chat_message_id = run.output_message_id
WHERE run.owner_account_id = $1
  AND run.auto_analysis_exam_id = $2
  AND run.auto_analysis_role_id = $3
	  AND run.run_kind = 'auto_analysis'`, ownerDatabaseID, examID, roleID).Scan(
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
