package modelrelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
)

func TestBindUsesReadCommittedTransaction(t *testing.T) {
	t.Parallel()
	beginErr := errors.New("begin stopped after recording options")
	beginner := &recordingBeginner{err: beginErr}
	repository, err := NewRepository(beginner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Bind(context.Background(), testLoadedModel(t), ApplicationIdentity{
		Version: "0.2.0", Commit: strings.Repeat("a", 40), BuildTime: "2026-07-13T12:00:00Z",
	})
	if !errors.Is(err, beginErr) {
		t.Fatalf("Bind() error = %v", err)
	}
	if beginner.calls != 1 || beginner.options.IsoLevel != pgx.ReadCommitted {
		t.Fatalf("BeginTx() calls = %d, options = %#v", beginner.calls, beginner.options)
	}
}

func TestRequireCurrentUsesReadOnlyRepeatableReadTransaction(t *testing.T) {
	t.Parallel()
	beginErr := errors.New("begin stopped after recording options")
	beginner := &recordingBeginner{err: beginErr}
	repository, err := NewRepository(beginner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.RequireCurrent(context.Background(), testLoadedModel(t), ApplicationIdentity{
		Version: "0.2.0", Commit: strings.Repeat("a", 40), BuildTime: "2026-07-13T12:00:00Z",
	})
	if !errors.Is(err, beginErr) {
		t.Fatalf("RequireCurrent() error = %v", err)
	}
	if beginner.calls != 1 || beginner.options.IsoLevel != pgx.RepeatableRead || beginner.options.AccessMode != pgx.ReadOnly {
		t.Fatalf("BeginTx() calls = %d, options = %#v", beginner.calls, beginner.options)
	}
}

func TestValidateApplicationIdentity(t *testing.T) {
	t.Parallel()
	valid := ApplicationIdentity{Version: "0.2.0", Commit: strings.Repeat("a", 40), BuildTime: "2026-07-13T12:00:00Z"}
	if err := validateApplicationIdentity(valid); err != nil {
		t.Fatalf("validateApplicationIdentity() error = %v", err)
	}
	for name, value := range map[string]ApplicationIdentity{
		"empty version":   {Commit: valid.Commit, BuildTime: valid.BuildTime},
		"spaced commit":   {Version: valid.Version, Commit: " " + valid.Commit, BuildTime: valid.BuildTime},
		"long build time": {Version: valid.Version, Commit: valid.Commit, BuildTime: strings.Repeat("x", 129)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateApplicationIdentity(value); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("validateApplicationIdentity() error = %v", err)
			}
		})
	}
}

func TestEnsureHeadCreatesReplaysAndAdvances(t *testing.T) {
	t.Parallel()
	application := ApplicationIdentity{Version: "0.2.0", Commit: "commit", BuildTime: "build"}
	release := preparedRelease{artifactSHA256: strings.Repeat("a", 64)}

	t.Run("create", func(t *testing.T) {
		tx := &headTx{head: headRow{err: pgx.ErrNoRows}}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 1 || !activated || len(tx.execs) != 2 ||
			!strings.Contains(tx.execs[0], "recommendation_model_activation_events") ||
			!strings.Contains(tx.execs[1], "recommendation_model_head") {
			t.Fatalf("revision=%d activated=%t execs=%v error=%v", revision, activated, tx.execs, err)
		}
	})
	t.Run("replay", func(t *testing.T) {
		tx := &headTx{
			head:       headRow{releaseID: 7, revision: 4},
			activation: activationRow{artifactSHA256: release.artifactSHA256, application: application},
		}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 4 || activated || len(tx.execs) != 0 {
			t.Fatalf("revision=%d activated=%t execs=%v error=%v", revision, activated, tx.execs, err)
		}
	})
	t.Run("same model new application release", func(t *testing.T) {
		tx := &headTx{
			head: headRow{releaseID: 7, revision: 4},
			activation: activationRow{
				artifactSHA256: release.artifactSHA256,
				application:    ApplicationIdentity{Version: "0.1.0", Commit: "old", BuildTime: "old-build"},
			},
			updateRows: 1,
		}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 5 || !activated || len(tx.execs) != 2 ||
			!strings.Contains(tx.execs[0], "recommendation_model_activation_events") ||
			!strings.Contains(tx.execs[1], "UPDATE ascendany.recommendation_model_head") {
			t.Fatalf("revision=%d activated=%t execs=%v error=%v", revision, activated, tx.execs, err)
		}
	})
	t.Run("same model corrupt activation", func(t *testing.T) {
		tx := &headTx{
			head:       headRow{releaseID: 7, revision: 4},
			activation: activationRow{artifactSHA256: strings.Repeat("b", 64), application: application},
		}
		if _, _, err := ensureHead(context.Background(), tx, 7, release, application); !errors.Is(err, ErrStoredDataInvalid) {
			t.Fatalf("ensureHead() error = %v", err)
		}
	})
	t.Run("advance", func(t *testing.T) {
		tx := &headTx{head: headRow{releaseID: 6, revision: 4}, updateRows: 1}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 5 || !activated || len(tx.execs) != 2 ||
			!strings.Contains(tx.execs[1], "UPDATE ascendany.recommendation_model_head") {
			t.Fatalf("revision=%d activated=%t execs=%v error=%v", revision, activated, tx.execs, err)
		}
	})
}

type recordingBeginner struct {
	options pgx.TxOptions
	calls   int
	err     error
}

func (beginner *recordingBeginner) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	beginner.calls++
	beginner.options = options
	return nil, beginner.err
}

func testLoadedModel(t *testing.T) modelartifact.Loaded {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "contracts", "recommendation", "fixtures",
		"synthetic-test-only.inference-model.v1.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	model, err := inferencemodel.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return modelartifact.Loaded{
		Model: model, SHA256: model.SHA256(), Size: int64(len(raw)), Mode: modelartifact.RequiredMode,
	}
}

type activationRow struct {
	artifactSHA256 string
	application    ApplicationIdentity
	err            error
}

func (row activationRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	*destinations[0].(*string) = row.artifactSHA256
	*destinations[1].(*string) = row.application.Version
	*destinations[2].(*string) = row.application.Commit
	*destinations[3].(*string) = row.application.BuildTime
	return nil
}

type headRow struct {
	releaseID int64
	revision  int64
	err       error
}

func (row headRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	*destinations[0].(*int64) = row.releaseID
	*destinations[1].(*int64) = row.revision
	return nil
}

type headTx struct {
	head       headRow
	activation activationRow
	execs      []string
	updateRows int64
}

func (tx *headTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "recommendation_model_activation_events") {
		return tx.activation
	}
	return tx.head
}

func (tx *headTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, sql)
	rows := int64(1)
	if strings.Contains(sql, "UPDATE ascendany.recommendation_model_head") {
		rows = tx.updateRows
	}
	return pgconn.NewCommandTag("UPDATE " + string(rune('0'+rows))), nil
}

func (*headTx) Begin(context.Context) (pgx.Tx, error) { panic("unexpected Begin") }
func (*headTx) Commit(context.Context) error          { panic("unexpected Commit") }
func (*headTx) Rollback(context.Context) error        { panic("unexpected Rollback") }
func (*headTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("unexpected CopyFrom")
}
func (*headTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { panic("unexpected SendBatch") }
func (*headTx) LargeObjects() pgx.LargeObjects                         { panic("unexpected LargeObjects") }
func (*headTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("unexpected Prepare")
}
func (*headTx) Query(context.Context, string, ...any) (pgx.Rows, error) { panic("unexpected Query") }
func (*headTx) Conn() *pgx.Conn                                         { panic("unexpected Conn") }
