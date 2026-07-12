package oj

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

func (repository *PostgresRepository) CreateSubmission(
	ctx context.Context,
	command CreateSubmissionCommand,
) (result CreateSubmissionResult, resultErr error) {
	resultErr = repository.transaction(ctx, "create OJ submission", func(tx postgresTx) error {
		principal, err := principalguard.ResolveForUpdate(ctx, tx, command.Principal, principalguard.Roles(auth.RoleAdmin, auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize OJ submission", err)
		}
		existing, found, err := loadExistingSubmission(ctx, tx, command, principal.AccountDatabaseID)
		if err != nil {
			return err
		}
		if found {
			result = CreateSubmissionResult{Submission: existing, Created: false}
			return nil
		}
		var problemDatabaseID, versionDatabaseID, versionNumber int64
		var lifecycle string
		if err := tx.QueryRow(ctx, `
SELECT problem.oj_problem_id,
       version.oj_problem_version_id,
       version.version_number,
       version.lifecycle
FROM ascendany.oj_problems AS problem
JOIN ascendany.oj_problem_versions AS version
  ON version.oj_problem_version_id = problem.current_version_id
 AND version.oj_problem_id = problem.oj_problem_id
WHERE problem.public_id = $1::uuid
  AND problem.head_revision = $2
FOR SHARE OF problem`, command.ProblemID, command.ExpectedProblemHeadRevision).Scan(
			&problemDatabaseID, &versionDatabaseID, &versionNumber, &lifecycle,
		); errors.Is(err, pgx.ErrNoRows) {
			return ojError(ErrorHeadConflict, true, "bind OJ submission to problem head", errors.New("OJ problem head was not found"))
		} else if err != nil {
			return databaseFailure("bind OJ submission to problem head", err)
		}
		if Lifecycle(lifecycle) != LifecycleActive || versionNumber != command.ExpectedProblemHeadRevision {
			return ojError(ErrorNotFound, true, "bind OJ submission to problem head", errors.New("OJ problem is not active"))
		}
		sourceArtifactID, err := registerArtifact(ctx, tx, command.Source)
		if err != nil {
			return err
		}
		var stdinArtifactID *int64
		if command.Stdin != nil {
			id, err := registerArtifact(ctx, tx, *command.Stdin)
			if err != nil {
				return err
			}
			stdinArtifactID = &id
		}
		var submissionDatabaseID int64
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.oj_submissions (
    public_id, account_id, session_id, client_request_id,
    oj_problem_id, oj_problem_version_id, submission_mode, language_id,
    source_artifact_id, stdin_artifact_id
)
VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6, $7, $8, $9, $10)
RETURNING oj_submission_id, created_at`, command.SubmissionPublicID, principal.AccountDatabaseID,
			principal.SessionDatabaseID, command.ClientRequestID, problemDatabaseID, versionDatabaseID,
			string(command.Mode), command.LanguageID, sourceArtifactID, stdinArtifactID,
		).Scan(&submissionDatabaseID, &createdAt); err != nil {
			return databaseFailure("insert OJ submission", err)
		}
		var judgeJobDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.oj_judge_jobs (public_id, oj_submission_id, status)
VALUES ($1::uuid, $2, 'queued')
RETURNING judge_job_id`, command.JudgeJobPublicID, submissionDatabaseID).Scan(&judgeJobDatabaseID); err != nil {
			return databaseFailure("insert OJ judge job", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.oj_judge_job_events (judge_job_id, event_sequence, event_type, payload)
VALUES ($1, 1, 'queued', jsonb_build_object('submissionId', $2::text))`, judgeJobDatabaseID, command.SubmissionPublicID); err != nil {
			return databaseFailure("append OJ judge queued event", err)
		}
		result = CreateSubmissionResult{Created: true, Submission: Submission{
			ID:             command.SubmissionPublicID,
			JudgeJobID:     command.JudgeJobPublicID,
			ProblemID:      command.ProblemID,
			ProblemVersion: versionNumber,
			Mode:           command.Mode,
			LanguageID:     command.LanguageID,
			CreatedAt:      createdAt.UTC(),
		}}
		return nil
	})
	return result, resultErr
}

func loadExistingSubmission(
	ctx context.Context,
	tx postgresTx,
	command CreateSubmissionCommand,
	accountDatabaseID int64,
) (Submission, bool, error) {
	var submission Submission
	var mode string
	var storedAccountID int64
	var storedHeadRevision int64
	var sourceHash string
	var stdinHash *string
	err := tx.QueryRow(ctx, `
SELECT submission.public_id::text,
       job.public_id::text,
       problem.public_id::text,
       version.version_number,
       submission.submission_mode,
       submission.language_id,
       submission.created_at,
       submission.account_id,
       version.version_number,
       source.sha256,
       stdin.sha256
FROM ascendany.oj_submissions AS submission
JOIN ascendany.oj_judge_jobs AS job ON job.oj_submission_id = submission.oj_submission_id
JOIN ascendany.oj_problems AS problem ON problem.oj_problem_id = submission.oj_problem_id
JOIN ascendany.oj_problem_versions AS version
  ON version.oj_problem_version_id = submission.oj_problem_version_id
 AND version.oj_problem_id = submission.oj_problem_id
JOIN ascendany.artifacts AS source ON source.artifact_id = submission.source_artifact_id
LEFT JOIN ascendany.artifacts AS stdin ON stdin.artifact_id = submission.stdin_artifact_id
WHERE submission.account_id = $1
  AND submission.client_request_id = $2::uuid`, accountDatabaseID, command.ClientRequestID).Scan(
		&submission.ID, &submission.JudgeJobID, &submission.ProblemID, &submission.ProblemVersion,
		&mode, &submission.LanguageID, &submission.CreatedAt, &storedAccountID, &storedHeadRevision,
		&sourceHash, &stdinHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, false, nil
	}
	if err != nil {
		return Submission{}, false, databaseFailure("load idempotent OJ submission", err)
	}
	submission.Mode = SubmissionMode(mode)
	submission.CreatedAt = submission.CreatedAt.UTC()
	wantStdinHash := (*string)(nil)
	if command.Stdin != nil {
		wantStdinHash = &command.Stdin.SHA256
	}
	if storedAccountID != accountDatabaseID || submission.ProblemID != command.ProblemID ||
		storedHeadRevision != command.ExpectedProblemHeadRevision || submission.Mode != command.Mode ||
		submission.LanguageID != command.LanguageID || sourceHash != command.Source.SHA256 || !sameOptionalString(stdinHash, wantStdinHash) {
		return Submission{}, false, ojError(ErrorIdempotencyConflict, true, "validate idempotent OJ submission", errors.New("client request ID was already used for a different OJ submission"))
	}
	return submission, true, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
