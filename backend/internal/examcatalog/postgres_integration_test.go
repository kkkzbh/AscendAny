package examcatalog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresCatalogReadsOneImportedSnapshot(t *testing.T) {
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

	principal, found := loadIntegrationPrincipal(t, ctx, pool)
	if !found {
		t.Skip("integration database has no active account session")
	}
	var expectedExamID string
	var expectedProblems int64
	err = pool.QueryRow(ctx, `
SELECT exam.public_id::text, snapshot.problems_exported_count
FROM ascendany.logical_exams AS exam
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = exam.active_snapshot_id
ORDER BY exam.updated_at DESC, exam.exam_id DESC
LIMIT 1`).Scan(&expectedExamID, &expectedProblems)
	if err == pgx.ErrNoRows {
		t.Skip("integration database has no active imported exam")
	}
	if err != nil {
		t.Fatal(err)
	}

	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.List(ctx, ListQuery{Principal: principal, Limit: 1})
	if err != nil {
		t.Fatalf("List() error=%v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != expectedExamID {
		t.Fatalf("page=%#v expected exam=%s", page, expectedExamID)
	}
	detail, found, err := service.Get(ctx, DetailQuery{Principal: principal, ExamID: expectedExamID})
	if err != nil || !found {
		t.Fatalf("Get() found=%t error=%v", found, err)
	}
	if int64(len(detail.Problems)) != expectedProblems || detail.ProblemCount != expectedProblems {
		t.Fatalf("detail problem rows=%d count=%d expected=%d", len(detail.Problems), detail.ProblemCount, expectedProblems)
	}
}

func loadIntegrationPrincipal(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (auth.AccessPrincipal, bool) {
	t.Helper()
	var principal auth.AccessPrincipal
	var role string
	err := pool.QueryRow(ctx, `
SELECT account.public_id::text,
       session.public_id::text,
       account.role,
       account.auth_revision
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.auth_revision = account.auth_revision
WHERE account.disabled_at IS NULL
  AND session.revoked_at IS NULL
  AND session.expires_at > clock_timestamp()
ORDER BY session.session_id DESC
LIMIT 1`).Scan(&principal.AccountID, &principal.SessionID, &role, &principal.AuthRevision)
	if err == pgx.ErrNoRows {
		return auth.AccessPrincipal{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	principal.Role = auth.Role(role)
	principal.JWTID = "99999999-9999-4999-8999-999999999999"
	return principal, true
}
