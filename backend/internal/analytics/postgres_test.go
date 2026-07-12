package analytics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type analyticsTestRow func(...any) error

func (row analyticsTestRow) Scan(destinations ...any) error {
	return row(destinations...)
}

type analyticsRowError struct{ err error }

func (row analyticsRowError) Scan(...any) error { return row.err }

type scriptedAnalyticsTx struct {
	rows       []pgx.Row
	queries    []string
	queryArgs  [][]any
	execs      []string
	execArgs   [][]any
	committed  bool
	rolledBack bool
}

func (tx *scriptedAnalyticsTx) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, query)
	tx.execArgs = append(tx.execArgs, arguments)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (tx *scriptedAnalyticsTx) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	tx.queries = append(tx.queries, query)
	return nil, errors.New("unexpected Query")
}

func (tx *scriptedAnalyticsTx) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	tx.queries = append(tx.queries, query)
	tx.queryArgs = append(tx.queryArgs, arguments)
	if len(tx.rows) == 0 {
		return analyticsRowError{err: errors.New("unexpected QueryRow")}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func TestPostgresRenewLeaseRejectsStaleSameOwnerAttempt(t *testing.T) {
	t.Parallel()

	tx := &scriptedAnalyticsTx{rows: []pgx.Row{analyticsRowError{err: pgx.ErrNoRows}}}
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (analyticsTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	claim := Claim{GenerationID: 7, LeaseOwner: "worker-a", AttemptCount: 4}
	err = repository.RenewLease(context.Background(), claim, time.Minute)
	if code, ok := CodeOf(err); !ok || code != ErrorLeaseLost {
		t.Fatalf("RenewLease() code = %q, %v; error = %v", code, ok, err)
	}
	if len(tx.queries) != 1 || len(tx.queryArgs) != 1 {
		t.Fatalf("queries = %#v, args = %#v", tx.queries, tx.queryArgs)
	}
	query := tx.queries[0]
	if !strings.Contains(query, "status = 'running'") ||
		!strings.Contains(query, "lease_owner = $2") ||
		!strings.Contains(query, "attempt_count = $3") ||
		!strings.Contains(query, "lease_expires_at > clock_timestamp()") {
		t.Fatalf("renewal query lacks exact active-attempt fence: %s", query)
	}
	arguments := tx.queryArgs[0]
	if len(arguments) != 4 || arguments[0] != claim.GenerationID || arguments[1] != claim.LeaseOwner || arguments[2] != claim.AttemptCount {
		t.Fatalf("renewal arguments = %#v", arguments)
	}
}

func (tx *scriptedAnalyticsTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *scriptedAnalyticsTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

func TestPostgresClaimSelectsQueuedAndExpiredRunningGenerations(t *testing.T) {
	t.Parallel()

	queued := claimTestTx("queued", 1)
	queuedRepository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (analyticsTx, error) { return queued, nil })
	if err != nil {
		t.Fatalf("newPostgresRepository() error = %v", err)
	}
	first, err := queuedRepository.Claim(context.Background(), "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("Claim(queued) error = %v", err)
	}
	if first == nil || first.Reclaimed || first.AttemptCount != 1 || !queued.committed {
		t.Fatalf("queued claim = %#v, committed = %v", first, queued.committed)
	}
	if len(queued.queries) != 1 || !strings.Contains(queued.queries[0], "status = 'queued'") || !strings.Contains(queued.queries[0], "status = 'running'") || !strings.Contains(queued.queries[0], "lease_expires_at <= clock_timestamp()") || !strings.Contains(queued.queries[0], "FOR UPDATE SKIP LOCKED") {
		t.Fatalf("claim SQL = %q", queued.queries)
	}
	if len(queued.execs) != 1 || !strings.Contains(queued.execs[0], "analytics_generation_events") ||
		len(queued.execArgs[0]) != 3 || queued.execArgs[0][0] != int64(7) || queued.execArgs[0][1] != "running" ||
		queued.execArgs[0][2] != `{"attemptCount":1,"reclaimed":false}` {
		t.Fatalf("queued claim event = %#v, %#v", queued.execs, queued.execArgs)
	}

	expired := claimTestTx("running", 2)
	expiredRepository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (analyticsTx, error) { return expired, nil })
	if err != nil {
		t.Fatalf("newPostgresRepository() error = %v", err)
	}
	second, err := expiredRepository.Claim(context.Background(), "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("Claim(expired running) error = %v", err)
	}
	if second == nil || !second.Reclaimed || second.AttemptCount != 2 || second.LeaseOwner != "worker-b" || !expired.committed {
		t.Fatalf("reclaimed claim = %#v, committed = %v", second, expired.committed)
	}
	if len(expired.execArgs) != 1 || expired.execArgs[0][2] != `{"attemptCount":2,"reclaimed":true}` {
		t.Fatalf("reclaimed claim event = %#v", expired.execArgs)
	}
}

func TestEnsureReplacementGenerationReusesExactQueuedInput(t *testing.T) {
	t.Parallel()

	tx := &scriptedAnalyticsTx{rows: []pgx.Row{
		analyticsRowError{err: pgx.ErrNoRows},
		analyticsTestRow(func(destinations ...any) error {
			*(destinations[0].(*int64)) = 99
			return nil
		}),
	}}
	manifest, err := ParseManifest([]byte(validManifestJSON))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	generationID, err := ensureReplacementGeneration(context.Background(), tx, Claim{
		AlgorithmVersion: AlgorithmV1,
		ConfigSHA256:     repeatedHash('c'),
	}, manifest)
	if err != nil {
		t.Fatalf("ensureReplacementGeneration() error = %v", err)
	}
	if generationID != 99 || len(tx.execs) != 0 || len(tx.queries) != 2 {
		t.Fatalf("generation = %d, execs = %d, queries = %d", generationID, len(tx.execs), len(tx.queries))
	}
	if !strings.Contains(tx.queries[0], "ON CONFLICT") || !strings.Contains(tx.queries[0], "algorithm_version") || !strings.Contains(tx.queries[1], "input_manifest_sha256") {
		t.Fatalf("replacement queries = %#v", tx.queries)
	}
}

func claimTestTx(previousStatus string, attempt int32) *scriptedAnalyticsTx {
	return &scriptedAnalyticsTx{rows: []pgx.Row{analyticsTestRow(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 7
		*(destinations[1].(*string)) = map[bool]string{true: "worker-b", false: "worker-a"}[previousStatus == "running"]
		*(destinations[2].(*time.Time)) = time.Now().Add(time.Minute)
		*(destinations[3].(*int32)) = attempt
		*(destinations[4].(**int64)) = nil
		*(destinations[5].(*int64)) = 0
		*(destinations[6].(*int64)) = 1
		*(destinations[7].(*int64)) = 11
		*(destinations[8].(*int64)) = 1
		*(destinations[9].(*[]byte)) = []byte(validManifestJSON)
		*(destinations[10].(*string)) = repeatedHash('d')
		*(destinations[11].(*string)) = AlgorithmV1
		*(destinations[12].(*string)) = repeatedHash('c')
		*(destinations[13].(*string)) = previousStatus
		return nil
	})}}
}
