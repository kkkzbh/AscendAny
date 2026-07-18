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

type participantIdentityLockTx struct {
	queries   []string
	arguments [][]any
	err       error
}

func (tx *participantIdentityLockTx) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	tx.queries = append(tx.queries, query)
	tx.arguments = append(tx.arguments, append([]any(nil), arguments...))
	if tx.err != nil {
		return pgconn.CommandTag{}, tx.err
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (*participantIdentityLockTx) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("unexpected query row")
}

func (*participantIdentityLockTx) Commit(context.Context) error   { panic("unexpected commit") }
func (*participantIdentityLockTx) Rollback(context.Context) error { panic("unexpected rollback") }

func TestParticipantIdentityPublicationUsesSharedTransactionLock(t *testing.T) {
	t.Parallel()

	tx := &participantIdentityLockTx{}
	if err := lockParticipantIdentityPublication(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.queries) != 1 || tx.queries[0] != `SELECT pg_advisory_xact_lock($1)` || len(tx.arguments[0]) != 1 ||
		tx.arguments[0][0] != pintia.ParticipantIdentityAdvisoryLockID {
		t.Fatalf("queries/arguments = %#v/%#v", tx.queries, tx.arguments)
	}

	tx.err = errors.New("lock failed")
	err := lockParticipantIdentityPublication(context.Background(), tx)
	code, _ := CodeOf(err)
	if code != ErrorDatabase {
		t.Fatalf("lock failure = %v", err)
	}
}

func TestExamHeadPublicationLocksParticipantIdentityBeforeCompareAndSwap(t *testing.T) {
	t.Parallel()

	tx := &participantIdentityLockTx{}
	if err := publishExamHead(context.Background(), tx, lockedExam{ID: 7, HeadRevision: 3}, 11, 4); err != nil {
		t.Fatal(err)
	}
	if len(tx.queries) != 2 || tx.queries[0] != `SELECT pg_advisory_xact_lock($1)` ||
		!strings.Contains(tx.queries[1], "UPDATE ascendany.logical_exams") ||
		len(tx.arguments[0]) != 1 || tx.arguments[0][0] != pintia.ParticipantIdentityAdvisoryLockID {
		t.Fatalf("queries/arguments = %#v/%#v", tx.queries, tx.arguments)
	}
}
