package importing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func ensureActorsAndIdentifiers(
	ctx context.Context,
	tx dbTx,
	participants []pintia.Participant,
) (map[string]int64, error) {
	actorIDs := make(map[string]int64, len(participants))
	for _, participant := range sortedParticipants(participants) {
		actorID, err := ensureActor(ctx, tx, participant.UserID)
		if err != nil {
			return nil, err
		}
		actorIDs[participant.UserID] = actorID
		identifiers := []struct {
			kind  string
			value *string
		}{
			{"student_user_id", participant.StudentUserID},
			{"student_number", participant.StudentNumber},
		}
		for _, identifier := range identifiers {
			if identifier.value == nil {
				continue
			}
			if err := ensureIdentifier(ctx, tx, actorID, identifier.kind, *identifier.value); err != nil {
				return nil, err
			}
		}
	}
	return actorIDs, nil
}

func ensureActor(ctx context.Context, tx dbTx, userID string) (int64, error) {
	var actorID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO NOTHING
RETURNING actor_id`, userID).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
SELECT actor_id
FROM ascendany.pintia_actors
WHERE user_id = $1`, userID).Scan(&actorID); err != nil {
			return 0, databaseError("load Pintia actor", err)
		}
	} else if err != nil {
		return 0, databaseError("insert Pintia actor", err)
	}
	return actorID, nil
}

func ensureIdentifier(ctx context.Context, tx dbTx, actorID int64, kind, value string) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (
    identifier_kind,
    identifier_value,
    actor_id
)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING`, kind, value, actorID); err != nil {
		return databaseError("insert Pintia actor identifier", err)
	}

	var actorKindValue string
	err := tx.QueryRow(ctx, `
SELECT identifier_value
FROM ascendany.pintia_actor_identifiers
WHERE actor_id = $1
  AND identifier_kind = $2`, actorID, kind).Scan(&actorKindValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return importError(
			ErrorIdentityConflict,
			true,
			"bind Pintia actor identifier",
			fmt.Errorf("actor %d already conflicts with %s %q", actorID, kind, value),
		)
	}
	if err != nil {
		return databaseError("load actor identifier by actor", err)
	}
	var valueActorID int64
	err = tx.QueryRow(ctx, `
SELECT actor_id
FROM ascendany.pintia_actor_identifiers
WHERE identifier_kind = $1
  AND identifier_value = $2`, kind, value).Scan(&valueActorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return importError(
			ErrorIdentityConflict,
			true,
			"bind Pintia actor identifier",
			fmt.Errorf("%s %q is not owned by actor %d", kind, value, actorID),
		)
	}
	if err != nil {
		return databaseError("load actor identifier by value", err)
	}
	if actorKindValue != value || valueActorID != actorID {
		return importError(
			ErrorIdentityConflict,
			true,
			"bind Pintia actor identifier",
			fmt.Errorf("%s %q conflicts with immutable actor identity", kind, value),
		)
	}
	return nil
}

func insertSnapshotContents(
	ctx context.Context,
	tx dbTx,
	snapshot *pintia.Snapshot,
	examID int64,
	snapshotID int64,
	actorIDs map[string]int64,
) error {
	problems := append([]pintia.Problem(nil), snapshot.Problems...)
	sort.Slice(problems, func(left, right int) bool {
		return problems[left].ProblemSetProblemID < problems[right].ProblemSetProblemID
	})
	for index, problem := range problems {
		if err := insertProblem(ctx, tx, snapshotID, problem, index); err != nil {
			return err
		}
	}

	for _, participant := range sortedParticipants(snapshot.Participants) {
		actorID, exists := actorIDs[participant.UserID]
		if !exists {
			return importError(ErrorIdentityConflict, true, "insert snapshot participant", fmt.Errorf("actor is missing for %q", participant.UserID))
		}
		if err := insertParticipant(ctx, tx, snapshotID, actorID, participant); err != nil {
			return err
		}
	}

	submissions := append([]pintia.Submission(nil), snapshot.Submissions...)
	sort.Slice(submissions, func(left, right int) bool {
		return submissions[left].SubmissionID < submissions[right].SubmissionID
	})
	for _, submission := range submissions {
		actorID, exists := actorIDs[submission.UserID]
		if !exists {
			return importError(ErrorIdentityConflict, true, "insert snapshot submission", fmt.Errorf("actor is missing for %q", submission.UserID))
		}
		identityID, err := ensureSubmissionIdentity(ctx, tx, examID, actorID, submission)
		if err != nil {
			return err
		}
		if err := insertSubmission(ctx, tx, snapshotID, examID, actorID, identityID, submission); err != nil {
			return err
		}
	}
	return nil
}

func insertProblem(ctx context.Context, tx dbTx, snapshotID int64, problem pintia.Problem, index int) error {
	maxScore, err := postgresDecimal(problem.MaxScore, fmt.Sprintf("$.problems[%d].maxScore", index))
	if err != nil {
		return err
	}
	timeLimit, err := postgresInteger(problem.TimeLimitMS, fmt.Sprintf("$.problems[%d].timeLimitMs", index))
	if err != nil {
		return err
	}
	memoryLimit, err := postgresInteger(problem.MemoryLimitBytes, fmt.Sprintf("$.problems[%d].memoryLimitBytes", index))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_problems (
    snapshot_id,
    problem_set_problem_id,
    problem_id,
    label,
    title,
    problem_type,
    max_score,
    content_html,
    time_limit_ms,
    memory_limit_bytes
)
VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9, $10)`,
		snapshotID,
		problem.ProblemSetProblemID,
		problem.ProblemID,
		problem.Label,
		problem.Title,
		problem.Type,
		maxScore,
		problem.ContentHTML,
		timeLimit,
		memoryLimit,
	)
	if err != nil {
		return databaseError("insert snapshot problem", err)
	}
	return nil
}

func insertParticipant(
	ctx context.Context,
	tx dbTx,
	snapshotID int64,
	actorID int64,
	participant pintia.Participant,
) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_participants (
    snapshot_id,
    actor_id,
    student_user_id,
    student_number,
    display_name,
    group_name
)
VALUES ($1, $2, $3, $4, $5, $6)`,
		snapshotID,
		actorID,
		participant.StudentUserID,
		participant.StudentNumber,
		participant.DisplayName,
		participant.GroupName,
	); err != nil {
		return databaseError("insert snapshot participant", err)
	}
	if participant.Ranking == nil {
		return nil
	}
	rank, err := requiredPostgresInteger(participant.Ranking.Rank, "$.participants[].ranking.rank")
	if err != nil {
		return err
	}
	totalScore, err := postgresDecimal(participant.Ranking.TotalScore, "$.participants[].ranking.totalScore")
	if err != nil {
		return err
	}
	timeUsed, err := postgresInteger(participant.Ranking.TimeUsedSeconds, "$.participants[].ranking.timeUsedSeconds")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_rankings (
    snapshot_id,
    actor_id,
    rank,
    total_score,
    time_used_seconds
)
VALUES ($1, $2, $3, $4::numeric, $5)`, snapshotID, actorID, rank, totalScore, timeUsed); err != nil {
		return databaseError("insert Pintia ranking", err)
	}
	results := append([]pintia.RankingProblemResult(nil), participant.Ranking.ProblemResults...)
	sort.Slice(results, func(left, right int) bool {
		return results[left].ProblemSetProblemID < results[right].ProblemSetProblemID
	})
	for _, result := range results {
		score, err := postgresDecimal(result.Score, "$.participants[].ranking.problemResults[].score")
		if err != nil {
			return err
		}
		count, err := postgresInteger(result.ValidSubmissionCount, "$.participants[].ranking.problemResults[].validSubmissionCount")
		if err != nil {
			return err
		}
		acceptTimeSeconds, err := requiredPostgresInteger(result.AcceptTimeSeconds, "$.participants[].ranking.problemResults[].acceptTimeSeconds")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_ranking_problem_results (
    snapshot_id,
    actor_id,
    problem_set_problem_id,
    score,
    passed,
    valid_submission_count,
    accept_time_seconds
)
VALUES ($1, $2, $3, $4::numeric, $5, $6, $7)`,
			snapshotID,
			actorID,
			result.ProblemSetProblemID,
			score,
			result.Passed,
			count,
			acceptTimeSeconds,
		); err != nil {
			return databaseError("insert ranking problem result", err)
		}
	}
	return nil
}

func ensureSubmissionIdentity(
	ctx context.Context,
	tx dbTx,
	examID int64,
	actorID int64,
	submission pintia.Submission,
) (int64, error) {
	submittedAt := normalizeInstant(submission.SubmittedAt.Time)
	var identityID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_submission_identities (
    submission_id,
    exam_id,
    actor_id,
    problem_set_problem_id,
    submitted_at,
    code,
    code_sha256
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (submission_id) DO NOTHING
RETURNING submission_identity_id`,
		submission.SubmissionID,
		examID,
		actorID,
		submission.ProblemSetProblemID,
		submittedAt,
		submission.Code,
		submission.CodeSHA256,
	).Scan(&identityID)
	if err == nil {
		return identityID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, databaseError("insert submission identity", err)
	}

	var existingExamID int64
	var existingActorID int64
	var existingProblemID string
	var existingSubmittedAt time.Time
	var existingCode string
	var existingCodeSHA256 string
	err = tx.QueryRow(ctx, `
SELECT submission_identity_id,
       exam_id,
       actor_id,
       problem_set_problem_id,
       submitted_at,
       code,
       code_sha256
FROM ascendany.pintia_submission_identities
WHERE submission_id = $1`, submission.SubmissionID).Scan(
		&identityID,
		&existingExamID,
		&existingActorID,
		&existingProblemID,
		&existingSubmittedAt,
		&existingCode,
		&existingCodeSHA256,
	)
	if err != nil {
		return 0, databaseError("load submission identity", err)
	}
	if existingExamID != examID ||
		existingActorID != actorID ||
		existingProblemID != submission.ProblemSetProblemID ||
		!existingSubmittedAt.Equal(submittedAt) ||
		existingCode != submission.Code ||
		existingCodeSHA256 != submission.CodeSHA256 {
		return 0, importError(
			ErrorSubmissionConflict,
			true,
			"verify submission identity",
			fmt.Errorf("submissionId %q changed an immutable invariant", submission.SubmissionID),
		)
	}
	return identityID, nil
}

func insertSubmission(
	ctx context.Context,
	tx dbTx,
	snapshotID int64,
	examID int64,
	actorID int64,
	identityID int64,
	submission pintia.Submission,
) error {
	score, err := postgresDecimal(submission.Score, "$.submissions[].score")
	if err != nil {
		return err
	}
	timeMS, err := postgresInteger(submission.TimeMS, "$.submissions[].timeMs")
	if err != nil {
		return err
	}
	memoryBytes, err := postgresInteger(submission.MemoryBytes, "$.submissions[].memoryBytes")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_submissions (
    snapshot_id,
    exam_id,
    submission_identity_id,
    actor_id,
    problem_set_problem_id,
    language,
    compiler,
    verdict,
    score,
    time_ms,
    memory_bytes,
    compile_log
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10, $11, $12)`,
		snapshotID,
		examID,
		identityID,
		actorID,
		submission.ProblemSetProblemID,
		submission.Language,
		submission.Compiler,
		submission.Verdict,
		score,
		timeMS,
		memoryBytes,
		submission.CompileLog,
	); err != nil {
		return databaseError("insert snapshot submission", err)
	}
	cases := append([]pintia.CaseResult(nil), submission.CaseResults...)
	sort.Slice(cases, func(left, right int) bool { return cases[left].CaseID < cases[right].CaseID })
	for _, result := range cases {
		score, err := postgresDecimal(result.Score, "$.submissions[].caseResults[].score")
		if err != nil {
			return err
		}
		timeMS, err := postgresInteger(result.TimeMS, "$.submissions[].caseResults[].timeMs")
		if err != nil {
			return err
		}
		memoryBytes, err := postgresInteger(result.MemoryBytes, "$.submissions[].caseResults[].memoryBytes")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_submission_case_results (
    snapshot_id,
    submission_identity_id,
    case_id,
    verdict,
    score,
    time_ms,
    memory_bytes,
    message
)
VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8)`,
			snapshotID,
			identityID,
			result.CaseID,
			result.Verdict,
			score,
			timeMS,
			memoryBytes,
			result.Message,
		); err != nil {
			return databaseError("insert submission case result", err)
		}
	}
	return nil
}
