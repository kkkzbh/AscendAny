package importing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestListJobsPaginatesNewestFirstAndSanitizesFailure(t *testing.T) {
	t.Parallel()

	firstID := "33333333-3333-4333-8333-333333333333"
	secondID := "22222222-2222-4222-8222-222222222222"
	thirdID := "11111111-1111-4111-8111-111111111111"
	zone := time.FixedZone("source", 8*60*60)
	pool := &jobPageQueryReader{rows: []jobResultRow{
		{
			job: PublicJob{
				ID: firstID, ArtifactSHA256: strings.Repeat("a", 64), Status: JobQueued, Stage: StageReceived,
				CreatedAt: time.Date(2026, 7, 11, 10, 0, 0, 0, zone), UpdatedAt: time.Date(2026, 7, 11, 10, 1, 0, 0, zone),
			},
		},
		{
			job: PublicJob{
				ID: secondID, ArtifactSHA256: strings.Repeat("b", 64), Status: JobFailed, Stage: StageFailed,
				CreatedAt: time.Date(2026, 7, 11, 9, 0, 0, 0, zone), UpdatedAt: time.Date(2026, 7, 11, 9, 1, 0, 0, zone),
			},
			errorCode:      stringPointer(string(ErrorValidation)),
			errorPermanent: boolPointer(true),
		},
		{
			job: PublicJob{
				ID: thirdID, ArtifactSHA256: strings.Repeat("c", 64), Status: JobSucceeded, Stage: StageCompleted,
				CreatedAt: time.Date(2026, 7, 11, 8, 0, 0, 0, zone), UpdatedAt: time.Date(2026, 7, 11, 8, 1, 0, 0, zone),
			},
		},
	}}
	reader, err := NewPostgresReader(pool)
	if err != nil {
		t.Fatal(err)
	}

	page, err := reader.ListJobs(context.Background(), nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != firstID || page.Items[1].ID != secondID {
		t.Fatalf("items=%#v", page.Items)
	}
	if page.NextCursor == nil || *page.NextCursor != secondID {
		t.Fatalf("next cursor=%v", page.NextCursor)
	}
	if page.Items[0].CreatedAt.Location() != time.UTC || page.Items[1].UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps were not normalized to UTC: %#v", page.Items)
	}
	if page.Items[1].Error == nil || page.Items[1].Error.Code != string(ErrorValidation) ||
		page.Items[1].Error.Message != "The Pintia snapshot failed validation." || !page.Items[1].Error.Permanent {
		t.Fatalf("failed job error=%#v", page.Items[1].Error)
	}
	if pool.queryCalls != 1 || pool.queryRowCalls != 0 || len(pool.queryArguments) != 2 || pool.queryArguments[0] != nil || pool.queryArguments[1] != 3 {
		t.Fatalf("query calls=%d row calls=%d arguments=%#v", pool.queryCalls, pool.queryRowCalls, pool.queryArguments)
	}
}

func TestListJobsResolvesCursorAndRejectsMissingCursor(t *testing.T) {
	t.Parallel()

	cursor := "22222222-2222-4222-8222-222222222222"
	pool := &jobPageQueryReader{cursorIDs: map[string]int64{cursor: 42}, rows: []jobResultRow{}}
	reader, err := NewPostgresReader(pool)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader.ListJobs(context.Background(), &cursor, MaxJobPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.NextCursor != nil || pool.queryRowCalls != 1 || pool.queryCalls != 1 {
		t.Fatalf("page=%#v row calls=%d query calls=%d", page, pool.queryRowCalls, pool.queryCalls)
	}
	if id, ok := pool.queryArguments[0].(int64); !ok || id != 42 || pool.queryArguments[1] != MaxJobPageSize+1 {
		t.Fatalf("query arguments=%#v", pool.queryArguments)
	}

	missing := "11111111-1111-4111-8111-111111111111"
	_, err = reader.ListJobs(context.Background(), &missing, DefaultJobPageSize)
	if code, owned := CodeOf(err); !owned || code != ErrorJobCursorInvalid || !IsPermanent(err) {
		t.Fatalf("missing cursor error=%v code=%q owned=%t", err, code, owned)
	}
}

func TestListJobsRejectsInvalidArgumentsWithoutDatabaseAccess(t *testing.T) {
	t.Parallel()

	pool := &jobPageQueryReader{}
	reader, err := NewPostgresReader(pool)
	if err != nil {
		t.Fatal(err)
	}
	invalidCursor := "not-a-uuid"
	for _, test := range []struct {
		cursor *string
		limit  int
	}{
		{limit: 0},
		{limit: MaxJobPageSize + 1},
		{cursor: &invalidCursor, limit: DefaultJobPageSize},
	} {
		if _, err := reader.ListJobs(context.Background(), test.cursor, test.limit); err == nil {
			t.Fatalf("cursor=%v limit=%d was accepted", test.cursor, test.limit)
		}
	}
	if pool.queryCalls != 0 || pool.queryRowCalls != 0 {
		t.Fatalf("invalid inputs reached database: query=%d row=%d", pool.queryCalls, pool.queryRowCalls)
	}
}

type jobResultRow struct {
	job            PublicJob
	errorCode      *string
	errorPermanent *bool
}

type jobPageQueryReader struct {
	cursorIDs      map[string]int64
	rows           []jobResultRow
	queryCalls     int
	queryRowCalls  int
	queryArguments []any
}

func (reader *jobPageQueryReader) QueryRow(_ context.Context, _ string, arguments ...any) pgx.Row {
	reader.queryRowCalls++
	if len(arguments) != 1 {
		return rowError{err: errors.New("unexpected cursor argument count")}
	}
	cursor, ok := arguments[0].(string)
	if !ok {
		return rowError{err: errors.New("cursor is not a string")}
	}
	id, found := reader.cursorIDs[cursor]
	if !found {
		return rowError{err: pgx.ErrNoRows}
	}
	return rowScan(func(targets ...any) error {
		if len(targets) != 1 {
			return errors.New("unexpected cursor scan target count")
		}
		*(targets[0].(*int64)) = id
		return nil
	})
}

func (reader *jobPageQueryReader) Query(_ context.Context, _ string, arguments ...any) (pgx.Rows, error) {
	reader.queryCalls++
	if len(arguments) != 2 {
		return nil, errors.New("unexpected job page argument count")
	}
	reader.queryArguments = append([]any(nil), arguments...)
	return &jobPageRows{rows: append([]jobResultRow(nil), reader.rows...)}, nil
}

type jobPageRows struct {
	rows    []jobResultRow
	index   int
	current *jobResultRow
	closed  bool
}

func (rows *jobPageRows) Close()                                       { rows.closed = true }
func (rows *jobPageRows) Err() error                                   { return nil }
func (rows *jobPageRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *jobPageRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (rows *jobPageRows) Next() bool {
	if rows.closed || rows.index >= len(rows.rows) {
		rows.closed = true
		rows.current = nil
		return false
	}
	rows.current = &rows.rows[rows.index]
	rows.index++
	return true
}

func (rows *jobPageRows) Scan(targets ...any) error {
	if rows.current == nil || len(targets) != 10 {
		return errors.New("invalid import job row scan")
	}
	job := rows.current.job
	*(targets[0].(*string)) = job.ID
	*(targets[1].(*string)) = job.ArtifactSHA256
	*(targets[2].(*JobStatus)) = job.Status
	*(targets[3].(*JobStage)) = job.Stage
	*(targets[4].(*time.Time)) = job.CreatedAt
	*(targets[5].(*time.Time)) = job.UpdatedAt
	*(targets[6].(**string)) = cloneStringPointer(job.ExamID)
	*(targets[7].(**string)) = cloneStringPointer(job.SnapshotID)
	*(targets[8].(**string)) = cloneStringPointer(rows.current.errorCode)
	*(targets[9].(**bool)) = cloneBoolPointer(rows.current.errorPermanent)
	return nil
}

func (rows *jobPageRows) Values() ([]any, error) { return nil, errors.New("unused") }
func (rows *jobPageRows) RawValues() [][]byte    { return nil }
func (rows *jobPageRows) Conn() *pgx.Conn        { return nil }

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func TestReadEventsKeepsTerminalFalseUntilLargeBacklogIsDrained(t *testing.T) {
	allEvents := make([]PublicEvent, 205)
	for index := range allEvents {
		allEvents[index] = PublicEvent{
			Sequence:   int64(index + 1),
			Type:       "progress",
			OccurredAt: time.Date(2026, 7, 10, 1, 2, index%60, 0, time.FixedZone("test", 8*60*60)),
			Payload:    json.RawMessage(`{"step":1}`),
		}
	}
	pool := &eventQueryReader{found: true, status: JobSucceeded, events: allEvents}
	reader, err := NewPostgresReader(pool)
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []struct {
		after    int64
		count    int
		last     int64
		terminal bool
	}{
		{after: 0, count: 100, last: 100, terminal: false},
		{after: 100, count: 100, last: 200, terminal: false},
		{after: 200, count: 5, last: 205, terminal: true},
	} {
		batch, found, err := reader.ReadEvents(
			context.Background(),
			"11111111-1111-4111-8111-111111111111",
			expected.after,
			MaxEventBatchSize,
		)
		if err != nil || !found {
			t.Fatalf("ReadEvents(after=%d) found=%t error=%v", expected.after, found, err)
		}
		if len(batch.Events) != expected.count || batch.Terminal != expected.terminal {
			t.Fatalf("ReadEvents(after=%d) = count %d terminal %t", expected.after, len(batch.Events), batch.Terminal)
		}
		if batch.Events[len(batch.Events)-1].Sequence != expected.last {
			t.Fatalf("ReadEvents(after=%d) last sequence = %d, want %d", expected.after, batch.Events[len(batch.Events)-1].Sequence, expected.last)
		}
		for _, event := range batch.Events {
			if event.OccurredAt.Location() != time.UTC {
				t.Fatalf("event %d time location = %v, want UTC", event.Sequence, event.OccurredAt.Location())
			}
		}
	}
	if pool.queryRowCalls != 0 || pool.queryCalls != 3 {
		t.Fatalf("QueryRow calls = %d, Query calls = %d", pool.queryRowCalls, pool.queryCalls)
	}
	for _, limit := range pool.requestedLimits {
		if limit != MaxEventBatchSize+1 {
			t.Fatalf("database lookahead limit = %d, want %d", limit, MaxEventBatchSize+1)
		}
	}
}

func TestReadEventsRejectsCursorAheadOfDurableHead(t *testing.T) {
	t.Parallel()

	pool := &eventQueryReader{
		found:  true,
		status: JobRunning,
		events: []PublicEvent{{
			Sequence:   1,
			Type:       "queued",
			OccurredAt: time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC),
			Payload:    json.RawMessage(`{"stage":"received"}`),
		}},
	}
	reader, err := NewPostgresReader(pool)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := reader.ReadEvents(
		context.Background(),
		"11111111-1111-4111-8111-111111111111",
		2,
		MaxEventBatchSize,
	)
	if found {
		t.Fatal("cursor-ahead read unexpectedly returned a job")
	}
	if code, owned := CodeOf(err); !owned || code != ErrorEventCursorAhead {
		t.Fatalf("ReadEvents() error=%v code=%q owned=%t", err, code, owned)
	}
}

type eventQueryReader struct {
	found           bool
	status          JobStatus
	events          []PublicEvent
	queryCalls      int
	queryRowCalls   int
	requestedLimits []int
}

func (reader *eventQueryReader) QueryRow(context.Context, string, ...any) pgx.Row {
	reader.queryRowCalls++
	return rowError{err: errors.New("unexpected QueryRow")}
}

func (reader *eventQueryReader) Query(_ context.Context, _ string, arguments ...any) (pgx.Rows, error) {
	reader.queryCalls++
	if len(arguments) != 3 {
		return nil, errors.New("unexpected query argument count")
	}
	after, ok := arguments[1].(int64)
	if !ok {
		return nil, errors.New("event cursor is not int64")
	}
	limit, ok := arguments[2].(int)
	if !ok {
		return nil, errors.New("event limit is not int")
	}
	reader.requestedLimits = append(reader.requestedLimits, limit)
	if !reader.found {
		return &eventRows{}, nil
	}
	eventHead := int64(0)
	if len(reader.events) > 0 {
		eventHead = reader.events[len(reader.events)-1].Sequence
	}
	results := make([]eventResultRow, 0, limit)
	for index := range reader.events {
		if reader.events[index].Sequence <= after {
			continue
		}
		event := reader.events[index]
		results = append(results, eventResultRow{status: reader.status, head: eventHead, event: &event})
		if len(results) == limit {
			break
		}
	}
	if len(results) == 0 {
		results = append(results, eventResultRow{status: reader.status, head: eventHead})
	}
	return &eventRows{rows: results}, nil
}

type eventResultRow struct {
	status JobStatus
	head   int64
	event  *PublicEvent
}

type eventRows struct {
	rows    []eventResultRow
	index   int
	current *eventResultRow
	closed  bool
}

func (rows *eventRows) Close()                                       { rows.closed = true }
func (rows *eventRows) Err() error                                   { return nil }
func (rows *eventRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *eventRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (rows *eventRows) Next() bool {
	if rows.closed || rows.index >= len(rows.rows) {
		rows.closed = true
		rows.current = nil
		return false
	}
	rows.current = &rows.rows[rows.index]
	rows.index++
	return true
}

func (rows *eventRows) Scan(targets ...any) error {
	if rows.current == nil || len(targets) != 6 {
		return errors.New("invalid event row scan")
	}
	*(targets[0].(*JobStatus)) = rows.current.status
	*(targets[1].(*int64)) = rows.current.head
	if rows.current.event == nil {
		*(targets[2].(**int64)) = nil
		*(targets[3].(**string)) = nil
		*(targets[4].(**time.Time)) = nil
		*(targets[5].(**string)) = nil
		return nil
	}
	sequence := rows.current.event.Sequence
	eventType := rows.current.event.Type
	occurredAt := rows.current.event.OccurredAt
	payload := string(rows.current.event.Payload)
	*(targets[2].(**int64)) = &sequence
	*(targets[3].(**string)) = &eventType
	*(targets[4].(**time.Time)) = &occurredAt
	*(targets[5].(**string)) = &payload
	return nil
}

func (rows *eventRows) Values() ([]any, error) { return nil, errors.New("unused") }
func (rows *eventRows) RawValues() [][]byte    { return nil }
func (rows *eventRows) Conn() *pgx.Conn        { return nil }
