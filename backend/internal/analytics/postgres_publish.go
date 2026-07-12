package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const maxPersistedErrorDetailBytes = 4096

type currentAnalyticsState struct {
	generationID *int64
	revision     int64
	snapshots    []ManifestSnapshot
	target       ManifestTarget
}

func (repository *PostgresRepository) Publish(
	ctx context.Context,
	claim Claim,
	result Result,
) (published PublishResult, resultErr error) {
	if err := validateResult(result); err != nil {
		return PublishResult{}, err
	}
	resultErr = repository.transaction(ctx, "publish analytics generation", pgx.TxOptions{}, func(tx analyticsTx) error {
		manifest, err := lockClaimedGeneration(ctx, tx, claim)
		if err != nil {
			return err
		}
		if err := validateResultSnapshots(result, manifest.Value.Snapshots); err != nil {
			return err
		}
		state, err := lockCurrentAnalyticsState(ctx, tx, claim.TargetExamID)
		if err != nil {
			return err
		}
		winner := sameOptionalInt64(state.generationID, manifest.Value.BaseAnalyticsGenerationID) &&
			state.revision == manifest.Value.BaseHeadRevision &&
			state.target.SnapshotID == manifest.Value.Target.SnapshotID &&
			state.target.ExamHeadRevision == manifest.Value.Target.ExamHeadRevision &&
			slices.Equal(state.snapshots, manifest.Value.Snapshots)
		if winner {
			if err := publishWinner(ctx, tx, claim, state, result); err != nil {
				return err
			}
			published = PublishResult{Disposition: PublishSucceeded}
			return nil
		}
		replacementID, err := publishReplacement(ctx, tx, claim, state)
		if err != nil {
			return err
		}
		published = PublishResult{Disposition: PublishSuperseded, ReplacementGenerationID: &replacementID}
		return nil
	})
	return published, resultErr
}

func lockClaimedGeneration(ctx context.Context, tx analyticsTx, claim Claim) (ParsedManifest, error) {
	record := generationRecord{}
	err := tx.QueryRow(ctx, `
SELECT base_analytics_generation_id,
       base_head_revision,
       target_exam_id,
       target_snapshot_id,
       target_exam_head_revision,
       input_manifest::text,
       input_manifest_sha256,
       algorithm_version,
       config_sha256
FROM ascendany.analytics_generations
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND lease_owner = $2
  AND attempt_count = $3
  AND lease_expires_at > clock_timestamp()
FOR UPDATE`, claim.GenerationID, claim.LeaseOwner, claim.AttemptCount).Scan(
		&record.BaseAnalyticsGenerationID,
		&record.BaseHeadRevision,
		&record.TargetExamID,
		&record.TargetSnapshotID,
		&record.TargetExamHeadRevision,
		&record.ManifestJSON,
		&record.ManifestSHA256,
		&record.AlgorithmVersion,
		&record.ConfigSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ParsedManifest{}, analyticsError(ErrorLeaseLost, false, "lock claimed generation", errors.New("analytics lease is no longer active"))
	}
	if err != nil {
		return ParsedManifest{}, databaseError("lock claimed generation", err)
	}
	if !sameOptionalInt64(record.BaseAnalyticsGenerationID, claim.BaseAnalyticsGenerationID) ||
		record.BaseHeadRevision != claim.BaseHeadRevision ||
		record.TargetExamID != claim.TargetExamID ||
		record.TargetSnapshotID != claim.TargetSnapshotID ||
		record.TargetExamHeadRevision != claim.TargetExamHeadRevision ||
		record.ManifestSHA256 != claim.ManifestSHA256 ||
		record.AlgorithmVersion != claim.AlgorithmVersion ||
		record.ConfigSHA256 != claim.ConfigSHA256 {
		return ParsedManifest{}, analyticsError(ErrorStateConflict, false, "lock claimed generation", errors.New("claimed columns changed"))
	}
	manifest, err := ParseManifest(record.ManifestJSON)
	if err != nil {
		return ParsedManifest{}, err
	}
	if manifest.SHA256 != record.ManifestSHA256 ||
		!sameOptionalInt64(manifest.Value.BaseAnalyticsGenerationID, record.BaseAnalyticsGenerationID) ||
		manifest.Value.BaseHeadRevision != record.BaseHeadRevision ||
		manifest.Value.Target.ExamID != record.TargetExamID ||
		manifest.Value.Target.SnapshotID != record.TargetSnapshotID ||
		manifest.Value.Target.ExamHeadRevision != record.TargetExamHeadRevision {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "lock claimed generation", errors.New("manifest differs from generation columns or hash"))
	}
	return manifest, nil
}

func lockCurrentAnalyticsState(ctx context.Context, tx analyticsTx, targetExamID int64) (currentAnalyticsState, error) {
	state := currentAnalyticsState{}
	if err := tx.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton
FOR UPDATE`).Scan(&state.generationID, &state.revision); err != nil {
		return currentAnalyticsState{}, databaseError("lock analytics head", err)
	}
	rows, err := tx.Query(ctx, `
SELECT exam.exam_id,
       snapshot.snapshot_id,
       snapshot.domain_hash,
       exam.head_revision
FROM ascendany.logical_exams AS exam
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = exam.active_snapshot_id
WHERE exam.active_snapshot_id IS NOT NULL
ORDER BY exam.exam_id`)
	if err != nil {
		return currentAnalyticsState{}, databaseError("load current analytics snapshots", err)
	}
	defer rows.Close()
	targetFound := false
	for rows.Next() {
		var entry ManifestSnapshot
		var headRevision int64
		if err := rows.Scan(&entry.ExamID, &entry.SnapshotID, &entry.DomainHash, &headRevision); err != nil {
			return currentAnalyticsState{}, databaseError("scan current analytics snapshot", err)
		}
		state.snapshots = append(state.snapshots, entry)
		if entry.ExamID == targetExamID {
			state.target = ManifestTarget{ExamID: entry.ExamID, SnapshotID: entry.SnapshotID, ExamHeadRevision: headRevision}
			targetFound = true
		}
	}
	if err := rows.Err(); err != nil {
		return currentAnalyticsState{}, databaseError("iterate current analytics snapshots", err)
	}
	if !targetFound {
		return currentAnalyticsState{}, analyticsError(ErrorStateConflict, false, "load current analytics snapshots", fmt.Errorf("target exam %d has no active snapshot", targetExamID))
	}
	if _, err := CanonicalManifest(Manifest{
		Protocol:                  ManifestProtocolV1,
		BaseAnalyticsGenerationID: state.generationID,
		BaseHeadRevision:          state.revision,
		Target:                    state.target,
		Snapshots:                 state.snapshots,
	}); err != nil {
		return currentAnalyticsState{}, analyticsError(ErrorStateConflict, false, "validate current analytics state", err)
	}
	return state, nil
}

func publishWinner(
	ctx context.Context,
	tx analyticsTx,
	claim Claim,
	state currentAnalyticsState,
	result Result,
) error {
	if state.revision == math.MaxInt64 {
		return analyticsError(ErrorStateConflict, false, "advance analytics head", errors.New("analytics head revision is exhausted"))
	}
	for _, student := range result.Students {
		metrics, err := json.Marshal(student.Metrics)
		if err != nil {
			return analyticsError(ErrorInvalidDataset, true, "encode student analytics", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.student_analytics (
    analytics_generation_id,
    actor_id,
    rating,
    metrics
)
VALUES ($1, $2, $3, $4::jsonb)`, claim.GenerationID, student.ActorID, student.Rating, string(metrics)); err != nil {
			return databaseError("insert student analytics", err)
		}
	}
	for _, problem := range result.Problems {
		metrics, err := json.Marshal(problem.Metrics)
		if err != nil {
			return analyticsError(ErrorInvalidDataset, true, "encode problem analytics", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.problem_analytics (
    analytics_generation_id,
    snapshot_id,
    problem_set_problem_id,
    metrics
)
VALUES ($1, $2, $3, $4::jsonb)`, claim.GenerationID, problem.SnapshotID, problem.ProblemSetProblemID, string(metrics)); err != nil {
			return databaseError("insert problem analytics", err)
		}
	}
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'succeeded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = NULL,
    error_detail = NULL,
    finished_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND lease_owner = $2
  AND attempt_count = $3
  AND lease_expires_at > clock_timestamp()`, claim.GenerationID, claim.LeaseOwner, claim.AttemptCount)
	if err != nil {
		return databaseError("mark analytics generation succeeded", err)
	}
	if commandTag.RowsAffected() != 1 {
		return analyticsError(ErrorLeaseLost, false, "mark analytics generation succeeded", errors.New("analytics lease changed"))
	}
	if err := appendGenerationEvent(ctx, tx, claim.GenerationID, "succeeded", map[string]any{
		"headRevision": state.revision + 1,
		"problemCount": len(result.Problems),
		"studentCount": len(result.Students),
	}); err != nil {
		return err
	}
	commandTag, err = tx.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $1,
    head_revision = $2,
    updated_at = clock_timestamp()
WHERE singleton
  AND current_generation_id IS NOT DISTINCT FROM $3
  AND head_revision = $4`, claim.GenerationID, state.revision+1, state.generationID, state.revision)
	if err != nil {
		return databaseError("advance analytics head", err)
	}
	if commandTag.RowsAffected() != 1 {
		return analyticsError(ErrorStateConflict, false, "advance analytics head", errors.New("analytics head changed while locked"))
	}
	return completeTargetImportJob(ctx, tx, claim)
}

func completeTargetImportJob(ctx context.Context, tx analyticsTx, claim Claim) error {
	jobID, err := lockTargetImportJob(ctx, tx, claim.TargetExamID, claim.TargetSnapshotID, "complete target import job")
	if err != nil {
		return err
	}
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'succeeded',
    stage = 'completed',
    snapshot_id = $2,
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = NULL,
    error_detail = NULL,
    error_permanent = NULL,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'analyzing'
  AND snapshot_id IS NULL`, jobID, claim.TargetSnapshotID)
	if err != nil {
		return databaseError("complete target import job", err)
	}
	if commandTag.RowsAffected() != 1 {
		return analyticsError(ErrorStateConflict, false, "complete target import job", errors.New("target import job is not analyzing"))
	}
	return appendImportEvent(ctx, tx, jobID, "completed", struct {
		AnalyticsGenerationID int64 `json:"analyticsGenerationId"`
		SnapshotID            int64 `json:"snapshotId"`
	}{AnalyticsGenerationID: claim.GenerationID, SnapshotID: claim.TargetSnapshotID})
}

func publishReplacement(
	ctx context.Context,
	tx analyticsTx,
	claim Claim,
	state currentAnalyticsState,
) (int64, error) {
	replacement, err := CanonicalManifest(Manifest{
		Protocol:                  ManifestProtocolV1,
		BaseAnalyticsGenerationID: state.generationID,
		BaseHeadRevision:          state.revision,
		Target:                    state.target,
		Snapshots:                 state.snapshots,
	})
	if err != nil {
		return 0, analyticsError(ErrorStateConflict, false, "build replacement generation", err)
	}
	replacementID, err := ensureReplacementGeneration(ctx, tx, claim, replacement)
	if err != nil {
		return 0, err
	}
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'superseded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = NULL,
    error_detail = NULL,
    finished_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND lease_owner = $2
  AND attempt_count = $3
  AND lease_expires_at > clock_timestamp()`, claim.GenerationID, claim.LeaseOwner, claim.AttemptCount)
	if err != nil {
		return 0, databaseError("mark analytics generation superseded", err)
	}
	if commandTag.RowsAffected() != 1 {
		return 0, analyticsError(ErrorLeaseLost, false, "mark analytics generation superseded", errors.New("analytics lease changed"))
	}
	if err := appendGenerationEvent(ctx, tx, claim.GenerationID, "superseded", map[string]any{
		"replacementGenerationId": replacementID,
	}); err != nil {
		return 0, err
	}
	if state.target.SnapshotID != claim.TargetSnapshotID {
		if err := supersedeTargetImportJob(ctx, tx, claim, replacementID, state.target.SnapshotID); err != nil {
			return 0, err
		}
	}
	return replacementID, nil
}

func ensureReplacementGeneration(
	ctx context.Context,
	tx analyticsTx,
	claim Claim,
	manifest ParsedManifest,
) (int64, error) {
	var generationID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.analytics_generations (
    status,
    base_analytics_generation_id,
    base_head_revision,
    target_exam_id,
    target_snapshot_id,
    target_exam_head_revision,
    input_manifest,
    input_manifest_sha256,
    algorithm_version,
    config_sha256
)
VALUES ('queued', $1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
ON CONFLICT (
    target_snapshot_id,
    input_manifest_sha256,
    algorithm_version,
    config_sha256
) DO NOTHING
RETURNING analytics_generation_id`,
		manifest.Value.BaseAnalyticsGenerationID,
		manifest.Value.BaseHeadRevision,
		manifest.Value.Target.ExamID,
		manifest.Value.Target.SnapshotID,
		manifest.Value.Target.ExamHeadRevision,
		string(manifest.Canonical),
		manifest.SHA256,
		claim.AlgorithmVersion,
		claim.ConfigSHA256,
	).Scan(&generationID)
	created := true
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		err = tx.QueryRow(ctx, `
SELECT analytics_generation_id
FROM ascendany.analytics_generations
WHERE target_snapshot_id = $1
  AND input_manifest_sha256 = $2
  AND algorithm_version = $3
  AND config_sha256 = $4`, manifest.Value.Target.SnapshotID, manifest.SHA256, claim.AlgorithmVersion, claim.ConfigSHA256).Scan(&generationID)
	}
	if err != nil {
		return 0, databaseError("insert or reuse replacement generation", err)
	}
	if !created {
		return generationID, nil
	}
	for _, entry := range manifest.Value.Snapshots {
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id,
    exam_id,
    snapshot_id,
    domain_hash
)
VALUES ($1, $2, $3, $4)`, generationID, entry.ExamID, entry.SnapshotID, entry.DomainHash); err != nil {
			return 0, databaseError("insert replacement generation snapshot", err)
		}
	}
	if err := appendGenerationEvent(ctx, tx, generationID, "queued", map[string]any{
		"attemptCount": 0,
	}); err != nil {
		return 0, err
	}
	return generationID, nil
}

func supersedeTargetImportJob(ctx context.Context, tx analyticsTx, claim Claim, replacementID, replacementSnapshotID int64) error {
	jobID, err := lockTargetImportJob(ctx, tx, claim.TargetExamID, claim.TargetSnapshotID, "supersede target import job")
	if err != nil {
		return err
	}
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'superseded',
    stage = 'superseded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = NULL,
    error_detail = NULL,
    error_permanent = NULL,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'analyzing'
  AND snapshot_id IS NULL`, jobID)
	if err != nil {
		return databaseError("supersede target import job", err)
	}
	if commandTag.RowsAffected() != 1 {
		return analyticsError(ErrorStateConflict, false, "supersede target import job", errors.New("target import job is not analyzing"))
	}
	return appendImportEvent(ctx, tx, jobID, "superseded", struct {
		AnalyticsGenerationID   int64 `json:"analyticsGenerationId"`
		ReplacementGenerationID int64 `json:"replacementGenerationId"`
		ReplacementSnapshotID   int64 `json:"replacementSnapshotId"`
	}{
		AnalyticsGenerationID:   claim.GenerationID,
		ReplacementGenerationID: replacementID,
		ReplacementSnapshotID:   replacementSnapshotID,
	})
}

func (repository *PostgresRepository) FailPermanent(
	ctx context.Context,
	claim Claim,
	code ErrorCode,
	detail string,
) (resultErr error) {
	if !permanentFailureCode(code) {
		return analyticsError(ErrorStateConflict, false, "fail analytics generation", fmt.Errorf("error code %q is not permanent", code))
	}
	detail = truncateErrorDetail(detail)
	return repository.transaction(ctx, "fail analytics generation", pgx.TxOptions{}, func(tx analyticsTx) error {
		if _, err := lockClaimedGeneration(ctx, tx, claim); err != nil {
			return err
		}
		jobID, err := lockTargetImportJob(ctx, tx, claim.TargetExamID, claim.TargetSnapshotID, "fail target import job")
		if err != nil {
			return err
		}
		commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'failed',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $4,
    error_detail = $5,
    finished_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND lease_owner = $2
  AND attempt_count = $3
  AND lease_expires_at > clock_timestamp()`, claim.GenerationID, claim.LeaseOwner, claim.AttemptCount, code, detail)
		if err != nil {
			return databaseError("mark analytics generation failed", err)
		}
		if commandTag.RowsAffected() != 1 {
			return analyticsError(ErrorLeaseLost, false, "mark analytics generation failed", errors.New("analytics lease changed"))
		}
		if err := appendGenerationEvent(ctx, tx, claim.GenerationID, "failed", map[string]any{
			"code":      code,
			"permanent": true,
		}); err != nil {
			return err
		}
		commandTag, err = tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'failed',
    stage = 'failed',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $2,
    error_detail = $3,
    error_permanent = true,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'analyzing'
  AND snapshot_id IS NULL`, jobID, code, detail)
		if err != nil {
			return databaseError("fail target import job", err)
		}
		if commandTag.RowsAffected() != 1 {
			return analyticsError(ErrorStateConflict, false, "fail target import job", errors.New("target import job is not analyzing"))
		}
		return appendImportEvent(ctx, tx, jobID, "failed", struct {
			AnalyticsGenerationID int64     `json:"analyticsGenerationId"`
			Code                  ErrorCode `json:"code"`
			Permanent             bool      `json:"permanent"`
		}{AnalyticsGenerationID: claim.GenerationID, Code: code, Permanent: true})
	})
}

func lockTargetImportJob(
	ctx context.Context,
	tx analyticsTx,
	examID int64,
	snapshotID int64,
	operation string,
) (int64, error) {
	var jobID int64
	err := tx.QueryRow(ctx, `
SELECT job.import_job_id
FROM ascendany.exam_snapshots AS snapshot
JOIN ascendany.import_jobs AS job
  ON job.import_job_id = snapshot.import_job_id
WHERE snapshot.exam_id = $1
  AND snapshot.snapshot_id = $2
FOR UPDATE OF job`, examID, snapshotID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, analyticsError(ErrorStateConflict, false, operation, errors.New("target import job does not exist"))
	}
	if err != nil {
		return 0, databaseError(operation, err)
	}
	return jobID, nil
}

func appendImportEvent(ctx context.Context, tx analyticsTx, jobID int64, eventType string, payload any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return analyticsError(ErrorStateConflict, false, "encode import event", err)
	}
	if len(payloadJSON) == 0 || payloadJSON[0] != '{' {
		return analyticsError(ErrorStateConflict, false, "encode import event", errors.New("event payload must be an object"))
	}
	var previousSequence int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(event_sequence), 0)
FROM ascendany.import_job_events
WHERE import_job_id = $1`, jobID).Scan(&previousSequence); err != nil {
		return databaseError("load import event sequence", err)
	}
	if previousSequence == math.MaxInt64 {
		return analyticsError(ErrorStateConflict, false, "append import event", errors.New("import event sequence is exhausted"))
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.import_job_events (
    import_job_id,
    event_sequence,
    event_type,
    payload
)
VALUES ($1, $2, $3, $4::jsonb)`, jobID, previousSequence+1, eventType, string(payloadJSON)); err != nil {
		return databaseError("append import event", err)
	}
	return nil
}

func permanentFailureCode(code ErrorCode) bool {
	switch code {
	case ErrorInvalidConfiguration, ErrorInvalidManifest, ErrorAlgorithmMismatch, ErrorConfigMismatch, ErrorInvalidDataset:
		return true
	case ErrorLeaseLost, ErrorStateConflict, ErrorDatabase, ErrorCanceled:
		return false
	default:
		return false
	}
}

func truncateErrorDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "analytics generation failed"
	}
	if len(detail) <= maxPersistedErrorDetailBytes {
		return detail
	}
	truncated := detail[:maxPersistedErrorDetailBytes]
	for !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
