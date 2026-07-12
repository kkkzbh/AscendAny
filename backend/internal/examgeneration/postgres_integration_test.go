package examgeneration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresCurrentGenerationUsesActiveSnapshotAndRevalidatesPrincipal(t *testing.T) {
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

	fixture := seedExamGenerationFixture(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	core, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &applicationVerifierStub{principal: fixture.principal}
	application, err := NewApplicationService(verifier, core)
	if err != nil {
		t.Fatal(err)
	}

	generation, found, err := application.GetCurrent(ctx, "integration-access", fixture.examPublicID)
	if err != nil || !found {
		t.Fatalf("GetCurrent() found=%t error=%v", found, err)
	}
	if generation.GenerationID != fmt.Sprint(fixture.currentGenerationID) || generation.Status != StatusFailed ||
		generation.AttemptCount != 1 || generation.ErrorCode == nil || *generation.ErrorCode != "invalid_dataset" ||
		generation.EventHead != 3 || generation.StartedAt == nil || generation.FinishedAt == nil {
		t.Fatalf("generation=%#v", generation)
	}
	encoded, err := json.Marshal(generation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), fixture.privateErrorDetail) {
		t.Fatalf("public generation leaked error detail: %s", encoded)
	}

	firstPage, found, err := application.ReadEvents(ctx, "integration-access", fixture.examPublicID, generation.GenerationID, 1, 1)
	if err != nil || !found || firstPage.GenerationID != generation.GenerationID || firstPage.EventHead != 3 ||
		len(firstPage.Events) != 1 || firstPage.Events[0].Sequence != 2 || firstPage.Events[0].Type != EventRunning || firstPage.Terminal {
		t.Fatalf("first event page=%#v found=%t error=%v", firstPage, found, err)
	}
	terminalPage, found, err := application.ReadEvents(ctx, "integration-access", fixture.examPublicID, generation.GenerationID, 2, 10)
	if err != nil || !found || len(terminalPage.Events) != 1 || terminalPage.Events[0].Type != EventFailed || !terminalPage.Terminal {
		t.Fatalf("terminal event page=%#v found=%t error=%v", terminalPage, found, err)
	}
	caughtUp, found, err := application.ReadEvents(ctx, "integration-access", fixture.examPublicID, generation.GenerationID, 3, 10)
	if err != nil || !found || len(caughtUp.Events) != 0 || !caughtUp.Terminal || caughtUp.EventHead != 3 {
		t.Fatalf("caught-up event page=%#v found=%t error=%v", caughtUp, found, err)
	}
	if _, _, err := application.ReadEvents(ctx, "integration-access", fixture.examPublicID, generation.GenerationID, 4, 10); CodeOf(err) != ErrorEventCursorInvalid {
		t.Fatalf("cursor above head error=%v code=%q", err, CodeOf(err))
	}
	historical, found, err := application.ReadEvents(
		ctx, "integration-access", fixture.examPublicID, fmt.Sprint(fixture.historicalGenerationID), 0, 10,
	)
	if err != nil || !found || historical.GenerationID != fmt.Sprint(fixture.historicalGenerationID) || historical.EventHead != 1 {
		t.Fatalf("historical pinned generation=%#v found=%t error=%v", historical, found, err)
	}
	if _, found, err := application.ReadEvents(
		ctx, "integration-access", fixture.examPublicID, fmt.Sprint(fixture.otherExamGenerationID), 0, 10,
	); err != nil || found {
		t.Fatalf("cross-exam generation found=%t error=%v", found, err)
	}
	if _, found, err := application.GetCurrent(ctx, "integration-access", randomUUIDv4(t)); err != nil || found {
		t.Fatalf("unknown exam found=%t error=%v", found, err)
	}

	if _, err := pool.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET revoked_at = clock_timestamp(),
    revocation_reason = 'exam_generation_integration'
WHERE public_id = $1::uuid`, fixture.principal.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.GetCurrent(ctx, "integration-access", fixture.examPublicID); CodeOf(err) != ErrorPrincipalRejected {
		t.Fatalf("revoked principal error=%v code=%q", err, CodeOf(err))
	}
}

type examGenerationFixture struct {
	principal              auth.AccessPrincipal
	examPublicID           string
	currentGenerationID    int64
	historicalGenerationID int64
	otherExamGenerationID  int64
	privateErrorDetail     string
}

func seedExamGenerationFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) examGenerationFixture {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	fixture := examGenerationFixture{
		principal: auth.AccessPrincipal{
			AccountID: randomUUIDv4(t), SessionID: randomUUIDv4(t), JWTID: randomUUIDv4(t),
			Role: auth.RoleAdmin, AuthRevision: 1,
		},
		examPublicID:       randomUUIDv4(t),
		privateErrorDetail: "private integration failure detail",
	}
	username := "eg_" + randomHex(t, 8)
	var accountID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, 'integration-phc', 'Exam Generation Integration', 'admin', 1,
        clock_timestamp(), clock_timestamp())
RETURNING account_id`, fixture.principal.AccountID, username).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp() - interval '1 minute',
        clock_timestamp() + interval '1 hour', clock_timestamp())`, fixture.principal.SessionID, accountID); err != nil {
		t.Fatal(err)
	}

	var examID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', $2)
RETURNING exam_id`, fixture.examPublicID, "exam-generation-"+randomHex(t, 8)).Scan(&examID); err != nil {
		t.Fatal(err)
	}
	firstSnapshotID, firstDomainHash := insertExamGenerationSnapshot(t, ctx, tx, examID, 1)
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2,
    head_revision = 1,
    updated_at = clock_timestamp()
WHERE exam_id = $1`, examID, firstSnapshotID); err != nil {
		t.Fatal(err)
	}
	secondSnapshotID, secondDomainHash := insertExamGenerationSnapshot(t, ctx, tx, examID, 2)
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2,
    head_revision = 2,
    updated_at = clock_timestamp()
WHERE exam_id = $1`, examID, secondSnapshotID); err != nil {
		t.Fatal(err)
	}

	insertExamGeneration(t, ctx, tx, examID, secondSnapshotID, 2, secondDomainHash)
	fixture.historicalGenerationID = insertExamGeneration(t, ctx, tx, examID, firstSnapshotID, 1, firstDomainHash)
	fixture.currentGenerationID = insertExamGeneration(t, ctx, tx, examID, secondSnapshotID, 2, secondDomainHash)
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running',
    attempt_count = 1,
    lease_owner = 'exam-generation-integration',
    lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'queued'
  AND attempt_count = 0`, fixture.currentGenerationID); err != nil {
		t.Fatal(err)
	}
	insertExamGenerationEvent(t, ctx, tx, fixture.currentGenerationID, 2, "running", `{"attemptCount":1,"reclaimed":false}`)
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'failed',
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = 'invalid_dataset',
    error_detail = $2,
    finished_at = clock_timestamp()
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND attempt_count = 1`, fixture.currentGenerationID, fixture.privateErrorDetail); err != nil {
		t.Fatal(err)
	}
	insertExamGenerationEvent(t, ctx, tx, fixture.currentGenerationID, 3, "failed", `{"code":"invalid_dataset","permanent":true}`)

	// This row has the greatest identity and the active snapshot, while its target
	// revision is stale. The read contract must continue to select the exact
	// active snapshot/head pair represented by currentGenerationID.
	insertExamGeneration(t, ctx, tx, examID, secondSnapshotID, 1, secondDomainHash)

	var otherExamID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', $2)
RETURNING exam_id`, randomUUIDv4(t), "exam-generation-other-"+randomHex(t, 8)).Scan(&otherExamID); err != nil {
		t.Fatal(err)
	}
	otherSnapshotID, otherDomainHash := insertExamGenerationSnapshot(t, ctx, tx, otherExamID, 1)
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2,
    head_revision = 1,
    updated_at = clock_timestamp()
WHERE exam_id = $1`, otherExamID, otherSnapshotID); err != nil {
		t.Fatal(err)
	}
	fixture.otherExamGenerationID = insertExamGeneration(
		t, ctx, tx, otherExamID, otherSnapshotID, 1, otherDomainHash,
	)

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func insertExamGenerationSnapshot(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	examID int64,
	sequence int64,
) (int64, string) {
	t.Helper()
	artifactHash := randomHex(t, 32)
	var artifactID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/vnd.ascendany.pintia.snapshot.v2+json',
        'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, artifactHash).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	var importJobID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (public_id, artifact_id, job_kind, status, stage)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, randomUUIDv4(t), artifactID).Scan(&importJobID); err != nil {
		t.Fatal(err)
	}
	domainHash := randomHex(t, 32)
	var snapshotID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.exam_snapshots (
    public_id, exam_id, snapshot_sequence, source_artifact_id, import_job_id,
    contract_schema, contract_schema_sha256, domain_hash_protocol, domain_hash,
    exporter_name, exporter_version, exported_at, title, source_url,
    problems_source_count, problems_observed_count, problems_exported_count, problems_pagination_exhausted,
    rankings_source_count, rankings_observed_count, rankings_exported_count, rankings_pagination_exhausted,
    submissions_source_count, submissions_observed_count, submissions_exported_count, submissions_pagination_exhausted,
    participants_exported_count
)
VALUES (
    $1::uuid, $2, $3, $4, $5,
    'ascendany.pintia.snapshot.v2', $6, 'domain_hash_proto_v1', $7,
    'ascendany-pintia-exporter', 'integration', clock_timestamp(),
    'Exam generation integration', 'https://pintia.cn/problem-sets/exam-generation-integration',
    1, 1, 1, true,
    0, 0, 0, true,
    0, 0, 0, true,
    0
)
RETURNING snapshot_id`, randomUUIDv4(t), examID, sequence, artifactID, importJobID,
		randomHex(t, 32), domainHash).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_problems (
    snapshot_id, problem_set_problem_id, problem_id, title, problem_type, max_score
)
VALUES ($1, 'integration-problem', 'integration-problem', 'Integration Problem', 'PROGRAMMING', 100)`, snapshotID); err != nil {
		t.Fatal(err)
	}
	return snapshotID, domainHash
}

func insertExamGeneration(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	examID int64,
	snapshotID int64,
	headRevision int64,
	domainHash string,
) int64 {
	t.Helper()
	manifest, err := analytics.CanonicalManifest(analytics.Manifest{
		Protocol:         analytics.ManifestProtocolV1,
		BaseHeadRevision: 0,
		Target: analytics.ManifestTarget{
			ExamID: examID, SnapshotID: snapshotID, ExamHeadRevision: headRevision,
		},
		Snapshots: []analytics.ManifestSnapshot{{
			ExamID: examID, SnapshotID: snapshotID, DomainHash: domainHash,
		}},
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
VALUES ('queued', NULL, 0, $1, $2, $3, $4::jsonb, $5, 'analytics_v1', $6)
RETURNING analytics_generation_id`, examID, snapshotID, headRevision, string(manifest.Canonical), manifest.SHA256,
		randomHex(t, 32)).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id, exam_id, snapshot_id, domain_hash
)
VALUES ($1, $2, $3, $4)`, generationID, examID, snapshotID, domainHash); err != nil {
		t.Fatal(err)
	}
	insertExamGenerationEvent(t, ctx, tx, generationID, 1, "queued", `{"attemptCount":0}`)
	return generationID
}

func insertExamGenerationEvent(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	generationID int64,
	sequence int64,
	eventType string,
	payload string,
) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_events (
    analytics_generation_id, event_sequence, event_type, payload
)
VALUES ($1, $2, $3, $4::jsonb)`, generationID, sequence, eventType, payload); err != nil {
		t.Fatal(err)
	}
}

func randomUUIDv4(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func randomHex(t *testing.T, bytes int) string {
	t.Helper()
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}
