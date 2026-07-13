package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

type analyticsState struct {
	GenerationID        *int64
	HeadRevision        int64
	InputManifestSHA256 string
}

type analyticsGenerationState struct {
	Status                    string
	BaseAnalyticsGenerationID *int64
	BaseHeadRevision          int64
	TargetExamID              int64
	TargetSnapshotID          int64
	TargetExamHeadRevision    int64
	InputManifest             string
	InputManifestSHA256       string
}

func loadAnalyticsState(ctx context.Context, tx recommendationQuery) (analyticsState, error) {
	return readAnalyticsState(ctx, tx, false)
}

func lockAnalyticsState(ctx context.Context, tx recommendationQuery) (analyticsState, error) {
	return readAnalyticsState(ctx, tx, true)
}

func readAnalyticsState(ctx context.Context, tx recommendationQuery, lock bool) (analyticsState, error) {
	var state analyticsState
	query := `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton`
	if lock {
		query += ` FOR SHARE`
	}
	if err := tx.QueryRow(ctx, query).Scan(&state.GenerationID, &state.HeadRevision); errors.Is(err, pgx.ErrNoRows) {
		return analyticsState{}, domainError(ErrorStoredDataInvalid, true, "load recommendation analytics head", errors.New("analytics head singleton is missing"))
	} else if err != nil {
		return analyticsState{}, databaseError("load recommendation analytics head", err)
	}
	if state.GenerationID == nil {
		if state.HeadRevision != 0 {
			return analyticsState{}, domainError(ErrorStoredDataInvalid, true, "validate recommendation analytics head", errors.New("empty analytics head has a nonzero revision"))
		}
		return state, nil
	}
	if *state.GenerationID <= 0 || state.HeadRevision <= 0 {
		return analyticsState{}, domainError(ErrorStoredDataInvalid, true, "validate recommendation analytics head", errors.New("published analytics identity is invalid"))
	}
	var generation analyticsGenerationState
	if err := tx.QueryRow(ctx, `
SELECT status,
       base_analytics_generation_id,
       base_head_revision,
       target_exam_id,
       target_snapshot_id,
       target_exam_head_revision,
       input_manifest::text,
       input_manifest_sha256
FROM ascendany.analytics_generations
WHERE analytics_generation_id = $1`, *state.GenerationID).Scan(
		&generation.Status,
		&generation.BaseAnalyticsGenerationID,
		&generation.BaseHeadRevision,
		&generation.TargetExamID,
		&generation.TargetSnapshotID,
		&generation.TargetExamHeadRevision,
		&generation.InputManifest,
		&generation.InputManifestSHA256,
	); errors.Is(err, pgx.ErrNoRows) {
		return analyticsState{}, domainError(ErrorStoredDataInvalid, true, "load recommendation analytics generation", errors.New("analytics head target is missing"))
	} else if err != nil {
		return analyticsState{}, databaseError("load recommendation analytics generation", err)
	}
	if err := validateAnalyticsGenerationState(state, generation); err != nil {
		return analyticsState{}, domainError(ErrorStoredDataInvalid, true, "validate recommendation analytics generation", err)
	}
	state.InputManifestSHA256 = generation.InputManifestSHA256
	return state, nil
}

func validateAnalyticsGenerationState(head analyticsState, generation analyticsGenerationState) error {
	if head.GenerationID == nil || *head.GenerationID <= 0 || head.HeadRevision <= 0 ||
		generation.Status != "succeeded" || !lowercaseSHA256Pattern.MatchString(generation.InputManifestSHA256) {
		return errors.New("analytics head target is not a succeeded canonical generation")
	}
	if generation.BaseHeadRevision != head.HeadRevision-1 ||
		(generation.BaseAnalyticsGenerationID == nil && generation.BaseHeadRevision != 0) ||
		(generation.BaseAnalyticsGenerationID != nil && (*generation.BaseAnalyticsGenerationID <= 0 || generation.BaseHeadRevision <= 0)) ||
		generation.TargetExamID <= 0 || generation.TargetSnapshotID <= 0 || generation.TargetExamHeadRevision <= 0 {
		return errors.New("analytics head and generation scalar columns are inconsistent")
	}
	manifest, err := analytics.ParseManifest([]byte(generation.InputManifest))
	if err != nil {
		return fmt.Errorf("parse analytics input manifest: %w", err)
	}
	if manifest.SHA256 != generation.InputManifestSHA256 {
		return errors.New("analytics input manifest SHA-256 differs")
	}
	if !sameOptionalAnalyticsGenerationID(manifest.Value.BaseAnalyticsGenerationID, generation.BaseAnalyticsGenerationID) ||
		manifest.Value.BaseHeadRevision != generation.BaseHeadRevision ||
		manifest.Value.Target.ExamID != generation.TargetExamID ||
		manifest.Value.Target.SnapshotID != generation.TargetSnapshotID ||
		manifest.Value.Target.ExamHeadRevision != generation.TargetExamHeadRevision {
		return errors.New("analytics input manifest differs from generation scalar columns")
	}
	return nil
}

func sameOptionalAnalyticsGenerationID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type catalogState struct {
	Available bool
	Catalog   knowledgeCatalog
	Digest    string
}

func loadActiveCatalog(ctx context.Context, tx recommendationTx) (catalogState, error) {
	var key, itemKind, versionKind, schemaID, documentText, documentSHA256 string
	var credentialRef *string
	var headRevision, versionID, versionNumber int64
	err := tx.QueryRow(ctx, `
SELECT item.configuration_key,
       item.configuration_kind,
       item.head_revision,
       version.configuration_version_id,
       version.configuration_kind,
       version.version_number,
       version.schema_id,
       version.document::text,
       version.document_sha256,
       version.credential_ref
FROM ascendany.configuration_items AS item
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = item.active_version_id
 AND version.configuration_item_id = item.configuration_item_id
 AND version.configuration_kind = item.configuration_kind
WHERE item.configuration_key = $1`, configuration.KnowledgeCatalogKey).Scan(
		&key, &itemKind, &headRevision, &versionID, &versionKind, &versionNumber,
		&schemaID, &documentText, &documentSHA256, &credentialRef,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalogState{}, nil
	}
	if err != nil {
		return catalogState{}, databaseError("query active recommendation knowledge catalog", err)
	}
	if key != configuration.KnowledgeCatalogKey || itemKind != string(configuration.KindKnowledgeCatalog) ||
		versionKind != itemKind || headRevision <= 0 || versionID <= 0 || versionNumber != headRevision ||
		schemaID != KnowledgeCatalogSchemaV1 || credentialRef != nil || !lowercaseSHA256Pattern.MatchString(documentSHA256) {
		return catalogState{}, domainError(ErrorStoredDataInvalid, true, "validate active recommendation knowledge catalog", errors.New("catalog provenance is invalid"))
	}
	catalog, _, digest, err := parseKnowledgeCatalog(json.RawMessage(documentText))
	if err != nil {
		return catalogState{}, domainError(ErrorStoredDataInvalid, true, "validate active recommendation knowledge catalog", fmt.Errorf("catalog document is invalid: %w", err))
	}
	if digest != documentSHA256 {
		return catalogState{}, domainError(ErrorStoredDataInvalid, true, "validate active recommendation knowledge catalog", errors.New("catalog document digest does not match its provenance"))
	}
	return catalogState{Available: true, Catalog: catalog, Digest: digest}, nil
}

type studentState struct {
	Available   bool
	Rating      string
	MetricsJSON json.RawMessage
}

func loadStudentState(ctx context.Context, tx recommendationTx, generationID, actorID int64) (studentState, error) {
	var state studentState
	var metricsText string
	err := tx.QueryRow(ctx, `
SELECT rating::text, metrics::text
FROM ascendany.student_analytics
WHERE analytics_generation_id = $1
  AND actor_id = $2`, generationID, actorID).Scan(&state.Rating, &metricsText)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return studentState{}, databaseError("load current student recommendation analytics", err)
	}
	state.Available = true
	state.MetricsJSON = json.RawMessage(metricsText)
	return state, nil
}

func queryProblemRows(ctx context.Context, tx recommendationQuery, generationID int64, withMetrics bool) ([]problemRow, error) {
	metricsJoin := ""
	metricsColumn := "NULL::text"
	if withMetrics {
		metricsJoin = `
LEFT JOIN ascendany.problem_analytics AS analytics
  ON analytics.analytics_generation_id = generation_snapshot.analytics_generation_id
 AND analytics.snapshot_id = generation_snapshot.snapshot_id
 AND analytics.problem_set_problem_id = problem.problem_set_problem_id`
		metricsColumn = "analytics.metrics::text"
	}
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
       problem.memory_limit_bytes,
       `+metricsColumn+`
FROM ascendany.analytics_generation_snapshots AS generation_snapshot
JOIN ascendany.logical_exams AS exam
  ON exam.exam_id = generation_snapshot.exam_id
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = generation_snapshot.snapshot_id
 AND snapshot.exam_id = generation_snapshot.exam_id
JOIN ascendany.pintia_snapshot_problems AS problem
  ON problem.snapshot_id = generation_snapshot.snapshot_id`+metricsJoin+`
WHERE generation_snapshot.analytics_generation_id = $1
ORDER BY generation_snapshot.snapshot_id, problem.problem_set_problem_id`, generationID)
	if err != nil {
		return nil, databaseError("query current recommendation problems", err)
	}
	defer rows.Close()
	result := make([]problemRow, 0)
	for rows.Next() {
		var value problemRow
		var metricsText *string
		if err := rows.Scan(
			&value.SnapshotID, &value.ProblemSetID, &value.ProblemSetProblemID, &value.SourceURL,
			&value.Platform, &value.ProblemID, &value.Title, &value.ContentHTML, &value.MaxScore,
			&value.TimeLimitMS, &value.MemoryLimitBytes, &metricsText,
		); err != nil {
			return nil, databaseError("scan current recommendation problem", err)
		}
		if withMetrics {
			if metricsText == nil {
				return nil, domainError(ErrorStoredDataInvalid, true, "load current recommendation problems", errors.New("analytics generation lacks problem metrics"))
			}
			value.MetricsJSON = json.RawMessage(*metricsText)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate current recommendation problems", err)
	}
	if len(result) < 1 || len(result) > maximumProblems {
		return nil, domainError(ErrorStoredDataInvalid, true, "load current recommendation problems", errors.New("problem count is outside the runtime contract"))
	}
	return result, nil
}

type observationRow struct {
	Problem                 problemRow
	Score                   *string
	Passed                  *bool
	RankingValidCount       *int64
	ExportedSubmissionCount int64
}

func queryObservations(ctx context.Context, tx recommendationTx, generationID, actorID int64) ([]observationRow, error) {
	rows, err := tx.Query(ctx, `
SELECT result.snapshot_id,
       exam.source_exam_id,
       result.problem_set_problem_id,
       snapshot.source_url,
       exam.platform,
       problem.problem_id,
       problem.title,
       problem.content_html,
       problem.max_score::text,
       problem.time_limit_ms,
       problem.memory_limit_bytes,
       result.score::text,
       result.passed,
       result.valid_submission_count,
       count(submission.submission_identity_id)::bigint
FROM ascendany.analytics_generation_snapshots AS generation_snapshot
JOIN ascendany.logical_exams AS exam
  ON exam.exam_id = generation_snapshot.exam_id
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = generation_snapshot.snapshot_id
 AND snapshot.exam_id = generation_snapshot.exam_id
JOIN ascendany.pintia_ranking_problem_results AS result
  ON result.snapshot_id = generation_snapshot.snapshot_id
 AND result.actor_id = $2
JOIN ascendany.pintia_snapshot_problems AS problem
  ON problem.snapshot_id = result.snapshot_id
 AND problem.problem_set_problem_id = result.problem_set_problem_id
LEFT JOIN ascendany.pintia_snapshot_submissions AS submission
  ON submission.snapshot_id = result.snapshot_id
 AND submission.actor_id = result.actor_id
 AND submission.problem_set_problem_id = result.problem_set_problem_id
WHERE generation_snapshot.analytics_generation_id = $1
GROUP BY result.snapshot_id, exam.source_exam_id, result.problem_set_problem_id, snapshot.source_url,
         exam.platform, problem.problem_id, problem.title, problem.content_html, problem.max_score,
         problem.time_limit_ms, problem.memory_limit_bytes, result.score, result.passed, result.valid_submission_count
ORDER BY result.snapshot_id, result.problem_set_problem_id`, generationID, actorID)
	if err != nil {
		return nil, databaseError("query current recommendation observations", err)
	}
	defer rows.Close()
	result := make([]observationRow, 0)
	for rows.Next() {
		var value observationRow
		if err := rows.Scan(
			&value.Problem.SnapshotID, &value.Problem.ProblemSetID, &value.Problem.ProblemSetProblemID,
			&value.Problem.SourceURL, &value.Problem.Platform, &value.Problem.ProblemID, &value.Problem.Title,
			&value.Problem.ContentHTML, &value.Problem.MaxScore, &value.Problem.TimeLimitMS,
			&value.Problem.MemoryLimitBytes, &value.Score, &value.Passed, &value.RankingValidCount, &value.ExportedSubmissionCount,
		); err != nil {
			return nil, databaseError("scan current recommendation observation", err)
		}
		if err := validateObservationCounts(value.RankingValidCount, value.ExportedSubmissionCount); err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "validate current recommendation observations", err)
		}
		if value.Score != nil {
			if _, _, err := nonnegativeFiniteNumber(*value.Score, "observation score"); err != nil {
				return nil, domainError(ErrorStoredDataInvalid, true, "validate current recommendation observations", err)
			}
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate current recommendation observations", err)
	}
	return result, nil
}

func validateObservationCounts(rankingValidCount *int64, exportedSubmissionCount int64) error {
	if exportedSubmissionCount < 0 || rankingValidCount != nil && *rankingValidCount < 0 {
		return errors.New("observation counts must be nonnegative")
	}
	return nil
}

func analyticsIDString(value int64) string { return strconv.FormatInt(value, 10) }
