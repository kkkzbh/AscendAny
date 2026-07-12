package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/examgeneration"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

type examGenerationServiceStub struct {
	getCurrent func(context.Context, string, string) (examgeneration.Generation, bool, error)
	readEvents func(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error)
}

func (stub *examGenerationServiceStub) GetCurrent(
	ctx context.Context,
	access string,
	examID string,
) (examgeneration.Generation, bool, error) {
	return stub.getCurrent(ctx, access, examID)
}

func (stub *examGenerationServiceStub) ReadEvents(
	ctx context.Context,
	access string,
	examID string,
	generationID string,
	after int64,
	limit int,
) (examgeneration.EventBatch, bool, error) {
	return stub.readEvents(ctx, access, examID, generationID, after, limit)
}

func TestCurrentExamGenerationReturnsAuthenticatedCurrentState(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	startedAt := createdAt.Add(time.Minute)
	want := examgeneration.Generation{
		GenerationID: "42",
		Status:       examgeneration.StatusRunning,
		AttemptCount: 1,
		CreatedAt:    createdAt,
		StartedAt:    &startedAt,
		EventHead:    2,
	}
	var calls int
	service := &examGenerationServiceStub{
		getCurrent: func(_ context.Context, access, examID string) (examgeneration.Generation, bool, error) {
			calls++
			if access != "exam-generation-access" || examID != testExamID {
				t.Fatalf("access=%q examID=%q", access, examID)
			}
			return want, true, nil
		},
		readEvents: unexpectedExamGenerationEvents,
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generation", "",
	))
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	var actual examgeneration.Generation
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil || actual.GenerationID != "42" || actual.Status != examgeneration.StatusRunning {
		t.Fatalf("generation=%#v error=%v", actual, err)
	}
}

func TestHTTPHandlerRequiresExamGenerationDependency(t *testing.T) {
	t.Parallel()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ExamGeneration = nil
	if _, err := New(options); err == nil {
		t.Fatal("New accepted a missing exam generation dependency")
	}
}

func TestCurrentExamGenerationRejectsAmbiguousRequestsBeforeService(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	service := &examGenerationServiceStub{
		getCurrent: func(context.Context, string, string) (examgeneration.Generation, bool, error) {
			calls.Add(1)
			return examgeneration.Generation{}, false, nil
		},
		readEvents: func(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
			calls.Add(1)
			return examgeneration.EventBatch{}, false, nil
		},
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	tests := []struct {
		path   string
		body   string
		header func(*http.Request)
		code   string
	}{
		{path: "/api/v2/exams/invalid/analysis-generation", code: "invalid_exam_id"},
		{path: "/api/v2/exams/" + testExamID + "/analysis-generation?", code: "invalid_query"},
		{path: "/api/v2/exams/" + testExamID + "/analysis-generation", body: `{}`, code: "request_body_not_allowed"},
		{path: "/api/v2/exams/" + testExamID + "/analysis-generations/01/events", code: "invalid_generation_id"},
		{
			path: "/api/v2/exams/" + testExamID + "/analysis-generations/42/events",
			header: func(request *http.Request) {
				request.Header.Add("Last-Event-ID", "1")
				request.Header.Add("Last-Event-ID", "2")
			},
			code: "invalid_last_event_id",
		},
		{
			path: "/api/v2/exams/" + testExamID + "/analysis-generations/42/events",
			header: func(request *http.Request) {
				request.Header.Set("Last-Event-ID", "01")
			},
			code: "invalid_last_event_id",
		},
	}
	for _, test := range tests {
		request := examGenerationRequest(http.MethodGet, test.path, test.body)
		if test.header != nil {
			test.header(request)
		}
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("path=%q status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached service: calls=%d", calls.Load())
	}
}

func TestExamGenerationEventStreamPinsGenerationAcrossCurrentHeadSwitch(t *testing.T) {
	t.Parallel()
	currentGenerationID := "42"
	createdAt := time.Date(2026, 7, 11, 2, 0, 0, 0, time.UTC)
	service := &examGenerationServiceStub{
		getCurrent: func(context.Context, string, string) (examgeneration.Generation, bool, error) {
			return examgeneration.Generation{
				GenerationID: currentGenerationID,
				Status:       examgeneration.StatusRunning,
				AttemptCount: 1,
				CreatedAt:    createdAt,
				StartedAt:    &createdAt,
				EventHead:    1,
			}, true, nil
		},
		readEvents: func(_ context.Context, _ string, _ string, generationID string, after int64, _ int) (examgeneration.EventBatch, bool, error) {
			if generationID != "42" || after != 0 {
				t.Fatalf("generationID=%q after=%d", generationID, after)
			}
			return examgeneration.EventBatch{
				GenerationID: "42",
				EventHead:    1,
				Events: []examgeneration.Event{{
					Sequence: 1, Type: examgeneration.EventSuperseded,
					Payload: json.RawMessage(`{}`), CreatedAt: createdAt,
				}},
				Terminal: true,
			}, true, nil
		},
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	currentResponse := newTestResponseRecorder()
	handler.ServeHTTP(currentResponse, examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generation", "",
	))
	if currentResponse.Code != http.StatusOK || !strings.Contains(currentResponse.Body.String(), `"generationId":"42"`) {
		t.Fatalf("current status=%d body=%s", currentResponse.Code, currentResponse.Body.String())
	}

	currentGenerationID = "43"
	pinnedResponse := newTestResponseRecorder()
	handler.ServeHTTP(pinnedResponse, examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	))
	if pinnedResponse.Code != http.StatusOK || !strings.Contains(pinnedResponse.Body.String(), "event: superseded") {
		t.Fatalf("pinned status=%d body=%s", pinnedResponse.Code, pinnedResponse.Body.String())
	}
}

func TestExamGenerationEventStreamDoesNotExposeCrossExamGeneration(t *testing.T) {
	t.Parallel()
	service := &examGenerationServiceStub{
		getCurrent: unexpectedCurrentExamGeneration,
		readEvents: func(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
			return examgeneration.EventBatch{}, false, nil
		},
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/99/events", "",
	))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"exam_generation_not_found"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExamGenerationNotFoundAuthorizationAndFailuresAreBounded(t *testing.T) {
	t.Parallel()
	secret := "database-password-leak"
	tests := []struct {
		name   string
		found  bool
		err    error
		status int
		code   string
	}{
		{name: "not found", status: http.StatusNotFound, code: "exam_generation_not_found"},
		{
			name: "principal rejected",
			err: &examgeneration.Error{
				Code: examgeneration.ErrorPrincipalRejected, Permanent: true, Op: "read", Cause: errors.New(secret),
			},
			status: http.StatusForbidden,
			code:   "auth_forbidden",
		},
		{
			name: "database",
			err: &examgeneration.Error{
				Code: examgeneration.ErrorDatabase, Op: "read", Cause: errors.New(secret),
			},
			status: http.StatusInternalServerError,
			code:   "internal_error",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &examGenerationServiceStub{
				getCurrent: func(context.Context, string, string) (examgeneration.Generation, bool, error) {
					return examgeneration.Generation{}, test.found, test.err
				},
				readEvents: unexpectedExamGenerationEvents,
			}
			handler := newExamGenerationTestHandler(t, service, nil)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, examGenerationRequest(
				http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generation", "",
			))
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) ||
				strings.Contains(response.Body.String(), secret) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestExamGenerationEventStreamResumesDurableSequenceAndClosesAtTerminalHead(t *testing.T) {
	t.Parallel()
	eventTime := time.Date(2026, 7, 11, 2, 3, 4, 0, time.UTC)
	var calls int
	service := &examGenerationServiceStub{
		getCurrent: unexpectedCurrentExamGeneration,
		readEvents: func(_ context.Context, access, examID, generationID string, after int64, limit int) (examgeneration.EventBatch, bool, error) {
			calls++
			if access != "exam-generation-access" || examID != testExamID || generationID != "42" || after != 2 || limit != examgeneration.MaxEventPageSize {
				t.Fatalf("access=%q examID=%q generationID=%q after=%d limit=%d", access, examID, generationID, after, limit)
			}
			return examgeneration.EventBatch{
				GenerationID: "42",
				EventHead:    4,
				Events: []examgeneration.Event{
					{Sequence: 3, Type: examgeneration.EventRunning, Payload: json.RawMessage(`{"attempt":1}`), CreatedAt: eventTime},
					{Sequence: 4, Type: examgeneration.EventSucceeded, Payload: json.RawMessage(`{"students":35}`), CreatedAt: eventTime.Add(time.Second)},
				},
				Terminal: true,
			}, true, nil
		},
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	request := examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	)
	request.Header.Set("Last-Event-ID", "2")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers=%#v", response.Header())
	}
	body := response.Body.String()
	for _, fragment := range []string{
		"id: 3\nevent: running\n",
		`"sequence":3`,
		`"payload":{"attempt":1}`,
		"id: 4\nevent: succeeded\n",
		`"sequence":4`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("missing %q in %s", fragment, body)
		}
	}
}

func TestExamGenerationEventStreamDrainsAFullDurablePageBeforeTerminal(t *testing.T) {
	t.Parallel()
	events := make([]examgeneration.Event, examgeneration.MaxEventPageSize)
	createdAt := time.Date(2026, 7, 11, 2, 30, 0, 0, time.UTC)
	for index := range events {
		events[index] = examgeneration.Event{
			Sequence:  int64(index + 1),
			Type:      examgeneration.EventRunning,
			Payload:   json.RawMessage(`{}`),
			CreatedAt: createdAt.Add(time.Duration(index) * time.Millisecond),
		}
	}
	var calls int
	service := &examGenerationServiceStub{
		getCurrent: unexpectedCurrentExamGeneration,
		readEvents: func(_ context.Context, _ string, _ string, generationID string, after int64, limit int) (examgeneration.EventBatch, bool, error) {
			calls++
			if generationID != "42" || limit != examgeneration.MaxEventPageSize {
				t.Fatalf("generationID=%q limit=%d", generationID, limit)
			}
			switch after {
			case 0:
				return examgeneration.EventBatch{
					GenerationID: "42", EventHead: 201, Events: events,
				}, true, nil
			case 200:
				return examgeneration.EventBatch{
					GenerationID: "42",
					EventHead:    201,
					Events: []examgeneration.Event{{
						Sequence: 201, Type: examgeneration.EventSucceeded, Payload: json.RawMessage(`{}`),
						CreatedAt: createdAt.Add(time.Second),
					}},
					Terminal: true,
				}, true, nil
			default:
				t.Fatalf("unexpected cursor %d", after)
				return examgeneration.EventBatch{}, false, nil
			}
		},
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	))
	if response.Code != http.StatusOK || calls != 2 || !strings.Contains(response.Body.String(), "id: 201\nevent: succeeded\n") {
		t.Fatalf("status=%d calls=%d body-tail=%q", response.Code, calls, response.Body.String()[max(0, response.Body.Len()-200):])
	}
}

func TestExamGenerationEventStreamRejectsCursorAheadOfDurableHead(t *testing.T) {
	t.Parallel()
	service := &examGenerationServiceStub{
		getCurrent: unexpectedCurrentExamGeneration,
		readEvents: func(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
			return examgeneration.EventBatch{}, false, &examgeneration.Error{
				Code: examgeneration.ErrorEventCursorInvalid, Permanent: true, Op: "read", Cause: errors.New("cursor 9 exceeds head 4"),
			}
		},
	}
	handler := newExamGenerationTestHandler(t, service, nil)
	request := examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	)
	request.Header.Set("Last-Event-ID", "9")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_event_cursor"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExamGenerationEventStreamUsesGlobalCapacityAndPeriodicReauthorization(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	var calls atomic.Int64
	service := &examGenerationServiceStub{
		getCurrent: unexpectedCurrentExamGeneration,
		readEvents: func(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
			call := calls.Add(1)
			startedOnce.Do(func() { close(started) })
			if call == 1 {
				return examgeneration.EventBatch{GenerationID: "42", EventHead: 1}, true, nil
			}
			return examgeneration.EventBatch{}, false, &examgeneration.Error{
				Code: examgeneration.ErrorPrincipalRejected, Permanent: true, Op: "reauthorize", Cause: errors.New("revoked"),
			}
		},
	}
	settings := func(options *Options) {
		options.SSEMaxDuration = time.Second
		options.SSEReauthInterval = 20 * time.Millisecond
		options.SSEWriteTimeout = 10 * time.Millisecond
		options.MaxActiveSSE = 1
	}
	handler := newExamGenerationTestHandler(t, service, settings)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	).WithContext(ctx)
	firstResponse := newTestResponseRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(firstResponse, request)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first stream did not start")
	}

	secondResponse := newTestResponseRecorder()
	handler.ServeHTTP(secondResponse, examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	))
	if secondResponse.Code != http.StatusTooManyRequests || !strings.Contains(secondResponse.Body.String(), `"code":"sse_capacity_exhausted"`) {
		t.Fatalf("second status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		cancel()
		t.Fatal("periodic reauthorization did not end the stream")
	}
	if calls.Load() != 2 || firstResponse.Code != http.StatusOK {
		t.Fatalf("calls=%d first status=%d body=%s", calls.Load(), firstResponse.Code, firstResponse.Body.String())
	}
}

func TestExamGenerationEventPreflightAllowsResumeHeaderAndRateScopesAreDistinct(t *testing.T) {
	t.Parallel()
	service := &examGenerationServiceStub{
		getCurrent: func(context.Context, string, string) (examgeneration.Generation, bool, error) {
			return examgeneration.Generation{GenerationID: "42"}, true, nil
		},
		readEvents: func(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
			return examgeneration.EventBatch{
				GenerationID: "42",
				EventHead:    1,
				Events: []examgeneration.Event{{
					Sequence: 1, Type: examgeneration.EventSucceeded, Payload: json.RawMessage(`{}`),
					CreatedAt: time.Date(2026, 7, 11, 3, 4, 5, 0, time.UTC),
				}},
				Terminal: true,
			}, true, nil
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ExamGeneration = service
	capture := &captureRateLimiter{}
	options.RateLimiter = capture
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	preflight := httptest.NewRequest(
		http.MethodOptions,
		"https://api.example/api/v2/exams/"+testExamID+"/analysis-generations/42/events",
		nil,
	)
	preflight.RemoteAddr = "192.0.2.1:44000"
	preflight.Header.Set("Origin", testWebOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization,last-event-id")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Headers") != "Authorization, Last-Event-ID" {
		t.Fatalf("status=%d scope=%q headers=%#v body=%s", response.Code, capture.scope, response.Header(), response.Body.String())
	}

	current := examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generation", "",
	)
	currentResponse := newTestResponseRecorder()
	handler.ServeHTTP(currentResponse, current)
	if capture.scope != "exams.analysis-generation.get" {
		t.Fatalf("current rate scope=%q", capture.scope)
	}
	events := examGenerationRequest(
		http.MethodGet, "/api/v2/exams/"+testExamID+"/analysis-generations/42/events", "",
	)
	eventsResponse := newTestResponseRecorder()
	handler.ServeHTTP(eventsResponse, events)
	if eventsResponse.Code != http.StatusOK || capture.scope != "exams.analysis-generation.events" {
		t.Fatalf("events status=%d rate scope=%q body=%s", eventsResponse.Code, capture.scope, eventsResponse.Body.String())
	}
}

func newExamGenerationTestHandler(
	t *testing.T,
	service ExamGenerationService,
	mutate func(*Options),
) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ExamGeneration = service
	if mutate != nil {
		mutate(&options)
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func examGenerationRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer exam-generation-access")
	return request
}

func unexpectedCurrentExamGeneration(context.Context, string, string) (examgeneration.Generation, bool, error) {
	panic("unexpected current exam generation read")
}

func unexpectedExamGenerationEvents(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
	panic("unexpected exam generation event read")
}
