package examcatalog

import (
	"context"
	"errors"
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
		return nil, catalogError(ErrorInvalidConfiguration, "construct exam catalog PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (readTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginReadTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, catalogError(ErrorInvalidConfiguration, "construct exam catalog PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) LoadPage(ctx context.Context, query ListQuery) (Page, error) {
	var page Page
	err := repository.readTransaction(ctx, "load exam catalog page", func(tx readTx) error {
		if err := resolvePrincipal(ctx, tx, query.Principal); err != nil {
			return err
		}
		var cursorUpdatedAt *time.Time
		var cursorDatabaseID *int64
		if query.Cursor != nil {
			var updatedAt time.Time
			var databaseID int64
			err := tx.QueryRow(ctx, `
SELECT updated_at, exam_id
FROM ascendany.logical_exams
WHERE public_id = $1::uuid
  AND active_snapshot_id IS NOT NULL`, *query.Cursor).Scan(&updatedAt, &databaseID)
			if errors.Is(err, pgx.ErrNoRows) {
				return catalogError(ErrorCursorInvalid, "resolve exam catalog cursor", errors.New("cursor does not identify an active exam"))
			}
			if err != nil {
				return databaseFailure("resolve exam catalog cursor", err)
			}
			cursorUpdatedAt = &updatedAt
			cursorDatabaseID = &databaseID
		}

		rows, err := tx.Query(ctx, summarySelect+`
WHERE exam.active_snapshot_id IS NOT NULL
  AND ($1::timestamptz IS NULL OR (exam.updated_at, exam.exam_id) < ($1, $2))
ORDER BY exam.updated_at DESC, exam.exam_id DESC
LIMIT $3`, cursorUpdatedAt, cursorDatabaseID, query.Limit+1)
		if err != nil {
			return databaseFailure("query exam catalog page", err)
		}
		defer rows.Close()
		items := make([]ExamSummary, 0, query.Limit+1)
		for rows.Next() {
			item, _, err := scanSummary(rows)
			if err != nil {
				return databaseFailure("scan exam catalog page", err)
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate exam catalog page", err)
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
			cursor := items[len(items)-1].ID
			page.NextCursor = &cursor
		}
		page.Items = items
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func (repository *PostgresRepository) LoadDetail(ctx context.Context, query DetailQuery) (Detail, bool, error) {
	var detail Detail
	found := false
	err := repository.readTransaction(ctx, "load exam catalog detail", func(tx readTx) error {
		if err := resolvePrincipal(ctx, tx, query.Principal); err != nil {
			return err
		}
		summary, snapshotDatabaseID, err := scanSummary(tx.QueryRow(ctx, summarySelect+`
WHERE exam.public_id = $1::uuid
  AND exam.active_snapshot_id IS NOT NULL`, query.ExamID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("query exam catalog detail", err)
		}
		rows, err := tx.Query(ctx, `
SELECT problem.problem_set_problem_id,
       problem.problem_id,
       problem.label,
       problem.title,
       problem.max_score::text,
       problem.time_limit_ms,
       problem.memory_limit_bytes,
       (
           SELECT count(*)
           FROM ascendany.pintia_snapshot_submissions AS submission
           WHERE submission.snapshot_id = problem.snapshot_id
             AND submission.problem_set_problem_id = problem.problem_set_problem_id
       ),
       (
           SELECT count(DISTINCT submission.actor_id)
           FROM ascendany.pintia_snapshot_submissions AS submission
           WHERE submission.snapshot_id = problem.snapshot_id
             AND submission.problem_set_problem_id = problem.problem_set_problem_id
       ),
       (
           SELECT count(*)
           FROM ascendany.pintia_ranking_problem_results AS result
           WHERE result.snapshot_id = problem.snapshot_id
             AND result.problem_set_problem_id = problem.problem_set_problem_id
             AND result.passed
       )
FROM ascendany.pintia_snapshot_problems AS problem
WHERE problem.snapshot_id = $1
ORDER BY problem.label ASC NULLS LAST, problem.problem_set_problem_id ASC`, snapshotDatabaseID)
		if err != nil {
			return databaseFailure("query exam catalog problems", err)
		}
		defer rows.Close()
		problems := make([]Problem, 0, summary.ProblemCount)
		for rows.Next() {
			var problem Problem
			if err := rows.Scan(
				&problem.ID,
				&problem.ProblemID,
				&problem.Label,
				&problem.Title,
				&problem.MaxScore,
				&problem.TimeLimitMS,
				&problem.MemoryLimitBytes,
				&problem.SubmissionCount,
				&problem.SubmittingParticipantCount,
				&problem.PassedParticipantCount,
			); err != nil {
				return databaseFailure("scan exam catalog problems", err)
			}
			problems = append(problems, problem)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate exam catalog problems", err)
		}
		detail = Detail{ExamSummary: summary, Problems: problems}
		found = true
		return nil
	})
	if err != nil {
		return Detail{}, false, err
	}
	return detail, found, nil
}

const summarySelect = `
SELECT exam.public_id::text,
       snapshot.public_id::text,
       snapshot.snapshot_id,
       exam.platform,
       exam.source_exam_id,
       snapshot.title,
       snapshot.source_url,
       snapshot.starts_at,
       snapshot.ends_at,
       snapshot.total_score::text,
       snapshot.problems_exported_count,
       snapshot.participants_exported_count,
       snapshot.rankings_exported_count,
       snapshot.submissions_exported_count,
       snapshot.snapshot_sequence,
       exam.head_revision,
       snapshot.exporter_version,
       snapshot.exported_at,
       exam.updated_at
FROM ascendany.logical_exams AS exam
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.exam_id = exam.exam_id
 AND snapshot.snapshot_id = exam.active_snapshot_id
`

type summaryScanner interface {
	Scan(...any) error
}

func scanSummary(scanner summaryScanner) (ExamSummary, int64, error) {
	var summary ExamSummary
	var snapshotDatabaseID int64
	if err := scanner.Scan(
		&summary.ID,
		&summary.SnapshotID,
		&snapshotDatabaseID,
		&summary.Platform,
		&summary.ProblemSetID,
		&summary.Title,
		&summary.SourceURL,
		&summary.StartsAt,
		&summary.EndsAt,
		&summary.TotalScore,
		&summary.ProblemCount,
		&summary.ParticipantCount,
		&summary.RankingCount,
		&summary.SubmissionCount,
		&summary.SnapshotSequence,
		&summary.HeadRevision,
		&summary.ExporterVersion,
		&summary.ExportedAt,
		&summary.UpdatedAt,
	); err != nil {
		return ExamSummary{}, 0, err
	}
	normalizeSummaryTimes(&summary)
	return summary, snapshotDatabaseID, nil
}

func normalizeSummaryTimes(summary *ExamSummary) {
	summary.ExportedAt = summary.ExportedAt.UTC()
	summary.UpdatedAt = summary.UpdatedAt.UTC()
	if summary.StartsAt != nil {
		value := summary.StartsAt.UTC()
		summary.StartsAt = &value
	}
	if summary.EndsAt != nil {
		value := summary.EndsAt.UTC()
		summary.EndsAt = &value
	}
}

func resolvePrincipal(ctx context.Context, tx readTx, principal auth.AccessPrincipal) error {
	_, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleStudent, auth.RoleAdmin))
	if err == nil {
		return nil
	}
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorRejected:
		return catalogError(ErrorPrincipalRejected, "revalidate exam catalog principal", err)
	case principalguard.ErrorCanceled:
		return catalogError(ErrorCanceled, "revalidate exam catalog principal", err)
	case principalguard.ErrorDatabase:
		return catalogError(ErrorDatabase, "revalidate exam catalog principal", err)
	default:
		return catalogError(ErrorStoredDataInvalid, "revalidate exam catalog principal", err)
	}
}

func (repository *PostgresRepository) readTransaction(
	ctx context.Context,
	operation string,
	run func(readTx) error,
) (resultErr error) {
	tx, err := repository.begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
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
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}
