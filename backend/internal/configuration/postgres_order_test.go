package configuration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type configurationRowFunc func(...any) error

func (row configurationRowFunc) Scan(destinations ...any) error {
	return row(destinations...)
}

type publicationOrderTx struct {
	queryRowCount int
	committed     bool
	rolledBack    bool
}

func (tx *publicationOrderTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	tx.queryRowCount++
	if tx.queryRowCount != 1 {
		return configurationRowFunc(func(...any) error {
			return errors.New("configuration storage was reached before the publication precondition succeeded")
		})
	}
	return configurationRowFunc(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testConfigurationAccountID
		*(destinations[2].(*string)) = string(auth.RoleAdmin)
		*(destinations[3].(*int64)) = 3
		*(destinations[4].(**int64)) = nil
		*(destinations[5].(**string)) = nil
		*(destinations[6].(*int64)) = 33
		*(destinations[7].(*string)) = testConfigurationSessionID
		*(destinations[8].(*int64)) = 3
		*(destinations[9].(**int64)) = nil
		*(destinations[10].(**string)) = nil
		return nil
	})
}

func (*publicationOrderTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (*publicationOrderTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected mutation")
}

func (tx *publicationOrderTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *publicationOrderTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

type rejectingPublicationPrecondition struct {
	called bool
}

func (precondition *rejectingPublicationPrecondition) ValidateVersionWrite(context.Context, VersionWriteTransaction, CreateVersionCommand) error {
	precondition.called = true
	return configurationError(ErrorDocumentConflict, "validate publication order", errors.New("publication provenance changed"))
}

const (
	testConfigurationAccountID = "123e4567-e89b-42d3-a456-426614174000"
	testConfigurationSessionID = "123e4567-e89b-42d3-a456-426614174001"
	testConfigurationJWTID     = "123e4567-e89b-42d3-a456-426614174002"
)

func TestPostgresValidatesPublicationBeforeAllocatingConfigurationIdentity(t *testing.T) {
	t.Parallel()
	tx := &publicationOrderTx{}
	precondition := &rejectingPublicationPrecondition{}
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (postgresTx, error) {
		return tx, nil
	}, precondition)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repository.StoreVersion(context.Background(), CreateVersionCommand{
		Principal: auth.AccessPrincipal{
			AccountID:    testConfigurationAccountID,
			SessionID:    testConfigurationSessionID,
			JWTID:        testConfigurationJWTID,
			Role:         auth.RoleAdmin,
			AuthRevision: 3,
		},
		Key:      "test.prompt.publication-order",
		Kind:     KindPrompt,
		SchemaID: "ascendany.prompt.v1",
		Document: []byte(`{"value":1}`),
	}, "08f271887ce94707da822d5263bae19d5519cb3614fb3bd89123798bbf7d7a2e")
	if CodeOf(err) != ErrorDocumentConflict {
		t.Fatalf("StoreVersion() error=%v code=%q", err, CodeOf(err))
	}
	if !precondition.called || tx.queryRowCount != 1 || tx.committed || !tx.rolledBack {
		t.Fatalf("preconditionCalled=%t queryRows=%d committed=%t rolledBack=%t", precondition.called, tx.queryRowCount, tx.committed, tx.rolledBack)
	}
}
