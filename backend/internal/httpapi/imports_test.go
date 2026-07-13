package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

const testImportJobID = "123e4567-e89b-42d3-a456-426614174000"

type stubArtifactPublisher struct {
	publish func(context.Context, io.Reader) (*artifact.Publication, error)
}

func (publisher stubArtifactPublisher) Publish(ctx context.Context, reader io.Reader) (*artifact.Publication, error) {
	return publisher.publish(ctx, reader)
}

type stubImportQueue struct {
	queue func(context.Context, *artifact.Publication, string) (importing.QueueResult, error)
}

func (queue stubImportQueue) QueuePublication(ctx context.Context, publication *artifact.Publication, mediaType string) (importing.QueueResult, error) {
	return queue.queue(ctx, publication, mediaType)
}

type stubImportReader struct {
	list   func(context.Context, *string, int) (importing.JobPage, error)
	get    func(context.Context, string) (importing.PublicJob, bool, error)
	events func(context.Context, string, int64, int) (importing.EventBatch, bool, error)
}

func (reader stubImportReader) ListJobs(ctx context.Context, cursor *string, limit int) (importing.JobPage, error) {
	return reader.list(ctx, cursor, limit)
}

func (reader stubImportReader) GetJob(ctx context.Context, publicID string) (importing.PublicJob, bool, error) {
	return reader.get(ctx, publicID)
}

func TestListImportJobsUsesStrictCursorPagination(t *testing.T) {
	t.Parallel()

	secondID := "223e4567-e89b-42d3-a456-426614174001"
	first := testPublicJob()
	second := testPublicJob()
	second.ID = secondID
	second.Status = importing.JobSucceeded
	second.Stage = importing.StageCompleted
	reader := stubImportReader{list: func(_ context.Context, cursor *string, limit int) (importing.JobPage, error) {
		if cursor == nil || *cursor != testImportJobID || limit != 2 {
			t.Fatalf("cursor=%v limit=%d", cursor, limit)
		}
		next := secondID
		return importing.JobPage{Items: []importing.PublicJob{first, second}, NextCursor: &next}, nil
	}}
	handler := newImportTestHandler(t, true, unusedArtifactPublisher{}, unusedImportQueue{}, reader)
	request := importRequest(http.MethodGet, "/api/v2/imports?limit=2&cursor="+testImportJobID, "")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page importing.JobPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != first.ID || page.Items[1].ID != second.ID || page.NextCursor == nil || *page.NextCursor != secondID {
		t.Fatalf("page=%#v", page)
	}
}

func TestListImportJobsDefaultsAndRejectsNonCanonicalQueryBeforeAuthorization(t *testing.T) {
	t.Parallel()

	authCalls := 0
	listCalls := 0
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		authCalls++
		return auth.Account{Role: auth.RoleAdmin}, nil
	}}
	reader := stubImportReader{list: func(_ context.Context, cursor *string, limit int) (importing.JobPage, error) {
		listCalls++
		if cursor != nil || limit != importing.DefaultJobPageSize {
			t.Fatalf("cursor=%v limit=%d", cursor, limit)
		}
		return importing.JobPage{Items: []importing.PublicJob{}}, nil
	}}
	handler := newImportTestHandlerWithAuth(t, true, service, unusedArtifactPublisher{}, unusedImportQueue{}, reader)

	valid := importRequest(http.MethodGet, "/api/v2/imports", "")
	validResponse := newTestResponseRecorder()
	handler.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusOK || authCalls != 1 || listCalls != 1 || validResponse.Body.String() != "{\"items\":[],\"nextCursor\":null}\n" {
		t.Fatalf("valid response=%d %s auth=%d list=%d", validResponse.Code, validResponse.Body.String(), authCalls, listCalls)
	}

	invalidPaths := []string{
		"/api/v2/imports?",
		"/api/v2/imports?limit=",
		"/api/v2/imports?limit=0",
		"/api/v2/imports?limit=01",
		"/api/v2/imports?limit=101",
		"/api/v2/imports?limit=+1",
		"/api/v2/imports?limit=%31",
		"/api/v2/imports?limit=1&limit=2",
		"/api/v2/imports?cursor=" + testImportJobID + "&cursor=" + testImportJobID,
		"/api/v2/imports?cursor=not-a-uuid",
		"/api/v2/imports?other=1",
	}
	for _, path := range invalidPaths {
		request := importRequest(http.MethodGet, path, "")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_import_page"`) {
			t.Fatalf("path=%q status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if authCalls != 1 || listCalls != 1 {
		t.Fatalf("invalid query reached auth/store: auth=%d list=%d", authCalls, listCalls)
	}
}

func TestListImportJobsMapsUnknownCursorAndUsesDedicatedRateScope(t *testing.T) {
	t.Parallel()

	reader := stubImportReader{list: func(context.Context, *string, int) (importing.JobPage, error) {
		return importing.JobPage{}, &importing.ImportError{
			Code:      importing.ErrorJobCursorInvalid,
			Permanent: true,
			Op:        "list import jobs",
			Err:       errors.New("cursor does not exist"),
		}
	}}
	limiter := &captureRateLimiter{}
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
	}}
	settings := defaultImportHandlerLifetimeSettings()
	handler, err := New(Options{
		Readiness:                 staticReadiness{report: health.Report{Status: health.StatusReady}},
		Version:                   version.Info{},
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:                      service,
		Enrollment:                unusedEnrollmentService{},
		AccountManagement:         unusedAccountManagementService{},
		AllowedOrigins:            []string{testWebOrigin},
		RateLimiter:               limiter,
		RequestIDRandom:           bytes.NewReader([]byte("abcdefgh")),
		Artifacts:                 unusedArtifactPublisher{},
		Imports:                   unusedImportQueue{},
		ImportReader:              reader,
		StudentAnalytics:          unusedStudentAnalyticsService{},
		Achievement:               unusedAchievementService{},
		ExamCatalog:               unusedExamCatalogService{},
		ExamGeneration:            unusedExamGenerationService{},
		Administration:            unusedAdministrationService{},
		Configuration:             unusedConfigurationService{},
		Feedback:                  unusedFeedbackService{},
		AgentNotes:                unusedAgentNotesService{},
		ChatAgent:                 unusedChatAgentService{},
		OJ:                        unusedOJService{},
		OJPolicy:                  oj.DefaultPolicy(),
		RecommendationReader:      unusedRecommendationReader{},
		RecommendationAdminReader: unusedRecommendationAdminReader{},
		ModelProbe:                unusedModelProbeService{},
		Capabilities:              testCapabilities(true),
		AuthBodyTimeout:           settings.authBodyTimeout,
		UploadBodyTimeout:         settings.uploadBodyTimeout,
		SSEMaxDuration:            settings.sseMaxDuration,
		SSEReauthInterval:         settings.sseReauthInterval,
		SSEWriteTimeout:           settings.sseWriteTimeout,
		MaxActiveSSE:              settings.maxActiveSSE,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := importRequest(http.MethodGet, "/api/v2/imports?cursor="+testImportJobID, "")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_import_cursor"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if limiter.scope != "imports.list" || limiter.client != "192.0.2.1" {
		t.Fatalf("scope=%q client=%q", limiter.scope, limiter.client)
	}
}

func (reader stubImportReader) ReadEvents(ctx context.Context, publicID string, after int64, limit int) (importing.EventBatch, bool, error) {
	return reader.events(ctx, publicID, after, limit)
}

func TestCapabilitiesExposeExactActiveContract(t *testing.T) {
	handler := newImportTestHandler(t, true, stubArtifactPublisher{}, stubImportQueue{}, stubImportReader{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var got Capabilities
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != testCapabilities(true) {
		t.Fatalf("capabilities = %#v", got)
	}
}

func TestCreatePintiaImportPublishesAndReturnsDurableJob(t *testing.T) {
	const snapshot = `{"schema":"ascendany.pintia.snapshot.v2"}`
	publication := &artifact.Publication{Artifact: artifact.Artifact{
		Hash:       strings.Repeat("a", 64),
		Size:       int64(len(snapshot)),
		StorageKey: "sha256/aa/" + strings.Repeat("a", 64),
		Path:       "/var/lib/ascendany/artifacts/sha256/aa/" + strings.Repeat("a", 64),
	}}
	publisher := stubArtifactPublisher{publish: func(_ context.Context, reader io.Reader) (*artifact.Publication, error) {
		body, err := io.ReadAll(reader)
		if err != nil || string(body) != snapshot {
			t.Fatalf("body=%q err=%v", body, err)
		}
		return publication, nil
	}}
	queue := stubImportQueue{queue: func(_ context.Context, got *artifact.Publication, mediaType string) (importing.QueueResult, error) {
		if got != publication || mediaType != importing.PintiaSnapshotV2MediaType {
			t.Fatalf("publication=%p mediaType=%q", got, mediaType)
		}
		return importing.QueueResult{Job: importing.Job{PublicID: testImportJobID}, Created: true}, nil
	}}
	job := testPublicJob()
	reader := stubImportReader{get: func(_ context.Context, publicID string) (importing.PublicJob, bool, error) {
		if publicID != testImportJobID {
			t.Fatalf("public ID=%q", publicID)
		}
		return job, true, nil
	}}
	handler := newImportTestHandler(t, true, publisher, queue, reader)
	request := importRequest(http.MethodPost, "/api/v2/imports/pintia", snapshot)
	request.Header.Set("Content-Type", importing.PintiaSnapshotV2MediaType)
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got importing.PublicJob
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != job.ID || got.ArtifactSHA256 != job.ArtifactSHA256 || got.Status != job.Status {
		t.Fatalf("job=%#v", got)
	}
}

func TestCreatePintiaImportRejectsBoundaryFailuresBeforeStorage(t *testing.T) {
	calls := 0
	publisher := stubArtifactPublisher{publish: func(context.Context, io.Reader) (*artifact.Publication, error) {
		calls++
		return nil, errors.New("must not run")
	}}
	handler := newImportTestHandler(t, true, publisher, stubImportQueue{}, stubImportReader{})
	tests := []struct {
		name        string
		contentType string
		encoding    string
		role        auth.Role
		want        int
	}{
		{name: "missing media type", role: auth.RoleAdmin, want: http.StatusUnsupportedMediaType},
		{name: "media type parameter", contentType: importing.PintiaSnapshotV2MediaType + "; charset=utf-8", role: auth.RoleAdmin, want: http.StatusUnsupportedMediaType},
		{name: "content encoding", contentType: importing.PintiaSnapshotV2MediaType, encoding: "gzip", role: auth.RoleAdmin, want: http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := importRequest(http.MethodPost, "/api/v2/imports/pintia", `{}`)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.encoding != "" {
				request.Header.Set("Content-Encoding", test.encoding)
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("storage called %d times", calls)
	}
}

func TestImportEndpointsRequireAdmin(t *testing.T) {
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{Role: auth.RoleStudent}, nil
	}}
	handler := newImportTestHandlerWithAuth(t, true, service, unusedArtifactPublisher{}, unusedImportQueue{}, unusedImportReader{})
	request := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID, "")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWriteModeDisablesAllHTTPMutations(t *testing.T) {
	service := stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		t.Fatal("disabled login reached service")
		return auth.AuthResult{}, nil
	}, me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{Role: auth.RoleAdmin}, nil
	}}
	handler := newImportTestHandlerWithAuth(t, false, service, nil, nil, unusedImportReader{})

	login := authRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"name","password":"long-enough-password"}`)
	login.Header.Set("Content-Type", "application/json")
	loginResponse := newTestResponseRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}

	create := importRequest(http.MethodPost, "/api/v2/imports/pintia", `{}`)
	create.Header.Set("Content-Type", importing.PintiaSnapshotV2MediaType)
	createResponse := newTestResponseRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
}

func TestGetImportJobValidatesIDAndMapsNotFound(t *testing.T) {
	reader := stubImportReader{get: func(context.Context, string) (importing.PublicJob, bool, error) {
		return importing.PublicJob{}, false, nil
	}}
	handler := newImportTestHandler(t, true, unusedArtifactPublisher{}, unusedImportQueue{}, reader)

	invalid := importRequest(http.MethodGet, "/api/v2/imports/not-a-uuid", "")
	invalidResponse := newTestResponseRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}

	missing := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID, "")
	missingResponse := newTestResponseRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
}

func TestImportEventStreamResumesInOrderAndEndsWhenDrained(t *testing.T) {
	called := false
	reader := stubImportReader{events: func(_ context.Context, publicID string, after int64, limit int) (importing.EventBatch, bool, error) {
		called = true
		if publicID != testImportJobID || after != 2 || limit != importing.MaxEventBatchSize {
			t.Fatalf("read args id=%q after=%d limit=%d", publicID, after, limit)
		}
		return importing.EventBatch{Terminal: true, Events: []importing.PublicEvent{
			{Sequence: 3, Type: "validating", OccurredAt: time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC), Payload: json.RawMessage(`{"stage":"validating"}`)},
			{Sequence: 4, Type: "completed", OccurredAt: time.Date(2026, 7, 10, 1, 2, 4, 0, time.UTC), Payload: json.RawMessage(`{"stage":"completed"}`)},
		}}, true, nil
	}}
	handler := newImportTestHandler(t, true, unusedArtifactPublisher{}, unusedImportQueue{}, reader)
	request := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "")
	request.Header.Set("Last-Event-ID", "2")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusOK {
		t.Fatalf("called=%t status=%d body=%s", called, response.Code, response.Body.String())
	}
	wantParts := []string{"id: 3\n", "event: validating\n", `"sequence":3`, "id: 4\n", "event: completed\n", `"sequence":4`}
	for _, part := range wantParts {
		if !strings.Contains(response.Body.String(), part) {
			t.Fatalf("stream lacks %q: %s", part, response.Body.String())
		}
	}
	if response.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers=%#v", response.Header())
	}
}

func TestImportEventStreamEnforcesGlobalCapacity(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var startedOnce sync.Once
	reader := stubImportReader{events: func(context.Context, string, int64, int) (importing.EventBatch, bool, error) {
		startedOnce.Do(func() { close(started) })
		return importing.EventBatch{}, true, nil
	}}
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
	}}
	settings := defaultImportHandlerLifetimeSettings()
	settings.sseMaxDuration = time.Second
	settings.sseReauthInterval = 250 * time.Millisecond
	settings.sseWriteTimeout = 100 * time.Millisecond
	settings.maxActiveSSE = 1
	handler := newImportTestHandlerWithSettings(t, true, service, unusedArtifactPublisher{}, unusedImportQueue{}, reader, settings)

	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstRequest := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "").WithContext(firstContext)
	firstResponse := newTestResponseRecorder()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(firstResponse, firstRequest)
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancelFirst()
		t.Fatal("first event stream did not start")
	}

	secondRequest := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "")
	secondResponse := newTestResponseRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusTooManyRequests || secondResponse.Header().Get("Retry-After") != "1" {
		cancelFirst()
		t.Fatalf("status=%d headers=%#v body=%s", secondResponse.Code, secondResponse.Header(), secondResponse.Body.String())
	}

	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first event stream did not stop after cancellation")
	}
}

func TestImportEventStreamPeriodicallyReauthorizes(t *testing.T) {
	t.Parallel()

	var meCalls atomic.Int32
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		if meCalls.Add(1) <= 2 {
			return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
		}
		return auth.Account{}, &auth.Error{Code: auth.ErrorAuthentication, Message: "Authentication was rejected."}
	}}
	reader := stubImportReader{events: func(context.Context, string, int64, int) (importing.EventBatch, bool, error) {
		return importing.EventBatch{}, true, nil
	}}
	settings := defaultImportHandlerLifetimeSettings()
	settings.sseMaxDuration = 500 * time.Millisecond
	settings.sseReauthInterval = 20 * time.Millisecond
	settings.sseWriteTimeout = 10 * time.Millisecond
	handler := newImportTestHandlerWithSettings(t, true, service, unusedArtifactPublisher{}, unusedImportQueue{}, reader, settings)

	requestContext, cancelRequest := context.WithTimeout(context.Background(), time.Second)
	defer cancelRequest()
	request := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "").WithContext(requestContext)
	response := newTestResponseRecorder()
	started := time.Now()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if meCalls.Load() < 3 {
		t.Fatalf("authorization calls=%d", meCalls.Load())
	}
	if time.Since(started) >= settings.sseMaxDuration {
		t.Fatal("event stream ended by maximum duration instead of failed reauthorization")
	}
}

func TestImportEventStreamMaximumDurationReleasesCapacity(t *testing.T) {
	t.Parallel()

	var terminal atomic.Bool
	reader := stubImportReader{events: func(context.Context, string, int64, int) (importing.EventBatch, bool, error) {
		return importing.EventBatch{Terminal: terminal.Load()}, true, nil
	}}
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
	}}
	settings := defaultImportHandlerLifetimeSettings()
	settings.sseMaxDuration = 80 * time.Millisecond
	settings.sseReauthInterval = 20 * time.Millisecond
	settings.sseWriteTimeout = 10 * time.Millisecond
	settings.maxActiveSSE = 1
	handler := newImportTestHandlerWithSettings(t, true, service, unusedArtifactPublisher{}, unusedImportQueue{}, reader, settings)

	firstRequest := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "")
	firstResponse := newTestResponseRecorder()
	started := time.Now()
	handler.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusOK || time.Since(started) > time.Second {
		t.Fatalf("first stream status=%d duration=%s body=%s", firstResponse.Code, time.Since(started), firstResponse.Body.String())
	}

	terminal.Store(true)
	secondRequest := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "")
	secondResponse := newTestResponseRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("second stream status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
}

func TestDynamicImportPreflightUsesExactPolicy(t *testing.T) {
	handler := newImportTestHandler(t, true, unusedArtifactPublisher{}, unusedImportQueue{}, unusedImportReader{})
	request := httptest.NewRequest(http.MethodOptions, "/api/v2/imports/"+testImportJobID+"/events", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Origin", testWebOrigin)
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "authorization, last-event-id")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Headers") != "Authorization, Last-Event-ID" {
		t.Fatalf("status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestLastEventIDRequiresCanonicalDecimal(t *testing.T) {
	for _, value := range []string{"", "00", "01", "+1", "-1", " 1", "1 ", "9223372036854775808"} {
		header := make(http.Header)
		header.Set("Last-Event-ID", value)
		if _, err := parseLastEventID(header); err == nil {
			t.Fatalf("value %q accepted", value)
		}
	}
	for value, want := range map[string]int64{"": 0, "0": 0, "1": 1, "9223372036854775807": 1<<63 - 1} {
		header := make(http.Header)
		if value != "" {
			header.Set("Last-Event-ID", value)
		}
		got, err := parseLastEventID(header)
		if err != nil || got != want {
			t.Fatalf("value=%q got=%d err=%v", value, got, err)
		}
	}
}

func TestImportEventStreamRejectsCursorAheadOfDurableHead(t *testing.T) {
	t.Parallel()

	reader := stubImportReader{events: func(context.Context, string, int64, int) (importing.EventBatch, bool, error) {
		return importing.EventBatch{}, false, &importing.ImportError{
			Code:      importing.ErrorEventCursorAhead,
			Permanent: true,
			Op:        "read import events",
			Err:       errors.New("cursor exceeds durable head"),
		}
	}}
	handler := newImportTestHandler(t, true, unusedArtifactPublisher{}, unusedImportQueue{}, reader)
	request := importRequest(http.MethodGet, "/api/v2/imports/"+testImportJobID+"/events", "")
	request.Header.Set("Last-Event-ID", "2")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_event_cursor"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newImportTestHandler(
	t *testing.T,
	writesEnabled bool,
	publisher ArtifactPublisher,
	queue ImportQueue,
	reader ImportReader,
) http.Handler {
	t.Helper()
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
	}}
	return newImportTestHandlerWithAuth(t, writesEnabled, service, publisher, queue, reader)
}

func newImportTestHandlerWithAuth(
	t *testing.T,
	writesEnabled bool,
	service AuthService,
	publisher ArtifactPublisher,
	queue ImportQueue,
	reader ImportReader,
) http.Handler {
	t.Helper()
	return newImportTestHandlerWithSettings(
		t,
		writesEnabled,
		service,
		publisher,
		queue,
		reader,
		defaultImportHandlerLifetimeSettings(),
	)
}

type importHandlerLifetimeSettings struct {
	authBodyTimeout   time.Duration
	uploadBodyTimeout time.Duration
	sseMaxDuration    time.Duration
	sseReauthInterval time.Duration
	sseWriteTimeout   time.Duration
	maxActiveSSE      int
}

func defaultImportHandlerLifetimeSettings() importHandlerLifetimeSettings {
	return importHandlerLifetimeSettings{
		authBodyTimeout:   time.Second,
		uploadBodyTimeout: time.Second,
		sseMaxDuration:    time.Minute,
		sseReauthInterval: 10 * time.Second,
		sseWriteTimeout:   time.Second,
		maxActiveSSE:      4,
	}
}

func newImportTestHandlerWithSettings(
	t *testing.T,
	writesEnabled bool,
	service AuthService,
	publisher ArtifactPublisher,
	queue ImportQueue,
	reader ImportReader,
	settings importHandlerLifetimeSettings,
) http.Handler {
	t.Helper()
	handler, err := New(Options{
		Readiness:                 staticReadiness{report: health.Report{Status: health.StatusReady}},
		Version:                   version.Info{},
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:                      service,
		Enrollment:                unusedEnrollmentService{},
		AccountManagement:         unusedAccountManagementService{},
		AllowedOrigins:            []string{testWebOrigin},
		RateLimiter:               allowAllRateLimiter{},
		RequestIDRandom:           bytes.NewReader([]byte("abcdefgh")),
		Artifacts:                 publisher,
		Imports:                   queue,
		ImportReader:              reader,
		StudentAnalytics:          unusedStudentAnalyticsService{},
		Achievement:               unusedAchievementService{},
		ExamCatalog:               unusedExamCatalogService{},
		ExamGeneration:            unusedExamGenerationService{},
		Administration:            unusedAdministrationService{},
		Configuration:             unusedConfigurationService{},
		Feedback:                  unusedFeedbackService{},
		AgentNotes:                unusedAgentNotesService{},
		ChatAgent:                 unusedChatAgentService{},
		OJ:                        unusedOJService{},
		OJPolicy:                  oj.DefaultPolicy(),
		RecommendationReader:      unusedRecommendationReader{},
		RecommendationAdminReader: unusedRecommendationAdminReader{},
		ModelProbe:                testModelProbe(writesEnabled),
		Capabilities:              testCapabilities(writesEnabled),
		AuthBodyTimeout:           settings.authBodyTimeout,
		UploadBodyTimeout:         settings.uploadBodyTimeout,
		SSEMaxDuration:            settings.sseMaxDuration,
		SSEReauthInterval:         settings.sseReauthInterval,
		SSEWriteTimeout:           settings.sseWriteTimeout,
		MaxActiveSSE:              settings.maxActiveSSE,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func importRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Origin", testWebOrigin)
	request.Header.Set("Authorization", "Bearer admin-token")
	return request
}

func testPublicJob() importing.PublicJob {
	return importing.PublicJob{
		ID:             testImportJobID,
		ArtifactSHA256: strings.Repeat("a", 64),
		Status:         importing.JobQueued,
		Stage:          importing.StageReceived,
		CreatedAt:      time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC),
	}
}
