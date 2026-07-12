package importing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestClaimedStateOperationsFenceExactAttempt(t *testing.T) {
	claim := fencedTestClaim()
	tests := []struct {
		name string
		run  func(*PostgresRepository) error
	}{
		{
			name: "renew lease",
			run: func(repository *PostgresRepository) error {
				return repository.RenewLease(context.Background(), claim, time.Minute)
			},
		},
		{
			name: "load artifact",
			run: func(repository *PostgresRepository) error {
				_, err := repository.LoadArtifact(context.Background(), claim)
				return err
			},
		},
		{
			name: "mark importing",
			run: func(repository *PostgresRepository) error {
				_, err := repository.MarkImporting(context.Background(), claim, time.Minute)
				return err
			},
		},
		{
			name: "requeue",
			run: func(repository *PostgresRepository) error {
				return repository.Requeue(context.Background(), claim, time.Second, ErrorDatabase)
			},
		},
		{
			name: "fail permanently",
			run: func(repository *PostgresRepository) error {
				return repository.FailPermanent(context.Background(), claim, ErrorValidation, "invalid snapshot")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := &scriptedTx{rows: []pgx.Row{rowError{err: pgx.ErrNoRows}}}
			repository, err := newPostgresRepository(func(context.Context) (dbTx, error) { return tx, nil })
			if err != nil {
				t.Fatal(err)
			}
			resultErr := test.run(repository)
			assertImportCode(t, resultErr, ErrorLeaseLost)
			if len(tx.queries) != 1 || len(tx.queryArgs) != 1 {
				t.Fatalf("queries = %v args = %v", tx.queries, tx.queryArgs)
			}
			assertAttemptFence(t, tx.queries[0], tx.queryArgs[0], claim)
		})
	}
}

func TestPostgresRenewLeaseRequiresRunningUnexpiredSameAttempt(t *testing.T) {
	claim := fencedTestClaim()
	tx := &scriptedTx{rows: []pgx.Row{rowError{err: pgx.ErrNoRows}}}
	repository, err := newPostgresRepository(func(context.Context) (dbTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	err = repository.RenewLease(context.Background(), claim, time.Minute)
	assertImportCode(t, err, ErrorLeaseLost)
	if len(tx.queries) != 1 || len(tx.queryArgs) != 1 {
		t.Fatalf("queries = %#v, args = %#v", tx.queries, tx.queryArgs)
	}
	query := tx.queries[0]
	if !strings.Contains(query, "status = 'running'") ||
		!strings.Contains(query, "lease_owner = $3") ||
		!strings.Contains(query, "attempt_count = $4") ||
		!strings.Contains(query, "lease_expires_at > clock_timestamp()") {
		t.Fatalf("renewal query lacks exact active-attempt fence: %s", query)
	}
	assertAttemptFence(t, query, tx.queryArgs[0], claim)
}

func TestSnapshotLockAndTerminalUpdatesFenceExactAttempt(t *testing.T) {
	claim := fencedTestClaim()
	claim.Stage = StageImporting

	lockTx := &scriptedTx{rows: []pgx.Row{rowError{err: pgx.ErrNoRows}}}
	err := lockImportingJob(context.Background(), lockTx, claim)
	assertImportCode(t, err, ErrorLeaseLost)
	assertAttemptFence(t, lockTx.queries[0], lockTx.queryArgs[0], claim)

	for _, test := range []struct {
		name string
		run  func(*scriptedTx) error
	}{
		{
			name: "supersede duplicate",
			run: func(tx *scriptedTx) error {
				return supersedeDuplicateJob(context.Background(), tx, claim, existingSnapshot{
					ID: 9, PublicID: "22222222-2222-4222-8222-222222222222",
				}, strings.Repeat("a", 64))
			},
		},
		{
			name: "mark analyzing",
			run: func(tx *scriptedTx) error {
				return markJobAnalyzing(
					context.Background(), tx, claim, 9,
					"22222222-2222-4222-8222-222222222222", 10, strings.Repeat("a", 64),
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &scriptedTx{}
			_ = test.run(tx)
			if len(tx.execs) == 0 || len(tx.execArgs) == 0 {
				t.Fatalf("execs = %v args = %v", tx.execs, tx.execArgs)
			}
			assertAttemptFence(t, tx.execs[0], tx.execArgs[0], claim)
		})
	}
}

func fencedTestClaim() Claim {
	owner := "worker-a"
	expires := time.Now().Add(time.Minute)
	return Claim{Job: Job{
		ID:             7,
		PublicID:       "11111111-1111-4111-8111-111111111111",
		ArtifactID:     3,
		Status:         JobRunning,
		Stage:          StageValidating,
		AttemptCount:   4,
		LeaseOwner:     &owner,
		LeaseExpiresAt: &expires,
	}}
}

func assertAttemptFence(t *testing.T, query string, arguments []any, claim Claim) {
	t.Helper()
	if !strings.Contains(query, "lease_owner = $3") || !strings.Contains(query, "attempt_count = $4") {
		t.Fatalf("query has no exact owner+attempt fence: %s", query)
	}
	if len(arguments) < 4 || arguments[2] != *claim.LeaseOwner || arguments[3] != claim.AttemptCount {
		t.Fatalf("fence arguments = %#v, want owner=%q attempt=%d", arguments, *claim.LeaseOwner, claim.AttemptCount)
	}
}
