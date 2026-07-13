package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRestoredDatabaseConnection struct {
	transaction *fakeRestoredDatabaseTransaction
	options     pgx.TxOptions
}

func (connection *fakeRestoredDatabaseConnection) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	connection.options = options
	return connection.transaction, nil
}

type fakeRestoredDatabaseTransaction struct {
	pgx.Tx
	statement  string
	execError  error
	rolledBack bool
}

type fakeRowTypeACLRow struct {
	violationCount int64
	err            error
}

func (row fakeRowTypeACLRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return errors.New("unexpected scan shape")
	}
	target, ok := destinations[0].(*int64)
	if !ok {
		return errors.New("unexpected scan target")
	}
	*target = row.violationCount
	return nil
}

type fakeRowTypeACLTransaction struct {
	pgx.Tx
	statements     []string
	violationCount int64
	queryError     error
	committed      bool
	rolledBack     bool
}

type failingRecommendationModelQueryer struct {
	err error
}

func (queryer failingRecommendationModelQueryer) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return fakeRowTypeACLRow{err: queryer.err}
}

func (transaction *fakeRowTypeACLTransaction) Exec(
	_ context.Context,
	statement string,
	_ ...any,
) (pgconn.CommandTag, error) {
	transaction.statements = append(transaction.statements, statement)
	return pgconn.CommandTag{}, nil
}

func (transaction *fakeRowTypeACLTransaction) QueryRow(
	_ context.Context,
	statement string,
	_ ...any,
) pgx.Row {
	if statement != dumpOmittedRowTypeACLContractQuery {
		return fakeRowTypeACLRow{err: errors.New("unexpected ACL query")}
	}
	return fakeRowTypeACLRow{
		violationCount: transaction.violationCount,
		err:            transaction.queryError,
	}
}

func (transaction *fakeRowTypeACLTransaction) Commit(context.Context) error {
	transaction.committed = true
	return nil
}

func (transaction *fakeRowTypeACLTransaction) Rollback(context.Context) error {
	transaction.rolledBack = true
	return nil
}

func (transaction *fakeRestoredDatabaseTransaction) Exec(
	_ context.Context,
	statement string,
	_ ...any,
) (pgconn.CommandTag, error) {
	transaction.statement = statement
	return pgconn.CommandTag{}, transaction.execError
}

func (transaction *fakeRestoredDatabaseTransaction) Rollback(context.Context) error {
	transaction.rolledBack = true
	return nil
}

func TestBeginRestoredDatabaseVerificationUsesLocalOwnerRole(t *testing.T) {
	t.Parallel()
	transaction := &fakeRestoredDatabaseTransaction{}
	connection := &fakeRestoredDatabaseConnection{transaction: transaction}
	actual, err := beginRestoredDatabaseVerification(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	if actual != transaction || transaction.statement != "SET LOCAL ROLE ascendany_owner" {
		t.Fatalf("transaction=%T statement=%q", actual, transaction.statement)
	}
	if connection.options.IsoLevel != pgx.RepeatableRead || connection.options.AccessMode != pgx.ReadOnly {
		t.Fatalf("transaction options = %#v", connection.options)
	}
}

func TestReadRecommendationModelDescriptorPreservesDatabaseFailure(t *testing.T) {
	t.Parallel()
	databaseFailure := errors.New("permission denied")
	_, err := readRecommendationModelDescriptor(
		context.Background(),
		failingRecommendationModelQueryer{err: databaseFailure},
	)
	if !errors.Is(err, databaseFailure) ||
		!strings.Contains(err.Error(), "read active recommendation model snapshot") {
		t.Fatalf("error = %v", err)
	}
}

func TestBeginRestoredDatabaseVerificationRollsBackRoleFailure(t *testing.T) {
	t.Parallel()
	transaction := &fakeRestoredDatabaseTransaction{execError: errors.New("permission denied")}
	connection := &fakeRestoredDatabaseConnection{transaction: transaction}
	_, err := beginRestoredDatabaseVerification(context.Background(), connection)
	if err == nil || !strings.Contains(err.Error(), "owner role") || !transaction.rolledBack {
		t.Fatalf("error=%v rolledBack=%v", err, transaction.rolledBack)
	}
}

func TestReconstructDumpOmittedRowTypeACLsUsesExactWritableOwnerTransaction(t *testing.T) {
	t.Parallel()
	transaction := &fakeRowTypeACLTransaction{}
	connectionWithACLTransaction := &fakeRowTypeACLConnection{transaction: transaction}

	err := reconstructDumpOmittedRowTypeACLs(context.Background(), connectionWithACLTransaction)
	if err != nil {
		t.Fatal(err)
	}
	if connectionWithACLTransaction.options.IsoLevel != pgx.RepeatableRead ||
		connectionWithACLTransaction.options.AccessMode != pgx.ReadWrite {
		t.Fatalf("transaction options = %#v", connectionWithACLTransaction.options)
	}
	if len(transaction.statements) != 2 ||
		transaction.statements[0] != "SET LOCAL ROLE ascendany_owner" ||
		transaction.statements[1] != reconstructDumpOmittedRowTypeACLsSQL {
		t.Fatalf("ACL reconstruction statements = %#v", transaction.statements)
	}
	if !transaction.committed {
		t.Fatal("ACL reconstruction transaction was not committed")
	}
}

type fakeRowTypeACLConnection struct {
	transaction *fakeRowTypeACLTransaction
	options     pgx.TxOptions
}

func (connection *fakeRowTypeACLConnection) BeginTx(
	_ context.Context,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	connection.options = options
	return connection.transaction, nil
}

func TestReconstructDumpOmittedRowTypeACLsRejectsContractDifference(t *testing.T) {
	t.Parallel()
	transaction := &fakeRowTypeACLTransaction{violationCount: 3}
	connection := &fakeRowTypeACLConnection{transaction: transaction}

	err := reconstructDumpOmittedRowTypeACLs(context.Background(), connection)
	if err == nil || !strings.Contains(err.Error(), "3 differences") {
		t.Fatalf("error = %v", err)
	}
	if transaction.committed || !transaction.rolledBack {
		t.Fatalf("committed=%v rolledBack=%v", transaction.committed, transaction.rolledBack)
	}
}

func TestDumpOmittedRowTypeACLContractTargetsOnlyRelationRowTypes(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		"JOIN pg_class AS relation ON relation.oid = type.typrelid",
		"type.typelem = 0",
		"relation.relkind <> 'c'",
	} {
		if !strings.Contains(dumpOmittedRowTypeACLContractQuery, required) ||
			!strings.Contains(reconstructDumpOmittedRowTypeACLsSQL, required) {
			t.Fatalf("row-type ACL SQL is missing exact boundary %q", required)
		}
	}
	if !strings.Contains(
		dumpOmittedRowTypeACLContractQuery,
		"ARRAY['ascendany_owner', 'ascendany_runtime', 'ascendany_backup']",
	) || !strings.Contains(
		reconstructDumpOmittedRowTypeACLsSQL,
		"GRANT USAGE ON TYPE %I.%I TO ascendany_owner, ascendany_runtime, ascendany_backup",
	) {
		t.Fatal("row-type ACL SQL does not encode the exact owner/runtime/backup grant set")
	}
}
