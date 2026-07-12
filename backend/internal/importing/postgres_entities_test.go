package importing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func TestInsertParticipantStoresRequiredAcceptTimeSeconds(t *testing.T) {
	t.Parallel()

	passed := true
	tx := &scriptedTx{}
	err := insertParticipant(context.Background(), tx, 42, 84, pintia.Participant{
		UserID: "user-100",
		Ranking: &pintia.Ranking{
			Rank: pintia.NewNonNegativeInteger(1),
			ProblemResults: []pintia.RankingProblemResult{
				{
					ProblemSetProblemID: "psp-100",
					Passed:              &passed,
					AcceptTimeSeconds:   pintia.NewNonNegativeInteger(179),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("insertParticipant() error = %v", err)
	}
	if len(tx.execs) != 3 || len(tx.execArgs) != 3 {
		t.Fatalf("executions = %d/%d, want 3/3", len(tx.execs), len(tx.execArgs))
	}
	resultQuery := tx.execs[2]
	if !strings.Contains(resultQuery, "accept_time_seconds") || strings.Contains(resultQuery, "accepted_at") {
		t.Fatalf("ranking result query uses the wrong time field: %s", resultQuery)
	}
	resultArgs := tx.execArgs[2]
	if len(resultArgs) != 7 {
		t.Fatalf("ranking result argument count = %d, want 7", len(resultArgs))
	}
	if got, ok := resultArgs[6].(int64); !ok || got != 179 {
		t.Fatalf("accept_time_seconds argument = %#v, want int64(179)", resultArgs[6])
	}
}

func TestEnsureIdentifierRejectsValueOwnedByOtherActor(t *testing.T) {
	tx := &scriptedTx{
		rows: []pgx.Row{
			rowError{err: pgx.ErrNoRows},
		},
	}
	err := ensureIdentifier(context.Background(), tx, 10, "student_number", "20260001")
	assertImportCode(t, err, ErrorIdentityConflict)
	if !IsPermanent(err) {
		t.Fatalf("identity conflict is not permanent: %v", err)
	}
}

func TestEnsureIdentifierRejectsActorWithDifferentValue(t *testing.T) {
	tx := &scriptedTx{
		rows: []pgx.Row{
			rowScan(func(targets ...any) error {
				*(targets[0].(*string)) = "old-number"
				return nil
			}),
			rowError{err: pgx.ErrNoRows},
		},
	}
	err := ensureIdentifier(context.Background(), tx, 10, "student_number", "new-number")
	assertImportCode(t, err, ErrorIdentityConflict)
	if !IsPermanent(err) {
		t.Fatalf("identity conflict is not permanent: %v", err)
	}
}

type rowScan func(...any) error

func (row rowScan) Scan(targets ...any) error {
	return row(targets...)
}

type rowError struct{ err error }

func (rowError rowError) Scan(...any) error { return rowError.err }

type scriptedTx struct {
	rows       []pgx.Row
	queries    []string
	queryArgs  [][]any
	execs      []string
	execArgs   [][]any
	committed  bool
	rolledBack bool
}

func (tx *scriptedTx) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, query)
	tx.execArgs = append(tx.execArgs, arguments)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (tx *scriptedTx) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	tx.queries = append(tx.queries, query)
	tx.queryArgs = append(tx.queryArgs, arguments)
	if len(tx.rows) == 0 {
		return rowError{err: errors.New("unexpected QueryRow")}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *scriptedTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *scriptedTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}
