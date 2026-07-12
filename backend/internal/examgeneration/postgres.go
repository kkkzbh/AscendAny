package examgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type readTx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginReadTransaction func(context.Context, pgx.TxOptions) (readTx, error)

type PostgresRepository struct {
	begin beginReadTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, domainError(
			ErrorInvalidConfiguration,
			true,
			"construct exam generation PostgreSQL repository",
			errors.New("database pool is required"),
		)
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (readTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginReadTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, domainError(
			ErrorInvalidConfiguration,
			true,
			"construct exam generation PostgreSQL repository",
			errors.New("transaction beginner is required"),
		)
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) LoadCurrent(
	ctx context.Context,
	query CurrentQuery,
) (generation Generation, found bool, resultErr error) {
	resultErr = repository.readTransaction(ctx, "load current exam generation", func(tx readTx) error {
		if err := revalidatePrincipal(ctx, tx, query.Principal); err != nil {
			return err
		}
		stored, generationFound, err := loadActiveGeneration(ctx, tx, query.ExamID)
		if err != nil || !generationFound {
			found = generationFound
			return err
		}
		if err := validateStoredGeneration(stored); err != nil {
			return domainError(ErrorStoredDataInvalid, true, "load current exam generation", err)
		}
		generation = stored.Generation
		found = true
		return nil
	})
	return generation, found, resultErr
}

func (repository *PostgresRepository) LoadEvents(
	ctx context.Context,
	query EventQuery,
) (batch EventBatch, found bool, resultErr error) {
	resultErr = repository.readTransaction(ctx, "load pinned exam generation events", func(tx readTx) error {
		if err := revalidatePrincipal(ctx, tx, query.Principal); err != nil {
			return err
		}
		stored, generationFound, err := loadPinnedGeneration(ctx, tx, query.ExamID, query.GenerationID)
		if err != nil || !generationFound {
			found = generationFound
			return err
		}
		if err := validateStoredGeneration(stored); err != nil {
			return domainError(ErrorStoredDataInvalid, true, "load pinned exam generation events", err)
		}
		generation := stored.Generation
		if query.AfterSequence > generation.EventHead {
			return domainError(
				ErrorEventCursorInvalid,
				true,
				"load pinned exam generation events",
				fmt.Errorf("event cursor %d exceeds durable head %d", query.AfterSequence, generation.EventHead),
			)
		}

		generationDatabaseID, err := strconv.ParseInt(generation.GenerationID, 10, 64)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "load pinned exam generation events", err)
		}
		rows, err := tx.Query(ctx, `
SELECT event_sequence,
       event_type,
       payload::text,
       created_at
FROM ascendany.analytics_generation_events
WHERE analytics_generation_id = $1
  AND event_sequence > $2
ORDER BY event_sequence
LIMIT $3`, generationDatabaseID, query.AfterSequence, query.Limit)
		if err != nil {
			return databaseError("query pinned exam generation events", err)
		}
		defer rows.Close()

		batch = EventBatch{
			GenerationID: generation.GenerationID,
			EventHead:    generation.EventHead,
			Events:       make([]Event, 0, query.Limit),
		}
		previous := query.AfterSequence
		for rows.Next() {
			var event Event
			var eventType string
			var payload string
			if err := rows.Scan(&event.Sequence, &eventType, &payload, &event.CreatedAt); err != nil {
				return databaseError("scan pinned exam generation event", err)
			}
			event.Type = EventType(eventType)
			event.Payload = json.RawMessage(payload)
			event.CreatedAt = event.CreatedAt.UTC()
			if event.Sequence != previous+1 {
				return domainError(
					ErrorStoredDataInvalid,
					true,
					"load pinned exam generation events",
					errors.New("stored generation event sequence contains a gap"),
				)
			}
			batch.Events = append(batch.Events, event)
			previous = event.Sequence
		}
		if err := rows.Err(); err != nil {
			return databaseError("iterate pinned exam generation events", err)
		}
		if previous < generation.EventHead && len(batch.Events) < query.Limit {
			return domainError(
				ErrorStoredDataInvalid,
				true,
				"load pinned exam generation events",
				errors.New("stored generation event history ends before its durable head"),
			)
		}
		batch.Terminal = terminalStatus(generation.Status) && previous == generation.EventHead
		found = true
		return nil
	})
	return batch, found, resultErr
}

const activeGenerationSelect = `
SELECT generation.analytics_generation_id,
       generation.status,
       generation.attempt_count,
       generation.created_at,
	       generation.started_at,
	       generation.finished_at,
	       generation.error_code,
	       COALESCE(head.event_sequence, 0),
	       head.event_type
FROM ascendany.logical_exams AS exam
JOIN LATERAL (
    SELECT candidate.analytics_generation_id,
           candidate.status,
           candidate.attempt_count,
           candidate.created_at,
           candidate.started_at,
           candidate.finished_at,
           candidate.error_code
    FROM ascendany.analytics_generations AS candidate
    WHERE candidate.target_exam_id = exam.exam_id
      AND candidate.target_snapshot_id = exam.active_snapshot_id
      AND candidate.target_exam_head_revision = exam.head_revision
    ORDER BY candidate.analytics_generation_id DESC
    LIMIT 1
) AS generation ON true
LEFT JOIN LATERAL (
    SELECT event.event_sequence,
           event.event_type
    FROM ascendany.analytics_generation_events AS event
    WHERE event.analytics_generation_id = generation.analytics_generation_id
    ORDER BY event.event_sequence DESC
    LIMIT 1
) AS head ON true
WHERE exam.public_id = $1::uuid
  AND exam.active_snapshot_id IS NOT NULL`

const pinnedGenerationSelect = `
SELECT generation.analytics_generation_id,
       generation.status,
       generation.attempt_count,
       generation.created_at,
	       generation.started_at,
	       generation.finished_at,
	       generation.error_code,
	       COALESCE(head.event_sequence, 0),
	       head.event_type
FROM ascendany.logical_exams AS exam
JOIN ascendany.analytics_generations AS generation
  ON generation.target_exam_id = exam.exam_id
 AND generation.analytics_generation_id = $2::bigint
LEFT JOIN LATERAL (
    SELECT event.event_sequence,
           event.event_type
    FROM ascendany.analytics_generation_events AS event
    WHERE event.analytics_generation_id = generation.analytics_generation_id
    ORDER BY event.event_sequence DESC
    LIMIT 1
) AS head ON true
WHERE exam.public_id = $1::uuid`

type storedGeneration struct {
	Generation
	HeadEventType *EventType
}

func loadActiveGeneration(ctx context.Context, tx readTx, examID string) (storedGeneration, bool, error) {
	return scanStoredGeneration(tx.QueryRow(ctx, activeGenerationSelect, examID), "query current exam generation")
}

func loadPinnedGeneration(
	ctx context.Context,
	tx readTx,
	examID string,
	generationID string,
) (storedGeneration, bool, error) {
	return scanStoredGeneration(
		tx.QueryRow(ctx, pinnedGenerationSelect, examID, generationID),
		"query pinned exam generation",
	)
}

func scanStoredGeneration(row pgx.Row, operation string) (storedGeneration, bool, error) {
	var databaseID int64
	var status string
	var headEventType *string
	var stored storedGeneration
	generation := &stored.Generation
	err := row.Scan(
		&databaseID,
		&status,
		&generation.AttemptCount,
		&generation.CreatedAt,
		&generation.StartedAt,
		&generation.FinishedAt,
		&generation.ErrorCode,
		&generation.EventHead,
		&headEventType,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedGeneration{}, false, nil
	}
	if err != nil {
		return storedGeneration{}, false, databaseError(operation, err)
	}
	if databaseID <= 0 {
		return storedGeneration{}, false, domainError(
			ErrorStoredDataInvalid,
			true,
			operation,
			errors.New("generation database ID is not positive"),
		)
	}
	generation.GenerationID = strconv.FormatInt(databaseID, 10)
	generation.Status = Status(status)
	if headEventType != nil {
		value := EventType(*headEventType)
		stored.HeadEventType = &value
	}
	normalizeGenerationTimes(generation)
	return stored, true, nil
}

func validateStoredGeneration(stored storedGeneration) error {
	if err := validateGeneration(stored.Generation); err != nil {
		return err
	}
	if stored.HeadEventType == nil || !validEventType(*stored.HeadEventType) ||
		*stored.HeadEventType != EventType(stored.Status) {
		return errors.New("generation status and durable head event disagree")
	}
	return nil
}

func normalizeGenerationTimes(generation *Generation) {
	generation.CreatedAt = generation.CreatedAt.UTC()
	if generation.StartedAt != nil {
		value := generation.StartedAt.UTC()
		generation.StartedAt = &value
	}
	if generation.FinishedAt != nil {
		value := generation.FinishedAt.UTC()
		generation.FinishedAt = &value
	}
}

func revalidatePrincipal(ctx context.Context, tx readTx, principal auth.AccessPrincipal) error {
	_, err := principalguard.Resolve(
		ctx,
		tx,
		principal,
		principalguard.Roles(auth.RoleStudent, auth.RoleAdmin),
	)
	if err != nil {
		return mapPrincipalError("revalidate exam generation principal", err)
	}
	return nil
}

func (repository *PostgresRepository) readTransaction(
	ctx context.Context,
	operation string,
	run func(readTx) error,
) (resultErr error) {
	if ctx == nil {
		return domainError(ErrorInvalidInput, true, operation, errors.New("context is required"))
	}
	tx, err := repository.begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return databaseError("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseError("rollback "+operation, rollbackErr)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseError("commit "+operation, err)
	}
	finished = true
	return nil
}
