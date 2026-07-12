package oj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type storedProblem struct {
	databaseID       int64
	currentVersionID *int64
	Problem
}

func (repository *PostgresRepository) CreateProblemVersion(
	ctx context.Context,
	command CreateProblemVersionCommand,
) (result CreateProblemVersionResult, resultErr error) {
	resultErr = repository.transaction(ctx, "create OJ problem version", func(tx postgresTx) error {
		principal, err := principalguard.ResolveForUpdate(ctx, tx, command.Principal, principalguard.Roles(auth.RoleAdmin))
		if err != nil {
			return mapPrincipalError("authorize OJ problem version", err)
		}
		stored, found, err := lockProblemBySlug(ctx, tx, command.Slug)
		if err != nil {
			return err
		}
		if !found {
			if command.ExpectedHeadRevision != 0 {
				return ojError(ErrorHeadConflict, true, "create OJ problem", fmt.Errorf("expected head revision %d, found 0", command.ExpectedHeadRevision))
			}
			var inserted bool
			stored, inserted, err = insertProblem(ctx, tx, command.ProblemPublicID, command.Slug)
			if err != nil {
				return err
			}
			if !inserted {
				stored, found, err = lockProblemBySlug(ctx, tx, command.Slug)
				if err != nil {
					return err
				}
				if !found {
					return ojError(ErrorStoredDataInvalid, true, "lock concurrently created OJ problem", errors.New("conflicting slug has no durable row"))
				}
			}
		}
		existing, existingVersionID, versionFound, err := findProblemVersionByHash(ctx, tx, stored.databaseID, command.ContentSHA256)
		if err != nil {
			return err
		}
		if versionFound {
			if stored.currentVersionID == nil || *stored.currentVersionID != existingVersionID {
				return ojError(ErrorIdempotencyConflict, true, "replay OJ problem version", errors.New("content exists as an inactive immutable version"))
			}
			if command.ExpectedHeadRevision != stored.HeadRevision &&
				(stored.HeadRevision == 0 || command.ExpectedHeadRevision != stored.HeadRevision-1) {
				return ojError(ErrorHeadConflict, true, "replay OJ problem version", fmt.Errorf("expected head revision %d, found %d", command.ExpectedHeadRevision, stored.HeadRevision))
			}
			problem, err := loadProblem(ctx, tx, stored.databaseID, true)
			if err != nil {
				return err
			}
			if problem.CurrentVersion == nil || problem.CurrentVersion.ContentSHA256 != existing.ContentSHA256 {
				return ojError(ErrorStoredDataInvalid, true, "replay OJ problem version", errors.New("active OJ problem version differs from idempotent content"))
			}
			result = CreateProblemVersionResult{Problem: problem, Idempotent: true}
			return nil
		}
		if stored.HeadRevision != command.ExpectedHeadRevision {
			return ojError(ErrorHeadConflict, true, "create OJ problem version", fmt.Errorf("expected head revision %d, found %d", command.ExpectedHeadRevision, stored.HeadRevision))
		}
		testBundleID, err := registerArtifact(ctx, tx, command.TestBundle)
		if err != nil {
			return err
		}
		nextNumber := stored.HeadRevision + 1
		var versionID int64
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read OJ problem mutation time", err)
		}
		mutationTime = mutationTime.UTC()
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.oj_problem_versions (
    oj_problem_id, version_number, lifecycle, title, statement_markdown, solution_markdown,
    knowledge_tags, time_limit_ms, memory_limit_bytes, output_limit_bytes,
    problem_schema, problem_spec, test_bundle_artifact_id, content_sha256,
    created_by_account_id, created_by_role, created_by_session_id, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
        $11, $12::jsonb, $13, $14, $15, 'admin', $16, $17)
RETURNING oj_problem_version_id`,
			stored.databaseID, nextNumber, string(command.Lifecycle), command.Title, command.StatementMarkdown,
			command.SolutionMarkdown, command.KnowledgeTags, command.TimeLimitMS, command.MemoryLimitBytes,
			command.OutputLimitBytes, command.ProblemSchema, string(command.ProblemSpec), testBundleID,
			command.ContentSHA256, principal.AccountDatabaseID, principal.SessionDatabaseID, mutationTime,
		).Scan(&versionID); err != nil {
			return databaseFailure("insert OJ problem version", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.oj_problems
SET current_version_id = $2,
    head_revision = head_revision + 1,
    updated_at = $3
WHERE oj_problem_id = $1
  AND head_revision = $4`, stored.databaseID, versionID, mutationTime, command.ExpectedHeadRevision)
		if err != nil {
			return databaseFailure("advance OJ problem head", err)
		}
		if tag.RowsAffected() != 1 {
			return ojError(ErrorHeadConflict, true, "advance OJ problem head", errors.New("OJ problem head changed concurrently"))
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.audit_events (account_id, session_id, event_type, occurred_at, payload)
VALUES ($1, $2, 'admin.oj_problem_version_created', $3,
        jsonb_build_object('problemId', $4::text, 'slug', $5::text,
                           'version', $6::bigint, 'contentSha256', $7::text,
                           'lifecycle', $8::text))`, principal.AccountDatabaseID, principal.SessionDatabaseID,
			mutationTime, stored.ID, command.Slug, nextNumber, command.ContentSHA256, string(command.Lifecycle)); err != nil {
			return databaseFailure("append OJ problem audit", err)
		}
		problem, err := loadProblem(ctx, tx, stored.databaseID, true)
		if err != nil {
			return err
		}
		result = CreateProblemVersionResult{Problem: problem}
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) GetProblem(ctx context.Context, query ProblemQuery) (result Problem, found bool, resultErr error) {
	resultErr = repository.readTransaction(ctx, "get OJ problem", func(tx postgresTx) error {
		principal, err := principalguard.Resolve(ctx, tx, query.Principal, principalguard.Roles(auth.RoleAdmin, auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize OJ problem read", err)
		}
		var databaseID int64
		if err := tx.QueryRow(ctx, `SELECT oj_problem_id FROM ascendany.oj_problems WHERE public_id = $1::uuid`, query.ProblemID).Scan(&databaseID); errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		} else if err != nil {
			return databaseFailure("locate OJ problem", err)
		}
		result, err = loadProblem(ctx, tx, databaseID, principal.Role == auth.RoleAdmin)
		if err != nil {
			return err
		}
		if principal.Role == auth.RoleStudent && (result.CurrentVersion == nil || result.CurrentVersion.Lifecycle != LifecycleActive) {
			result = Problem{}
			found = false
			return nil
		}
		found = true
		return nil
	})
	return result, found, resultErr
}

func (repository *PostgresRepository) ListProblems(ctx context.Context, query ProblemListQuery) (page ProblemPage, resultErr error) {
	resultErr = repository.readTransaction(ctx, "list OJ problems", func(tx postgresTx) error {
		principal, err := principalguard.Resolve(ctx, tx, query.Principal, principalguard.Roles(auth.RoleAdmin, auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize OJ problem list", err)
		}
		if query.IncludeArchived && principal.Role != auth.RoleAdmin {
			return ojError(ErrorPrincipalRejected, true, "authorize archived OJ problem list", errors.New("administrator role is required"))
		}
		rows, err := tx.Query(ctx, `
SELECT problem.oj_problem_id
FROM ascendany.oj_problems AS problem
JOIN ascendany.oj_problem_versions AS version
  ON version.oj_problem_version_id = problem.current_version_id
 AND version.oj_problem_id = problem.oj_problem_id
WHERE ($1::text IS NULL OR problem.slug > $1 COLLATE "C")
  AND ($2::boolean OR version.lifecycle = 'active')
ORDER BY problem.slug COLLATE "C"
LIMIT $3`, query.AfterSlug, query.IncludeArchived, query.Limit+1)
		if err != nil {
			return databaseFailure("query OJ problem page", err)
		}
		defer rows.Close()
		ids := make([]int64, 0, query.Limit+1)
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return databaseFailure("scan OJ problem page", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate OJ problem page", err)
		}
		more := len(ids) > query.Limit
		if more {
			ids = ids[:query.Limit]
		}
		page.Items = make([]Problem, 0, len(ids))
		for _, id := range ids {
			problem, err := loadProblem(ctx, tx, id, principal.Role == auth.RoleAdmin)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, problem)
		}
		if more && len(page.Items) > 0 {
			cursor := page.Items[len(page.Items)-1].Slug
			page.NextCursor = &cursor
		}
		return nil
	})
	return page, resultErr
}

func lockProblemBySlug(ctx context.Context, tx postgresTx, slug string) (storedProblem, bool, error) {
	stored, err := scanStoredProblem(tx.QueryRow(ctx, `
SELECT oj_problem_id, public_id::text, slug, current_version_id, head_revision, created_at, updated_at
FROM ascendany.oj_problems
WHERE slug = $1
FOR UPDATE`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return storedProblem{}, false, nil
	}
	if err != nil {
		return storedProblem{}, false, databaseFailure("lock OJ problem", err)
	}
	return stored, true, nil
}

func insertProblem(ctx context.Context, tx postgresTx, publicID, slug string) (storedProblem, bool, error) {
	stored, err := scanStoredProblem(tx.QueryRow(ctx, `
INSERT INTO ascendany.oj_problems (public_id, slug)
VALUES ($1::uuid, $2)
ON CONFLICT (slug) DO NOTHING
RETURNING oj_problem_id, public_id::text, slug, current_version_id, head_revision, created_at, updated_at`, publicID, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return storedProblem{}, false, nil
	}
	if err != nil {
		return storedProblem{}, false, databaseFailure("insert OJ problem", err)
	}
	return stored, true, nil
}

func scanStoredProblem(scanner interface{ Scan(...any) error }) (storedProblem, error) {
	var stored storedProblem
	if err := scanner.Scan(&stored.databaseID, &stored.ID, &stored.Slug, &stored.currentVersionID,
		&stored.HeadRevision, &stored.CreatedAt, &stored.UpdatedAt); err != nil {
		return storedProblem{}, err
	}
	stored.CreatedAt = stored.CreatedAt.UTC()
	stored.UpdatedAt = stored.UpdatedAt.UTC()
	return stored, nil
}

func findProblemVersionByHash(ctx context.Context, tx postgresTx, problemID int64, contentHash string) (ProblemVersion, int64, bool, error) {
	version, versionID, err := scanProblemVersion(tx.QueryRow(ctx, problemVersionSelect+`
WHERE version.oj_problem_id = $1 AND version.content_sha256 = $2`, problemID, contentHash), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProblemVersion{}, 0, false, nil
	}
	if err != nil {
		return ProblemVersion{}, 0, false, databaseFailure("find OJ problem version by hash", err)
	}
	return version, versionID, true, nil
}

func loadProblem(ctx context.Context, tx postgresTx, databaseID int64, includeSolution bool) (Problem, error) {
	stored, err := scanStoredProblem(tx.QueryRow(ctx, `
SELECT oj_problem_id, public_id::text, slug, current_version_id, head_revision, created_at, updated_at
FROM ascendany.oj_problems
WHERE oj_problem_id = $1`, databaseID))
	if err != nil {
		return Problem{}, databaseFailure("load OJ problem", err)
	}
	if stored.currentVersionID == nil || stored.HeadRevision < 1 {
		return Problem{}, ojError(ErrorStoredDataInvalid, true, "load OJ problem", errors.New("OJ problem lacks an active immutable version"))
	}
	version, versionID, err := scanProblemVersion(tx.QueryRow(ctx, problemVersionSelect+`
WHERE version.oj_problem_version_id = $1 AND version.oj_problem_id = $2`, *stored.currentVersionID, stored.databaseID), includeSolution)
	if err != nil {
		return Problem{}, databaseFailure("load current OJ problem version", err)
	}
	if versionID != *stored.currentVersionID || version.Number != stored.HeadRevision {
		return Problem{}, ojError(ErrorStoredDataInvalid, true, "load OJ problem", errors.New("OJ problem head and version disagree"))
	}
	stored.CurrentVersion = &version
	return stored.Problem, nil
}

const problemVersionSelect = `
SELECT version.oj_problem_version_id,
       version.version_number,
       version.lifecycle,
       version.title,
       version.statement_markdown,
       version.solution_markdown,
       version.knowledge_tags,
       version.time_limit_ms,
       version.memory_limit_bytes,
       version.output_limit_bytes,
       version.problem_schema,
       version.problem_spec::text,
       artifact.sha256,
       artifact.size_bytes,
       artifact.media_type,
       artifact.storage_key,
       version.content_sha256,
       account.public_id::text,
       version.created_at
FROM ascendany.oj_problem_versions AS version
JOIN ascendany.artifacts AS artifact ON artifact.artifact_id = version.test_bundle_artifact_id
JOIN ascendany.auth_accounts AS account ON account.account_id = version.created_by_account_id
`

func scanProblemVersion(scanner interface{ Scan(...any) error }, includePrivate bool) (ProblemVersion, int64, error) {
	var version ProblemVersion
	var testBundle Artifact
	var versionID int64
	var lifecycle string
	var spec string
	if err := scanner.Scan(&versionID, &version.Number, &lifecycle, &version.Title, &version.StatementMarkdown,
		&version.SolutionMarkdown, &version.KnowledgeTags, &version.TimeLimitMS, &version.MemoryLimitBytes,
		&version.OutputLimitBytes, &version.ProblemSchema, &spec, &testBundle.SHA256,
		&testBundle.SizeBytes, &testBundle.MediaType, &testBundle.StorageKey,
		&version.ContentSHA256, &version.CreatedByAccountID, &version.CreatedAt); err != nil {
		return ProblemVersion{}, 0, err
	}
	version.Lifecycle = Lifecycle(lifecycle)
	version.CreatedAt = version.CreatedAt.UTC()
	canonical, digest, err := canonicaljson.Object(json.RawMessage(spec), 1<<20)
	if err != nil {
		return ProblemVersion{}, 0, errors.New("stored OJ problem spec is not canonical")
	}
	version.ProblemSpec = canonical
	version.ProblemSpecSHA256 = digest
	version.TestBundle = &testBundle
	if version.Number < 1 || (version.Lifecycle != LifecycleActive && version.Lifecycle != LifecycleArchived) ||
		version.ProblemSchema != ProblemSchemaV1 || !lowercaseSHA256.MatchString(version.ContentSHA256) ||
		validateArtifact(testBundle, TestBundleMediaType, 1<<40) != nil {
		return ProblemVersion{}, 0, errors.New("stored OJ problem version violates its contract")
	}
	recomputed := problemContentHash(CreateProblemVersionInput{
		Lifecycle: version.Lifecycle, Title: version.Title, StatementMarkdown: version.StatementMarkdown,
		SolutionMarkdown: version.SolutionMarkdown, KnowledgeTags: version.KnowledgeTags,
		TimeLimitMS: version.TimeLimitMS, MemoryLimitBytes: version.MemoryLimitBytes,
		OutputLimitBytes: version.OutputLimitBytes, ProblemSpec: version.ProblemSpec,
		TestBundle: testBundle,
	}, version.ProblemSpecSHA256)
	if recomputed != version.ContentSHA256 {
		return ProblemVersion{}, 0, errors.New("stored OJ problem content hash is inconsistent")
	}
	if !includePrivate {
		version.SolutionMarkdown = nil
		version.ProblemSpec = nil
		version.ProblemSpecSHA256 = ""
		version.TestBundle = nil
	}
	return version, versionID, nil
}
