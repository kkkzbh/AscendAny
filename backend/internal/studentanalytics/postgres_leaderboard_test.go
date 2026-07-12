package studentanalytics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type leaderboardTestRow struct {
	studentNumber string
	displayName   *string
	rating        string
	metrics       string
	rank          int64
	population    int64
}

type leaderboardTestRows struct {
	values  []leaderboardTestRow
	index   int
	current *leaderboardTestRow
	closed  bool
	err     error
}

func (rows *leaderboardTestRows) Close()                                       { rows.closed = true }
func (rows *leaderboardTestRows) Err() error                                   { return rows.err }
func (rows *leaderboardTestRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *leaderboardTestRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *leaderboardTestRows) Values() ([]any, error)                       { return nil, errors.New("unused") }
func (rows *leaderboardTestRows) RawValues() [][]byte                          { return nil }
func (rows *leaderboardTestRows) Conn() *pgx.Conn                              { return nil }

func (rows *leaderboardTestRows) Next() bool {
	if rows.closed || rows.index >= len(rows.values) {
		rows.closed = true
		rows.current = nil
		return false
	}
	rows.current = &rows.values[rows.index]
	rows.index++
	return true
}

func (rows *leaderboardTestRows) Scan(destinations ...any) error {
	if rows.current == nil || len(destinations) != 6 {
		return errors.New("invalid leaderboard row scan")
	}
	*(destinations[0].(*string)) = rows.current.studentNumber
	*(destinations[1].(**string)) = rows.current.displayName
	*(destinations[2].(*string)) = rows.current.rating
	*(destinations[3].(*string)) = rows.current.metrics
	*(destinations[4].(*int64)) = rows.current.rank
	*(destinations[5].(*int64)) = rows.current.population
	return nil
}

func TestPostgresLoadLeaderboardUsesCurrentHeadAndCanonicalRanking(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	metrics := testMetricsJSON(t, []int64{1, 2, 3}, []int64{11, 22, 33})
	firstName := "Student One"
	secondName := "Student Two"
	tx := &scriptedReadTx{
		rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded")},
		queryRows: &leaderboardTestRows{values: []leaderboardTestRow{
			{studentNumber: "20260001", displayName: &firstName, rating: "1506", metrics: metrics, rank: 1, population: 3},
			{studentNumber: "20260002", displayName: &secondName, rating: "1506", metrics: metrics, rank: 1, population: 3},
		}},
	}
	repository := mustRepository(t, tx)
	result, err := repository.LoadLeaderboard(context.Background(), LeaderboardQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 5,
		ExpectedRole:         "student",
		Limit:                2,
	})
	if err != nil || !tx.committed || tx.rolledBack || result.State != StateReady ||
		result.HeadRevision != 1 || result.Population != 3 || len(result.Items) != 2 {
		t.Fatalf("LoadLeaderboard() result=%#v committed=%t rolledBack=%t error=%v", result, tx.committed, tx.rolledBack, err)
	}
	if len(tx.queries) != 2 || !strings.Contains(tx.queries[1], "rank() OVER") ||
		!strings.Contains(tx.queries[1], "analytics_generation_snapshots") ||
		!strings.Contains(tx.queries[1], "ORDER BY rating DESC, student_number ASC, actor_id ASC") {
		t.Fatalf("leaderboard queries=%#v", tx.queries)
	}
	if len(tx.arguments[1]) != 2 || tx.arguments[1][0] != int64(77) || tx.arguments[1][1] != 2 {
		t.Fatalf("leaderboard arguments=%#v", tx.arguments)
	}
}

func TestPostgresLoadLeaderboardReturnsExplicitEmptyStates(t *testing.T) {
	t.Parallel()
	t.Run("not generated", func(t *testing.T) {
		t.Parallel()
		tx := &scriptedReadTx{rows: []pgx.Row{resolvedRow(nil, 0, 0, "")}}
		result, err := mustRepository(t, tx).LoadLeaderboard(context.Background(), testLeaderboardQuery())
		if err != nil || result.State != StateNotGenerated || result.HeadRevision != 0 ||
			result.Population != 0 || len(result.Items) != 0 || !tx.committed {
			t.Fatalf("result=%#v committed=%t error=%v", result, tx.committed, err)
		}
	})
	t.Run("no observations", func(t *testing.T) {
		t.Parallel()
		manifest := testManifest(t)
		tx := &scriptedReadTx{
			rows:      []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded")},
			queryRows: &leaderboardTestRows{},
		}
		result, err := mustRepository(t, tx).LoadLeaderboard(context.Background(), testLeaderboardQuery())
		if err != nil || result.State != StateNoObservations || result.HeadRevision != 1 ||
			result.Population != 0 || len(result.Items) != 0 || !tx.committed {
			t.Fatalf("result=%#v committed=%t error=%v", result, tx.committed, err)
		}
	})
}

func TestPostgresLoadLeaderboardRejectsStoredRatingDrift(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	metrics := testMetricsJSON(t, []int64{1, 2, 3}, []int64{11, 22, 33})
	tx := &scriptedReadTx{
		rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded")},
		queryRows: &leaderboardTestRows{values: []leaderboardTestRow{
			{studentNumber: "20260001", rating: "1505", metrics: metrics, rank: 1, population: 1},
		}},
	}
	_, err := mustRepository(t, tx).LoadLeaderboard(context.Background(), testLeaderboardQuery())
	if CodeOf(err) != ErrorStoredDataInvalid || !tx.rolledBack || tx.committed {
		t.Fatalf("error=%v code=%q committed=%t rolledBack=%t", err, CodeOf(err), tx.committed, tx.rolledBack)
	}
}

func testLeaderboardQuery() LeaderboardQuery {
	return LeaderboardQuery{
		AccountID:            testAccountID,
		SessionID:            testSessionID,
		ExpectedAuthRevision: 5,
		ExpectedRole:         "student",
		Limit:                50,
	}
}
