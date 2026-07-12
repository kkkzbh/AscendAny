package importing

import (
	"context"
	"errors"
	"fmt"
)

func enqueueAnalytics(
	ctx context.Context,
	tx dbTx,
	request ImportRequest,
	targetExamID int64,
	targetSnapshotID int64,
	targetRevision int64,
) (int64, error) {
	var baseGenerationID *int64
	var baseHeadRevision int64
	if err := tx.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton
FOR UPDATE`).Scan(&baseGenerationID, &baseHeadRevision); err != nil {
		return 0, databaseError("lock analytics head", err)
	}

	var examIDs []int64
	var snapshotIDs []int64
	var domainHashes []string
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(array_agg(exam.exam_id ORDER BY exam.exam_id), '{}'::bigint[]),
       COALESCE(array_agg(snapshot.snapshot_id ORDER BY exam.exam_id), '{}'::bigint[]),
       COALESCE(array_agg(snapshot.domain_hash ORDER BY exam.exam_id), '{}'::text[])
FROM ascendany.logical_exams AS exam
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = exam.active_snapshot_id
WHERE exam.active_snapshot_id IS NOT NULL`).Scan(&examIDs, &snapshotIDs, &domainHashes); err != nil {
		return 0, databaseError("load analytics input snapshots", err)
	}
	if len(examIDs) != len(snapshotIDs) || len(examIDs) != len(domainHashes) {
		return 0, importError(ErrorManifest, false, "build analytics manifest", errors.New("snapshot input arrays differ in length"))
	}
	entries := make([]AnalyticsManifestEntryV1, len(examIDs))
	for index := range examIDs {
		entries[index] = AnalyticsManifestEntryV1{
			ExamID:     examIDs[index],
			SnapshotID: snapshotIDs[index],
			DomainHash: domainHashes[index],
		}
	}
	manifest := AnalyticsManifestV1{
		Protocol:                  AnalyticsManifestProtocolV1,
		BaseAnalyticsGenerationID: baseGenerationID,
		BaseHeadRevision:          baseHeadRevision,
		Target: AnalyticsManifestTargetV1{
			ExamID:           targetExamID,
			SnapshotID:       targetSnapshotID,
			ExamHeadRevision: targetRevision,
		},
		Snapshots: entries,
	}
	manifestJSON, manifestHash, err := manifest.CanonicalJSON()
	if err != nil {
		return 0, err
	}

	var generationID int64
	err = tx.QueryRow(ctx, `
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
RETURNING analytics_generation_id`,
		baseGenerationID,
		baseHeadRevision,
		targetExamID,
		targetSnapshotID,
		targetRevision,
		string(manifestJSON),
		manifestHash,
		request.Analytics.AlgorithmVersion,
		request.Analytics.ConfigSHA256,
	).Scan(&generationID)
	if err != nil {
		return 0, databaseError("insert analytics generation", err)
	}
	for _, entry := range entries {
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id,
    exam_id,
    snapshot_id,
    domain_hash
)
VALUES ($1, $2, $3, $4)`, generationID, entry.ExamID, entry.SnapshotID, entry.DomainHash); err != nil {
			return 0, databaseError("insert analytics generation snapshot", err)
		}
	}
	if len(entries) == 0 {
		return 0, importError(ErrorManifest, false, "insert analytics generation snapshots", fmt.Errorf("manifest has no snapshots"))
	}
	payload, err := canonicalEventPayload(struct {
		AttemptCount int `json:"attemptCount"`
	}{AttemptCount: 0})
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_events (
    analytics_generation_id,
    event_sequence,
    event_type,
    payload
)
VALUES ($1, 1, 'queued', $2::jsonb)`, generationID, payload); err != nil {
		return 0, databaseError("append queued analytics generation event", err)
	}
	return generationID, nil
}
