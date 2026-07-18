package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func (repository *PostgresRepository) Claim(
	ctx context.Context,
	owner string,
	attemptToken string,
	leaseDuration time.Duration,
) (claim *Claim, resultErr error) {
	if strings.TrimSpace(owner) != owner || owner == "" || len(owner) > 128 || !utf8.ValidString(owner) || !canonicalUUIDv4.MatchString(attemptToken) ||
		leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return nil, domainError(ErrorInvalidInput, true, "claim agent run", errors.New("bounded owner, canonical attempt token, and positive lease are required"))
	}
	resultErr = repository.transaction(ctx, "claim agent run", pgx.TxOptions{}, func(tx postgresTx) error {
		candidate := Claim{}
		var previousStatus string
		err := tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT candidate_run.agent_run_id, candidate_run.status
    FROM ascendany.agent_runs AS candidate_run
    WHERE (
        (
            candidate_run.status = 'queued'
            AND candidate_run.next_attempt_at <= clock_timestamp()
        ) OR (
            candidate_run.status = 'running'
            AND candidate_run.lease_expires_at <= clock_timestamp()
        )
    )
    AND NOT EXISTS (
        SELECT 1
        FROM ascendany.agent_runs AS earlier_run
        WHERE earlier_run.chat_thread_id = candidate_run.chat_thread_id
          AND earlier_run.agent_run_id < candidate_run.agent_run_id
          AND earlier_run.status IN ('queued', 'running')
    )
    ORDER BY
        CASE
            WHEN candidate_run.status = 'queued' THEN candidate_run.next_attempt_at
            ELSE candidate_run.lease_expires_at
        END,
        candidate_run.agent_run_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE ascendany.agent_runs AS run
SET status = 'running',
    attempt_count = run.attempt_count + 1,
    attempt_token = $2::uuid,
    lease_owner = $1,
    lease_expires_at = clock_timestamp() + ($3::bigint * interval '1 millisecond'),
    started_at = COALESCE(run.started_at, clock_timestamp()),
    updated_at = clock_timestamp()
FROM candidate
WHERE run.agent_run_id = candidate.agent_run_id
RETURNING run.agent_run_id,
          run.public_id::text,
          run.attempt_count,
          run.attempt_token::text,
          run.lease_owner,
          run.lease_expires_at,
          candidate.status`, owner, attemptToken, leaseDuration.Milliseconds()).Scan(
			&candidate.DatabaseID,
			&candidate.ID,
			&candidate.AttemptCount,
			&candidate.AttemptToken,
			&candidate.LeaseOwner,
			&candidate.LeaseExpiresAt,
			&previousStatus,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			claim = nil
			return nil
		}
		if err != nil {
			return databaseFailure("claim agent run", err)
		}
		candidate.LeaseExpiresAt = candidate.LeaseExpiresAt.UTC()
		candidate.Reclaimed = previousStatus == "running"
		eventType := "claimed"
		if candidate.Reclaimed {
			eventType = "reclaimed"
		}
		if err := appendRunEvent(ctx, tx, candidate.DatabaseID, eventType, map[string]any{
			"attemptCount": candidate.AttemptCount,
			"leaseOwner":   candidate.LeaseOwner,
		}); err != nil {
			return err
		}
		claim = &candidate
		return nil
	})
	return claim, resultErr
}

func (repository *PostgresRepository) RenewLease(ctx context.Context, claim Claim, leaseDuration time.Duration) error {
	if err := validateClaim(claim); err != nil {
		return err
	}
	if leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return domainError(ErrorInvalidInput, true, "renew agent run lease", errors.New("positive lease duration is required"))
	}
	return repository.transaction(ctx, "renew agent run lease", pgx.TxOptions{}, func(tx postgresTx) error {
		var expiresAt time.Time
		err := tx.QueryRow(ctx, `
UPDATE ascendany.agent_runs
SET lease_expires_at = clock_timestamp() + ($6::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE agent_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()
RETURNING lease_expires_at`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner,
			leaseDuration.Milliseconds()).Scan(&expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return leaseLost("renew agent run lease")
		}
		if err != nil {
			return databaseFailure("renew agent run lease", err)
		}
		return nil
	})
}

func (repository *PostgresRepository) LoadWork(ctx context.Context, claim Claim, maximumContextItems int) (work Work, resultErr error) {
	if err := validateClaim(claim); err != nil {
		return Work{}, err
	}
	if maximumContextItems < 1 || maximumContextItems > 1000 {
		return Work{}, domainError(ErrorInvalidInput, true, "load agent work", errors.New("bounded context item count is required"))
	}
	resultErr = repository.transaction(ctx, "load agent work", readOnlyOptions(), func(tx postgresTx) error {
		var (
			inputSequence         int64
			analyticsGenerationID *int64
			analyticsHeadRevision *int64
			analyticsStatus       *string
			promptDocument        string
			modelDocument         string
		)
		err := tx.QueryRow(ctx, `
SELECT run.public_id::text,
       run.run_kind,
       thread.public_id::text,
       account.student_number,
       input.public_id::text,
       input.message_sequence,
	       run.analytics_generation_id,
	       NULLIF(queued_event.payload ->> 'analyticsHeadRevision', '')::bigint,
	       generation.status,
       prompt_version.configuration_version_id,
       prompt_item.configuration_key,
       prompt_version.schema_id,
       prompt_version.document::text,
       prompt_version.document_sha256,
       prompt_version.credential_ref,
       model_version.configuration_version_id,
       model_item.configuration_key,
       model_version.schema_id,
       model_version.document::text,
       model_version.document_sha256,
       model_version.credential_ref
FROM ascendany.agent_runs AS run
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = run.chat_thread_id
JOIN ascendany.auth_accounts AS account
  ON account.account_id = run.owner_account_id
JOIN ascendany.chat_messages AS input
  ON input.chat_message_id = run.input_message_id
JOIN ascendany.configuration_versions AS prompt_version
  ON prompt_version.configuration_version_id = run.prompt_configuration_version_id
 AND prompt_version.configuration_kind = 'prompt'
JOIN ascendany.configuration_items AS prompt_item
  ON prompt_item.configuration_item_id = prompt_version.configuration_item_id
JOIN ascendany.configuration_versions AS model_version
  ON model_version.configuration_version_id = run.model_configuration_version_id
 AND model_version.configuration_kind = 'model_connection'
JOIN ascendany.configuration_items AS model_item
  ON model_item.configuration_item_id = model_version.configuration_item_id
LEFT JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = run.analytics_generation_id
JOIN ascendany.agent_run_events AS queued_event
  ON queued_event.agent_run_id = run.agent_run_id
 AND queued_event.event_sequence = 1
 AND queued_event.event_type = 'queued'
WHERE run.agent_run_id = $1
  AND run.public_id = $2::uuid
  AND run.status = 'running'
  AND run.attempt_count = $3
  AND run.attempt_token = $4::uuid
  AND run.lease_owner = $5
  AND run.lease_expires_at > clock_timestamp()`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner).Scan(
			&work.RunID,
			&work.Kind,
			&work.ThreadID,
			&work.StudentNumber,
			&work.InputMessageID,
			&inputSequence,
			&analyticsGenerationID,
			&analyticsHeadRevision,
			&analyticsStatus,
			&work.Prompt.VersionDatabaseID,
			&work.Prompt.Key,
			&work.Prompt.SchemaID,
			&promptDocument,
			&work.Prompt.DocumentSHA256,
			&work.Prompt.CredentialRef,
			&work.Model.VersionDatabaseID,
			&work.Model.Key,
			&work.Model.SchemaID,
			&modelDocument,
			&work.Model.DocumentSHA256,
			&work.Model.CredentialRef,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return leaseLost("load agent work")
		}
		if err != nil {
			return databaseFailure("load agent work", err)
		}
		if work.Kind != RunReply && work.Kind != RunAutoAnalysis {
			return domainError(ErrorStoredDataInvalid, true, "validate agent run kind", fmt.Errorf("stored run kind %q is invalid", work.Kind))
		}
		analyticsAbsent := analyticsGenerationID == nil && analyticsHeadRevision == nil && analyticsStatus == nil
		analyticsBound := analyticsGenerationID != nil && analyticsHeadRevision != nil && *analyticsHeadRevision > 0 &&
			analyticsStatus != nil && *analyticsStatus == "succeeded"
		if !analyticsAbsent && !analyticsBound || work.Kind == RunAutoAnalysis && !analyticsBound {
			return domainError(ErrorStoredDataInvalid, true, "validate agent analytics snapshot", errors.New("agent analytics provenance is incomplete or invalid"))
		}
		if analyticsBound {
			work.Analytics = &AnalyticsSnapshot{
				GenerationDatabaseID: *analyticsGenerationID,
				HeadRevision:         *analyticsHeadRevision,
			}
		}
		if err := loadConfigurationDocument(&work.Prompt, promptDocument, "prompt"); err != nil {
			return err
		}
		if err := loadConfigurationDocument(&work.Model, modelDocument, "model_connection"); err != nil {
			return err
		}
		if work.Prompt.CredentialRef != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate prompt configuration", errors.New("prompt configuration unexpectedly references a credential"))
		}
		if work.Model.CredentialRef != nil && !configurationKey.MatchString(*work.Model.CredentialRef) {
			return domainError(ErrorStoredDataInvalid, true, "validate model configuration", errors.New("model configuration credential reference is invalid"))
		}
		messages, err := loadConversation(ctx, tx, claim.DatabaseID, inputSequence, maximumContextItems)
		if err != nil {
			return err
		}
		var inputMessage *Message
		for _, message := range messages {
			if message.ID == work.InputMessageID {
				owned := message
				inputMessage = &owned
			}
		}
		if inputMessage == nil {
			return domainError(ErrorStoredDataInvalid, true, "validate agent conversation", errors.New("run input message is absent from its context"))
		}
		if work.Kind == RunReply && inputMessage.Kind != MessageUser {
			return domainError(ErrorStoredDataInvalid, true, "validate agent conversation", errors.New("reply input message has an invalid kind"))
		}
		if work.Kind == RunReply {
			frontendNotes, found, err := decodeReplyFrontendNotes(inputMessage.Content)
			if err != nil {
				return domainError(ErrorStoredDataInvalid, true, "validate Agent frontend notes context", err)
			}
			if found {
				work.FrontendNotes = frontendNotes
			}
		}
		if work.Kind == RunAutoAnalysis {
			if inputMessage.Kind != MessageAutoAnalysisRequest {
				return domainError(ErrorStoredDataInvalid, true, "validate agent conversation", errors.New("automatic analysis input message has an invalid kind"))
			}
			frontendContext, err := decodeAutoAnalysisInputContent(inputMessage.Content)
			if err != nil {
				return domainError(ErrorStoredDataInvalid, true, "validate automatic analysis frontend context", err)
			}
			work.AutoAnalysisContext = &frontendContext
			work.FrontendNotes = &FrontendNotesState{
				Content: frontendContext.Notes,
				Title:   frontendContext.NotesTitle,
				Locked:  frontendContext.NotesLocked,
			}
		}
		work.Conversation = messages
		toolCalls, err := loadToolCalls(ctx, tx, claim.DatabaseID)
		if err != nil {
			return err
		}
		work.ToolCalls = toolCalls
		return nil
	})
	return work, resultErr
}

func loadConfigurationDocument(snapshot *ConfigurationSnapshot, document, kind string) error {
	canonical, digest, err := canonicaljson.Object(json.RawMessage(document), MaxConfigurationBytes)
	if err != nil {
		return domainError(ErrorStoredDataInvalid, true, "canonicalize stored agent configuration", err)
	}
	if !sha256Pattern.MatchString(snapshot.DocumentSHA256) || digest != snapshot.DocumentSHA256 || !configurationKey.MatchString(snapshot.Key) ||
		!schemaIDPattern.MatchString(snapshot.SchemaID) || !strings.HasPrefix(snapshot.SchemaID, "ascendany."+kind+".") || snapshot.VersionDatabaseID < 1 {
		return domainError(ErrorStoredDataInvalid, true, "validate stored agent configuration", errors.New("immutable configuration metadata or digest is invalid"))
	}
	snapshot.Document = canonical
	return nil
}

func loadConversation(ctx context.Context, tx postgresTx, runDatabaseID, inputSequence int64, maximum int) ([]Message, error) {
	rows, err := tx.Query(ctx, `
SELECT message.public_id::text,
       thread.public_id::text,
       message.message_sequence,
       message.message_kind,
       message.content,
       message.reasoning_content,
       message.context_summary,
       source_run.public_id::text,
       message.created_at
FROM ascendany.agent_runs AS target_run
JOIN ascendany.chat_messages AS message
  ON message.chat_thread_id = target_run.chat_thread_id
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = message.chat_thread_id
LEFT JOIN ascendany.agent_runs AS source_run
  ON source_run.agent_run_id = message.agent_run_id
WHERE target_run.agent_run_id = $1
  AND (
      message.message_sequence <= $2
      OR (
          message.message_kind = 'assistant'
          AND source_run.agent_run_id < target_run.agent_run_id
      )
  )
ORDER BY
    (message.chat_message_id = target_run.input_message_id) DESC,
    message.message_sequence DESC
LIMIT $3`, runDatabaseID, inputSequence, maximum)
	if err != nil {
		return nil, databaseFailure("query agent conversation", err)
	}
	defer rows.Close()
	messages := make([]Message, 0, maximum)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, databaseFailure("scan agent conversation", err)
		}
		if err := validateMessage(message); err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "validate stored agent conversation", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseFailure("iterate agent conversation", err)
	}
	sort.Slice(messages, func(left, right int) bool {
		return messages[left].Sequence < messages[right].Sequence
	})
	return messages, nil
}

func loadToolCalls(ctx context.Context, tx postgresTx, runDatabaseID int64) ([]ToolCallRecord, error) {
	rows, err := tx.Query(ctx, `
SELECT tool_sequence,
       tool_call_key,
       tool_name,
       arguments_schema,
       arguments::text,
       arguments_sha256,
       result_schema,
       result::text,
       result_sha256,
       outcome,
       error_code,
       started_at,
       finished_at
FROM ascendany.agent_tool_calls
WHERE agent_run_id = $1
ORDER BY tool_sequence ASC`, runDatabaseID)
	if err != nil {
		return nil, databaseFailure("query agent tool calls", err)
	}
	defer rows.Close()
	result := make([]ToolCallRecord, 0)
	for rows.Next() {
		var record ToolCallRecord
		var arguments string
		var resultJSON *string
		if err := rows.Scan(
			&record.Sequence,
			&record.Key,
			&record.Name,
			&record.ArgumentsSchema,
			&arguments,
			&record.ArgumentsSHA256,
			&record.ResultSchema,
			&resultJSON,
			&record.ResultSHA256,
			&record.Outcome,
			&record.ErrorCode,
			&record.StartedAt,
			&record.FinishedAt,
		); err != nil {
			return nil, databaseFailure("scan agent tool call", err)
		}
		record.Arguments = json.RawMessage(arguments)
		if resultJSON != nil {
			record.Result = json.RawMessage(*resultJSON)
		}
		record.StartedAt = record.StartedAt.UTC()
		record.FinishedAt = record.FinishedAt.UTC()
		if err := validateStoredToolRecord(&record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseFailure("iterate agent tool calls", err)
	}
	return result, nil
}

func validateStoredToolRecord(record *ToolCallRecord) error {
	arguments, digest, err := canonicaljson.Object(record.Arguments, MaxToolDocumentBytes)
	if err != nil || digest != record.ArgumentsSHA256 || !sha256Pattern.MatchString(record.ArgumentsSHA256) || record.Sequence < 1 ||
		!toolCallKeyPattern.MatchString(record.Key) || !identifierPattern.MatchString(record.Name) || !schemaIDPattern.MatchString(record.ArgumentsSchema) {
		return domainError(ErrorStoredDataInvalid, true, "validate stored agent tool call", errors.New("tool arguments or immutable metadata are invalid"))
	}
	record.Arguments = arguments
	switch record.Outcome {
	case ToolSucceeded:
		if record.ResultSchema == nil || !schemaIDPattern.MatchString(*record.ResultSchema) || record.ResultSHA256 == nil || record.ErrorCode != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate stored agent tool call", errors.New("successful tool metadata is invalid"))
		}
		result, resultDigest, err := canonicaljson.Object(record.Result, MaxToolDocumentBytes)
		if err != nil || resultDigest != *record.ResultSHA256 {
			return domainError(ErrorStoredDataInvalid, true, "validate stored agent tool call", errors.New("tool result digest is invalid"))
		}
		record.Result = result
	case ToolFailed, ToolDenied:
		if record.ResultSchema != nil || len(record.Result) != 0 || record.ResultSHA256 != nil || record.ErrorCode == nil || !identifierPattern.MatchString(*record.ErrorCode) {
			return domainError(ErrorStoredDataInvalid, true, "validate stored agent tool call", errors.New("unsuccessful tool metadata is invalid"))
		}
	default:
		return domainError(ErrorStoredDataInvalid, true, "validate stored agent tool call", errors.New("tool outcome is invalid"))
	}
	return nil
}

func (repository *PostgresRepository) RecordToolCall(ctx context.Context, claim Claim, record ToolCallRecord, notesUpdate *NotesUpdate) (stored ToolCallRecord, resultErr error) {
	if err := validateClaim(claim); err != nil {
		return ToolCallRecord{}, err
	}
	if record.Sequence != 0 {
		return ToolCallRecord{}, domainError(ErrorInvalidInput, true, "record agent tool call", errors.New("repository owns tool sequence allocation"))
	}
	if err := validateNewToolRecord(&record); err != nil {
		return ToolCallRecord{}, err
	}
	if record.Name == ToolUpdateNotes && record.Outcome == ToolSucceeded {
		if notesUpdate == nil || validateNotesUpdateEvent(record, *notesUpdate) != nil {
			return ToolCallRecord{}, domainError(ErrorInvalidInput, true, "validate update_notes event", errors.New("a successful update_notes call requires its exact mutation event"))
		}
	} else if notesUpdate != nil {
		return ToolCallRecord{}, domainError(ErrorInvalidInput, true, "validate update_notes event", errors.New("only a successful update_notes call may publish a mutation event"))
	}
	resultErr = repository.transaction(ctx, "record agent tool call", pgx.TxOptions{}, func(tx postgresTx) error {
		if err := lockActiveRun(ctx, tx, claim); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(tool_sequence), 0) + 1
FROM ascendany.agent_tool_calls
WHERE agent_run_id = $1`, claim.DatabaseID).Scan(&record.Sequence); err != nil {
			return databaseFailure("allocate agent tool sequence", err)
		}
		var resultJSON any
		if len(record.Result) != 0 {
			resultJSON = string(record.Result)
		}
		_, err := tx.Exec(ctx, `
INSERT INTO ascendany.agent_tool_calls (
    agent_run_id,
    tool_sequence,
    tool_call_key,
    tool_name,
    arguments_schema,
    arguments,
    arguments_sha256,
    result_schema,
    result,
    result_sha256,
    outcome,
    error_code,
    started_at,
    finished_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9::jsonb, $10, $11, $12, $13, $14)`,
			claim.DatabaseID, record.Sequence, record.Key, record.Name, record.ArgumentsSchema, string(record.Arguments), record.ArgumentsSHA256,
			record.ResultSchema, resultJSON, record.ResultSHA256, string(record.Outcome), record.ErrorCode, record.StartedAt, record.FinishedAt)
		if err != nil {
			return databaseFailure("insert agent tool call", err)
		}
		if notesUpdate != nil {
			if err := appendRunEvent(ctx, tx, claim.DatabaseID, "notes_update", map[string]any{
				"mode":         notesUpdate.Mode,
				"next":         notesUpdate.Next,
				"patch":        notesUpdate.Patch,
				"previous":     notesUpdate.Previous,
				"toolCallKey":  record.Key,
				"toolName":     record.Name,
				"toolSequence": record.Sequence,
			}); err != nil {
				return err
			}
		}
		if err := appendRunEvent(ctx, tx, claim.DatabaseID, "tool."+string(record.Outcome), map[string]any{
			"toolCallKey":  record.Key,
			"toolName":     record.Name,
			"toolSequence": record.Sequence,
		}); err != nil {
			return err
		}
		if err := ensureActiveRun(ctx, tx, claim, "commit agent tool call"); err != nil {
			return err
		}
		stored = record
		return nil
	})
	return stored, resultErr
}

func validateNewToolRecord(record *ToolCallRecord) error {
	arguments, digest, err := canonicaljson.Object(record.Arguments, MaxToolDocumentBytes)
	if err != nil || digest != record.ArgumentsSHA256 || !toolCallKeyPattern.MatchString(record.Key) || !identifierPattern.MatchString(record.Name) ||
		!schemaIDPattern.MatchString(record.ArgumentsSchema) || record.StartedAt.IsZero() || record.FinishedAt.Before(record.StartedAt) {
		return domainError(ErrorInvalidInput, true, "validate agent tool call", errors.New("tool call violates its immutable contract"))
	}
	record.Arguments = arguments
	switch record.Outcome {
	case ToolSucceeded:
		if record.ResultSchema == nil || !schemaIDPattern.MatchString(*record.ResultSchema) || record.ResultSHA256 == nil || record.ErrorCode != nil {
			return domainError(ErrorInvalidInput, true, "validate agent tool call", errors.New("successful tool metadata is invalid"))
		}
		result, resultDigest, err := canonicaljson.Object(record.Result, MaxToolDocumentBytes)
		if err != nil || resultDigest != *record.ResultSHA256 {
			return domainError(ErrorInvalidInput, true, "validate agent tool call", errors.New("tool result digest is invalid"))
		}
		record.Result = result
	case ToolFailed, ToolDenied:
		if record.ResultSchema != nil || len(record.Result) != 0 || record.ResultSHA256 != nil || record.ErrorCode == nil || !identifierPattern.MatchString(*record.ErrorCode) {
			return domainError(ErrorInvalidInput, true, "validate agent tool call", errors.New("unsuccessful tool metadata is invalid"))
		}
	default:
		return domainError(ErrorInvalidInput, true, "validate agent tool call", errors.New("tool outcome is invalid"))
	}
	return nil
}

func (repository *PostgresRepository) Complete(ctx context.Context, claim Claim, completion Completion) error {
	if err := validateClaim(claim); err != nil {
		return err
	}
	if !canonicalUUIDv4.MatchString(completion.MessageID) {
		return domainError(ErrorInvalidInput, true, "complete agent run", errors.New("canonical assistant message ID is required"))
	}
	if err := validateAssistantOutput(completion.Output); err != nil {
		return domainError(ErrorInvalidInput, true, "complete agent run", err)
	}
	return repository.transaction(ctx, "complete agent run", pgx.TxOptions{}, func(tx postgresTx) error {
		var threadDatabaseID, ownerDatabaseID int64
		if err := lockActiveRunReturning(ctx, tx, claim, &threadDatabaseID, &ownerDatabaseID); err != nil {
			return err
		}
		var headRevision int64
		if err := tx.QueryRow(ctx, `
SELECT head_revision
FROM ascendany.chat_threads
WHERE chat_thread_id = $1
  AND owner_account_id = $2
FOR UPDATE`, threadDatabaseID, ownerDatabaseID).Scan(&headRevision); err != nil {
			return databaseFailure("lock completion chat thread", err)
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read agent completion time", err)
		}
		var messageDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.chat_messages (
    public_id,
    chat_thread_id,
    owner_account_id,
    message_sequence,
    message_kind,
    content,
    reasoning_content,
    context_summary,
    agent_run_id,
    created_at
)
VALUES ($1::uuid, $2, $3, $4, 'assistant', $5, $6, $7, $8, $9)
RETURNING chat_message_id`, completion.MessageID, threadDatabaseID, ownerDatabaseID, headRevision+1,
			completion.Output.Content, completion.Output.ReasoningContent, completion.Output.ContextSummary, claim.DatabaseID, mutationTime).Scan(&messageDatabaseID); err != nil {
			return databaseFailure("insert assistant message", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.chat_threads
SET head_revision = head_revision + 1,
    updated_at = $3
WHERE chat_thread_id = $1
  AND head_revision = $2`, threadDatabaseID, headRevision, mutationTime)
		if err != nil {
			return databaseFailure("advance completed chat thread", err)
		}
		if tag.RowsAffected() != 1 {
			return domainError(ErrorStoredDataInvalid, true, "advance completed chat thread", errors.New("locked thread head did not advance"))
		}
		tag, err = tx.Exec(ctx, `
UPDATE ascendany.agent_runs
SET output_message_id = $6,
    status = 'succeeded',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    finished_at = $7,
    updated_at = $7
WHERE agent_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken,
			claim.LeaseOwner, messageDatabaseID, mutationTime)
		if err != nil {
			return databaseFailure("complete agent run", err)
		}
		if tag.RowsAffected() != 1 {
			return leaseLost("complete agent run")
		}
		return appendRunEvent(ctx, tx, claim.DatabaseID, "completed", map[string]any{
			"messageId":       completion.MessageID,
			"messageSequence": headRevision + 1,
		})
	})
}

func (repository *PostgresRepository) Fail(ctx context.Context, claim Claim, code, detail string) error {
	if err := validateClaim(claim); err != nil {
		return err
	}
	if !identifierPattern.MatchString(code) || strings.TrimSpace(detail) == "" || len(detail) > MaxFailureDetailBytes || !utf8.ValidString(detail) {
		return domainError(ErrorInvalidInput, true, "fail agent run", errors.New("canonical error code and bounded detail are required"))
	}
	return repository.transaction(ctx, "fail agent run", pgx.TxOptions{}, func(tx postgresTx) error {
		var runDatabaseID int64
		err := tx.QueryRow(ctx, `
UPDATE ascendany.agent_runs
SET status = 'failed',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $6,
    error_detail = $7,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE agent_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()
RETURNING agent_run_id`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner, code, detail).Scan(&runDatabaseID)
		if errors.Is(err, pgx.ErrNoRows) {
			return leaseLost("fail agent run")
		}
		if err != nil {
			return databaseFailure("fail agent run", err)
		}
		return appendRunEvent(ctx, tx, runDatabaseID, "failed", map[string]any{"errorCode": code})
	})
}

func lockActiveRun(ctx context.Context, tx postgresTx, claim Claim) error {
	var runDatabaseID int64
	return lockActiveRunReturning(ctx, tx, claim, &runDatabaseID, nil)
}

func lockActiveRunReturning(ctx context.Context, tx postgresTx, claim Claim, threadDatabaseID, ownerDatabaseID *int64) error {
	var runDatabaseID, threadID, ownerID int64
	err := tx.QueryRow(ctx, `
SELECT agent_run_id, chat_thread_id, owner_account_id
FROM ascendany.agent_runs
WHERE agent_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()
FOR UPDATE`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner).Scan(&runDatabaseID, &threadID, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return leaseLost("lock active agent run")
	}
	if err != nil {
		return databaseFailure("lock active agent run", err)
	}
	if threadDatabaseID != nil {
		*threadDatabaseID = threadID
	}
	if ownerDatabaseID != nil {
		*ownerDatabaseID = ownerID
	}
	return nil
}

func ensureActiveRun(ctx context.Context, tx postgresTx, claim Claim, operation string) error {
	var active bool
	err := tx.QueryRow(ctx, `
SELECT true
FROM ascendany.agent_runs
WHERE agent_run_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return leaseLost(operation)
	}
	if err != nil {
		return databaseFailure(operation, err)
	}
	return nil
}

func validateClaim(claim Claim) error {
	if claim.DatabaseID < 1 || !canonicalUUIDv4.MatchString(claim.ID) || claim.AttemptCount < 1 || !canonicalUUIDv4.MatchString(claim.AttemptToken) ||
		strings.TrimSpace(claim.LeaseOwner) != claim.LeaseOwner || claim.LeaseOwner == "" || len(claim.LeaseOwner) > 128 ||
		!utf8.ValidString(claim.LeaseOwner) || claim.LeaseExpiresAt.IsZero() {
		return domainError(ErrorInvalidInput, true, "validate agent run claim", errors.New("claim violates its fence contract"))
	}
	return nil
}

func leaseLost(operation string) error {
	return domainError(ErrorLeaseLost, false, operation, errors.New("agent attempt is no longer active"))
}
