package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAnalyticsClaimReclaimPublishAndReplacementReuse(t *testing.T) {
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
	var existing int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ascendany.analytics_generations`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Skipf("analytics test database contains %d generations", existing)
	}

	configuration, err := ParseConfig([]byte(validConfigJSON))
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedAnalyticsFixture(t, ctx, pool, configuration)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Claim(ctx, "integration-expiring", time.Minute)
	if err != nil || first == nil || first.GenerationID != fixture.generationID || first.Reclaimed || first.AttemptCount != 1 {
		t.Fatalf("first claim = %#v, error = %v", first, err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET lease_expires_at = clock_timestamp() - interval '1 second'
WHERE analytics_generation_id = $1`, fixture.generationID); err != nil {
		t.Fatalf("expire analytics lease: %v", err)
	}
	reclaimed, err := repository.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || reclaimed == nil || reclaimed.GenerationID != fixture.generationID || !reclaimed.Reclaimed || reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaimed claim = %#v, error = %v", reclaimed, err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET lease_expires_at = clock_timestamp() - interval '1 second'
WHERE analytics_generation_id = $1`, fixture.generationID); err != nil {
		t.Fatalf("expire reclaimed analytics lease: %v", err)
	}
	active, err := repository.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || active == nil || active.GenerationID != fixture.generationID || !active.Reclaimed || active.AttemptCount != 3 {
		t.Fatalf("same-owner reclaimed claim = %#v, error = %v", active, err)
	}
	if err := repository.RenewLease(ctx, *reclaimed, time.Minute); err == nil {
		t.Fatal("stale same-owner analytics attempt renewed the active lease")
	} else if code, ok := CodeOf(err); !ok || code != ErrorLeaseLost {
		t.Fatalf("stale RenewLease() code = %q, %v; error = %v", code, ok, err)
	}
	worker, err := newWorker(repository, configuration, "integration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(ctx, *active)
	if err != nil {
		t.Fatalf("publish winner: %v", err)
	}
	if outcome.Disposition != RunSucceeded || outcome.GenerationID != fixture.generationID {
		t.Fatalf("winner outcome = %#v", outcome)
	}
	assertAtomicWinnerState(t, ctx, pool, fixture)
	assertNegativeRatingRejected(t, ctx, pool, fixture.generationID)

	staleGenerationID, replacementGenerationID, replacementManifestSHA := seedReplacementReuse(t, ctx, pool, fixture, configuration)
	staleClaim, err := repository.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || staleClaim == nil || staleClaim.GenerationID != staleGenerationID {
		t.Fatalf("stale claim = %#v, error = %v", staleClaim, err)
	}
	replacementOutcome, err := worker.Process(ctx, *staleClaim)
	if err != nil {
		t.Fatalf("publish stale generation: %v", err)
	}
	if replacementOutcome.Disposition != RunSuperseded || replacementOutcome.ReplacementGenerationID == nil || *replacementOutcome.ReplacementGenerationID != replacementGenerationID {
		t.Fatalf("replacement outcome = %#v", replacementOutcome)
	}
	var staleStatus string
	var replacementCount int64
	if err := pool.QueryRow(ctx, `
SELECT stale.status,
       (SELECT count(*) FROM ascendany.analytics_generations
        WHERE target_snapshot_id = $2
          AND input_manifest_sha256 = $3
          AND algorithm_version = $4
          AND config_sha256 = $5)
FROM ascendany.analytics_generations AS stale
	WHERE stale.analytics_generation_id = $1`, staleGenerationID, fixture.snapshotID, replacementManifestSHA, AlgorithmV1, configuration.SHA256).Scan(&staleStatus, &replacementCount); err != nil {
		t.Fatal(err)
	}
	if staleStatus != "superseded" || replacementCount != 1 {
		t.Fatalf("stale status = %q, exact replacement rows = %d", staleStatus, replacementCount)
	}
	mismatchedConfiguration := configuration
	mismatchedConfiguration.SHA256 = repeatedHash('f')
	failureFixture := seedAnalyticsFixture(t, ctx, pool, mismatchedConfiguration)
	failureClaim, err := repository.Claim(ctx, "integration-worker", time.Minute)
	if err != nil || failureClaim == nil || failureClaim.GenerationID != failureFixture.generationID {
		t.Fatalf("permanent failure claim = %#v, error = %v", failureClaim, err)
	}
	failureOutcome, err := worker.Process(ctx, *failureClaim)
	if err != nil {
		t.Fatalf("persist permanent failure: %v", err)
	}
	if failureOutcome.Disposition != RunFailed || failureOutcome.FailureCode == nil || *failureOutcome.FailureCode != ErrorConfigMismatch {
		t.Fatalf("permanent failure outcome = %#v", failureOutcome)
	}
	assertAtomicPermanentFailure(t, ctx, pool, failureFixture)
}

type analyticsFixture struct {
	examID       int64
	snapshotID   int64
	jobID        int64
	generationID int64
	domainHash   string
}

func seedAnalyticsFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, configuration ParsedConfig) analyticsFixture {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	artifactHash := integrationHash(newIntegrationUUID(t))
	domainHash := integrationHash(newIntegrationUUID(t))
	contractHash := integrationHash("contract")
	var artifactID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/vnd.ascendany.pintia.snapshot.v2+json', 'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, artifactHash).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	fixture := analyticsFixture{domainHash: domainHash}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (
    public_id, artifact_id, job_kind, status, stage
)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, newIntegrationUUID(t), artifactID).Scan(&fixture.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'running',
    stage = 'validating',
    attempt_count = 1,
    lease_owner = 'analytics-integration-import',
    lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'queued'
  AND attempt_count = 0`, fixture.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'importing',
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'validating'
  AND attempt_count = 1`, fixture.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'analyzing',
    lease_owner = NULL,
    lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'importing'
  AND attempt_count = 1`, fixture.jobID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', $2)
RETURNING exam_id`, newIntegrationUUID(t), "analytics-integration-"+newIntegrationUUID(t)).Scan(&fixture.examID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.exam_snapshots (
    public_id, exam_id, snapshot_sequence, source_artifact_id, import_job_id,
    contract_schema, contract_schema_sha256, domain_hash_protocol, domain_hash,
    exporter_name, exporter_version, exported_at, title, source_url, starts_at, ends_at, total_score,
    problems_source_count, problems_observed_count, problems_exported_count, problems_pagination_exhausted,
    rankings_source_count, rankings_observed_count, rankings_exported_count, rankings_pagination_exhausted,
    submissions_source_count, submissions_observed_count, submissions_exported_count, submissions_pagination_exhausted,
    participants_exported_count
)
VALUES (
    $1::uuid, $2, 1, $3, $4,
    'ascendany.pintia.snapshot.v2', $5, 'domain_hash_proto_v1', $6,
    'ascendany-pintia-exporter', 'integration', '2026-07-10T00:00:00Z',
    'Analytics integration', 'https://pintia.cn/problem-sets/integration',
    '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 1e100,
    1, 1, 1, true,
    1, 1, 1, true,
    0, 0, 0, true,
    1
)
RETURNING snapshot_id`, newIntegrationUUID(t), fixture.examID, artifactID, fixture.jobID, contractHash, domainHash).Scan(&fixture.snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE exam_id = $1`, fixture.examID, fixture.snapshotID); err != nil {
		t.Fatal(err)
	}
	var actorID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "analytics-user-"+newIntegrationUUID(t)).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_problems (
    snapshot_id, problem_set_problem_id, problem_id, title, problem_type, max_score
)
VALUES ($1, 'p1', 'problem-1', 'Problem 1', 'PROGRAMMING', 1e100)`, fixture.snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_participants (snapshot_id, actor_id)
VALUES ($1, $2)`, fixture.snapshotID, actorID); err != nil {
		t.Fatal(err)
	}
	// Exercise the exact contract maximum through PostgreSQL numeric decoding,
	// ranking/exam division, proficiency computation, and atomic publication.
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_rankings (
    snapshot_id, actor_id, rank, total_score, time_used_seconds
)
VALUES ($1, $2, 1, 1e100, 60)`, fixture.snapshotID, actorID); err != nil {
		t.Fatal(err)
	}
	manifest, err := CanonicalManifest(Manifest{
		Protocol:         ManifestProtocolV1,
		BaseHeadRevision: 0,
		Target:           ManifestTarget{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, ExamHeadRevision: 1},
		Snapshots:        []ManifestSnapshot{{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, DomainHash: domainHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.generationID = insertIntegrationGeneration(t, ctx, tx, manifest, configuration, 0)
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.import_job_events (import_job_id, event_sequence, event_type, payload)
VALUES ($1, 1, 'snapshot_imported', jsonb_build_object('analyticsGenerationId', $2::bigint))`, fixture.jobID, fixture.generationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func seedReplacementReuse(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture analyticsFixture,
	configuration ParsedConfig,
) (int64, int64, string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	baseGenerationID := fixture.generationID
	intervening, err := CanonicalManifest(Manifest{
		Protocol: ManifestProtocolV1, BaseAnalyticsGenerationID: &baseGenerationID, BaseHeadRevision: 1,
		Target:    ManifestTarget{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, ExamHeadRevision: 1},
		Snapshots: []ManifestSnapshot{{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, DomainHash: fixture.domainHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	interveningConfiguration := configuration
	interveningConfiguration.SHA256 = repeatedHash('e')
	interveningGenerationID := insertIntegrationGeneration(t, ctx, tx, intervening, interveningConfiguration, 0)
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running',
    attempt_count = 1,
    lease_owner = 'analytics-integration-head-advance',
    lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'queued'
  AND attempt_count = 0`, interveningGenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'succeeded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    finished_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND attempt_count = 1`, interveningGenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $2,
    head_revision = 2,
    updated_at = clock_timestamp()
WHERE singleton
  AND current_generation_id = $1
  AND head_revision = 1`, fixture.generationID, interveningGenerationID); err != nil {
		t.Fatal(err)
	}
	stale, err := CanonicalManifest(Manifest{
		Protocol: ManifestProtocolV1, BaseAnalyticsGenerationID: &baseGenerationID, BaseHeadRevision: 1,
		Target:    ManifestTarget{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, ExamHeadRevision: 1},
		Snapshots: []ManifestSnapshot{{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, DomainHash: fixture.domainHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacementBaseGenerationID := interveningGenerationID
	replacement, err := CanonicalManifest(Manifest{
		Protocol: ManifestProtocolV1, BaseAnalyticsGenerationID: &replacementBaseGenerationID, BaseHeadRevision: 2,
		Target:    ManifestTarget{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, ExamHeadRevision: 1},
		Snapshots: []ManifestSnapshot{{ExamID: fixture.examID, SnapshotID: fixture.snapshotID, DomainHash: fixture.domainHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	staleGenerationID := insertIntegrationGeneration(t, ctx, tx, stale, configuration, 0)
	replacementGenerationID := insertIntegrationGeneration(t, ctx, tx, replacement, configuration, time.Hour)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return staleGenerationID, replacementGenerationID, replacement.SHA256
}

func insertIntegrationGeneration(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	manifest ParsedManifest,
	configuration ParsedConfig,
	nextAttemptDelay time.Duration,
) int64 {
	t.Helper()
	var generationID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.analytics_generations (
    status, base_analytics_generation_id, base_head_revision,
    target_exam_id, target_snapshot_id, target_exam_head_revision,
    input_manifest, input_manifest_sha256, algorithm_version, config_sha256,
    next_attempt_at
)
VALUES (
    'queued', $1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9,
    clock_timestamp() + ($10::bigint * interval '1 millisecond')
)
RETURNING analytics_generation_id`,
		manifest.Value.BaseAnalyticsGenerationID,
		manifest.Value.BaseHeadRevision,
		manifest.Value.Target.ExamID,
		manifest.Value.Target.SnapshotID,
		manifest.Value.Target.ExamHeadRevision,
		string(manifest.Canonical),
		manifest.SHA256,
		configuration.Value.AlgorithmVersion,
		configuration.SHA256,
		nextAttemptDelay.Milliseconds(),
	).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range manifest.Value.Snapshots {
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id, exam_id, snapshot_id, domain_hash
)
VALUES ($1, $2, $3, $4)`, generationID, snapshot.ExamID, snapshot.SnapshotID, snapshot.DomainHash); err != nil {
			t.Fatal(err)
		}
	}
	return generationID
}

func assertAtomicWinnerState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture analyticsFixture) {
	t.Helper()
	var generationStatus string
	var attemptCount int32
	var leaseOwner *string
	var currentGenerationID int64
	var headRevision int64
	var jobStatus string
	var jobStage string
	var jobSnapshotID int64
	var events []string
	err := pool.QueryRow(ctx, `
SELECT generation.status,
       generation.attempt_count,
       generation.lease_owner,
       head.current_generation_id,
       head.head_revision,
       job.status,
       job.stage,
       job.snapshot_id,
       array_agg(event.event_type ORDER BY event.event_sequence)
FROM ascendany.analytics_generations AS generation
JOIN ascendany.analytics_head AS head ON head.singleton
JOIN ascendany.exam_snapshots AS snapshot ON snapshot.snapshot_id = generation.target_snapshot_id
JOIN ascendany.import_jobs AS job ON job.import_job_id = snapshot.import_job_id
JOIN ascendany.import_job_events AS event ON event.import_job_id = job.import_job_id
WHERE generation.analytics_generation_id = $1
GROUP BY generation.status, generation.attempt_count, generation.lease_owner,
         head.current_generation_id, head.head_revision,
         job.status, job.stage, job.snapshot_id`, fixture.generationID).Scan(
		&generationStatus, &attemptCount, &leaseOwner, &currentGenerationID, &headRevision,
		&jobStatus, &jobStage, &jobSnapshotID, &events,
	)
	if err != nil {
		t.Fatal(err)
	}
	if generationStatus != "succeeded" || attemptCount != 3 || leaseOwner != nil || currentGenerationID != fixture.generationID || headRevision != 1 || jobStatus != "succeeded" || jobStage != "completed" || jobSnapshotID != fixture.snapshotID || fmt.Sprint(events) != "[snapshot_imported completed]" {
		t.Fatalf("atomic winner state: generation=%s attempt=%d lease=%v head=%d/%d job=%s/%s/%d events=%v", generationStatus, attemptCount, leaseOwner, currentGenerationID, headRevision, jobStatus, jobStage, jobSnapshotID, events)
	}
}

func assertNegativeRatingRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, generationID int64) {
	t.Helper()
	var actorID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "negative-rating-"+newIntegrationUUID(t)).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
INSERT INTO ascendany.student_analytics (analytics_generation_id, actor_id, rating, metrics)
VALUES ($1, $2, -1, '{}'::jsonb)`, generationID, actorID)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" || postgresError.ConstraintName != "student_analytics_rating_finite" {
		t.Fatalf("negative rating error = %v", err)
	}
}

func assertAtomicPermanentFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture analyticsFixture) {
	t.Helper()
	var generationStatus string
	var generationCode string
	var jobStatus string
	var jobStage string
	var jobCode string
	var permanent bool
	var events []string
	if err := pool.QueryRow(ctx, `
SELECT generation.status,
       generation.error_code,
       job.status,
       job.stage,
       job.error_code,
       job.error_permanent,
       array_agg(event.event_type ORDER BY event.event_sequence)
FROM ascendany.analytics_generations AS generation
JOIN ascendany.exam_snapshots AS snapshot ON snapshot.snapshot_id = generation.target_snapshot_id
JOIN ascendany.import_jobs AS job ON job.import_job_id = snapshot.import_job_id
JOIN ascendany.import_job_events AS event ON event.import_job_id = job.import_job_id
WHERE generation.analytics_generation_id = $1
GROUP BY generation.status, generation.error_code,
         job.status, job.stage, job.error_code, job.error_permanent`, fixture.generationID).Scan(
		&generationStatus, &generationCode, &jobStatus, &jobStage, &jobCode, &permanent, &events,
	); err != nil {
		t.Fatal(err)
	}
	if generationStatus != "failed" || generationCode != string(ErrorConfigMismatch) || jobStatus != "failed" || jobStage != "failed" || jobCode != string(ErrorConfigMismatch) || !permanent || fmt.Sprint(events) != "[snapshot_imported failed]" {
		t.Fatalf("atomic permanent failure: generation=%s/%s job=%s/%s/%s/%v events=%v", generationStatus, generationCode, jobStatus, jobStage, jobCode, permanent, events)
	}
}

func newIntegrationUUID(t *testing.T) string {
	t.Helper()
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16])
}

func integrationHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
