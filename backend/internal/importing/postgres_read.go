package importing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type PgxReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type PostgresReader struct {
	pool PgxReader
}

type rowScanner interface {
	Scan(...any) error
}

func NewPostgresReader(pool PgxReader) (*PostgresReader, error) {
	if pool == nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct import reader", errors.New("database pool is required"))
	}
	return &PostgresReader{pool: pool}, nil
}

func (reader *PostgresReader) GetJob(ctx context.Context, publicID string) (PublicJob, bool, error) {
	if ctx == nil {
		return PublicJob{}, false, importError(ErrorInvalidConfiguration, false, "read import job", errors.New("context is required"))
	}
	if !ValidPublicID(publicID) {
		return PublicJob{}, false, importError(ErrorInvalidPublication, true, "read import job", errors.New("job public ID must be a canonical UUIDv4"))
	}
	job, errorCode, errorPermanent, err := scanPublicJob(reader.pool.QueryRow(ctx, `
SELECT
    job.public_id::text,
    artifact.sha256,
    job.status,
    job.stage,
    job.created_at,
    job.updated_at,
    exam.public_id::text,
    snapshot.public_id::text,
    job.error_code,
    job.error_permanent
FROM ascendany.import_jobs AS job
JOIN ascendany.artifacts AS artifact
    ON artifact.artifact_id = job.artifact_id
LEFT JOIN ascendany.exam_snapshots AS snapshot
    ON snapshot.import_job_id = job.import_job_id
LEFT JOIN ascendany.logical_exams AS exam
    ON exam.exam_id = snapshot.exam_id
WHERE job.public_id = $1::uuid`, publicID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicJob{}, false, nil
	}
	if err != nil {
		return PublicJob{}, false, databaseError("read import job", err)
	}
	job, err = finalizePublicJob(job, errorCode, errorPermanent, "read import job")
	if err != nil {
		return PublicJob{}, false, err
	}
	return job, true, nil
}

func (reader *PostgresReader) ListJobs(ctx context.Context, cursor *string, limit int) (JobPage, error) {
	if ctx == nil {
		return JobPage{}, importError(ErrorInvalidConfiguration, false, "list import jobs", errors.New("context is required"))
	}
	if limit < 1 || limit > MaxJobPageSize {
		return JobPage{}, importError(ErrorInvalidConfiguration, false, "list import jobs", fmt.Errorf("job page limit must be between 1 and %d", MaxJobPageSize))
	}
	var cursorID any
	if cursor != nil {
		if !ValidPublicID(*cursor) {
			return JobPage{}, importError(ErrorJobCursorInvalid, true, "list import jobs", errors.New("job cursor must be a canonical UUIDv4"))
		}
		var value int64
		err := reader.pool.QueryRow(ctx, `
SELECT import_job_id
FROM ascendany.import_jobs
WHERE public_id = $1::uuid`, *cursor).Scan(&value)
		if errors.Is(err, pgx.ErrNoRows) {
			return JobPage{}, importError(ErrorJobCursorInvalid, true, "list import jobs", errors.New("job cursor does not exist"))
		}
		if err != nil {
			return JobPage{}, databaseError("resolve import job cursor", err)
		}
		cursorID = value
	}

	rows, err := reader.pool.Query(ctx, `
SELECT
    job.public_id::text,
    artifact.sha256,
    job.status,
    job.stage,
    job.created_at,
    job.updated_at,
    exam.public_id::text,
    snapshot.public_id::text,
    job.error_code,
    job.error_permanent
FROM ascendany.import_jobs AS job
JOIN ascendany.artifacts AS artifact
    ON artifact.artifact_id = job.artifact_id
LEFT JOIN ascendany.exam_snapshots AS snapshot
    ON snapshot.import_job_id = job.import_job_id
LEFT JOIN ascendany.logical_exams AS exam
    ON exam.exam_id = snapshot.exam_id
WHERE $1::bigint IS NULL OR job.import_job_id < $1
ORDER BY job.import_job_id DESC
LIMIT $2`, cursorID, limit+1)
	if err != nil {
		return JobPage{}, databaseError("query import job page", err)
	}
	defer rows.Close()

	items := make([]PublicJob, 0, limit+1)
	for rows.Next() {
		job, errorCode, errorPermanent, err := scanPublicJob(rows)
		if err != nil {
			return JobPage{}, databaseError("scan import job page", err)
		}
		job, err = finalizePublicJob(job, errorCode, errorPermanent, "list import jobs")
		if err != nil {
			return JobPage{}, err
		}
		items = append(items, job)
	}
	if err := rows.Err(); err != nil {
		return JobPage{}, databaseError("iterate import job page", err)
	}

	var nextCursor *string
	if len(items) > limit {
		items = items[:limit]
		value := items[len(items)-1].ID
		nextCursor = &value
	}
	return JobPage{Items: items, NextCursor: nextCursor}, nil
}

func scanPublicJob(scanner rowScanner) (PublicJob, *string, *bool, error) {
	var job PublicJob
	var errorCode *string
	var errorPermanent *bool
	err := scanner.Scan(
		&job.ID,
		&job.ArtifactSHA256,
		&job.Status,
		&job.Stage,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.ExamID,
		&job.SnapshotID,
		&errorCode,
		&errorPermanent,
	)
	return job, errorCode, errorPermanent, err
}

func finalizePublicJob(job PublicJob, errorCode *string, errorPermanent *bool, operation string) (PublicJob, error) {
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()
	if job.Status == JobFailed {
		if errorCode == nil || errorPermanent == nil {
			return PublicJob{}, importError(ErrorStateConflict, false, operation, errors.New("failed job is missing its error contract"))
		}
		job.Error = &PublicJobError{
			Code:      *errorCode,
			Message:   publicFailureMessage(*errorCode),
			Permanent: *errorPermanent,
		}
	} else if errorCode != nil || errorPermanent != nil {
		return PublicJob{}, importError(ErrorStateConflict, false, operation, errors.New("non-failed job contains error state"))
	}
	return job, nil
}

func (reader *PostgresReader) ReadEvents(ctx context.Context, publicID string, after int64, limit int) (EventBatch, bool, error) {
	if ctx == nil {
		return EventBatch{}, false, importError(ErrorInvalidConfiguration, false, "read import events", errors.New("context is required"))
	}
	if !ValidPublicID(publicID) {
		return EventBatch{}, false, importError(ErrorInvalidPublication, true, "read import events", errors.New("job public ID must be a canonical UUIDv4"))
	}
	if after < 0 {
		return EventBatch{}, false, importError(ErrorInvalidPublication, true, "read import events", errors.New("event sequence must be non-negative"))
	}
	if limit < 1 || limit > MaxEventBatchSize {
		return EventBatch{}, false, importError(ErrorInvalidConfiguration, false, "read import events", fmt.Errorf("event batch limit must be between 1 and %d", MaxEventBatchSize))
	}

	// The job status and bounded event lookahead share one PostgreSQL statement
	// snapshot. The extra row fences Terminal from a still-pending backlog.
	rows, err := reader.pool.Query(ctx, `
SELECT
    job.status,
    COALESCE((
        SELECT MAX(head_event.event_sequence)
        FROM ascendany.import_job_events AS head_event
        WHERE head_event.import_job_id = job.import_job_id
    ), 0) AS event_head,
    event.event_sequence,
    event.event_type,
    event.created_at,
    event.payload::text
FROM ascendany.import_jobs AS job
LEFT JOIN LATERAL (
	SELECT event_sequence, event_type, created_at, payload
	FROM ascendany.import_job_events
	WHERE import_job_id = job.import_job_id
	  AND event_sequence > $2
	ORDER BY event_sequence
	LIMIT $3
) AS event ON true
WHERE job.public_id = $1::uuid
ORDER BY event.event_sequence NULLS LAST`, publicID, after, limit+1)
	if err != nil {
		return EventBatch{}, false, databaseError("query import events", err)
	}
	defer rows.Close()

	events := make([]PublicEvent, 0, limit+1)
	previousSequence := after
	var batchStatus JobStatus
	var batchHead int64
	found := false
	for rows.Next() {
		var status JobStatus
		var eventHead int64
		var sequence *int64
		var eventType *string
		var occurredAt *time.Time
		var payloadText *string
		if err := rows.Scan(&status, &eventHead, &sequence, &eventType, &occurredAt, &payloadText); err != nil {
			return EventBatch{}, false, databaseError("scan import event", err)
		}
		if !found {
			batchStatus = status
			batchHead = eventHead
			found = true
		} else if status != batchStatus || eventHead != batchHead {
			return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("job state changed inside a consistent event read"))
		}
		if eventHead < 0 {
			return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event head is negative"))
		}
		if sequence == nil {
			if eventType != nil || occurredAt != nil || payloadText != nil {
				return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event row is partially null"))
			}
			continue
		}
		if eventType == nil || occurredAt == nil || payloadText == nil {
			return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event row is partially null"))
		}
		event := PublicEvent{
			Sequence:   *sequence,
			Type:       *eventType,
			OccurredAt: occurredAt.UTC(),
			Payload:    json.RawMessage(*payloadText),
		}
		if event.Sequence != previousSequence+1 || !publicEventTypePattern.MatchString(event.Type) {
			return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event sequence or type violates the public contract"))
		}
		var payload map[string]json.RawMessage
		if !json.Valid(event.Payload) || json.Unmarshal(event.Payload, &payload) != nil || payload == nil {
			return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event payload violates the public contract"))
		}
		events = append(events, event)
		previousSequence = event.Sequence
	}
	if err := rows.Err(); err != nil {
		return EventBatch{}, false, databaseError("iterate import events", err)
	}
	if !found {
		return EventBatch{}, false, nil
	}
	if after > batchHead {
		return EventBatch{}, false, importError(ErrorEventCursorAhead, true, "read import events", fmt.Errorf("event cursor %d exceeds durable head %d", after, batchHead))
	}
	if len(events) == 0 && after < batchHead {
		return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event history contains a gap"))
	}
	if len(events) > 0 && events[len(events)-1].Sequence > batchHead {
		return EventBatch{}, false, importError(ErrorStateConflict, false, "read import events", errors.New("durable event exceeds the recorded head"))
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	return EventBatch{Events: events, Terminal: terminalStatus(batchStatus) && !hasMore}, true, nil
}
