package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type refreshSnapshotRow struct {
	now          time.Time
	secretDigest [32]byte
	csrfDigest   [32]byte
}

func (r refreshSnapshotRow) Scan(destinations ...any) error {
	if len(destinations) != 23 {
		return errors.New("unexpected refresh snapshot destination count")
	}
	studentNumber := "20260001"
	ptaNickname := "Student"
	usedAt := r.now.Add(-time.Minute)
	*destinations[0].(*int64) = 31
	*destinations[1].(*string) = "123e4567-e89b-42d3-a456-426614174010"
	*destinations[2].(*[]byte) = append([]byte(nil), r.secretDigest[:]...)
	*destinations[3].(*[]byte) = append([]byte(nil), r.csrfDigest[:]...)
	*destinations[4].(*time.Time) = r.now.Add(time.Hour)
	*destinations[5].(**time.Time) = &usedAt
	*destinations[6].(**time.Time) = nil
	*destinations[7].(*int64) = 41
	*destinations[8].(*int64) = 51
	*destinations[9].(*string) = testSessionID
	*destinations[10].(*string) = testAccountID
	*destinations[11].(*int64) = 1
	*destinations[12].(*time.Time) = r.now.Add(-time.Hour)
	*destinations[13].(*time.Time) = r.now.Add(time.Hour)
	*destinations[14].(*time.Time) = r.now.Add(-time.Minute)
	*destinations[15].(**time.Time) = nil
	*destinations[16].(*string) = "student_1"
	*destinations[17].(*string) = "Student"
	*destinations[18].(**string) = &studentNumber
	*destinations[19].(**string) = &ptaNickname
	*destinations[20].(*Role) = RoleStudent
	*destinations[21].(*int64) = 1
	*destinations[22].(**time.Time) = nil
	return nil
}

type scriptedPostgresTx struct {
	row        pgx.Row
	executed   []string
	committed  bool
	rolledBack bool
}

type boolRow bool

func (row boolRow) Scan(destinations ...any) error {
	*destinations[0].(*bool) = bool(row)
	return nil
}

type int64Row int64

func (row int64Row) Scan(destinations ...any) error {
	*destinations[0].(*int64) = int64(row)
	return nil
}

type bootstrapPostgresTx struct {
	queryCount int
	executed   []string
	committed  bool
}

func (tx *bootstrapPostgresTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	tx.queryCount++
	if tx.queryCount == 1 {
		return boolRow(false)
	}
	return int64Row(71)
}

func (tx *bootstrapPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.executed = append(tx.executed, sql)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (tx *bootstrapPostgresTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *bootstrapPostgresTx) Rollback(context.Context) error { return nil }

func (tx *scriptedPostgresTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return tx.row
}

func (tx *scriptedPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.executed = append(tx.executed, sql)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (tx *scriptedPostgresTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *scriptedPostgresTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

func TestPostgresRefreshReuseIsCommittedTransactionOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	tx := &scriptedPostgresTx{
		row: refreshSnapshotRow{
			now:          now,
			secretDigest: sha256.Sum256([]byte("secret")),
			csrfDigest:   sha256.Sum256([]byte("csrf")),
		},
	}
	repository, err := newPostgresRepository(func(context.Context) (postgresTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	decision, err := repository.TransactRefresh(
		context.Background(),
		"123e4567-e89b-42d3-a456-426614174010",
		now,
		func(snapshot RefreshSnapshot) RefreshDecision {
			if !snapshot.Found || snapshot.UsedAt == nil {
				t.Fatal("repository did not expose the locked used-token state")
			}
			return RefreshDecision{Kind: RefreshRevokeReuse}
		},
	)
	if err != nil || decision != RefreshRevokeReuse {
		t.Fatalf("reuse outcome failed: decision=%v err=%v", decision, err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("reuse revocation did not commit: committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
	joined := strings.Join(tx.executed, "\n")
	for _, table := range []string{"auth_sessions", "auth_refresh_tokens", "audit_events"} {
		if !strings.Contains(joined, table) {
			t.Fatalf("reuse transaction did not mutate %s: %s", table, joined)
		}
	}
}

func TestPostgresAdminBootstrapUsesSerializedOneShotTransaction(t *testing.T) {
	t.Parallel()
	tx := &bootstrapPostgresTx{}
	repository, err := newPostgresRepository(func(context.Context) (postgresTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.BootstrapFirstAdmin(context.Background(), AdminBootstrapCommand{
		Account: AccountRecord{
			Account: Account{
				ID:            testAccountID,
				Username:      "admin_1",
				DisplayName:   "Administrator",
				StudentNumber: nil,
				Role:          RoleAdmin,
				AuthRevision:  1,
			},
			PasswordPHC: "$argon2id$v=19$m=19456,t=2,p=1$aaaaaaaaaaaaaaaaaaaaaa$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Now: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil || result.Status != AdminBootstrapCreated || !tx.committed {
		t.Fatalf("result = %#v, committed=%v, err=%v", result, tx.committed, err)
	}
	joined := strings.Join(tx.executed, "\n")
	if !strings.Contains(joined, "pg_advisory_xact_lock") || !strings.Contains(joined, "audit_events") {
		t.Fatalf("bootstrap transaction missing lock or audit: %s", joined)
	}
}

func TestDatabaseFailurePreservesContextErrorCode(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if code := ErrorCodeOf(databaseFailure("wait for enrollment lock", cause)); code != ErrorCanceled {
			t.Fatalf("database cancellation code = %q for %v", code, cause)
		}
	}
	if code := ErrorCodeOf(databaseFailure("insert enrollment", errors.New("storage error"))); code != ErrorDatabase {
		t.Fatalf("ordinary database failure code = %q", code)
	}
}
