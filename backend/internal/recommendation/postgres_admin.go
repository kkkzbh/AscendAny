package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

const maximumTrainingEventPayloadBytes = 64 << 10

func (repository *PostgresRepository) ReadReviewContext(
	ctx context.Context,
	principal auth.AccessPrincipal,
) (result ReviewContext, resultErr error) {
	resultErr = repository.transaction(ctx, "read recommendation review context", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		if _, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin)); err != nil {
			return mapPrincipalError("authorize recommendation review context", err)
		}
		if err := tx.QueryRow(ctx, `
SELECT generation.analytics_generation_id,
       head.head_revision,
       generation.input_manifest_sha256
FROM ascendany.analytics_head AS head
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
 AND generation.status = 'succeeded'
WHERE head.singleton`).Scan(
			&result.AnalyticsGenerationID, &result.AnalyticsHeadRevision, &result.InputManifestSHA256,
		); errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorAnalyticsUnavailable, true, "read recommendation review context", errors.New("current succeeded analytics head is unavailable"))
		} else if err != nil {
			return databaseError("read recommendation review provenance", err)
		}
		rows, err := queryTrainingProblems(ctx, tx, result.AnalyticsGenerationID)
		if err != nil {
			return err
		}
		candidates, err := buildReviewProblemCandidates(rows)
		if err != nil {
			return err
		}
		result.Problems = candidates
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) ReadTrainingRun(
	ctx context.Context,
	principal auth.AccessPrincipal,
	runID string,
) (result TrainingRunDetail, found bool, resultErr error) {
	if !canonicalUUIDv4Pattern.MatchString(runID) {
		return TrainingRunDetail{}, false, domainError(ErrorInvalidInput, true, "read recommendation training run", errors.New("canonical run ID is required"))
	}
	resultErr = repository.transaction(ctx, "read recommendation training run", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		if _, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin)); err != nil {
			return mapPrincipalError("authorize recommendation training run", err)
		}
		err := scanTrainingRun(tx.QueryRow(ctx, `
SELECT `+trainingRunReturningColumns+`
FROM ascendany.recommendation_training_runs
WHERE public_id = $1::uuid`, runID), &result.Run)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseError("read recommendation training run", err)
		}
		var errorCode, errorDetail *string
		if err := tx.QueryRow(ctx, `
SELECT item.configuration_key,
       run.error_code,
       run.error_detail
FROM ascendany.recommendation_training_runs AS run
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = run.training_configuration_version_id
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = version.configuration_item_id
WHERE run.training_run_id = $1`, result.Run.DatabaseID).Scan(
			&result.TrainingConfigurationKey, &errorCode, &errorDetail,
		); err != nil {
			return databaseError("read recommendation training run detail", err)
		}
		if result.Run.Status == RunFailed {
			if errorCode == nil || errorDetail == nil || !failureCodePattern.MatchString(*errorCode) || *errorDetail == "" {
				return domainError(ErrorStoredDataInvalid, true, "read recommendation training run", errors.New("failed run lacks canonical failure provenance"))
			}
			result.Failure = &TrainingRunFailure{Code: *errorCode, Message: "Training ended with a recorded failure. See ordered events for its safe operational context."}
		} else if errorCode != nil || errorDetail != nil {
			return domainError(ErrorStoredDataInvalid, true, "read recommendation training run", errors.New("nonfailed run contains failure provenance"))
		}
		found = true
		return nil
	})
	return result, found, resultErr
}

func (repository *PostgresRepository) ReadTrainingEvents(
	ctx context.Context,
	principal auth.AccessPrincipal,
	runID string,
	afterSequence int64,
	limit int,
) (result TrainingEventPage, found bool, resultErr error) {
	if !canonicalUUIDv4Pattern.MatchString(runID) || afterSequence < 0 || limit < 1 || limit > 100 {
		return TrainingEventPage{}, false, domainError(ErrorInvalidInput, true, "read recommendation training events", errors.New("canonical run ID, cursor, and bounded limit are required"))
	}
	resultErr = repository.transaction(ctx, "read recommendation training events", pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx recommendationTx) error {
		if _, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin)); err != nil {
			return mapPrincipalError("authorize recommendation training events", err)
		}
		var databaseID int64
		if err := tx.QueryRow(ctx, `
SELECT training_run_id
FROM ascendany.recommendation_training_runs
WHERE public_id = $1::uuid`, runID).Scan(&databaseID); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return databaseError("resolve recommendation training events", err)
		}
		rows, err := tx.Query(ctx, `
SELECT event_sequence,
       event_type,
       payload::text,
       created_at
FROM ascendany.recommendation_training_events
WHERE training_run_id = $1
  AND event_sequence > $2
ORDER BY event_sequence
LIMIT $3`, databaseID, afterSequence, limit+1)
		if err != nil {
			return databaseError("query recommendation training events", err)
		}
		defer rows.Close()
		items := make([]TrainingEvent, 0, limit+1)
		for rows.Next() {
			var event TrainingEvent
			var payload string
			if err := rows.Scan(&event.Sequence, &event.Type, &payload, &event.CreatedAt); err != nil {
				return databaseError("scan recommendation training event", err)
			}
			if event.Sequence <= afterSequence || !failureCodePattern.MatchString(event.Type) || strings.TrimSpace(event.Type) != event.Type {
				return domainError(ErrorStoredDataInvalid, true, "read recommendation training events", errors.New("stored training event identity is invalid"))
			}
			canonical, _, err := canonicaljson.Object(json.RawMessage(payload), maximumTrainingEventPayloadBytes)
			if err != nil {
				return domainError(ErrorStoredDataInvalid, true, "read recommendation training events", fmt.Errorf("event %d payload: %w", event.Sequence, err))
			}
			event.Payload = canonical
			event.CreatedAt = event.CreatedAt.UTC()
			items = append(items, event)
		}
		if err := rows.Err(); err != nil {
			return databaseError("iterate recommendation training events", err)
		}
		if len(items) > limit {
			items = items[:limit]
			cursor := items[len(items)-1].Sequence
			result.NextAfterSequence = &cursor
		}
		result.RunID = runID
		result.Items = items
		found = true
		return nil
	})
	return result, found, resultErr
}

func queryTrainingProblems(ctx context.Context, tx recommendationTx, generationID int64) ([]TrainingProblem, error) {
	rows, err := tx.Query(ctx, `
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
ORDER BY generation_snapshot.snapshot_id, problem.problem_set_problem_id`, generationID)
	if err != nil {
		return nil, databaseError("query recommendation review problems", err)
	}
	defer rows.Close()
	problems := make([]TrainingProblem, 0)
	for rows.Next() {
		var problem TrainingProblem
		if err := rows.Scan(
			&problem.SnapshotID, &problem.ProblemSetID, &problem.ProblemSetProblemID,
			&problem.SourceURL, &problem.Platform, &problem.ProblemID, &problem.Title,
			&problem.ContentHTML, &problem.MaxScore, &problem.TimeLimitMS, &problem.MemoryLimitBytes,
		); err != nil {
			return nil, databaseError("scan recommendation review problem", err)
		}
		problems = append(problems, problem)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate recommendation review problems", err)
	}
	return problems, nil
}
