package migrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresImportLifecycleCannotBeBypassed(t *testing.T) {
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

	digest := randomHex(t, 32)
	var artifactID int64
	err = pool.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/vnd.ascendany.pintia.snapshot.v2+json', 'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, digest).Scan(&artifactID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, `
INSERT INTO ascendany.import_jobs (
    public_id, artifact_id, job_kind, status, stage, attempt_count,
    lease_owner, lease_expires_at, started_at
)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'running', 'validating', 1,
        'bypass', clock_timestamp() + interval '1 minute', clock_timestamp())`, randomUUID(t), artifactID)
	assertPostgresCode(t, err, "23514")

	var jobID int64
	err = pool.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (public_id, artifact_id, job_kind, status, stage)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, randomUUID(t), artifactID).Scan(&jobID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'failed', stage = 'failed', error_code = 'bypass',
    error_detail = 'bypass', error_permanent = true, finished_at = clock_timestamp()
WHERE import_job_id = $1`, jobID)
	assertPostgresCode(t, err, "40001")

	commandTag, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'running', stage = 'validating', attempt_count = attempt_count + 1,
    lease_owner = 'lifecycle-test', lease_expires_at = clock_timestamp() + interval '1 minute',
    started_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE import_job_id = $1`, jobID)
	if err != nil || commandTag.RowsAffected() != 1 {
		t.Fatalf("claim legal queued job: rows=%d error=%v", commandTag.RowsAffected(), err)
	}

	_, err = pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'analyzing', lease_owner = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE import_job_id = $1`, jobID)
	assertPostgresCode(t, err, "40001")

	commandTag, err = pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'failed', stage = 'failed', lease_owner = NULL, lease_expires_at = NULL,
    error_code = 'test_failure', error_detail = 'terminal lifecycle fixture',
    error_permanent = true, finished_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE import_job_id = $1`, jobID)
	if err != nil || commandTag.RowsAffected() != 1 {
		t.Fatalf("complete legal active job: rows=%d error=%v", commandTag.RowsAffected(), err)
	}

	_, err = pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET updated_at = clock_timestamp()
WHERE import_job_id = $1`, jobID)
	assertPostgresCode(t, err, "55000")
}

func TestPostgresAchievementRuleVersionsAreAppendOnly(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_MIGRATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_MIGRATE_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	configuration, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	configuration.Password = "local-rehearsal-password"
	connection, err := pgx.ConnectConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	if _, err := connection.Exec(ctx, `SET ROLE ascendany_owner`); err != nil {
		t.Fatal(err)
	}

	_, err = connection.Exec(ctx, `
UPDATE ascendany.achievement_rules
SET title = title
WHERE achievement_code = 'exam_count_first'`)
	assertPostgresCode(t, err, "55000")

	_, err = connection.Exec(ctx, `
DELETE FROM ascendany.achievement_rule_head
WHERE singleton`)
	assertPostgresCode(t, err, "55000")

	_, err = connection.Exec(ctx, `
UPDATE ascendany.achievement_rule_head
SET head_revision = head_revision + 2,
    updated_at = clock_timestamp()
WHERE singleton`)
	assertPostgresCode(t, err, "40001")
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}

func randomUUID(t *testing.T) string {
	t.Helper()
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func assertPostgresCode(t *testing.T, err error, want string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != want {
		t.Fatalf("PostgreSQL error = %v, want SQLSTATE %s", err, want)
	}
}
