package studentanalytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type testRow func(...any) error

func (row testRow) Scan(destinations ...any) error { return row(destinations...) }

type errorRow struct{ err error }

func (row errorRow) Scan(...any) error { return row.err }

type scriptedReadTx struct {
	rows       []pgx.Row
	queryRows  pgx.Rows
	queries    []string
	arguments  [][]any
	committed  bool
	rolledBack bool
}

func (tx *scriptedReadTx) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	tx.queries = append(tx.queries, query)
	tx.arguments = append(tx.arguments, arguments)
	if tx.queryRows == nil {
		return nil, errors.New("unexpected Query")
	}
	rows := tx.queryRows
	tx.queryRows = nil
	return rows, nil
}

func (tx *scriptedReadTx) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	tx.queries = append(tx.queries, query)
	tx.arguments = append(tx.arguments, arguments)
	if len(tx.rows) == 0 {
		return errorRow{err: errors.New("unexpected QueryRow")}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *scriptedReadTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *scriptedReadTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

type metadataRow struct {
	ordinal          int64
	examID           int64
	snapshotID       int64
	domainHash       string
	examPublicID     string
	snapshotPublicID string
	title            string
}

type metadataRows struct {
	values  []metadataRow
	index   int
	current *metadataRow
	closed  bool
	err     error
}

func (rows *metadataRows) Close()                                       { rows.closed = true }
func (rows *metadataRows) Err() error                                   { return rows.err }
func (rows *metadataRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *metadataRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *metadataRows) Values() ([]any, error)                       { return nil, errors.New("unused") }
func (rows *metadataRows) RawValues() [][]byte                          { return nil }
func (rows *metadataRows) Conn() *pgx.Conn                              { return nil }

func (rows *metadataRows) Next() bool {
	if rows.closed || rows.index >= len(rows.values) {
		rows.closed = true
		rows.current = nil
		return false
	}
	rows.current = &rows.values[rows.index]
	rows.index++
	return true
}

func (rows *metadataRows) Scan(destinations ...any) error {
	if rows.current == nil || len(destinations) != 7 {
		return errors.New("invalid metadata scan")
	}
	*(destinations[0].(*int64)) = rows.current.ordinal
	*(destinations[1].(*int64)) = rows.current.examID
	*(destinations[2].(*int64)) = rows.current.snapshotID
	*(destinations[3].(*string)) = rows.current.domainHash
	*(destinations[4].(*string)) = rows.current.examPublicID
	*(destinations[5].(*string)) = rows.current.snapshotPublicID
	*(destinations[6].(*string)) = rows.current.title
	return nil
}

func TestPostgresLoadSelfMapsLimitedHistoryInRepeatableReadSnapshot(t *testing.T) {
	t.Parallel()

	manifest := testManifest(t)
	metricsJSON := testMetricsJSON(t, []int64{1, 2, 3}, []int64{11, 22, 33})
	tx := &scriptedReadTx{
		rows: []pgx.Row{
			resolvedRow(&manifest, 77, 1, "succeeded"),
			metricsRow(1506, metricsJSON),
			testRow(func(destinations ...any) error {
				if len(destinations) != 22 {
					return fmt.Errorf("peer scan destination count = %d", len(destinations))
				}
				*(destinations[0].(*int64)) = 1
				*(destinations[1].(*int64)) = 1
				*(destinations[2].(*int64)) = 1
				*(destinations[3].(*int64)) = 1
				*(destinations[5].(*int64)) = 0
				zero := 0.0
				*(destinations[7].(**float64)) = &zero
				return nil
			}),
		},
		queryRows: &metadataRows{values: []metadataRow{
			{ordinal: 1, examID: 2, snapshotID: 22, domainHash: strings.Repeat("b", 64), examPublicID: "22222222-2222-4222-8222-222222222222", snapshotPublicID: "55555555-5555-4555-8555-555555555555", title: "Exam 2"},
			{ordinal: 2, examID: 3, snapshotID: 33, domainHash: strings.Repeat("c", 64), examPublicID: "33333333-3333-4333-8333-333333333333", snapshotPublicID: "66666666-6666-4666-8666-666666666666", title: "Exam 3"},
		}},
	}
	var options pgx.TxOptions
	repository, err := newPostgresRepository(func(_ context.Context, value pgx.TxOptions) (readTx, error) {
		options = value
		return tx, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	query := SelfQuery{AccountID: testAccountID, SessionID: testSessionID, ExpectedAuthRevision: 5, ExpectedRole: auth.RoleStudent, HistoryLimit: 2}
	result, err := repository.LoadSelf(context.Background(), query)
	if err != nil {
		t.Fatalf("LoadSelf() error = %v", err)
	}
	if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly {
		t.Fatalf("transaction options = %#v", options)
	}
	if !tx.committed || tx.rolledBack || result.State != StateReady || result.HeadRevision != 1 || result.Ready == nil {
		t.Fatalf("transaction/result = committed %t rolledBack %t result %#v", tx.committed, tx.rolledBack, result)
	}
	if result.Ready.Rating != 1506 || len(result.Ready.ExamHistory) != 2 || result.Ready.ExamHistory[0].Title != "Exam 2" || result.Ready.RatingHistory[1].ExamID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("ready = %#v", result.Ready)
	}
	if result.Ready.LatestPeer == nil || result.Ready.LatestPeer.TotalParticipants != 1 ||
		result.Ready.LatestPeer.BandMedian.Solved == nil {
		t.Fatalf("latest peer = %#v", result.Ready.LatestPeer)
	}
	if len(tx.queries) != 4 || !strings.Contains(tx.queries[0], "JOIN ascendany.auth_sessions") || !strings.Contains(tx.queries[0], "session.revoked_at IS NULL") || !strings.Contains(tx.queries[0], "session.expires_at > transaction_timestamp()") || !strings.Contains(tx.queries[0], "pintia_actor_identifiers") || !strings.Contains(tx.queries[0], "analytics_head") || !strings.Contains(tx.queries[0], "analytics_generations") || !strings.Contains(tx.queries[0], "base_analytics_generation_id") || !strings.Contains(tx.queries[0], "target_exam_head_revision") ||
		!strings.Contains(tx.queries[2], "unnest($3::bigint[], $4::bigint[])") || !strings.Contains(tx.queries[2], "analytics_generation_snapshots") || !strings.Contains(tx.queries[2], "pintia_snapshot_participants") || !strings.Contains(tx.queries[2], "ORDER BY requested.ordinal") {
		t.Fatalf("queries = %#v", tx.queries)
	}
	if len(tx.arguments) != 4 || len(tx.arguments[0]) != 4 || tx.arguments[0][0] != testAccountID || tx.arguments[0][1] != int64(5) || tx.arguments[0][2] != string(auth.RoleStudent) || tx.arguments[0][3] != testSessionID {
		t.Fatalf("principal arguments = %#v", tx.arguments)
	}
	if got := tx.arguments[2][2].([]int64); fmt.Sprint(got) != "[2 3]" {
		t.Fatalf("limited exam IDs = %v", got)
	}
	if got := tx.arguments[2][3].([]int64); fmt.Sprint(got) != "[22 33]" {
		t.Fatalf("limited snapshot IDs = %v", got)
	}
}

func TestParseCanonicalRating(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]int64{"0": 0, "1": 1, "9223372036854775807": 9223372036854775807} {
		got, err := parseCanonicalRating(raw)
		if err != nil || got != want {
			t.Fatalf("parseCanonicalRating(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "00", "01", "-1", "1.0", "NaN", "Infinity", "9223372036854775808"} {
		if _, err := parseCanonicalRating(raw); err == nil {
			t.Fatalf("parseCanonicalRating(%q) error = nil", raw)
		}
	}
}

func TestPostgresLoadSelfReturnsExplicitEmptyStates(t *testing.T) {
	t.Parallel()

	t.Run("not generated", func(t *testing.T) {
		t.Parallel()
		tx := &scriptedReadTx{rows: []pgx.Row{resolvedRow(nil, 0, 0, "")}}
		repository := mustRepository(t, tx)
		result, err := repository.LoadSelf(context.Background(), testSelfQuery())
		if err != nil || result != (Result{State: StateNotGenerated}) || !tx.committed || len(tx.queries) != 1 {
			t.Fatalf("result = %#v, error = %v, committed = %t, queries = %d", result, err, tx.committed, len(tx.queries))
		}
	})

	t.Run("no observations", func(t *testing.T) {
		t.Parallel()
		manifest := testManifest(t)
		tx := &scriptedReadTx{rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded"), errorRow{err: pgx.ErrNoRows}}}
		repository := mustRepository(t, tx)
		result, err := repository.LoadSelf(context.Background(), testSelfQuery())
		if err != nil || result != (Result{State: StateNoObservations, HeadRevision: 1}) || !tx.committed || len(tx.queries) != 2 {
			t.Fatalf("result = %#v, error = %v, committed = %t, queries = %d", result, err, tx.committed, len(tx.queries))
		}
	})
}

func TestPostgresLoadSelfRejectsPrincipalAndStoredInvariantViolations(t *testing.T) {
	t.Parallel()

	manifest := testManifest(t)
	validMetrics := testMetricsJSON(t, []int64{1, 2, 3}, []int64{11, 22, 33})
	tests := []struct {
		name string
		tx   func() *scriptedReadTx
		code ErrorCode
	}{
		{
			name: "principal no longer matches",
			tx:   func() *scriptedReadTx { return &scriptedReadTx{rows: []pgx.Row{errorRow{err: pgx.ErrNoRows}}} },
			code: ErrorPrincipalRejected,
		},
		{
			name: "head generation is not succeeded",
			tx: func() *scriptedReadTx {
				return &scriptedReadTx{rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "running")}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "head revision differs from generation base",
			tx: func() *scriptedReadTx {
				return &scriptedReadTx{rows: []pgx.Row{resolvedRow(&manifest, 77, 4, "succeeded")}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "generation target differs from manifest",
			tx: func() *scriptedReadTx {
				row := alteredResolvedRow(resolvedRow(&manifest, 77, 1, "succeeded"), func(destinations []any) {
					targetExamID := int64(999)
					*(destinations[15].(**int64)) = &targetExamID
				})
				return &scriptedReadTx{rows: []pgx.Row{row}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "student-number identifier is missing",
			tx: func() *scriptedReadTx {
				row := alteredResolvedRow(resolvedRow(&manifest, 77, 1, "succeeded"), func(destinations []any) {
					*(destinations[7].(**int64)) = nil
				})
				return &scriptedReadTx{rows: []pgx.Row{row}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "analytics head singleton is missing",
			tx: func() *scriptedReadTx {
				row := alteredResolvedRow(resolvedRow(&manifest, 77, 1, "succeeded"), func(destinations []any) {
					*(destinations[9].(**bool)) = nil
				})
				return &scriptedReadTx{rows: []pgx.Row{row}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "manifest digest mismatch",
			tx: func() *scriptedReadTx {
				changed := manifest
				changed.SHA256 = strings.Repeat("f", 64)
				return &scriptedReadTx{rows: []pgx.Row{resolvedRow(&changed, 77, 1, "succeeded")}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "metrics reference snapshot outside manifest",
			tx: func() *scriptedReadTx {
				outside := testMetricsJSON(t, []int64{1, 2, 4}, []int64{11, 22, 44})
				return &scriptedReadTx{rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded"), metricsRow(1506, outside)}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "canonical rating differs",
			tx: func() *scriptedReadTx {
				return &scriptedReadTx{rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded"), metricsRow(1505, validMetrics)}}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "metadata cardinality differs",
			tx: func() *scriptedReadTx {
				return &scriptedReadTx{
					rows:      []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded"), metricsRow(1506, validMetrics)},
					queryRows: &metadataRows{values: []metadataRow{{ordinal: 1, examID: 3, snapshotID: 33, domainHash: strings.Repeat("c", 64), examPublicID: "33333333-3333-4333-8333-333333333333", snapshotPublicID: "66666666-6666-4666-8666-666666666666", title: "Exam 3"}}},
				}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "metadata order differs",
			tx: func() *scriptedReadTx {
				return &scriptedReadTx{
					rows:      []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded"), metricsRow(1506, validMetrics)},
					queryRows: &metadataRows{values: []metadataRow{{ordinal: 2, examID: 3, snapshotID: 33, domainHash: strings.Repeat("c", 64), examPublicID: "33333333-3333-4333-8333-333333333333", snapshotPublicID: "66666666-6666-4666-8666-666666666666", title: "Exam 3"}}},
				}
			},
			code: ErrorStoredDataInvalid,
		},
		{
			name: "normalized manifest domain differs",
			tx: func() *scriptedReadTx {
				return &scriptedReadTx{
					rows: []pgx.Row{resolvedRow(&manifest, 77, 1, "succeeded"), metricsRow(1506, validMetrics)},
					queryRows: &metadataRows{values: []metadataRow{
						{ordinal: 1, examID: 2, snapshotID: 22, domainHash: strings.Repeat("a", 64), examPublicID: "22222222-2222-4222-8222-222222222222", snapshotPublicID: "55555555-5555-4555-8555-555555555555", title: "Exam 2"},
						{ordinal: 2, examID: 3, snapshotID: 33, domainHash: strings.Repeat("c", 64), examPublicID: "33333333-3333-4333-8333-333333333333", snapshotPublicID: "66666666-6666-4666-8666-666666666666", title: "Exam 3"},
					}},
				}
			},
			code: ErrorStoredDataInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tx := test.tx()
			repository := mustRepository(t, tx)
			_, err := repository.LoadSelf(context.Background(), testSelfQuery())
			if CodeOf(err) != test.code || !tx.rolledBack || tx.committed {
				t.Fatalf("LoadSelf() error = %v (%q), committed = %t, rolledBack = %t", err, CodeOf(err), tx.committed, tx.rolledBack)
			}
		})
	}
}

func mustRepository(t *testing.T, tx readTx) *PostgresRepository {
	t.Helper()
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (readTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func testSelfQuery() SelfQuery {
	return SelfQuery{AccountID: testAccountID, SessionID: testSessionID, ExpectedAuthRevision: 5, ExpectedRole: auth.RoleStudent, HistoryLimit: 2}
}

func resolvedRow(manifest *analytics.ParsedManifest, generationID int64, revision int64, status string) pgx.Row {
	return testRow(func(destinations ...any) error {
		if len(destinations) != 20 {
			return fmt.Errorf("resolved destination count = %d", len(destinations))
		}
		*(destinations[0].(*string)) = testAccountID
		*(destinations[1].(*int64)) = 5
		*(destinations[2].(*string)) = string(auth.RoleStudent)
		*(destinations[3].(*string)) = testSessionID
		*(destinations[4].(*int64)) = 5
		actorID := int64(9)
		studentNumber := "20260001"
		headSingleton := true
		*(destinations[5].(**int64)) = &actorID
		*(destinations[6].(**string)) = &studentNumber
		*(destinations[7].(**int64)) = &actorID
		*(destinations[8].(**string)) = &studentNumber
		*(destinations[9].(**bool)) = &headSingleton
		if manifest == nil {
			headRevision := int64(0)
			*(destinations[10].(**int64)) = nil
			*(destinations[11].(**int64)) = &headRevision
			*(destinations[12].(**string)) = nil
			*(destinations[13].(**int64)) = nil
			*(destinations[14].(**int64)) = nil
			*(destinations[15].(**int64)) = nil
			*(destinations[16].(**int64)) = nil
			*(destinations[17].(**int64)) = nil
			*(destinations[18].(**string)) = nil
			*(destinations[19].(**string)) = nil
			return nil
		}
		generation := generationID
		headRevision := revision
		generationStatus := status
		baseGenerationID := manifest.Value.BaseAnalyticsGenerationID
		baseHeadRevision := manifest.Value.BaseHeadRevision
		targetExamID := manifest.Value.Target.ExamID
		targetSnapshotID := manifest.Value.Target.SnapshotID
		targetRevision := manifest.Value.Target.ExamHeadRevision
		manifestJSON := string(manifest.Canonical)
		manifestSHA256 := manifest.SHA256
		*(destinations[10].(**int64)) = &generation
		*(destinations[11].(**int64)) = &headRevision
		*(destinations[12].(**string)) = &generationStatus
		*(destinations[13].(**int64)) = baseGenerationID
		*(destinations[14].(**int64)) = &baseHeadRevision
		*(destinations[15].(**int64)) = &targetExamID
		*(destinations[16].(**int64)) = &targetSnapshotID
		*(destinations[17].(**int64)) = &targetRevision
		*(destinations[18].(**string)) = &manifestJSON
		*(destinations[19].(**string)) = &manifestSHA256
		return nil
	})
}

func alteredResolvedRow(row pgx.Row, alter func([]any)) pgx.Row {
	return testRow(func(destinations ...any) error {
		if err := row.Scan(destinations...); err != nil {
			return err
		}
		alter(destinations)
		return nil
	})
}

func metricsRow(rating int64, metricsJSON string) pgx.Row {
	return testRow(func(destinations ...any) error {
		if len(destinations) != 2 {
			return fmt.Errorf("metrics destination count = %d", len(destinations))
		}
		*(destinations[0].(*string)) = fmt.Sprintf("%d", rating)
		*(destinations[1].(*string)) = metricsJSON
		return nil
	})
}

func testManifest(t *testing.T) analytics.ParsedManifest {
	t.Helper()
	manifest, err := analytics.CanonicalManifest(analytics.Manifest{
		Protocol:         analytics.ManifestProtocolV1,
		BaseHeadRevision: 0,
		Target:           analytics.ManifestTarget{ExamID: 3, SnapshotID: 33, ExamHeadRevision: 1},
		Snapshots: []analytics.ManifestSnapshot{
			{ExamID: 1, SnapshotID: 11, DomainHash: strings.Repeat("a", 64)},
			{ExamID: 2, SnapshotID: 22, DomainHash: strings.Repeat("b", 64)},
			{ExamID: 3, SnapshotID: 33, DomainHash: strings.Repeat("c", 64)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func testMetricsJSON(t *testing.T, examIDs []int64, snapshotIDs []int64) string {
	t.Helper()
	if len(examIDs) != 3 || len(snapshotIDs) != 3 {
		t.Fatal("test metrics require three identities")
	}
	referenceTime := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	metrics := analytics.StudentMetrics{
		Protocol:      analytics.StudentMetricsProtocolV1,
		ReferenceTime: referenceTime,
	}
	oldRating := int64(1500)
	for index := range examIDs {
		eventTime := time.Date(2026, 1, index+1, 0, 0, 0, 0, time.UTC)
		metrics.ExamHistory = append(metrics.ExamHistory, analytics.ExamMetricPoint{
			ExamID: examIDs[index], SnapshotID: snapshotIDs[index], EventTime: eventTime,
		})
		newRating := oldRating + int64(index+1)
		metrics.RatingHistory = append(metrics.RatingHistory, analytics.RatingHistoryPoint{
			ExamID: examIDs[index], SnapshotID: snapshotIDs[index], EventTime: eventTime,
			Rank: int64(index + 1), OldRating: oldRating, Delta: newRating - oldRating, NewRating: newRating,
			Seed: 1, Performance: 1500,
		})
		oldRating = newRating
	}
	data, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
