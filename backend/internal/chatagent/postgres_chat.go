package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func (repository *PostgresRepository) CreateThread(ctx context.Context, command CreateThreadCommand) (result Thread, resultErr error) {
	if err := validateStudentPrincipal(ctx, command.Principal); err != nil {
		return Thread{}, err
	}
	if !canonicalUUIDv4.MatchString(command.ThreadID) || command.Kind != ThreadConversation {
		return Thread{}, domainError(ErrorInvalidInput, true, "create chat thread", errors.New("canonical thread ID and conversation kind are required"))
	}
	resultErr = repository.transaction(ctx, "create chat thread", pgx.TxOptions{}, func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, command.Principal, true)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
VALUES ($1::uuid, $2, $3)
RETURNING public_id::text, thread_kind, head_revision, created_at, updated_at`, command.ThreadID, resolved.AccountDatabaseID, string(command.Kind)).Scan(
			&result.ID,
			&result.Kind,
			&result.HeadRevision,
			&result.CreatedAt,
			&result.UpdatedAt,
		)
		if err != nil {
			return databaseFailure("insert chat thread", err)
		}
		result.CreatedAt = result.CreatedAt.UTC()
		result.UpdatedAt = result.UpdatedAt.UTC()
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) ListThreads(ctx context.Context, query ThreadQuery) (result []Thread, resultErr error) {
	resultErr = repository.transaction(ctx, "list chat threads", readOnlyOptions(), func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, query.Principal, false)
		if err != nil {
			return err
		}
		var rows pgx.Rows
		if query.Cursor == nil {
			rows, err = tx.Query(ctx, `
SELECT public_id::text, thread_kind, head_revision, created_at, updated_at
FROM ascendany.chat_threads
WHERE owner_account_id = $1
ORDER BY updated_at DESC, chat_thread_id DESC
				LIMIT $2`, resolved.AccountDatabaseID, query.Limit+1)
		} else {
			var cursorUpdatedAt time.Time
			var cursorDatabaseID int64
			if err := tx.QueryRow(ctx, `
SELECT updated_at, chat_thread_id
FROM ascendany.chat_threads
WHERE owner_account_id = $1
  AND public_id = $2::uuid`, resolved.AccountDatabaseID, *query.Cursor).Scan(&cursorUpdatedAt, &cursorDatabaseID); errors.Is(err, pgx.ErrNoRows) {
				return domainError(ErrorThreadCursorInvalid, true, "resolve chat thread cursor", errors.New("chat thread cursor was not found"))
			} else if err != nil {
				return databaseFailure("resolve chat thread cursor", err)
			}
			rows, err = tx.Query(ctx, `
SELECT public_id::text, thread_kind, head_revision, created_at, updated_at
FROM ascendany.chat_threads
WHERE owner_account_id = $1
  AND (updated_at, chat_thread_id) < ($2, $3)
ORDER BY updated_at DESC, chat_thread_id DESC
LIMIT $4`, resolved.AccountDatabaseID, cursorUpdatedAt, cursorDatabaseID, query.Limit+1)
		}
		if err != nil {
			return databaseFailure("query chat threads", err)
		}
		defer rows.Close()
		result = make([]Thread, 0, query.Limit+1)
		for rows.Next() {
			var thread Thread
			if err := rows.Scan(&thread.ID, &thread.Kind, &thread.HeadRevision, &thread.CreatedAt, &thread.UpdatedAt); err != nil {
				return databaseFailure("scan chat thread", err)
			}
			thread.CreatedAt = thread.CreatedAt.UTC()
			thread.UpdatedAt = thread.UpdatedAt.UTC()
			result = append(result, thread)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate chat threads", err)
		}
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) ListMessages(ctx context.Context, query MessageQuery) (result []Message, resultErr error) {
	resultErr = repository.transaction(ctx, "list chat messages", readOnlyOptions(), func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, query.Principal, false)
		if err != nil {
			return err
		}
		var threadDatabaseID int64
		if err := tx.QueryRow(ctx, `
SELECT chat_thread_id
FROM ascendany.chat_threads
WHERE public_id = $1::uuid
  AND owner_account_id = $2`, query.ThreadID, resolved.AccountDatabaseID).Scan(&threadDatabaseID); errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorNotFound, true, "resolve chat thread", errors.New("chat thread was not found"))
		} else if err != nil {
			return databaseFailure("resolve chat thread", err)
		}
		rows, err := tx.Query(ctx, `
SELECT message.public_id::text,
       thread.public_id::text,
       message.message_sequence,
       message.message_kind,
       message.content,
       message.reasoning_content,
       message.context_summary,
       run.public_id::text,
       message.created_at
FROM ascendany.chat_messages AS message
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = message.chat_thread_id
LEFT JOIN ascendany.agent_runs AS run
  ON run.agent_run_id = message.agent_run_id
WHERE message.chat_thread_id = $1
  AND message.owner_account_id = $2
  AND message.message_sequence > $3
ORDER BY message.message_sequence ASC
LIMIT $4`, threadDatabaseID, resolved.AccountDatabaseID, query.AfterSequence, query.Limit)
		if err != nil {
			return databaseFailure("query chat messages", err)
		}
		defer rows.Close()
		result = make([]Message, 0, query.Limit)
		for rows.Next() {
			message, err := scanMessage(rows)
			if err != nil {
				return databaseFailure("scan chat message", err)
			}
			result = append(result, message)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate chat messages", err)
		}
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) GetRun(ctx context.Context, query RunQuery) (result Run, found bool, resultErr error) {
	resultErr = repository.transaction(ctx, "get agent run", readOnlyOptions(), func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, query.Principal, false)
		if err != nil {
			return err
		}
		result, err = scanRun(tx.QueryRow(ctx, runSelect+`
WHERE run.public_id = $1::uuid
  AND run.owner_account_id = $2`, query.RunID, resolved.AccountDatabaseID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("query agent run", err)
		}
		found = true
		return nil
	})
	return result, found, resultErr
}

func (repository *PostgresRepository) ReadRunEvents(ctx context.Context, query EventQuery) (result RunEventBatch, resultErr error) {
	resultErr = repository.transaction(ctx, "list agent run events", readOnlyOptions(), func(tx postgresTx) error {
		resolved, err := resolveStudent(ctx, tx, query.Principal, false)
		if err != nil {
			return err
		}
		var runDatabaseID int64
		var status string
		if err := tx.QueryRow(ctx, `
SELECT agent_run_id, status
FROM ascendany.agent_runs
WHERE public_id = $1::uuid
  AND owner_account_id = $2`, query.RunID, resolved.AccountDatabaseID).Scan(&runDatabaseID, &status); errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorNotFound, true, "resolve agent run events", errors.New("agent run was not found"))
		} else if err != nil {
			return databaseFailure("resolve agent run events", err)
		}
		switch RunStatus(status) {
		case RunQueued, RunRunning:
			result.Terminal = false
		case RunSucceeded, RunFailed, RunSuperseded:
			result.Terminal = true
		default:
			return domainError(ErrorStoredDataInvalid, true, "validate agent run event state", errors.New("stored agent run status is invalid"))
		}
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(event_sequence), 0)
FROM ascendany.agent_run_events
WHERE agent_run_id = $1`, runDatabaseID).Scan(&result.LastSequence); err != nil {
			return databaseFailure("read agent run event head", err)
		}
		if query.AfterSequence > result.LastSequence {
			return domainError(ErrorEventCursorInvalid, true, "validate agent run event cursor", errors.New("event cursor exceeds the durable head"))
		}
		rows, err := tx.Query(ctx, `
SELECT event_sequence, event_type, payload::text, created_at
FROM ascendany.agent_run_events
WHERE agent_run_id = $1
  AND event_sequence > $2
ORDER BY event_sequence ASC
LIMIT $3`, runDatabaseID, query.AfterSequence, query.Limit)
		if err != nil {
			return databaseFailure("query agent run events", err)
		}
		defer rows.Close()
		result.Events = make([]RunEvent, 0, query.Limit)
		for rows.Next() {
			var event RunEvent
			var payload string
			if err := rows.Scan(&event.Sequence, &event.Type, &payload, &event.CreatedAt); err != nil {
				return databaseFailure("scan agent run event", err)
			}
			canonical, _, err := canonicaljson.Object(json.RawMessage(payload), 32<<10)
			if err != nil {
				return domainError(ErrorStoredDataInvalid, true, "validate stored agent run event", err)
			}
			event.Payload = canonical
			event.CreatedAt = event.CreatedAt.UTC()
			result.Events = append(result.Events, event)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate agent run events", err)
		}
		return nil
	})
	return result, resultErr
}

const runSelect = `
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
       run.updated_at
FROM ascendany.agent_runs AS run
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = run.chat_thread_id
JOIN ascendany.chat_messages AS input
  ON input.chat_message_id = run.input_message_id
LEFT JOIN ascendany.chat_messages AS output
  ON output.chat_message_id = run.output_message_id
`

func scanRun(row pgx.Row) (Run, error) {
	var run Run
	err := row.Scan(
		&run.ID,
		&run.ThreadID,
		&run.ClientRequestID,
		&run.Kind,
		&run.InputMessageID,
		&run.OutputMessageID,
		&run.Status,
		&run.AttemptCount,
		&run.ErrorCode,
		&run.ErrorDetail,
		&run.CreatedAt,
		&run.StartedAt,
		&run.FinishedAt,
		&run.UpdatedAt,
	)
	if err == nil {
		normalizeRunTimes(&run)
	}
	return run, err
}

type rowScanner interface {
	Scan(...any) error
}

func scanMessage(row rowScanner) (Message, error) {
	var message Message
	err := row.Scan(
		&message.ID,
		&message.ThreadID,
		&message.Sequence,
		&message.Kind,
		&message.Content,
		&message.ReasoningContent,
		&message.ContextSummary,
		&message.RunID,
		&message.CreatedAt,
	)
	if err == nil {
		message.CreatedAt = message.CreatedAt.UTC()
	}
	return message, err
}
