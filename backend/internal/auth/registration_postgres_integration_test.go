package auth

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

type postgresRegistrationOutcome struct {
	result RegisterStudentResult
	err    error
}

func TestPostgresRegistrationSerializesCurrentNicknameAndPersistsOwnerProjection(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	poolConfig.ConnConfig.StatementCacheCapacity = 0
	poolConfig.ConnConfig.DescriptionCacheCapacity = 0
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	var accountCount, examCount int64
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.auth_accounts),
       (SELECT count(*) FROM ascendany.logical_exams)`).Scan(&accountCount, &examCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 || examCount != 0 {
		t.Fatalf("registration integration database is not empty: accounts=%d exams=%d", accountCount, examCount)
	}

	examID, oldSnapshotID, currentSnapshotID := seedPostgresRegistrationSnapshots(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	legacy := testRegisterStudentCommand(now, "registration-student", "Legacy Real Name")
	legacyResult, err := repository.RegisterStudent(ctx, legacy)
	if err != nil || legacyResult.Status != RegistrationIdentityUnavailable {
		t.Fatalf("legacy exporter registration result/error = %#v/%v", legacyResult, err)
	}

	publisher, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publisherOpen := true
	defer func() {
		if publisherOpen {
			_ = publisher.Rollback(context.Background())
		}
	}()
	if _, err := publisher.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pintia.ParticipantIdentityAdvisoryLockID); err != nil {
		t.Fatal(err)
	}

	command := testRegisterStudentCommand(now, "registration-student", "Current Nickname")
	registration := make(chan postgresRegistrationOutcome, 1)
	go func() {
		result, registerErr := repository.RegisterStudent(ctx, command)
		registration <- postgresRegistrationOutcome{result: result, err: registerErr}
	}()
	waitForPostgresAdvisoryWaiter(t, ctx, pool)
	commandTag, err := publisher.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2,
    head_revision = 2,
    updated_at = clock_timestamp()
WHERE exam_id = $1
  AND active_snapshot_id = $3
  AND head_revision = 1`, examID, currentSnapshotID, oldSnapshotID)
	if err != nil || commandTag.RowsAffected() != 1 {
		t.Fatalf("publish current registration identity: tag=%s error=%v", commandTag, err)
	}
	if err := publisher.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	publisherOpen = false

	outcome := <-registration
	if outcome.err != nil || outcome.result.Status != StudentRegistered {
		t.Fatalf("serialized registration result/error = %#v/%v", outcome.result, outcome.err)
	}
	if outcome.result.Account.PTANickname == nil || *outcome.result.Account.PTANickname != "Current Nickname" {
		t.Fatalf("registered account = %#v", outcome.result.Account)
	}

	var storedNickname *string
	if err := pool.QueryRow(ctx, `
SELECT pta_nickname
FROM ascendany.auth_accounts
WHERE public_id = $1::uuid`, command.Account.ID).Scan(&storedNickname); err != nil {
		t.Fatal(err)
	}
	if storedNickname == nil || *storedNickname != "Current Nickname" {
		t.Fatalf("stored PTA nickname = %v", storedNickname)
	}
	loginAccount, found, err := repository.FindLoginAccount(ctx, command.Account.Username)
	if err != nil || !found || loginAccount.PTANickname == nil || *loginAccount.PTANickname != "Current Nickname" {
		t.Fatalf("login projection/found/error = %#v/%t/%v", loginAccount, found, err)
	}
	principal, err := repository.LoadPrincipal(ctx, command.Account.ID, command.SessionID, now)
	if err != nil || !principal.Found || principal.Account.PTANickname == nil || *principal.Account.PTANickname != "Current Nickname" {
		t.Fatalf("principal projection/error = %#v/%v", principal, err)
	}
	decision, err := repository.TransactRefresh(ctx, command.RefreshToken.ID, now, func(snapshot RefreshSnapshot) RefreshDecision {
		if !snapshot.Found || snapshot.Account.PTANickname == nil || *snapshot.Account.PTANickname != "Current Nickname" {
			t.Fatalf("refresh projection = %#v", snapshot)
		}
		return RefreshDecision{Kind: RefreshReject}
	})
	if err != nil || decision != RefreshReject {
		t.Fatalf("refresh decision/error = %d/%v", decision, err)
	}
}

func seedPostgresRegistrationSnapshots(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (examID, oldSnapshotID, currentSnapshotID int64) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var actorID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ('registration-user')
RETURNING actor_id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', 'registration-student', $1)`, actorID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ('51000000-0000-4000-8000-000000000001'::uuid, 'pintia', 'registration-exam')
RETURNING exam_id`).Scan(&examID); err != nil {
		t.Fatal(err)
	}

	for index, snapshot := range []struct {
		publicID        string
		artifactSHA     string
		artifactKey     string
		jobPublicID     string
		domainHash      string
		exporterVersion string
		nickname        string
	}{
		{
			publicID: "52000000-0000-4000-8000-000000000001", artifactSHA: strings.Repeat("a", 64),
			artifactKey: "sha256/aa/" + strings.Repeat("a", 64), jobPublicID: "53000000-0000-4000-8000-000000000001",
			domainHash: strings.Repeat("c", 64), exporterVersion: "2.2.2", nickname: "Legacy Real Name",
		},
		{
			publicID: "52000000-0000-4000-8000-000000000002", artifactSHA: strings.Repeat("b", 64),
			artifactKey: "sha256/bb/" + strings.Repeat("b", 64), jobPublicID: "53000000-0000-4000-8000-000000000002",
			domainHash: strings.Repeat("d", 64), exporterVersion: "2.2.3", nickname: "Current Nickname",
		},
	} {
		var artifactID, jobID, snapshotID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/vnd.ascendany.pintia.snapshot.v2+json', $2)
RETURNING artifact_id`, snapshot.artifactSHA, snapshot.artifactKey).Scan(&artifactID); err != nil {
			t.Fatalf("insert registration artifact %d: %v", index, err)
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (public_id, artifact_id, job_kind, status, stage)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, snapshot.jobPublicID, artifactID).Scan(&jobID); err != nil {
			t.Fatalf("insert registration job %d: %v", index, err)
		}
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
    $8, $9, clock_timestamp(), 'Registration Exam', 'https://pintia.cn/problem-sets/registration-exam',
    1, 1, 1, true,
    0, 0, 0, true,
    0, 0, 0, true,
    1
)
RETURNING snapshot_id`,
			snapshot.publicID, examID, index+1, artifactID, jobID, pintia.ExpectedSchemaSHA256,
			snapshot.domainHash, pintia.ExporterName, snapshot.exporterVersion,
		).Scan(&snapshotID); err != nil {
			t.Fatalf("insert registration snapshot %d: %v", index, err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_participants (
    snapshot_id, actor_id, student_number, display_name
)
VALUES ($1, $2, 'registration-student', $3)`, snapshotID, actorID, snapshot.nickname); err != nil {
			t.Fatalf("insert registration participant %d: %v", index, err)
		}
		if index == 0 {
			oldSnapshotID = snapshotID
		} else {
			currentSnapshotID = snapshotID
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2,
    head_revision = 1,
    updated_at = clock_timestamp()
WHERE exam_id = $1`, examID, oldSnapshotID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return examID, oldSnapshotID, currentSnapshotID
}
