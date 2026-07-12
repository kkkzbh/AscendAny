package examgeneration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPostgresReadsUseRepeatableReadReadOnlyTransactions(t *testing.T) {
	t.Parallel()
	beginFailure := errors.New("begin failed")
	var options []pgx.TxOptions
	repository, err := newPostgresRepository(func(_ context.Context, value pgx.TxOptions) (readTx, error) {
		options = append(options, value)
		return nil, beginFailure
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.LoadCurrent(context.Background(), CurrentQuery{}); CodeOf(err) != ErrorDatabase {
		t.Fatalf("LoadCurrent() error=%v", err)
	}
	if _, _, err := repository.LoadEvents(context.Background(), EventQuery{}); CodeOf(err) != ErrorDatabase {
		t.Fatalf("LoadEvents() error=%v", err)
	}
	if len(options) != 2 {
		t.Fatalf("transaction options=%#v", options)
	}
	for _, option := range options {
		if option.IsoLevel != pgx.RepeatableRead || option.AccessMode != pgx.ReadOnly {
			t.Fatalf("transaction options=%#v", option)
		}
	}
}

func TestPostgresReadRejectsNilContextBeforeBeginning(t *testing.T) {
	t.Parallel()
	calls := 0
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (readTx, error) {
		calls++
		return nil, errors.New("unexpected begin")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.LoadCurrent(nil, CurrentQuery{}); CodeOf(err) != ErrorInvalidInput || calls != 0 {
		t.Fatalf("LoadCurrent(nil) error=%v calls=%d", err, calls)
	}
}
