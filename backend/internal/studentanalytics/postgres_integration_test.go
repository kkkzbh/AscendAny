package studentanalytics

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresSelfAnalyticsReadPath(t *testing.T) {
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
		t.Fatal(err)
	}
	var existingAccounts int64
	var existingGenerations int64
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.auth_accounts),
       (SELECT count(*) FROM ascendany.analytics_generations)`).Scan(&existingAccounts, &existingGenerations); err != nil {
		t.Fatal(err)
	}
	if existingAccounts != 0 || existingGenerations != 0 {
		t.Skipf("student analytics integration database is not empty: %d accounts, %d generations", existingAccounts, existingGenerations)
	}

	fixture := seedStudentAnalyticsReadFixture(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetSelf(ctx, SelfQuery{
		AccountID:            fixture.accountPublicID,
		SessionID:            fixture.sessionPublicID,
		ExpectedAuthRevision: 1,
		ExpectedRole:         auth.RoleStudent,
		HistoryLimit:         1,
	})
	if err != nil {
		t.Fatalf("GetSelf() error = %v", err)
	}
	if result.State != StateReady || result.HeadRevision != 1 || result.Ready == nil || result.Ready.Rating != 1510 ||
		len(result.Ready.ExamHistory) != 1 || len(result.Ready.RatingHistory) != 1 ||
		result.Ready.ExamHistory[0].ExamID != fixture.examPublicID || result.Ready.ExamHistory[0].SnapshotID != fixture.snapshotPublicID ||
		result.Ready.ExamHistory[0].Title != "Student analytics integration" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET revoked_at = clock_timestamp(), revocation_reason = 'integration_test'
WHERE public_id = $1::uuid`, fixture.sessionPublicID); err != nil {
		t.Fatal(err)
	}
	_, err = service.GetSelf(ctx, SelfQuery{
		AccountID:            fixture.accountPublicID,
		SessionID:            fixture.sessionPublicID,
		ExpectedAuthRevision: 1,
		ExpectedRole:         auth.RoleStudent,
		HistoryLimit:         1,
	})
	if CodeOf(err) != ErrorPrincipalRejected {
		t.Fatalf("revoked session error = %v (%q)", err, CodeOf(err))
	}
}

type studentAnalyticsReadFixture struct {
	accountPublicID  string
	sessionPublicID  string
	examPublicID     string
	snapshotPublicID string
}

func seedStudentAnalyticsReadFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) studentAnalyticsReadFixture {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	fixture := studentAnalyticsReadFixture{
		accountPublicID:  "11111111-1111-4111-8111-111111111111",
		sessionPublicID:  "77777777-7777-4777-8777-777777777777",
		examPublicID:     "22222222-2222-4222-8222-222222222222",
		snapshotPublicID: "33333333-3333-4333-8333-333333333333",
	}
	artifactHash := strings.Repeat("a", 64)
	domainHash := strings.Repeat("b", 64)
	var artifactID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/vnd.ascendany.pintia.snapshot.v2+json', 'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, artifactHash).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	var importJobID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (
    public_id, artifact_id, job_kind, status, stage
)
VALUES ('44444444-4444-4444-8444-444444444444'::uuid, $1, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, artifactID).Scan(&importJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'running',
    stage = 'validating',
    attempt_count = 1,
    lease_owner = 'student-analytics-integration-import',
    lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'queued'
  AND attempt_count = 0`, importJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'importing',
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'validating'
  AND attempt_count = 1`, importJobID); err != nil {
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
  AND attempt_count = 1`, importJobID); err != nil {
		t.Fatal(err)
	}
	var examID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', 'student-analytics-integration')
RETURNING exam_id`, fixture.examPublicID).Scan(&examID); err != nil {
		t.Fatal(err)
	}
	var snapshotID int64
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
    'ascendany-pintia-exporter', 'integration', '2026-07-11T00:00:00Z',
    'Student analytics integration', 'https://pintia.cn/problem-sets/student-analytics-integration',
    '2026-07-11T00:00:00Z', '2026-07-11T01:00:00Z', 100,
    1, 1, 1, true,
    0, 0, 0, true,
    0, 0, 0, true,
    1
)
RETURNING snapshot_id`, fixture.snapshotPublicID, examID, artifactID, importJobID, strings.Repeat("c", 64), domainHash).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE exam_id = $1`, examID, snapshotID); err != nil {
		t.Fatal(err)
	}
	var actorID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ('student-analytics-integration-user')
RETURNING actor_id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', '20260001', $1)`, actorID); err != nil {
		t.Fatal(err)
	}
	var accountID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, student_number, actor_id,
    role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, 'student_analytics', 'integration-phc', 'Integration Student', '20260001', $2,
		'student', 1, clock_timestamp(), clock_timestamp())
RETURNING account_id`, fixture.accountPublicID, actorID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp() - interval '1 minute',
        clock_timestamp() + interval '1 hour', clock_timestamp())`, fixture.sessionPublicID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_problems (
    snapshot_id, problem_set_problem_id, problem_id, title, problem_type, max_score
)
VALUES ($1, 'p1', 'problem-1', 'Problem 1', 'PROGRAMMING', 100)`, snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_participants (
    snapshot_id, actor_id, student_number, display_name
)
VALUES ($1, $2, '20260001', 'Integration Student')`, snapshotID, actorID); err != nil {
		t.Fatal(err)
	}

	manifest, err := analytics.CanonicalManifest(analytics.Manifest{
		Protocol:         analytics.ManifestProtocolV1,
		BaseHeadRevision: 0,
		Target:           analytics.ManifestTarget{ExamID: examID, SnapshotID: snapshotID, ExamHeadRevision: 1},
		Snapshots:        []analytics.ManifestSnapshot{{ExamID: examID, SnapshotID: snapshotID, DomainHash: domainHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var generationID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.analytics_generations (
    status, base_analytics_generation_id, base_head_revision,
    target_exam_id, target_snapshot_id, target_exam_head_revision,
    input_manifest, input_manifest_sha256, algorithm_version, config_sha256
)
VALUES (
    'queued', NULL, 0,
    $1, $2, 1,
    $3::jsonb, $4, 'analytics_v1', $5
)
RETURNING analytics_generation_id`, examID, snapshotID, string(manifest.Canonical), manifest.SHA256, strings.Repeat("d", 64)).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id, exam_id, snapshot_id, domain_hash
)
VALUES ($1, $2, $3, $4)`, generationID, examID, snapshotID, domainHash); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running',
    attempt_count = 1,
    lease_owner = 'student-analytics-integration-generation',
    lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'queued'
  AND attempt_count = 0`, generationID); err != nil {
		t.Fatal(err)
	}
	eventTime := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	knowledge := 80.0
	metrics := analytics.StudentMetrics{
		Protocol:      analytics.StudentMetricsProtocolV1,
		ReferenceTime: eventTime,
		Current:       analytics.MetricValues{Knowledge: &knowledge},
		ExamHistory: []analytics.ExamMetricPoint{{
			ExamID: examID, SnapshotID: snapshotID, EventTime: eventTime,
			Values: analytics.MetricValues{Knowledge: &knowledge},
		}},
		RatingHistory: []analytics.RatingHistoryPoint{{
			ExamID: examID, SnapshotID: snapshotID, EventTime: eventTime,
			Rank: 1, OldRating: 1500, Delta: 10, NewRating: 1510, Seed: 1, Performance: 1510,
		}},
	}
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.student_analytics (
    analytics_generation_id, actor_id, rating, metrics
)
VALUES ($1, $2, 1510, $3::jsonb)`, generationID, actorID, string(metricsJSON)); err != nil {
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
  AND attempt_count = 1`, generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $1, head_revision = 1, updated_at = clock_timestamp()
WHERE singleton AND current_generation_id IS NULL AND head_revision = 0`, generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'succeeded',
    stage = 'completed',
    snapshot_id = $2,
    lease_owner = NULL,
    lease_expires_at = NULL,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND status = 'running'
  AND stage = 'analyzing'
  AND attempt_count = 1`, importJobID, snapshotID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return fixture
}
