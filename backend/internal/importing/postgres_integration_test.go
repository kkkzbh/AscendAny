package importing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func TestPostgresPintiaImportVertical(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("test database ping: %v", err)
	}
	var dispatchable int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.import_jobs
WHERE status = 'queued'
   OR (status = 'running' AND lease_expires_at <= clock_timestamp())`).Scan(&dispatchable); err != nil {
		t.Fatalf("inspect test queue: %v", err)
	}
	if dispatchable != 0 {
		t.Skipf("test database has %d unrelated dispatchable jobs", dispatchable)
	}

	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool)
	if err != nil {
		t.Fatal(err)
	}
	postgresRepository, ok := service.repository.(*PostgresRepository)
	if !ok {
		t.Fatalf("service repository = %T", service.repository)
	}
	worker, err := NewWorker(pool, store, WorkerConfig{
		LeaseDuration: time.Minute,
		RetryDelay:    time.Second,
		PintiaLimits:  pintia.DefaultLimits(),
		Analytics: AnalyticsConfig{
			AlgorithmVersion: "integration_analytics_v1",
			ConfigSHA256:     strings.Repeat("c", 64),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewPostgresReader(pool)
	if err != nil {
		t.Fatal(err)
	}

	base := integrationFixtureDocument(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	invalidDocument, invalidIDs := uniqueIntegrationSnapshot(t, base, suffix+"-invalid-decimal")
	invalidDocument["exam"].(map[string]any)["totalScore"] = json.Number("1e101")
	invalidQueue := queueIntegrationPayload(t, ctx, store, service, marshalIntegrationDocument(t, invalidDocument))
	invalidClaim, err := service.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || invalidClaim == nil || invalidClaim.Job.ID != invalidQueue.Job.ID {
		t.Fatalf("invalid claim = %#v, %v", invalidClaim, err)
	}
	invalidOutcome, err := worker.Process(ctx, *invalidClaim)
	if err != nil {
		t.Fatalf("process invalid Decimal snapshot: %v", err)
	}
	if invalidOutcome.Disposition != ImportFailed || invalidOutcome.FailureCode == nil ||
		*invalidOutcome.FailureCode != ErrorValidation {
		t.Fatalf("invalid Decimal outcome = %#v", invalidOutcome)
	}
	var invalidErrorPermanent bool
	if err := pool.QueryRow(ctx, `
SELECT error_permanent
FROM ascendany.import_jobs
WHERE import_job_id = $1`, invalidQueue.Job.ID).Scan(&invalidErrorPermanent); err != nil {
		t.Fatalf("read invalid Decimal job permanence: %v", err)
	}
	if !invalidErrorPermanent {
		t.Fatal("invalid Decimal job error_permanent = false, want true")
	}
	var invalidExamRows int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.logical_exams
WHERE platform = 'pintia' AND source_exam_id = $1`, invalidIDs.problemSetID).Scan(&invalidExamRows); err != nil {
		t.Fatal(err)
	}
	if invalidExamRows != 0 {
		t.Fatalf("invalid Decimal snapshot created %d logical exam/head rows", invalidExamRows)
	}
	var invalidSnapshotRows int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.exam_snapshots AS snapshot
JOIN ascendany.logical_exams AS exam ON exam.exam_id = snapshot.exam_id
WHERE exam.platform = 'pintia' AND exam.source_exam_id = $1`, invalidIDs.problemSetID).Scan(&invalidSnapshotRows); err != nil {
		t.Fatalf("count invalid Decimal snapshots: %v", err)
	}
	if invalidSnapshotRows != 0 {
		t.Fatalf("invalid Decimal import created %d exam snapshots", invalidSnapshotRows)
	}
	var invalidAnalyticsRows int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.analytics_generations AS generation
JOIN ascendany.logical_exams AS exam ON exam.exam_id = generation.target_exam_id
WHERE exam.platform = 'pintia' AND exam.source_exam_id = $1`, invalidIDs.problemSetID).Scan(&invalidAnalyticsRows); err != nil {
		t.Fatalf("count invalid Decimal analytics generations: %v", err)
	}
	if invalidAnalyticsRows != 0 {
		t.Fatalf("invalid Decimal import created %d analytics generations", invalidAnalyticsRows)
	}
	// analytics_head is a global singleton. Project it through its current
	// generation into this logical-exam scope so generations from unrelated
	// integration cases cannot affect the assertion.
	var invalidCurrentGenerationID *int64
	var invalidHeadRevision int64
	if err := pool.QueryRow(ctx, `
WITH scoped_head AS (
    SELECT head.current_generation_id, head.head_revision
    FROM ascendany.analytics_head AS head
    JOIN ascendany.analytics_generations AS generation
      ON generation.analytics_generation_id = head.current_generation_id
    JOIN ascendany.logical_exams AS exam ON exam.exam_id = generation.target_exam_id
    WHERE head.singleton
      AND exam.platform = 'pintia'
      AND exam.source_exam_id = $1
)
SELECT max(current_generation_id), coalesce(max(head_revision), 0)
FROM scoped_head`, invalidIDs.problemSetID).Scan(&invalidCurrentGenerationID, &invalidHeadRevision); err != nil {
		t.Fatalf("read invalid Decimal analytics head: %v", err)
	}
	if invalidCurrentGenerationID != nil || invalidHeadRevision != 0 {
		t.Fatalf(
			"invalid Decimal analytics head = generation %v revision %d, want nil/0",
			invalidCurrentGenerationID,
			invalidHeadRevision,
		)
	}
	assertIntegrationEvents(t, ctx, pool, invalidQueue.Job.ID, []string{"received", "claimed", "failed"})

	firstDocument, firstIDs := uniqueIntegrationSnapshot(t, base, suffix)
	firstPayload := marshalIntegrationDocument(t, firstDocument)
	firstQueue := queueIntegrationPayload(t, ctx, store, service, firstPayload)

	// Same bytes return the same durable job and do not create another event.
	idempotent := queueIntegrationPayload(t, ctx, store, service, firstPayload)
	if idempotent.Created || idempotent.Job.ID != firstQueue.Job.ID {
		t.Fatalf("idempotent queue result = %#v, first = %#v", idempotent, firstQueue)
	}

	firstClaim, err := service.Claim(ctx, "integration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if firstClaim == nil || firstClaim.Job.ID != firstQueue.Job.ID || firstClaim.Reclaimed {
		t.Fatalf("first claim = %#v", firstClaim)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET lease_expires_at = clock_timestamp() - interval '1 second'
	WHERE import_job_id = $1`, firstClaim.Job.ID); err != nil {
		t.Fatalf("expire first claim lease: %v", err)
	}
	if err := postgresRepository.Requeue(ctx, *firstClaim, time.Second, ErrorDatabase); err == nil {
		t.Fatal("expired claim requeued the job")
	} else {
		assertImportCode(t, err, ErrorLeaseLost)
	}
	reclaimed, err := service.Claim(ctx, "integration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed == nil || reclaimed.Job.ID != firstQueue.Job.ID || !reclaimed.Reclaimed || reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaimed job = %#v", reclaimed)
	}
	assertStaleImportAttemptRejected(t, ctx, postgresRepository, *firstClaim)

	importingClaim, err := postgresRepository.MarkImporting(ctx, *reclaimed, time.Minute)
	if err != nil {
		t.Fatalf("mark reclaimed attempt importing: %v", err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET lease_expires_at = clock_timestamp() - interval '1 second'
WHERE import_job_id = $1`, importingClaim.ID); err != nil {
		t.Fatalf("expire importing claim lease: %v", err)
	}
	activeClaim, err := service.Claim(ctx, "integration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if activeClaim == nil || activeClaim.ID != firstQueue.Job.ID || !activeClaim.Reclaimed || activeClaim.AttemptCount != 3 || activeClaim.Stage != StageImporting {
		t.Fatalf("second reclaimed job = %#v", activeClaim)
	}
	if err := postgresRepository.RenewLease(ctx, importingClaim, time.Minute); err == nil {
		t.Fatal("stale same-owner import attempt renewed the active lease")
	} else {
		assertImportCode(t, err, ErrorLeaseLost)
	}
	assertStaleSnapshotCommitRejected(t, ctx, postgresRepository, importingClaim, firstPayload)

	firstOutcome, err := worker.Process(ctx, *activeClaim)
	if err != nil {
		t.Fatalf("process first snapshot: %v", err)
	}
	if firstOutcome.Disposition != ImportCreated || firstOutcome.SnapshotID == nil || firstOutcome.AnalyticsGenerationID == nil {
		t.Fatalf("first outcome = %#v", firstOutcome)
	}
	var storedAcceptTimeSeconds int64
	if err := pool.QueryRow(ctx, `
SELECT result.accept_time_seconds
FROM ascendany.pintia_ranking_problem_results AS result
JOIN ascendany.exam_snapshots AS snapshot ON snapshot.snapshot_id = result.snapshot_id
JOIN ascendany.logical_exams AS exam ON exam.exam_id = snapshot.exam_id
WHERE exam.platform = 'pintia'
  AND exam.source_exam_id = $1
  AND result.problem_set_problem_id = $2`, firstIDs.problemSetID, firstIDs.problemSetProblemID).Scan(&storedAcceptTimeSeconds); err != nil {
		t.Fatalf("read stored accept time: %v", err)
	}
	if storedAcceptTimeSeconds != 60 {
		t.Fatalf("stored accept_time_seconds = %d, want 60", storedAcceptTimeSeconds)
	}

	assertIntegrationEvents(t, ctx, pool, firstQueue.Job.ID, []string{
		"received", "claimed", "reclaimed", "validation_completed", "reclaimed", "snapshot_imported",
	})
	assertIntegrationManifest(t, ctx, pool, *firstOutcome.AnalyticsGenerationID, *firstOutcome.SnapshotID)
	firstPublicJob, found, err := reader.GetJob(ctx, firstQueue.Job.PublicID)
	if err != nil || !found {
		t.Fatalf("read first public job: found=%t error=%v", found, err)
	}
	if firstPublicJob.Status != JobRunning || firstPublicJob.Stage != StageAnalyzing ||
		firstPublicJob.ExamID == nil || firstPublicJob.SnapshotID == nil || firstPublicJob.Error != nil {
		t.Fatalf("first public job = %#v", firstPublicJob)
	}
	firstEvents, found, err := reader.ReadEvents(ctx, firstQueue.Job.PublicID, 0, MaxEventBatchSize)
	if err != nil || !found {
		t.Fatalf("read first public events: found=%t error=%v", found, err)
	}
	if firstEvents.Terminal || len(firstEvents.Events) != 6 || firstEvents.Events[0].Sequence != 1 {
		t.Fatalf("first public events = %#v", firstEvents)
	}

	// Transport-only exporter metadata changes the artifact bytes while keeping
	// the domain hash identical. The second job must be superseded atomically.
	duplicateDocument := cloneIntegrationDocument(t, firstDocument)
	duplicateDocument["exporter"].(map[string]any)["exportedAt"] = "2026-07-10T01:02:04Z"
	duplicateQueue := queueIntegrationPayload(t, ctx, store, service, marshalIntegrationDocument(t, duplicateDocument))
	duplicateClaim, err := service.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || duplicateClaim == nil || duplicateClaim.Job.ID != duplicateQueue.Job.ID {
		t.Fatalf("duplicate claim = %#v, %v", duplicateClaim, err)
	}
	duplicateOutcome, err := worker.Process(ctx, *duplicateClaim)
	if err != nil {
		t.Fatalf("process domain duplicate: %v", err)
	}
	if duplicateOutcome.Disposition != ImportDuplicate || duplicateOutcome.SnapshotID == nil || *duplicateOutcome.SnapshotID != *firstOutcome.SnapshotID {
		t.Fatalf("duplicate outcome = %#v", duplicateOutcome)
	}
	assertIntegrationEvents(t, ctx, pool, duplicateQueue.Job.ID, []string{
		"received", "claimed", "validation_completed", "superseded",
	})

	var snapshotCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.exam_snapshots AS snapshot
JOIN ascendany.logical_exams AS exam ON exam.exam_id = snapshot.exam_id
WHERE exam.platform = 'pintia' AND exam.source_exam_id = $1`, firstIDs.problemSetID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 1 {
		t.Fatalf("domain duplicate created %d snapshots, want 1", snapshotCount)
	}

	// A student number owned by the first actor cannot be rebound in another
	// exam. The logical exam and all partial actor rows must roll back.
	conflictDocument, conflictIDs := uniqueIntegrationSnapshot(t, base, suffix+"-conflict")
	conflictParticipants := conflictDocument["participants"].([]any)
	conflictParticipants[0].(map[string]any)["studentNumber"] = firstIDs.studentNumber
	conflictQueue := queueIntegrationPayload(t, ctx, store, service, marshalIntegrationDocument(t, conflictDocument))
	conflictClaim, err := service.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || conflictClaim == nil || conflictClaim.Job.ID != conflictQueue.Job.ID {
		t.Fatalf("conflict claim = %#v, %v", conflictClaim, err)
	}
	conflictOutcome, err := worker.Process(ctx, *conflictClaim)
	if err != nil {
		t.Fatalf("process identity conflict: %v", err)
	}
	if conflictOutcome.Disposition != ImportFailed || conflictOutcome.FailureCode == nil || *conflictOutcome.FailureCode != ErrorIdentityConflict {
		t.Fatalf("conflict outcome = %#v", conflictOutcome)
	}
	assertIntegrationEvents(t, ctx, pool, conflictQueue.Job.ID, []string{
		"received", "claimed", "validation_completed", "failed",
	})
	failedPublicJob, found, err := reader.GetJob(ctx, conflictQueue.Job.PublicID)
	if err != nil || !found {
		t.Fatalf("read failed public job: found=%t error=%v", found, err)
	}
	if failedPublicJob.Error == nil || failedPublicJob.Error.Code != string(ErrorIdentityConflict) ||
		failedPublicJob.Error.Message != "The snapshot conflicts with immutable imported identity." ||
		!failedPublicJob.Error.Permanent {
		t.Fatalf("failed public job = %#v", failedPublicJob)
	}
	var rolledBackExamCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.logical_exams
WHERE platform = 'pintia' AND source_exam_id = $1`, conflictIDs.problemSetID).Scan(&rolledBackExamCount); err != nil {
		t.Fatal(err)
	}
	if rolledBackExamCount != 0 {
		t.Fatalf("identity conflict left %d logical exam rows", rolledBackExamCount)
	}
}

func assertStaleImportAttemptRejected(
	t *testing.T,
	ctx context.Context,
	repository workerStore,
	stale Claim,
) {
	t.Helper()
	if _, err := repository.LoadArtifact(ctx, stale); err == nil {
		t.Fatal("stale same-owner attempt loaded the reclaimed artifact")
	} else {
		assertImportCode(t, err, ErrorLeaseLost)
	}
	if _, err := repository.MarkImporting(ctx, stale, time.Minute); err == nil {
		t.Fatal("stale same-owner attempt advanced the reclaimed job stage")
	} else {
		assertImportCode(t, err, ErrorLeaseLost)
	}
	if err := repository.Requeue(ctx, stale, time.Second, ErrorDatabase); err == nil {
		t.Fatal("stale same-owner attempt requeued the reclaimed job")
	} else {
		assertImportCode(t, err, ErrorLeaseLost)
	}
	if err := repository.FailPermanent(ctx, stale, ErrorValidation, "stale attempt"); err == nil {
		t.Fatal("stale same-owner attempt failed the reclaimed job")
	} else {
		assertImportCode(t, err, ErrorLeaseLost)
	}
}

func assertStaleSnapshotCommitRejected(
	t *testing.T,
	ctx context.Context,
	repository workerStore,
	stale Claim,
	payload []byte,
) {
	t.Helper()
	validator, err := pintia.NewEmbeddedValidator(pintia.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := validator.Validate(payload)
	if err != nil {
		t.Fatal(err)
	}
	domainHash, err := pintia.DomainHash(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	logicalExamID, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	snapshotID, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ImportSnapshot(ctx, ImportRequest{
		Claim:      stale,
		Snapshot:   snapshot,
		DomainHash: domainHash,
		PublicIDs:  PublicIDs{LogicalExam: logicalExamID, Snapshot: snapshotID},
		Analytics: AnalyticsConfig{
			AlgorithmVersion: "integration_analytics_v1",
			ConfigSHA256:     strings.Repeat("c", 64),
		},
	})
	if err == nil {
		t.Fatal("stale same-owner attempt entered the snapshot transaction")
	}
	assertImportCode(t, err, ErrorLeaseLost)
}

type integrationIDs struct {
	problemSetID        string
	problemSetProblemID string
	studentNumber       string
}

func queueIntegrationPayload(
	t *testing.T,
	ctx context.Context,
	store *artifact.Store,
	service *Service,
	payload []byte,
) QueueResult {
	t.Helper()
	publication, err := store.Publish(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.QueuePublication(ctx, publication, PintiaSnapshotV2MediaType)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertIntegrationEvents(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	jobID int64,
	want []string,
) {
	t.Helper()
	var sequences []int64
	var eventTypes []string
	if err := pool.QueryRow(ctx, `
SELECT array_agg(event_sequence ORDER BY event_sequence),
       array_agg(event_type ORDER BY event_sequence)
FROM ascendany.import_job_events
WHERE import_job_id = $1`, jobID).Scan(&sequences, &eventTypes); err != nil {
		t.Fatal(err)
	}
	if len(sequences) != len(want) || len(eventTypes) != len(want) {
		t.Fatalf("events sequences=%v types=%v want=%v", sequences, eventTypes, want)
	}
	for index := range want {
		if sequences[index] != int64(index+1) || eventTypes[index] != want[index] {
			t.Fatalf("events sequences=%v types=%v want=%v", sequences, eventTypes, want)
		}
	}
}

func assertIntegrationManifest(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	generationID int64,
	targetSnapshotID int64,
) {
	t.Helper()
	var manifestJSON []byte
	var manifestHash string
	if err := pool.QueryRow(ctx, `
SELECT input_manifest::text, input_manifest_sha256
FROM ascendany.analytics_generations
WHERE analytics_generation_id = $1`, generationID).Scan(&manifestJSON, &manifestHash); err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseAnalyticsManifestV1(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if digest != manifestHash || manifest.Target.SnapshotID != targetSnapshotID {
		t.Fatalf("manifest digest=%s/%s target=%d/%d", digest, manifestHash, manifest.Target.SnapshotID, targetSnapshotID)
	}
	var relationalRows int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.analytics_generation_snapshots
WHERE analytics_generation_id = $1`, generationID).Scan(&relationalRows); err != nil {
		t.Fatal(err)
	}
	if relationalRows != len(manifest.Snapshots) {
		t.Fatalf("manifest snapshots=%d relational rows=%d", len(manifest.Snapshots), relationalRows)
	}
}

func integrationFixtureDocument(t *testing.T) map[string]any {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "pintia", "fixtures", "valid", "complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func uniqueIntegrationSnapshot(
	t *testing.T,
	base map[string]any,
	suffix string,
) (map[string]any, integrationIDs) {
	t.Helper()
	document := cloneIntegrationDocument(t, base)
	safeSuffix := strings.ReplaceAll(suffix, "-", "_")
	problemSetID := "ps_" + safeSuffix
	problemSetProblemID := "psp_" + safeSuffix
	problemID := "problem_" + safeSuffix
	studentNumber := "student_" + safeSuffix

	exam := document["exam"].(map[string]any)
	exam["problemSetId"] = problemSetID
	exam["sourceUrl"] = "https://pintia.cn/problem-sets/" + problemSetID + "/submissions"
	problem := document["problems"].([]any)[0].(map[string]any)
	problem["problemSetProblemId"] = problemSetProblemID
	problem["problemId"] = problemID

	participants := document["participants"].([]any)
	userMapping := make(map[string]string, len(participants))
	for index, raw := range participants {
		participant := raw.(map[string]any)
		oldUserID := participant["userId"].(string)
		newUserID := fmt.Sprintf("user_%d_%s", index, safeSuffix)
		userMapping[oldUserID] = newUserID
		participant["userId"] = newUserID
		if index == 0 {
			participant["studentUserId"] = "student_user_" + safeSuffix
			participant["studentNumber"] = studentNumber
		}
		if ranking, ok := participant["ranking"].(map[string]any); ok {
			for _, rawResult := range ranking["problemResults"].([]any) {
				rawResult.(map[string]any)["problemSetProblemId"] = problemSetProblemID
			}
		}
	}
	for index, raw := range document["submissions"].([]any) {
		submission := raw.(map[string]any)
		submission["submissionId"] = fmt.Sprintf("submission_%d_%s", index, safeSuffix)
		submission["problemSetProblemId"] = problemSetProblemID
		submission["userId"] = userMapping[submission["userId"].(string)]
	}
	document["exporter"].(map[string]any)["exportedAt"] = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	return document, integrationIDs{
		problemSetID:        problemSetID,
		problemSetProblemID: problemSetProblemID,
		studentNumber:       studentNumber,
	}
}

func cloneIntegrationDocument(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	payload := marshalIntegrationDocument(t, source)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var clone map[string]any
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func marshalIntegrationDocument(t *testing.T, document map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
