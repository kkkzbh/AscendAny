package oj

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

func (repository *PostgresRepository) ClaimJudge(
	ctx context.Context,
	owner string,
	attemptToken string,
	leaseDuration time.Duration,
) (claim *JudgeClaim, resultErr error) {
	if owner == "" || len(owner) > 128 || !canonicalUUIDv4.MatchString(attemptToken) || leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return nil, ojError(ErrorInvalidInput, true, "claim OJ judge job", errors.New("bounded owner, canonical attempt token, and positive lease are required"))
	}
	resultErr = repository.transaction(ctx, "claim OJ judge job", func(tx postgresTx) error {
		candidate := JudgeClaim{}
		var previousStatus string
		err := tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT judge_job_id, status
    FROM ascendany.oj_judge_jobs
    WHERE (status = 'queued' AND next_attempt_at <= clock_timestamp())
       OR (status = 'running' AND lease_expires_at <= clock_timestamp())
    ORDER BY CASE WHEN status = 'queued' THEN next_attempt_at ELSE lease_expires_at END,
             judge_job_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE ascendany.oj_judge_jobs AS job
SET status = 'running',
    attempt_count = job.attempt_count + 1,
    attempt_token = $2::uuid,
    lease_owner = $1,
    lease_expires_at = clock_timestamp() + ($3::bigint * interval '1 millisecond'),
    started_at = COALESCE(job.started_at, clock_timestamp()),
    updated_at = clock_timestamp()
FROM candidate
WHERE job.judge_job_id = candidate.judge_job_id
RETURNING job.judge_job_id, job.public_id::text, job.attempt_count,
          job.attempt_token::text, job.lease_owner, job.lease_expires_at,
          candidate.status`, owner, attemptToken, leaseDuration.Milliseconds()).Scan(
			&candidate.DatabaseID, &candidate.ID, &candidate.AttemptCount, &candidate.AttemptToken,
			&candidate.LeaseOwner, &candidate.LeaseExpiresAt, &previousStatus,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			claim = nil
			return nil
		}
		if err != nil {
			return databaseFailure("claim OJ judge job", err)
		}
		candidate.LeaseExpiresAt = candidate.LeaseExpiresAt.UTC()
		candidate.Reclaimed = previousStatus == "running"
		eventType := "claimed"
		if candidate.Reclaimed {
			eventType = "reclaimed"
		}
		if err := appendJudgeEvent(ctx, tx, candidate.DatabaseID, eventType, map[string]any{
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

func (repository *PostgresRepository) RenewJudgeLease(ctx context.Context, claim JudgeClaim, leaseDuration time.Duration) error {
	if err := validateJudgeClaim(claim); err != nil {
		return err
	}
	if leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return ojError(ErrorInvalidInput, true, "renew OJ judge lease", errors.New("positive lease duration is required"))
	}
	return repository.transaction(ctx, "renew OJ judge lease", func(tx postgresTx) error {
		var expires time.Time
		err := tx.QueryRow(ctx, `
UPDATE ascendany.oj_judge_jobs
SET lease_expires_at = clock_timestamp() + ($6::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE judge_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()
RETURNING lease_expires_at`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken,
			claim.LeaseOwner, leaseDuration.Milliseconds()).Scan(&expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return ojError(ErrorLeaseLost, false, "renew OJ judge lease", errors.New("judge attempt is no longer active"))
		}
		if err != nil {
			return databaseFailure("renew OJ judge lease", err)
		}
		return nil
	})
}

func (repository *PostgresRepository) LoadExecution(ctx context.Context, claim JudgeClaim) (request judgecontract.ExecutionRequest, resultErr error) {
	if err := validateJudgeClaim(claim); err != nil {
		return judgecontract.ExecutionRequest{}, err
	}
	resultErr = repository.transaction(ctx, "load OJ execution", func(tx postgresTx) error {
		var mode string
		var problemSpec string
		var stdinHash, stdinMediaType, stdinStorageKey *string
		var stdinSize *int64
		err := tx.QueryRow(ctx, `
SELECT job.public_id::text,
       submission.public_id::text,
       problem.public_id::text,
       version.version_number,
       submission.submission_mode,
       submission.language_id,
       source.sha256, source.size_bytes, source.media_type, source.storage_key,
       stdin.sha256, stdin.size_bytes, stdin.media_type, stdin.storage_key,
       tests.sha256, tests.size_bytes, tests.media_type, tests.storage_key,
       version.problem_schema,
       version.problem_spec::text,
       version.time_limit_ms,
       version.memory_limit_bytes,
       version.output_limit_bytes
FROM ascendany.oj_judge_jobs AS job
JOIN ascendany.oj_submissions AS submission ON submission.oj_submission_id = job.oj_submission_id
JOIN ascendany.oj_problems AS problem ON problem.oj_problem_id = submission.oj_problem_id
JOIN ascendany.oj_problem_versions AS version
  ON version.oj_problem_version_id = submission.oj_problem_version_id
 AND version.oj_problem_id = submission.oj_problem_id
JOIN ascendany.artifacts AS source ON source.artifact_id = submission.source_artifact_id
LEFT JOIN ascendany.artifacts AS stdin ON stdin.artifact_id = submission.stdin_artifact_id
JOIN ascendany.artifacts AS tests ON tests.artifact_id = version.test_bundle_artifact_id
WHERE job.judge_job_id = $1
  AND job.public_id = $2::uuid
  AND job.status = 'running'
  AND job.attempt_count = $3
  AND job.attempt_token = $4::uuid
  AND job.lease_owner = $5
  AND job.lease_expires_at > clock_timestamp()`, claim.DatabaseID, claim.ID, claim.AttemptCount,
			claim.AttemptToken, claim.LeaseOwner).Scan(
			&request.JudgeJobID, &request.SubmissionID, &request.ProblemID, &request.ProblemVersion,
			&mode, &request.LanguageID,
			&request.Source.SHA256, &request.Source.SizeBytes, &request.Source.MediaType, &request.Source.StorageKey,
			&stdinHash, &stdinSize, &stdinMediaType, &stdinStorageKey,
			&request.TestBundle.SHA256, &request.TestBundle.SizeBytes, &request.TestBundle.MediaType, &request.TestBundle.StorageKey,
			&request.ProblemSchema, &problemSpec, &request.TimeLimitMS, &request.MemoryLimitBytes, &request.OutputLimitBytes,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ojError(ErrorLeaseLost, false, "load OJ execution", errors.New("judge attempt is no longer active"))
		}
		if err != nil {
			return databaseFailure("load OJ execution", err)
		}
		request.Mode = judgecontract.SubmissionMode(mode)
		if stdinHash != nil {
			if stdinSize == nil || stdinMediaType == nil || stdinStorageKey == nil {
				return ojError(ErrorStoredDataInvalid, true, "load OJ execution", errors.New("stdin artifact join is partial"))
			}
			request.Stdin = &judgecontract.Artifact{SHA256: *stdinHash, SizeBytes: *stdinSize, MediaType: *stdinMediaType, StorageKey: *stdinStorageKey}
		}
		canonical, _, err := canonicaljsonObject(problemSpec)
		if err != nil {
			return err
		}
		request.ProblemSpec = canonical
		if request.JudgeJobID != claim.ID || request.ProblemVersion < 1 || request.ProblemSchema != judgecontract.ProblemSchemaV1 ||
			request.LanguageID != judgecontract.LanguageCPP20 || judgecontract.ValidateArtifact(request.Source, judgecontract.CPP20SourceMediaType, 1<<30) != nil ||
			judgecontract.ValidateArtifact(request.TestBundle, judgecontract.TestBundleMediaType, 1<<40) != nil ||
			(request.Mode == judgecontract.SubmissionRun && (request.Stdin == nil || judgecontract.ValidateArtifact(*request.Stdin, judgecontract.PlainTextMediaType, 1<<30) != nil)) ||
			(request.Mode == judgecontract.SubmissionSubmit && request.Stdin != nil) || request.TimeLimitMS < 1 ||
			request.MemoryLimitBytes < 1 || request.OutputLimitBytes < 1 {
			return ojError(ErrorStoredDataInvalid, true, "load OJ execution", errors.New("stored execution request violates the OJ contract"))
		}
		return nil
	})
	return request, resultErr
}

func canonicaljsonObject(raw string) (json.RawMessage, string, error) {
	canonical, digest, err := canonicaljson.Object(json.RawMessage(raw), 1<<20)
	if err != nil {
		return nil, "", ojError(ErrorStoredDataInvalid, true, "validate stored OJ JSON", err)
	}
	return canonical, digest, nil
}

func (repository *PostgresRepository) CompleteJudge(ctx context.Context, command CompleteJudgeCommand) (result JudgeResult, resultErr error) {
	if err := validateJudgeClaim(command.Claim); err != nil {
		return JudgeResult{}, err
	}
	manifest, manifestHash, err := validateJudgeResult(command.JudgeResultInput, maxJudgeResultManifestBytes)
	expectedResultHash := judgeResultHash(command.JudgeResultInput, manifestHash)
	if err != nil || command.ResultSchema != JudgeResultSchemaV1 || command.ResultSHA256 != expectedResultHash ||
		string(command.ResultManifest) != string(manifest) {
		if err == nil {
			err = errors.New("canonical judge result provenance is inconsistent")
		}
		return JudgeResult{}, ojError(ErrorInvalidInput, true, "complete OJ judge job", err)
	}
	resultErr = repository.transaction(ctx, "complete OJ judge job", func(tx postgresTx) error {
		var outputLimit int64
		if err := tx.QueryRow(ctx, `
SELECT version.output_limit_bytes
FROM ascendany.oj_judge_jobs AS job
JOIN ascendany.oj_submissions AS submission ON submission.oj_submission_id = job.oj_submission_id
JOIN ascendany.oj_problem_versions AS version ON version.oj_problem_version_id = submission.oj_problem_version_id
WHERE job.judge_job_id = $1
  AND job.public_id = $2::uuid
  AND job.status = 'running'
  AND job.attempt_count = $3
  AND job.attempt_token = $4::uuid
  AND job.lease_owner = $5
  AND job.lease_expires_at > clock_timestamp()
FOR UPDATE OF job`, command.Claim.DatabaseID, command.Claim.ID, command.Claim.AttemptCount,
			command.Claim.AttemptToken, command.Claim.LeaseOwner).Scan(&outputLimit); errors.Is(err, pgx.ErrNoRows) {
			return ojError(ErrorLeaseLost, false, "complete OJ judge job", errors.New("judge attempt is no longer active"))
		} else if err != nil {
			return databaseFailure("lock completing OJ judge job", err)
		}
		var outputArtifactID *int64
		if command.Output != nil {
			if command.Output.SizeBytes > outputLimit {
				return ojError(ErrorInvalidInput, true, "complete OJ judge job", errors.New("judge output exceeds problem limit"))
			}
			id, err := registerArtifact(ctx, tx, *command.Output)
			if err != nil {
				return err
			}
			outputArtifactID = &id
		}
		var resultDatabaseID int64
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.oj_judge_results (
    judge_job_id, verdict, score_fraction, passed_case_count, total_case_count,
    max_time_ms, max_memory_bytes, output_artifact_id,
    result_schema, result_manifest, result_sha256
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
RETURNING judge_result_id, created_at`, command.Claim.DatabaseID, string(command.Verdict), command.ScoreFraction,
			command.PassedCaseCount, command.TotalCaseCount, command.MaxTimeMS, command.MaxMemoryBytes,
			outputArtifactID, command.ResultSchema, string(command.ResultManifest), command.ResultSHA256,
		).Scan(&resultDatabaseID, &createdAt); err != nil {
			return databaseFailure("insert OJ judge result", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.oj_judge_jobs
SET status = 'completed', attempt_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    judge_result_id = $6, finished_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE judge_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, command.Claim.DatabaseID, command.Claim.ID,
			command.Claim.AttemptCount, command.Claim.AttemptToken, command.Claim.LeaseOwner, resultDatabaseID)
		if err != nil {
			return databaseFailure("complete OJ judge job", err)
		}
		if tag.RowsAffected() != 1 {
			return ojError(ErrorLeaseLost, false, "complete OJ judge job", errors.New("judge attempt is no longer active"))
		}
		if err := appendJudgeEvent(ctx, tx, command.Claim.DatabaseID, "completed", map[string]any{
			"verdict": command.Verdict, "scoreFraction": command.ScoreFraction, "resultSha256": command.ResultSHA256,
		}); err != nil {
			return err
		}
		result = JudgeResult{
			Verdict: command.Verdict, ScoreFraction: command.ScoreFraction,
			PassedCaseCount: command.PassedCaseCount, TotalCaseCount: command.TotalCaseCount,
			MaxTimeMS: command.MaxTimeMS, MaxMemoryBytes: command.MaxMemoryBytes,
			Output: command.Output, ResultSchema: command.ResultSchema,
			ResultManifest: command.ResultManifest, ResultSHA256: command.ResultSHA256,
			CreatedAt: createdAt.UTC(),
		}
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) RequeueJudge(ctx context.Context, claim JudgeClaim, retryDelay time.Duration, reason string) error {
	if err := validateJudgeClaim(claim); err != nil {
		return err
	}
	if retryDelay < time.Second || !executionFailureCodePattern.MatchString(reason) {
		return ojError(ErrorInvalidInput, true, "requeue OJ judge job", errors.New("bounded retry delay and canonical reason are required"))
	}
	return repository.transitionJudge(ctx, claim, "requeue OJ judge job", `
UPDATE ascendany.oj_judge_jobs
SET status = 'queued', attempt_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    next_attempt_at = clock_timestamp() + ($6::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE judge_job_id = $1 AND public_id = $2::uuid AND status = 'running'
  AND attempt_count = $3 AND attempt_token = $4::uuid AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, []any{retryDelay.Milliseconds()}, "retry_scheduled", map[string]any{
		"delayMilliseconds": retryDelay.Milliseconds(), "reason": reason,
	})
}

func (repository *PostgresRepository) FailJudge(ctx context.Context, claim JudgeClaim, code, detail string) error {
	if err := validateJudgeClaim(claim); err != nil {
		return err
	}
	if !executionFailureCodePattern.MatchString(code) || detail == "" || len(detail) > 4096 {
		return ojError(ErrorInvalidInput, true, "fail OJ judge job", errors.New("canonical code and bounded detail are required"))
	}
	return repository.transitionJudge(ctx, claim, "fail OJ judge job", `
UPDATE ascendany.oj_judge_jobs
SET status = 'system_error', attempt_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    error_code = $6, error_detail = $7, finished_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE judge_job_id = $1 AND public_id = $2::uuid AND status = 'running'
  AND attempt_count = $3 AND attempt_token = $4::uuid AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, []any{code, detail}, "system_error", map[string]any{"code": code})
}

func (repository *PostgresRepository) transitionJudge(
	ctx context.Context,
	claim JudgeClaim,
	operation, query string,
	extraArguments []any,
	eventType string,
	payload map[string]any,
) error {
	return repository.transaction(ctx, operation, func(tx postgresTx) error {
		arguments := []any{claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner}
		arguments = append(arguments, extraArguments...)
		tag, err := tx.Exec(ctx, query, arguments...)
		if err != nil {
			return databaseFailure(operation, err)
		}
		if tag.RowsAffected() != 1 {
			return ojError(ErrorLeaseLost, false, operation, errors.New("judge attempt is no longer active"))
		}
		return appendJudgeEvent(ctx, tx, claim.DatabaseID, eventType, payload)
	})
}

func appendJudgeEvent(ctx context.Context, tx postgresTx, jobID int64, eventType string, payload map[string]any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return ojError(ErrorStoredDataInvalid, true, "encode OJ judge event", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.oj_judge_job_events (judge_job_id, event_sequence, event_type, payload)
SELECT $1, COALESCE(MAX(event_sequence), 0) + 1, $2, $3::jsonb
FROM ascendany.oj_judge_job_events
WHERE judge_job_id = $1`, jobID, eventType, string(payloadJSON)); err != nil {
		return databaseFailure("append OJ judge event", err)
	}
	return nil
}

func validateJudgeClaim(claim JudgeClaim) error {
	if claim.DatabaseID <= 0 || !canonicalUUIDv4.MatchString(claim.ID) || claim.AttemptCount <= 0 ||
		!canonicalUUIDv4.MatchString(claim.AttemptToken) || claim.LeaseOwner == "" || len(claim.LeaseOwner) > 128 {
		return ojError(ErrorInvalidInput, true, "validate OJ judge claim", errors.New("canonical active judge claim is required"))
	}
	return nil
}
