package achievement

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type scanRow func(...any) error

func (row scanRow) Scan(destinations ...any) error { return row(destinations...) }

type staticErrorRow struct{ err error }

func (row staticErrorRow) Scan(...any) error { return row.err }

type ruleRow struct {
	code, title, description, progressKey string
	bronze, silver, gold                  string
	sortOrder                             int64
}

type ruleRows struct {
	values  []ruleRow
	index   int
	current *ruleRow
	closed  bool
	err     error
}

func (rows *ruleRows) Close()                                       { rows.closed = true }
func (rows *ruleRows) Err() error                                   { return rows.err }
func (rows *ruleRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *ruleRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *ruleRows) Values() ([]any, error)                       { return nil, errors.New("unused") }
func (rows *ruleRows) RawValues() [][]byte                          { return nil }
func (rows *ruleRows) Conn() *pgx.Conn                              { return nil }

func (rows *ruleRows) Next() bool {
	if rows.closed || rows.index >= len(rows.values) {
		rows.closed = true
		rows.current = nil
		return false
	}
	rows.current = &rows.values[rows.index]
	rows.index++
	return true
}

func (rows *ruleRows) Scan(destinations ...any) error {
	if rows.current == nil || len(destinations) != 8 {
		return errors.New("invalid rule scan")
	}
	*(destinations[0].(*string)) = rows.current.code
	*(destinations[1].(*string)) = rows.current.title
	*(destinations[2].(*string)) = rows.current.description
	*(destinations[3].(*string)) = rows.current.progressKey
	*(destinations[4].(*string)) = rows.current.bronze
	*(destinations[5].(*string)) = rows.current.silver
	*(destinations[6].(*string)) = rows.current.gold
	*(destinations[7].(*int64)) = rows.current.sortOrder
	return nil
}

type scriptedReadTx struct {
	rows       []pgx.Row
	ruleRows   pgx.Rows
	queries    []string
	arguments  [][]any
	committed  bool
	rolledBack bool
}

func (tx *scriptedReadTx) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	tx.queries = append(tx.queries, query)
	tx.arguments = append(tx.arguments, append([]any(nil), arguments...))
	if tx.ruleRows == nil {
		return nil, errors.New("unexpected Query")
	}
	rows := tx.ruleRows
	tx.ruleRows = nil
	return rows, nil
}

func (tx *scriptedReadTx) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	tx.queries = append(tx.queries, query)
	tx.arguments = append(tx.arguments, append([]any(nil), arguments...))
	if len(tx.rows) == 0 {
		return staticErrorRow{err: errors.New("unexpected QueryRow")}
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

func TestPostgresLoadsRulesLiveDialogueAndMetricsInOneRepeatableRead(t *testing.T) {
	t.Parallel()

	metricsJSON, err := json.Marshal(testMetrics())
	if err != nil {
		t.Fatal(err)
	}
	rules := &ruleRows{values: []ruleRow{
		{code: "exam", title: "Exam", description: "Exam progress", progressKey: string(ProgressExamCount), bronze: "1", silver: "3", gold: "8", sortOrder: 1},
		{code: "dialogue", title: "Dialogue", description: "Dialogue progress", progressKey: string(ProgressAIDialogueCount), bronze: "3", silver: "15", gold: "40", sortOrder: 2},
	}}
	tx := &scriptedReadTx{
		rows: []pgx.Row{
			validPrincipalRow(),
			headRow(int64Pointer(77), 4, stringPointer("succeeded"), 9, 2, 1),
			countRow(15),
			textRow(string(metricsJSON)),
		},
		ruleRows: rules,
	}
	var options pgx.TxOptions
	repository, err := newPostgresRepository(func(_ context.Context, value pgx.TxOptions) (readTx, error) {
		options = value
		return tx, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.LoadSelf(context.Background(), testQuery())
	if err != nil {
		t.Fatalf("LoadSelf() error = %v", err)
	}
	if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly || !tx.committed || tx.rolledBack {
		t.Fatalf("transaction = %#v committed=%t rolledBack=%t", options, tx.committed, tx.rolledBack)
	}
	if snapshot.RuleSetVersion != 1 || snapshot.RuleHeadRevision != 2 || snapshot.AnalyticsHeadRevision != 4 ||
		snapshot.AIDialogueCount != 15 || len(snapshot.Rules) != 2 || snapshot.Metrics == nil || len(snapshot.Metrics.ExamHistory) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(tx.queries) != 5 || !strings.Contains(tx.queries[0], "pintia_actor_identifiers") ||
		!strings.Contains(tx.queries[1], "achievement_rule_head") ||
		!strings.Contains(tx.queries[2], "ORDER BY sort_order ASC") ||
		!strings.Contains(tx.queries[3], "run_kind = 'reply'") ||
		!strings.Contains(tx.queries[4], "student_analytics") {
		t.Fatalf("queries = %#v", tx.queries)
	}
	if len(tx.arguments[0]) != 4 || tx.arguments[0][0] != testAccountID || tx.arguments[0][1] != int64(3) ||
		tx.arguments[0][2] != string(auth.RoleStudent) || tx.arguments[0][3] != testSessionID ||
		len(tx.arguments[2]) != 1 || tx.arguments[2][0] != int64(9) ||
		len(tx.arguments[3]) != 1 || tx.arguments[3][0] != int64(11) ||
		len(tx.arguments[4]) != 2 || tx.arguments[4][0] != int64(77) || tx.arguments[4][1] != int64(101) {
		t.Fatalf("arguments = %#v", tx.arguments)
	}
}

func TestPostgresLoadsStudentNumberSelectorInOneRepeatableRead(t *testing.T) {
	t.Parallel()

	metricsJSON, err := json.Marshal(testMetrics())
	if err != nil {
		t.Fatal(err)
	}
	tx := &scriptedReadTx{
		rows: []pgx.Row{
			studentNumberSubjectRow(11, 101, "20260001"),
			headRow(int64Pointer(77), 4, stringPointer("succeeded"), 9, 2, 1),
			countRow(15),
			textRow(string(metricsJSON)),
		},
		ruleRows: &ruleRows{values: []ruleRow{{
			code: "exam", title: "Exam", description: "Exam progress", progressKey: string(ProgressExamCount),
			bronze: "1", silver: "3", gold: "8", sortOrder: 1,
		}}},
	}
	var options pgx.TxOptions
	repository, err := newPostgresRepository(func(_ context.Context, value pgx.TxOptions) (readTx, error) {
		options = value
		return tx, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.LoadByStudentNumber(context.Background(), StudentNumberQuery{StudentNumber: "20260001"})
	if err != nil {
		t.Fatalf("LoadByStudentNumber() error = %v", err)
	}
	if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly || !tx.committed || tx.rolledBack ||
		snapshot.AIDialogueCount != 15 || snapshot.Metrics == nil || len(snapshot.Rules) != 1 {
		t.Fatalf("options/snapshot/transaction = %#v/%#v/%#v", options, snapshot, tx)
	}
	if len(tx.queries) != 5 || !strings.Contains(tx.queries[0], "account.student_number = $1") ||
		!strings.Contains(tx.queries[0], "identifier.identifier_kind = 'student_number'") ||
		!strings.Contains(tx.queries[0], "account.disabled_at IS NULL") ||
		len(tx.arguments[0]) != 1 || tx.arguments[0][0] != "20260001" ||
		len(tx.arguments[3]) != 1 || tx.arguments[3][0] != int64(11) ||
		len(tx.arguments[4]) != 2 || tx.arguments[4][0] != int64(77) || tx.arguments[4][1] != int64(101) {
		t.Fatalf("queries/arguments = %#v/%#v", tx.queries, tx.arguments)
	}
}

func TestPostgresLoadsExactStudentIdentitySelectorInOneRepeatableRead(t *testing.T) {
	t.Parallel()

	tx := &scriptedReadTx{
		rows: []pgx.Row{
			studentIdentitySubjectRow(11, 101, "20260001", "Alice"),
			headRow(nil, 0, nil, 9, 1, 1),
			countRow(4),
		},
		ruleRows: &ruleRows{values: []ruleRow{{
			code: "dialogue", title: "Dialogue", description: "Dialogue progress", progressKey: string(ProgressAIDialogueCount),
			bronze: "3", silver: "15", gold: "40", sortOrder: 1,
		}}},
	}
	repository := mustPostgresRepository(t, tx)
	snapshot, err := repository.LoadByStudentIdentity(context.Background(), StudentIdentityQuery{
		StudentNumber: "20260001",
		PTANickname:   "Alice",
	})
	if err != nil || !tx.committed || tx.rolledBack || snapshot.AIDialogueCount != 4 || len(tx.queries) != 4 {
		t.Fatalf("snapshot/error/transaction = %#v/%v/%#v", snapshot, err, tx)
	}
	if !strings.Contains(tx.queries[0], "account.student_number = $1") ||
		!strings.Contains(tx.queries[0], "account.pta_nickname = $2") ||
		len(tx.arguments[0]) != 2 || tx.arguments[0][0] != "20260001" || tx.arguments[0][1] != "Alice" {
		t.Fatalf("queries/arguments = %#v/%#v", tx.queries, tx.arguments)
	}
}

func TestPostgresExactStudentIdentityMismatchReturnsNotFound(t *testing.T) {
	t.Parallel()

	tx := &scriptedReadTx{rows: []pgx.Row{staticErrorRow{err: pgx.ErrNoRows}}}
	repository := mustPostgresRepository(t, tx)
	_, err := repository.LoadByStudentIdentity(context.Background(), StudentIdentityQuery{
		StudentNumber: "20260001",
		PTANickname:   "Wrong",
	})
	if CodeOf(err) != ErrorStudentNotFound || tx.committed || !tx.rolledBack || len(tx.queries) != 1 {
		t.Fatalf("error/transaction = %v/%#v", err, tx)
	}
}

func TestPostgresStudentNumberSelectorReturnsNotFoundAndRollsBack(t *testing.T) {
	t.Parallel()

	tx := &scriptedReadTx{rows: []pgx.Row{staticErrorRow{err: pgx.ErrNoRows}}}
	repository := mustPostgresRepository(t, tx)
	_, err := repository.LoadByStudentNumber(context.Background(), StudentNumberQuery{StudentNumber: "20269999"})
	if CodeOf(err) != ErrorStudentNotFound || tx.committed || !tx.rolledBack || len(tx.queries) != 1 {
		t.Fatalf("error/transaction = %v/%#v", err, tx)
	}
}

func TestPostgresStudentNumberSelectorRejectsInconsistentActorBinding(t *testing.T) {
	t.Parallel()

	tx := &scriptedReadTx{rows: []pgx.Row{
		studentNumberBindingRow(11, 101, "20260001", 102, "20260001"),
	}}
	repository := mustPostgresRepository(t, tx)
	_, err := repository.LoadByStudentNumber(context.Background(), StudentNumberQuery{StudentNumber: "20260001"})
	if CodeOf(err) != ErrorStoredDataInvalid || tx.committed || !tx.rolledBack || len(tx.queries) != 1 {
		t.Fatalf("error/transaction = %v/%#v", err, tx)
	}
}

func TestPostgresReturnsEmptyAnalyticsWithLiveDialogue(t *testing.T) {
	t.Parallel()

	tx := &scriptedReadTx{
		rows: []pgx.Row{
			validPrincipalRow(),
			headRow(nil, 0, nil, 9, 1, 1),
			countRow(4),
		},
		ruleRows: &ruleRows{values: []ruleRow{{
			code: "dialogue", title: "Dialogue", description: "Dialogue progress", progressKey: string(ProgressAIDialogueCount),
			bronze: "3", silver: "15", gold: "40", sortOrder: 1,
		}}},
	}
	repository := mustPostgresRepository(t, tx)
	snapshot, err := repository.LoadSelf(context.Background(), testQuery())
	if err != nil || snapshot.AnalyticsHeadRevision != 0 || snapshot.Metrics != nil || snapshot.AIDialogueCount != 4 || !tx.committed || len(tx.queries) != 4 {
		t.Fatalf("snapshot/error/transaction = %#v/%v/%#v", snapshot, err, tx)
	}
}

func TestPostgresRejectsRevokedPrincipalAndRollsBack(t *testing.T) {
	t.Parallel()

	tx := &scriptedReadTx{rows: []pgx.Row{staticErrorRow{err: pgx.ErrNoRows}}}
	repository := mustPostgresRepository(t, tx)
	_, err := repository.LoadSelf(context.Background(), testQuery())
	if CodeOf(err) != ErrorPrincipalRejected || tx.committed || !tx.rolledBack || len(tx.queries) != 1 {
		t.Fatalf("error/transaction = %v/%#v", err, tx)
	}
}

func TestParseCanonicalTargetRejectsNoncanonicalOrUnboundedValues(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]float64{"0": 0, "1": 1, "55.5": 55.5} {
		got, err := parseCanonicalTarget(raw)
		if err != nil || got != want {
			t.Fatalf("parseCanonicalTarget(%q) = %v, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "01", "1.", ".5", "-1", "NaN", "Infinity", "1e3"} {
		if _, err := parseCanonicalTarget(raw); err == nil {
			t.Fatalf("parseCanonicalTarget(%q) error = nil", raw)
		}
	}
}

func mustPostgresRepository(t *testing.T, tx readTx) *PostgresRepository {
	t.Helper()
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (readTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func validPrincipalRow() pgx.Row {
	return scanRow(func(destinations ...any) error {
		if len(destinations) != 11 {
			return errors.New("invalid principal scan")
		}
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testAccountID
		*(destinations[2].(*string)) = string(auth.RoleStudent)
		*(destinations[3].(*int64)) = 3
		actorID := int64(101)
		studentNumber := "20260001"
		*(destinations[4].(**int64)) = &actorID
		*(destinations[5].(**string)) = &studentNumber
		*(destinations[6].(*int64)) = 22
		*(destinations[7].(*string)) = testSessionID
		*(destinations[8].(*int64)) = 3
		*(destinations[9].(**int64)) = &actorID
		*(destinations[10].(**string)) = &studentNumber
		return nil
	})
}

func studentNumberSubjectRow(accountDatabaseID, actorID int64, studentNumber string) pgx.Row {
	return studentNumberBindingRow(accountDatabaseID, actorID, studentNumber, actorID, studentNumber)
}

func studentNumberBindingRow(
	accountDatabaseID int64,
	accountActorID int64,
	accountStudentNumber string,
	identifierActorID int64,
	identifierStudentNumber string,
) pgx.Row {
	return scanRow(func(destinations ...any) error {
		if len(destinations) != 5 {
			return errors.New("invalid student-number subject scan")
		}
		*(destinations[0].(*int64)) = accountDatabaseID
		*(destinations[1].(*int64)) = accountActorID
		*(destinations[2].(*string)) = accountStudentNumber
		*(destinations[3].(*int64)) = identifierActorID
		*(destinations[4].(*string)) = identifierStudentNumber
		return nil
	})
}

func studentIdentitySubjectRow(accountDatabaseID, actorID int64, studentNumber, ptaNickname string) pgx.Row {
	return scanRow(func(destinations ...any) error {
		if len(destinations) != 6 {
			return errors.New("invalid student-identity subject scan")
		}
		*(destinations[0].(*int64)) = accountDatabaseID
		*(destinations[1].(*int64)) = actorID
		*(destinations[2].(*string)) = studentNumber
		*(destinations[3].(*string)) = ptaNickname
		*(destinations[4].(*int64)) = actorID
		*(destinations[5].(*string)) = studentNumber
		return nil
	})
}

func headRow(generationID *int64, headRevision int64, status *string, ruleSetID, ruleHeadRevision, ruleSetVersion int64) pgx.Row {
	return scanRow(func(destinations ...any) error {
		if len(destinations) != 6 {
			return errors.New("invalid head scan")
		}
		*(destinations[0].(**int64)) = generationID
		*(destinations[1].(*int64)) = headRevision
		*(destinations[2].(**string)) = status
		*(destinations[3].(*int64)) = ruleSetID
		*(destinations[4].(*int64)) = ruleHeadRevision
		*(destinations[5].(*int64)) = ruleSetVersion
		return nil
	})
}

func countRow(value int64) pgx.Row {
	return scanRow(func(destinations ...any) error {
		*(destinations[0].(*int64)) = value
		return nil
	})
}

func textRow(value string) pgx.Row {
	return scanRow(func(destinations ...any) error {
		*(destinations[0].(*string)) = value
		return nil
	})
}

func int64Pointer(value int64) *int64    { return &value }
func stringPointer(value string) *string { return &value }
