package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type managementPrincipalRow struct{ now time.Time }

func (row managementPrincipalRow) Scan(destinations ...any) error {
	if len(destinations) != 15 {
		return errors.New("unexpected management principal destination count")
	}
	studentNumber := "20260001"
	*destinations[0].(*int64) = 11
	*destinations[1].(*string) = testAccountID
	*destinations[2].(*string) = "student_1"
	*destinations[3].(*string) = "Student One"
	*destinations[4].(**string) = &studentNumber
	*destinations[5].(*Role) = RoleStudent
	*destinations[6].(*int64) = 3
	*destinations[7].(**time.Time) = nil
	*destinations[8].(*int64) = 21
	*destinations[9].(*string) = testSessionID
	*destinations[10].(*int64) = 3
	*destinations[11].(*time.Time) = row.now.Add(-time.Hour)
	*destinations[12].(*time.Time) = row.now.Add(time.Hour)
	*destinations[13].(*time.Time) = row.now.Add(-time.Minute)
	*destinations[14].(**time.Time) = nil
	return nil
}

type stringRow string

func (row stringRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected string destination count")
	}
	*destinations[0].(*string) = string(row)
	return nil
}

type targetSessionRow struct {
	id      int64
	revoked *time.Time
}

func (row targetSessionRow) Scan(destinations ...any) error {
	if len(destinations) != 2 {
		return errors.New("unexpected target session destination count")
	}
	*destinations[0].(*int64) = row.id
	*destinations[1].(**time.Time) = row.revoked
	return nil
}

type sequencePostgresTx struct {
	rows       []pgx.Row
	queryIndex int
	executed   []string
	committed  bool
}

type managementErrorRow struct{ err error }

func (row managementErrorRow) Scan(...any) error { return row.err }

func (tx *sequencePostgresTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	if tx.queryIndex >= len(tx.rows) {
		return managementErrorRow{err: errors.New("unexpected query")}
	}
	row := tx.rows[tx.queryIndex]
	tx.queryIndex++
	return row
}

func (tx *sequencePostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.executed = append(tx.executed, sql)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (tx *sequencePostgresTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *sequencePostgresTx) Rollback(context.Context) error { return nil }

func TestPostgresAccountManagementMutationsAreAuditedTransactions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 5, 0, 0, 0, time.UTC)
	authenticated := testAuthenticatedAccount()
	targetID := "123e4567-e89b-42d3-a456-426614174088"
	tests := []struct {
		name   string
		rows   []pgx.Row
		run    func(*PostgresRepository) error
		tables []string
	}{
		{
			name: "profile",
			rows: []pgx.Row{managementPrincipalRow{now: now}},
			run: func(repository *PostgresRepository) error {
				result, err := repository.UpdateProfile(context.Background(), UpdateProfileCommand{
					Authenticated: authenticated,
					DisplayName:   "Updated Student",
					Now:           now,
				})
				if err == nil && (result.Status != AccountMutationApplied || result.Account.DisplayName != "Updated Student") {
					return errors.New("profile result is invalid")
				}
				return err
			},
			tables: []string{"auth_accounts", "audit_events"},
		},
		{
			name: "revoke_session",
			rows: []pgx.Row{managementPrincipalRow{now: now}, targetSessionRow{id: 31}},
			run: func(repository *PostgresRepository) error {
				status, err := repository.RevokeSession(context.Background(), RevokeSessionCommand{
					Authenticated: authenticated,
					TargetID:      targetID,
					Now:           now,
				})
				if err == nil && status != AccountMutationApplied {
					return errors.New("session revocation result is invalid")
				}
				return err
			},
			tables: []string{"auth_sessions", "auth_refresh_tokens", "audit_events"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tx := &sequencePostgresTx{rows: test.rows}
			repository, err := newPostgresRepository(func(context.Context) (postgresTx, error) { return tx, nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := test.run(repository); err != nil {
				t.Fatal(err)
			}
			if !tx.committed {
				t.Fatal("account-management mutation did not commit")
			}
			joined := strings.Join(tx.executed, "\n")
			for _, table := range test.tables {
				if !strings.Contains(joined, table) {
					t.Fatalf("transaction did not mutate %s: %s", table, joined)
				}
			}
		})
	}
}

func TestPostgresAccountManagementSessionListIsStrictAndCurrent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 5, 0, 0, 0, time.UTC)
	encoded := `[{"id":"` + testSessionID + `","createdAt":"2026-07-11T04:00:00Z","expiresAt":"2026-07-11T06:00:00Z","lastSeenAt":"2026-07-11T04:59:00Z","revokedAt":null,"revocationReason":null}]`
	tx := &sequencePostgresTx{rows: []pgx.Row{managementPrincipalRow{now: now}, stringRow(encoded)}}
	repository, err := newPostgresRepository(func(context.Context) (postgresTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.ListSessions(context.Background(), ListSessionsQuery{
		Authenticated: testAuthenticatedAccount(),
		Now:           now,
		Limit:         MaxListedSessions,
	})
	if err != nil || result.Status != AccountMutationApplied || len(result.Sessions) != 1 ||
		!result.Sessions[0].Current || !result.Sessions[0].Active || !tx.committed {
		t.Fatalf("ListSessions() result=%#v committed=%v error=%v", result, tx.committed, err)
	}
}

func TestDecodeManagedSessionsRejectsMissingCurrentAndUnknownFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 5, 0, 0, 0, time.UTC)
	otherID := "123e4567-e89b-42d3-a456-426614174088"
	base := `[{"id":"` + otherID + `","createdAt":"2026-07-11T04:00:00Z","expiresAt":"2026-07-11T06:00:00Z","lastSeenAt":"2026-07-11T04:59:00Z","revokedAt":null,"revocationReason":null%s}]`
	for _, encoded := range []string{
		fmt.Sprintf(base, ""),
		fmt.Sprintf(base, `,"unexpected":true`),
	} {
		if _, err := decodeManagedSessions([]byte(encoded), testSessionID, now); err == nil {
			t.Fatalf("invalid stored sessions were accepted: %s", encoded)
		}
	}
}
