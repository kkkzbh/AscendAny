package examgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID = "11111111-1111-4111-8111-111111111111"
	testSessionID = "22222222-2222-4222-8222-222222222222"
	testJWTID     = "33333333-3333-4333-8333-333333333333"
	testExamID    = "44444444-4444-4444-8444-444444444444"
)

type repositoryStub struct {
	generation   Generation
	batch        EventBatch
	found        bool
	err          error
	currentQuery CurrentQuery
	eventQuery   EventQuery
	currentCalls int
	eventCalls   int
}

func (stub *repositoryStub) LoadCurrent(
	_ context.Context,
	query CurrentQuery,
) (Generation, bool, error) {
	stub.currentCalls++
	stub.currentQuery = query
	return stub.generation, stub.found, stub.err
}

func (stub *repositoryStub) LoadEvents(
	_ context.Context,
	query EventQuery,
) (EventBatch, bool, error) {
	stub.eventCalls++
	stub.eventQuery = query
	return stub.batch, stub.found, stub.err
}

func TestServiceReturnsValidatedCurrentGeneration(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{generation: validGeneration(), found: true}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	generation, found, err := service.GetCurrent(context.Background(), CurrentQuery{
		Principal: validPrincipal(), ExamID: testExamID,
	})
	if err != nil || !found || generation.GenerationID != "42" || repository.currentCalls != 1 {
		t.Fatalf("generation=%#v found=%t calls=%d error=%v", generation, found, repository.currentCalls, err)
	}

	repository.found = false
	generation, found, err = service.GetCurrent(context.Background(), CurrentQuery{
		Principal: validPrincipal(), ExamID: testExamID,
	})
	if err != nil || found || generation.GenerationID != "" {
		t.Fatalf("not found generation=%#v found=%t error=%v", generation, found, err)
	}
}

func TestServiceRejectsInvalidQueriesBeforeRepositoryRead(t *testing.T) {
	t.Parallel()
	invalidPrincipal := validPrincipal()
	invalidPrincipal.JWTID = "invalid"
	currentQueries := []CurrentQuery{
		{Principal: invalidPrincipal, ExamID: testExamID},
		{Principal: validPrincipal(), ExamID: "invalid"},
	}
	for _, query := range currentQueries {
		repository := &repositoryStub{}
		service, _ := NewService(repository)
		if _, _, err := service.GetCurrent(context.Background(), query); CodeOf(err) != ErrorInvalidInput || repository.currentCalls != 0 {
			t.Fatalf("GetCurrent(%#v) error=%v calls=%d", query, err, repository.currentCalls)
		}
	}
	eventQueries := []EventQuery{
		{Principal: validPrincipal(), ExamID: testExamID, GenerationID: "", AfterSequence: 0, Limit: 1},
		{Principal: validPrincipal(), ExamID: testExamID, GenerationID: "42", AfterSequence: -1, Limit: 1},
		{Principal: validPrincipal(), ExamID: testExamID, GenerationID: "42", AfterSequence: 0, Limit: 0},
		{Principal: validPrincipal(), ExamID: testExamID, GenerationID: "42", AfterSequence: 0, Limit: MaxEventPageSize + 1},
	}
	for _, query := range eventQueries {
		repository := &repositoryStub{}
		service, _ := NewService(repository)
		if _, _, err := service.ReadEvents(context.Background(), query); CodeOf(err) != ErrorInvalidInput || repository.eventCalls != 0 {
			t.Fatalf("ReadEvents(%#v) error=%v calls=%d", query, err, repository.eventCalls)
		}
	}
}

func TestServiceValidatesGenerationStateAndSafeErrorCode(t *testing.T) {
	t.Parallel()
	tests := map[string]Generation{
		"noncanonical ID": func() Generation {
			value := validGeneration()
			value.GenerationID = "042"
			return value
		}(),
		"missing event head": func() Generation {
			value := validGeneration()
			value.EventHead = 0
			return value
		}(),
		"failed without safe code": func() Generation {
			value := validGeneration()
			value.Status = StatusFailed
			value.FinishedAt = timePointer(value.CreatedAt.Add(2 * time.Minute))
			value.ErrorCode = stringPointer("unsafe detail")
			return value
		}(),
		"failed with unknown identifier": func() Generation {
			value := validGeneration()
			value.Status = StatusFailed
			value.FinishedAt = timePointer(value.CreatedAt.Add(2 * time.Minute))
			value.ErrorCode = stringPointer("database_failure")
			return value
		}(),
		"non-UTC creation time": func() Generation {
			value := validGeneration()
			location := time.FixedZone("UTC+08", 8*60*60)
			value.CreatedAt = value.CreatedAt.In(location)
			value.StartedAt = timePointer(value.StartedAt.In(location))
			return value
		}(),
		"queued with attempt": func() Generation {
			value := validGeneration()
			value.Status = StatusQueued
			value.AttemptCount = 1
			value.StartedAt = nil
			return value
		}(),
	}
	for name, generation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := &repositoryStub{generation: generation, found: true}
			service, _ := NewService(repository)
			if _, _, err := service.GetCurrent(context.Background(), CurrentQuery{
				Principal: validPrincipal(), ExamID: testExamID,
			}); CodeOf(err) != ErrorStoredDataInvalid {
				t.Fatalf("GetCurrent() error=%v", err)
			}
		})
	}
}

func TestServiceAcceptsEveryPersistedGenerationStatus(t *testing.T) {
	t.Parallel()
	createdAt := testTime()
	startedAt := createdAt.Add(time.Minute)
	finishedAt := startedAt.Add(time.Minute)
	failureCode := "invalid_dataset"
	tests := map[Status]Generation{
		StatusQueued: {
			GenerationID: "1", Status: StatusQueued, CreatedAt: createdAt, EventHead: 1,
		},
		StatusRunning: {
			GenerationID: "2", Status: StatusRunning, AttemptCount: 1,
			CreatedAt: createdAt, StartedAt: &startedAt, EventHead: 2,
		},
		StatusSucceeded: {
			GenerationID: "3", Status: StatusSucceeded, AttemptCount: 1,
			CreatedAt: createdAt, StartedAt: &startedAt, FinishedAt: &finishedAt, EventHead: 3,
		},
		StatusSuperseded: {
			GenerationID: "4", Status: StatusSuperseded, AttemptCount: 2,
			CreatedAt: createdAt, StartedAt: &startedAt, FinishedAt: &finishedAt, EventHead: 4,
		},
		StatusFailed: {
			GenerationID: "5", Status: StatusFailed, AttemptCount: 1,
			CreatedAt: createdAt, StartedAt: &startedAt, FinishedAt: &finishedAt,
			ErrorCode: &failureCode, EventHead: 3,
		},
	}
	for status, generation := range tests {
		status, generation := status, generation
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			repository := &repositoryStub{generation: generation, found: true}
			service, _ := NewService(repository)
			actual, found, err := service.GetCurrent(context.Background(), CurrentQuery{
				Principal: validPrincipal(), ExamID: testExamID,
			})
			if err != nil || !found || actual.Status != status {
				t.Fatalf("generation=%#v found=%t error=%v", actual, found, err)
			}
		})
	}
}

func TestServiceReturnsCanonicalOrderedEventsAndTerminalState(t *testing.T) {
	t.Parallel()
	eventTime := testTime().Add(3 * time.Minute)
	repository := &repositoryStub{
		found: true,
		batch: EventBatch{
			GenerationID: "42",
			EventHead:    2,
			Events: []Event{{
				Sequence: 2, Type: EventSucceeded,
				Payload: json.RawMessage(` {"students":2,"problems":1.0} `), CreatedAt: eventTime,
			}},
			Terminal: true,
		},
	}
	service, _ := NewService(repository)
	batch, found, err := service.ReadEvents(context.Background(), EventQuery{
		Principal: validPrincipal(), ExamID: testExamID, GenerationID: "42", AfterSequence: 1, Limit: 5,
	})
	if err != nil || !found || !batch.Terminal || string(batch.Events[0].Payload) != `{"problems":1,"students":2}` {
		t.Fatalf("batch=%#v found=%t error=%v", batch, found, err)
	}
}

func TestServiceRejectsCursorAboveHeadAndInvalidEventPages(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		batch EventBatch
		code  ErrorCode
	}{
		"wrong pinned generation": {
			batch: EventBatch{GenerationID: "43", EventHead: 3},
			code:  ErrorStoredDataInvalid,
		},
		"cursor above head": {
			batch: EventBatch{GenerationID: "42", EventHead: 1, Events: []Event{}},
			code:  ErrorEventCursorInvalid,
		},
		"sequence gap": {
			batch: EventBatch{GenerationID: "42", EventHead: 4, Events: []Event{{
				Sequence: 4, Type: EventRunning, Payload: json.RawMessage(`{}`), CreatedAt: testTime(),
			}}},
			code: ErrorStoredDataInvalid,
		},
		"terminal backlog": {
			batch: EventBatch{GenerationID: "42", EventHead: 4, Events: []Event{{
				Sequence: 3, Type: EventSucceeded, Payload: json.RawMessage(`{}`), CreatedAt: testTime(),
			}}, Terminal: true},
			code: ErrorStoredDataInvalid,
		},
		"terminal event with false flag": {
			batch: EventBatch{GenerationID: "42", EventHead: 3, Events: []Event{{
				Sequence: 3, Type: EventSucceeded, Payload: json.RawMessage(`{}`), CreatedAt: testTime(),
			}}, Terminal: false},
			code: ErrorStoredDataInvalid,
		},
		"terminal event before head": {
			batch: EventBatch{GenerationID: "42", EventHead: 4, Events: []Event{{
				Sequence: 3, Type: EventFailed, Payload: json.RawMessage(`{}`), CreatedAt: testTime(),
			}}, Terminal: false},
			code: ErrorStoredDataInvalid,
		},
		"non-UTC event time": {
			batch: EventBatch{GenerationID: "42", EventHead: 3, Events: []Event{{
				Sequence: 3, Type: EventRunning, Payload: json.RawMessage(`{}`),
				CreatedAt: testTime().In(time.FixedZone("UTC+08", 8*60*60)),
			}}, Terminal: false},
			code: ErrorStoredDataInvalid,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := &repositoryStub{found: true, batch: test.batch}
			service, _ := NewService(repository)
			_, _, err := service.ReadEvents(context.Background(), EventQuery{
				Principal: validPrincipal(), ExamID: testExamID, GenerationID: "42", AfterSequence: 2, Limit: 5,
			})
			if CodeOf(err) != test.code {
				t.Fatalf("ReadEvents() error=%v code=%q", err, CodeOf(err))
			}
		})
	}
}

func TestServicePropagatesRepositoryError(t *testing.T) {
	t.Parallel()
	want := domainError(ErrorDatabase, false, "test", errors.New("database unavailable"))
	repository := &repositoryStub{err: want}
	service, _ := NewService(repository)
	if _, _, err := service.GetCurrent(context.Background(), CurrentQuery{
		Principal: validPrincipal(), ExamID: testExamID,
	}); !errors.Is(err, want) {
		t.Fatalf("GetCurrent() error=%v", err)
	}
}

func TestConstructorsAndCanonicalGenerationIDs(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewService(nil) error=%v", err)
	}
	if _, err := NewPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewPostgresRepository(nil) error=%v", err)
	}
	if _, err := newPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("newPostgresRepository(nil) error=%v", err)
	}
	for value, valid := range map[string]bool{
		"1": true, "42": true, "9223372036854775807": true,
		"": false, "0": false, "01": false, "-1": false, "9223372036854775808": false,
	} {
		if ValidGenerationID(value) != valid {
			t.Fatalf("ValidGenerationID(%q)=%t, want %t", value, ValidGenerationID(value), valid)
		}
	}
}

func validPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 3,
	}
}

func validGeneration() Generation {
	createdAt := testTime()
	startedAt := createdAt.Add(time.Minute)
	return Generation{
		GenerationID: "42", Status: StatusRunning, AttemptCount: 1,
		CreatedAt: createdAt, StartedAt: &startedAt, EventHead: 2,
	}
}

func testTime() time.Time {
	return time.Date(2026, 7, 11, 3, 4, 5, 0, time.UTC)
}

func timePointer(value time.Time) *time.Time { return &value }

func stringPointer(value string) *string { return &value }
