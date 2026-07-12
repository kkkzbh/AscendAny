package administration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type administrationTestTx struct {
	commitErr     error
	commitCalls   int
	rollbackCalls int
}

func (*administrationTestTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (*administrationTestTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (*administrationTestTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return administrationTestRow(func(...any) error { return errors.New("unexpected QueryRow") })
}

func (tx *administrationTestTx) Commit(context.Context) error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *administrationTestTx) Rollback(context.Context) error {
	tx.rollbackCalls++
	return nil
}

func TestPostgresWriteTransactionRetriesSerializationAndDeadlockConflicts(t *testing.T) {
	t.Parallel()
	for _, sqlState := range []string{"40001", "40P01"} {
		sqlState := sqlState
		t.Run(sqlState, func(t *testing.T) {
			t.Parallel()
			transactions := make([]*administrationTestTx, 0, 2)
			options := make([]pgx.TxOptions, 0, 2)
			repository, err := newPostgresRepository(func(_ context.Context, value pgx.TxOptions) (postgresTx, error) {
				options = append(options, value)
				tx := &administrationTestTx{}
				if len(transactions) == 0 {
					tx.commitErr = &pgconn.PgError{Code: sqlState}
				}
				transactions = append(transactions, tx)
				return tx, nil
			})
			if err != nil {
				t.Fatal(err)
			}

			runs := 0
			err = repository.transaction(context.Background(), "test administration mutation", false, func(postgresTx) error {
				runs++
				return nil
			})
			if err != nil {
				t.Fatalf("transaction() error = %v", err)
			}
			if runs != 2 || len(transactions) != 2 || len(options) != 2 {
				t.Fatalf("runs=%d transactions=%d options=%d", runs, len(transactions), len(options))
			}
			for index, option := range options {
				if option.IsoLevel != pgx.ReadCommitted || option.AccessMode != pgx.ReadWrite {
					t.Fatalf("options[%d]=%#v", index, option)
				}
			}
			if transactions[0].commitCalls != 1 || transactions[0].rollbackCalls != 1 {
				t.Fatalf("first transaction commits=%d rollbacks=%d", transactions[0].commitCalls, transactions[0].rollbackCalls)
			}
			if transactions[1].commitCalls != 1 || transactions[1].rollbackCalls != 0 {
				t.Fatalf("second transaction commits=%d rollbacks=%d", transactions[1].commitCalls, transactions[1].rollbackCalls)
			}
		})
	}
}

func TestPostgresWriteTransactionStopsAfterThreeConflicts(t *testing.T) {
	t.Parallel()
	for _, sqlState := range []string{"40001", "40P01"} {
		sqlState := sqlState
		t.Run(sqlState, func(t *testing.T) {
			t.Parallel()
			transactions := make([]*administrationTestTx, 0, 3)
			repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (postgresTx, error) {
				tx := &administrationTestTx{commitErr: &pgconn.PgError{Code: sqlState}}
				transactions = append(transactions, tx)
				return tx, nil
			})
			if err != nil {
				t.Fatal(err)
			}

			runs := 0
			err = repository.transaction(context.Background(), "test bounded administration mutation", false, func(postgresTx) error {
				runs++
				return nil
			})
			if CodeOf(err) != ErrorConcurrentMutation || runs != 3 || len(transactions) != 3 {
				t.Fatalf("error=%v code=%q runs=%d transactions=%d", err, CodeOf(err), runs, len(transactions))
			}
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != sqlState {
				t.Fatalf("terminal error lost SQLSTATE: %v", err)
			}
			for index, tx := range transactions {
				if tx.commitCalls != 1 || tx.rollbackCalls != 1 {
					t.Fatalf("transactions[%d] commits=%d rollbacks=%d", index, tx.commitCalls, tx.rollbackCalls)
				}
			}
		})
	}
}

type administrationTestRow func(...any) error

func (row administrationTestRow) Scan(destinations ...any) error { return row(destinations...) }

type administrationLockRows struct {
	remaining int
	closed    bool
}

func (rows *administrationLockRows) Close() { rows.closed = true }

func (*administrationLockRows) Err() error { return nil }

func (*administrationLockRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (*administrationLockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (*administrationLockRows) Scan(...any) error { return errors.New("unused") }

func (*administrationLockRows) Values() ([]any, error) { return nil, errors.New("unused") }

func (*administrationLockRows) RawValues() [][]byte { return nil }

func (*administrationLockRows) Conn() *pgx.Conn { return nil }

func (rows *administrationLockRows) Next() bool {
	if rows.closed || rows.remaining == 0 {
		return false
	}
	rows.remaining--
	return true
}

type administrationLockTx struct {
	events        []string
	accountQuery  string
	accountArgs   []any
	principalRows int
	lockRows      *administrationLockRows
}

func (*administrationLockTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (tx *administrationLockTx) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	tx.events = append(tx.events, "account-lock")
	tx.accountQuery = query
	tx.accountArgs = append([]any(nil), arguments...)
	tx.lockRows = &administrationLockRows{remaining: 2}
	return tx.lockRows, nil
}

func (tx *administrationLockTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "JOIN ascendany.auth_sessions"):
		tx.principalRows++
		tx.events = append(tx.events, "principal")
		return validAdministrationPrincipalRow()
	case strings.Contains(query, "WHERE public_id = $1::uuid"):
		tx.events = append(tx.events, "target")
		return administrationTestRow(func(destinations ...any) error {
			*(destinations[0].(*int64)) = 22
			return nil
		})
	case strings.Contains(query, "FROM ascendany.auth_sessions") && strings.Contains(query, "FOR UPDATE"):
		tx.events = append(tx.events, "session-lock")
		return administrationTestRow(func(destinations ...any) error {
			*(destinations[0].(*int64)) = 33
			return nil
		})
	default:
		return administrationTestRow(func(...any) error { return errors.New("unexpected QueryRow") })
	}
}

func (*administrationLockTx) Commit(context.Context) error { return nil }

func (*administrationLockTx) Rollback(context.Context) error { return nil }

func TestLockAdministrationMutationLocksAccountsInDatabaseOrderBeforeSession(t *testing.T) {
	t.Parallel()
	tx := &administrationLockTx{}
	resolved, err := lockAdministrationMutation(context.Background(), tx, testPrincipal(), testTargetID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AccountDatabaseID != 11 || resolved.SessionDatabaseID != 33 || tx.principalRows != 2 {
		t.Fatalf("resolved=%#v principalRows=%d", resolved, tx.principalRows)
	}
	wantEvents := []string{"principal", "target", "account-lock", "session-lock", "principal"}
	if strings.Join(tx.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events=%v want=%v", tx.events, wantEvents)
	}
	if !strings.Contains(tx.accountQuery, "ORDER BY account_id") || !strings.Contains(tx.accountQuery, "FOR UPDATE") {
		t.Fatalf("account lock query lacks deterministic order: %s", tx.accountQuery)
	}
	if len(tx.accountArgs) != 2 || tx.accountArgs[0] != int64(11) || tx.accountArgs[1] != int64(22) {
		t.Fatalf("account lock args=%#v", tx.accountArgs)
	}
	if tx.lockRows == nil || !tx.lockRows.closed {
		t.Fatalf("account lock rows were not closed: %#v", tx.lockRows)
	}
}

func validAdministrationPrincipalRow() pgx.Row {
	return administrationTestRow(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testPrincipal().AccountID
		*(destinations[2].(*string)) = string(auth.RoleAdmin)
		*(destinations[3].(*int64)) = testPrincipal().AuthRevision
		*(destinations[4].(**int64)) = nil
		*(destinations[5].(**string)) = nil
		*(destinations[6].(*int64)) = 33
		*(destinations[7].(*string)) = testPrincipal().SessionID
		*(destinations[8].(*int64)) = testPrincipal().AuthRevision
		*(destinations[9].(**int64)) = nil
		*(destinations[10].(**string)) = nil
		return nil
	})
}
