package oj

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

func (repository *PostgresRepository) GetSubmission(
	ctx context.Context,
	query SubmissionQuery,
) (detail SubmissionDetail, found bool, resultErr error) {
	resultErr = repository.readTransaction(ctx, "get OJ submission", func(tx postgresTx) error {
		principal, err := principalguard.Resolve(ctx, tx, query.Principal, principalguard.Roles(auth.RoleAdmin, auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize OJ submission read", err)
		}
		var mode, status string
		var failureCode *string
		var resultVerdict, resultSchema, resultManifest, resultHash *string
		var scoreFraction *float64
		var passedCases, totalCases, maxTimeMS, maxMemoryBytes *int64
		var resultCreatedAt *time.Time
		var outputHash, outputMediaType, outputStorageKey *string
		var outputSize *int64
		err = tx.QueryRow(ctx, `
SELECT submission.public_id::text,
       job.public_id::text,
       problem.public_id::text,
       version.version_number,
       submission.submission_mode,
       submission.language_id,
       submission.created_at,
       job.status,
       job.attempt_count,
       job.error_code,
       job.updated_at,
       result.verdict,
       result.score_fraction,
       result.passed_case_count,
       result.total_case_count,
       result.max_time_ms,
       result.max_memory_bytes,
       result.result_schema,
       result.result_manifest::text,
       result.result_sha256,
       result.created_at,
       output.sha256,
       output.size_bytes,
       output.media_type,
       output.storage_key
FROM ascendany.oj_submissions AS submission
JOIN ascendany.oj_judge_jobs AS job ON job.oj_submission_id = submission.oj_submission_id
JOIN ascendany.oj_problems AS problem ON problem.oj_problem_id = submission.oj_problem_id
JOIN ascendany.oj_problem_versions AS version
  ON version.oj_problem_version_id = submission.oj_problem_version_id
 AND version.oj_problem_id = submission.oj_problem_id
LEFT JOIN ascendany.oj_judge_results AS result ON result.judge_result_id = job.judge_result_id
LEFT JOIN ascendany.artifacts AS output ON output.artifact_id = result.output_artifact_id
WHERE submission.public_id = $1::uuid
  AND ($2::boolean OR submission.account_id = $3)`, query.SubmissionID, principal.Role == auth.RoleAdmin,
			principal.AccountDatabaseID).Scan(
			&detail.ID, &detail.JudgeJobID, &detail.ProblemID, &detail.ProblemVersion,
			&mode, &detail.LanguageID, &detail.CreatedAt, &status, &detail.AttemptCount,
			&failureCode, &detail.UpdatedAt,
			&resultVerdict, &scoreFraction, &passedCases, &totalCases, &maxTimeMS, &maxMemoryBytes,
			&resultSchema, &resultManifest, &resultHash, &resultCreatedAt,
			&outputHash, &outputSize, &outputMediaType, &outputStorageKey,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return databaseFailure("load OJ submission", err)
		}
		detail.Mode = SubmissionMode(mode)
		detail.Status = JobStatus(status)
		detail.FailureCode = failureCode
		detail.CreatedAt = detail.CreatedAt.UTC()
		detail.UpdatedAt = detail.UpdatedAt.UTC()
		if !validStoredJobStatus(detail.Status) || detail.AttemptCount < 0 ||
			(detail.Status == JobSystemError) != (detail.FailureCode != nil) {
			return ojError(ErrorStoredDataInvalid, true, "load OJ submission", errors.New("stored OJ job state is inconsistent"))
		}
		if detail.Status == JobCompleted {
			if resultVerdict == nil || scoreFraction == nil || passedCases == nil || totalCases == nil || maxTimeMS == nil ||
				maxMemoryBytes == nil || resultSchema == nil || resultManifest == nil || resultHash == nil || resultCreatedAt == nil {
				return ojError(ErrorStoredDataInvalid, true, "load OJ submission", errors.New("completed OJ job lacks its result"))
			}
			var output *Artifact
			if outputHash != nil || outputSize != nil || outputMediaType != nil || outputStorageKey != nil {
				if outputHash == nil || outputSize == nil || outputMediaType == nil || outputStorageKey == nil {
					return ojError(ErrorStoredDataInvalid, true, "load OJ submission", errors.New("OJ result output artifact join is partial"))
				}
				value := Artifact{SHA256: *outputHash, SizeBytes: *outputSize, MediaType: *outputMediaType, StorageKey: *outputStorageKey}
				if err := validateArtifact(value, JudgeOutputMediaType, 1<<40); err != nil {
					return ojError(ErrorStoredDataInvalid, true, "load OJ submission", err)
				}
				output = &value
			}
			manifest, manifestHash, err := canonicaljson.Object(json.RawMessage(*resultManifest), maxJudgeResultManifestBytes)
			if err != nil {
				return ojError(ErrorStoredDataInvalid, true, "load OJ submission result", err)
			}
			input := JudgeResultInput{
				Verdict: Verdict(*resultVerdict), ScoreFraction: *scoreFraction,
				PassedCaseCount: *passedCases, TotalCaseCount: *totalCases,
				MaxTimeMS: *maxTimeMS, MaxMemoryBytes: *maxMemoryBytes,
				Output: output, ResultManifest: manifest,
			}
			if _, _, err := validateJudgeResult(input, maxJudgeResultManifestBytes); err != nil ||
				*resultSchema != JudgeResultSchemaV1 || judgeResultHash(input, manifestHash) != *resultHash {
				return ojError(ErrorStoredDataInvalid, true, "load OJ submission result", errors.New("stored judge result provenance is invalid"))
			}
			createdAt := resultCreatedAt.UTC()
			detail.Result = &JudgeResult{
				Verdict: input.Verdict, ScoreFraction: input.ScoreFraction,
				PassedCaseCount: input.PassedCaseCount, TotalCaseCount: input.TotalCaseCount,
				MaxTimeMS: input.MaxTimeMS, MaxMemoryBytes: input.MaxMemoryBytes,
				Output: output, ResultSchema: *resultSchema, ResultManifest: manifest,
				ResultSHA256: *resultHash, CreatedAt: createdAt,
			}
		} else if resultVerdict != nil || resultHash != nil {
			return ojError(ErrorStoredDataInvalid, true, "load OJ submission", errors.New("non-completed OJ job unexpectedly owns a result"))
		}
		found = true
		return nil
	})
	return detail, found, resultErr
}

func (repository *PostgresRepository) ReadJudgeEvents(
	ctx context.Context,
	query JudgeEventQuery,
) (batch JudgeEventBatch, found bool, resultErr error) {
	resultErr = repository.readTransaction(ctx, "read OJ judge events", func(tx postgresTx) error {
		principal, err := principalguard.Resolve(ctx, tx, query.Principal, principalguard.Roles(auth.RoleAdmin, auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize OJ judge events", err)
		}
		var jobDatabaseID int64
		var jobStatus string
		err = tx.QueryRow(ctx, `
SELECT job.judge_job_id, job.status
FROM ascendany.oj_submissions AS submission
JOIN ascendany.oj_judge_jobs AS job ON job.oj_submission_id = submission.oj_submission_id
WHERE submission.public_id = $1::uuid
  AND ($2::boolean OR submission.account_id = $3)`, query.SubmissionID, principal.Role == auth.RoleAdmin,
			principal.AccountDatabaseID).Scan(&jobDatabaseID, &jobStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return databaseFailure("locate OJ judge event stream", err)
		}
		batch.Terminal = jobStatus == string(JobCompleted) || jobStatus == string(JobSystemError)
		rows, err := tx.Query(ctx, `
SELECT event_sequence, event_type, payload::text, created_at
FROM ascendany.oj_judge_job_events
WHERE judge_job_id = $1
  AND event_sequence > $2
ORDER BY event_sequence
LIMIT $3`, jobDatabaseID, query.AfterSequence, query.Limit)
		if err != nil {
			return databaseFailure("query OJ judge events", err)
		}
		defer rows.Close()
		batch.Events = make([]JudgeEvent, 0)
		batch.LastSequence = query.AfterSequence
		for rows.Next() {
			var event JudgeEvent
			var eventType, payload string
			if err := rows.Scan(&event.Sequence, &eventType, &payload, &event.CreatedAt); err != nil {
				return databaseFailure("scan OJ judge event", err)
			}
			canonical, _, err := canonicaljson.Object(json.RawMessage(payload), 64<<10)
			if err != nil {
				return ojError(ErrorStoredDataInvalid, true, "read OJ judge event", err)
			}
			event.Type, event.Payload, err = publicJudgeEvent(eventType, canonical)
			if err != nil {
				return err
			}
			event.CreatedAt = event.CreatedAt.UTC()
			batch.Events = append(batch.Events, event)
			batch.LastSequence = event.Sequence
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate OJ judge events", err)
		}
		found = true
		return nil
	})
	return batch, found, resultErr
}

func publicJudgeEvent(eventType string, payload json.RawMessage) (string, json.RawMessage, error) {
	switch eventType {
	case "queued", "retry_scheduled", "completed", "system_error":
		return eventType, payload, nil
	case "claimed", "reclaimed":
		return "running", json.RawMessage(`{"status":"running"}`), nil
	default:
		return "", nil, ojError(ErrorStoredDataInvalid, true, "read OJ judge event", errors.New("stored OJ judge event type is unsupported"))
	}
}

func validStoredJobStatus(status JobStatus) bool {
	switch status {
	case JobQueued, JobRunning, JobCompleted, JobSystemError:
		return true
	default:
		return false
	}
}
