package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

const (
	testAgentNoteID     = "123e4567-e89b-42d3-a456-426614174030"
	testAgentMutationID = "123e4567-e89b-42d3-a456-426614174031"
)

type agentNotesServiceStub struct {
	list    func(context.Context, string, *string, int) (agentnotes.Page, error)
	get     func(context.Context, string, string) (agentnotes.Note, bool, error)
	create  func(context.Context, string, agentnotes.CreateInput) (agentnotes.MutationResult, error)
	replace func(context.Context, string, string, agentnotes.ReplaceInput) (agentnotes.MutationResult, error)
	archive func(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error)
	restore func(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error)
}

func (stub agentNotesServiceStub) List(ctx context.Context, access string, cursor *string, limit int) (agentnotes.Page, error) {
	return stub.list(ctx, access, cursor, limit)
}

func (stub agentNotesServiceStub) Get(ctx context.Context, access, noteID string) (agentnotes.Note, bool, error) {
	return stub.get(ctx, access, noteID)
}

func (stub agentNotesServiceStub) Create(ctx context.Context, access string, input agentnotes.CreateInput) (agentnotes.MutationResult, error) {
	return stub.create(ctx, access, input)
}

func (stub agentNotesServiceStub) Replace(ctx context.Context, access, noteID string, input agentnotes.ReplaceInput) (agentnotes.MutationResult, error) {
	return stub.replace(ctx, access, noteID, input)
}

func (stub agentNotesServiceStub) Archive(ctx context.Context, access, noteID string, input agentnotes.StateInput) (agentnotes.MutationResult, error) {
	return stub.archive(ctx, access, noteID, input)
}

func (stub agentNotesServiceStub) Restore(ctx context.Context, access, noteID string, input agentnotes.StateInput) (agentnotes.MutationResult, error) {
	return stub.restore(ctx, access, noteID, input)
}

func TestAgentNotesReadRoutesUseOwnedCursorContracts(t *testing.T) {
	t.Parallel()
	note := testAgentNote()
	cursor := base64.RawURLEncoding.EncodeToString([]byte("agent-note.v1\x00" + note.UpdatedAt.Format(time.RFC3339Nano) + "\x00" + note.ID))
	listCalls := 0
	getCalls := 0
	service := agentNotesServiceStub{
		list: func(_ context.Context, access string, receivedCursor *string, limit int) (agentnotes.Page, error) {
			listCalls++
			if access != "student-token" || receivedCursor == nil || *receivedCursor != cursor || limit != 7 {
				t.Fatalf("list access=%q cursor=%v limit=%d", access, receivedCursor, limit)
			}
			return agentnotes.Page{Items: []agentnotes.Summary{note.Summary}}, nil
		},
		get: func(_ context.Context, access, noteID string) (agentnotes.Note, bool, error) {
			getCalls++
			if access != "student-token" || noteID != testAgentNoteID {
				t.Fatalf("get access=%q noteID=%q", access, noteID)
			}
			return note, true, nil
		},
	}
	handler := newAgentNotesTestHandler(t, service, true)

	listRequest := agentNotesRequest(http.MethodGet, "/api/v2/students/me/notes?cursor="+cursor+"&limit=7", "")
	listResponse := newTestResponseRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"id":"`+testAgentNoteID+`"`) {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	getRequest := agentNotesRequest(http.MethodGet, "/api/v2/students/me/notes/"+testAgentNoteID, "")
	getResponse := newTestResponseRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"content":"Study trees"`) {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}

	for _, path := range []string{
		"/api/v2/students/me/notes?",
		"/api/v2/students/me/notes?cursor=invalid",
		"/api/v2/students/me/notes?limit=01",
		"/api/v2/students/me/notes?limit=2&limit=3",
		"/api/v2/students/me/notes?unknown=1",
		"/api/v2/students/me/notes/invalid",
		"/api/v2/students/me/notes/" + testAgentNoteID + "?x=1",
	} {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, agentNotesRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if listCalls != 1 || getCalls != 1 {
		t.Fatalf("invalid reads reached service list=%d get=%d", listCalls, getCalls)
	}
}

func TestAgentNoteMutationsUseStrictRESTContracts(t *testing.T) {
	t.Parallel()
	note := testAgentNote()
	createCalls := 0
	replaceCalls := 0
	archiveCalls := 0
	restoreCalls := 0
	service := agentNotesServiceStub{
		create: func(_ context.Context, access string, input agentnotes.CreateInput) (agentnotes.MutationResult, error) {
			createCalls++
			if access != "student-token" || input.MutationID != testAgentMutationID || input.ExpectedHeadRevision != 0 || input.Title != "Plan" || input.Content != "Study trees" {
				t.Fatalf("create access=%q input=%#v", access, input)
			}
			return agentnotes.MutationResult{Note: note, Idempotent: createCalls == 2}, nil
		},
		replace: func(_ context.Context, access, noteID string, input agentnotes.ReplaceInput) (agentnotes.MutationResult, error) {
			replaceCalls++
			if access != "student-token" || noteID != testAgentNoteID || input.MutationID != testAgentMutationID || input.ExpectedHeadRevision != 1 || input.Title != "Plan v2" || input.Content != "Study graphs" {
				t.Fatalf("replace access=%q note=%q input=%#v", access, noteID, input)
			}
			return agentnotes.MutationResult{Note: note}, nil
		},
		archive: func(_ context.Context, access, noteID string, input agentnotes.StateInput) (agentnotes.MutationResult, error) {
			archiveCalls++
			assertAgentNoteStateCall(t, access, noteID, input, 2)
			return agentnotes.MutationResult{Note: note}, nil
		},
		restore: func(_ context.Context, access, noteID string, input agentnotes.StateInput) (agentnotes.MutationResult, error) {
			restoreCalls++
			assertAgentNoteStateCall(t, access, noteID, input, 3)
			return agentnotes.MutationResult{Note: note}, nil
		},
	}
	handler := newAgentNotesTestHandler(t, service, true)
	createBody := `{"mutationId":"` + testAgentMutationID + `","expectedHeadRevision":0,"title":"Plan","content":"Study trees"}`
	for index, expectedStatus := range []int{http.StatusCreated, http.StatusOK} {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, agentNotesRequest(http.MethodPost, "/api/v2/students/me/notes", createBody))
		if response.Code != expectedStatus || response.Header().Get("Location") != "/api/v2/students/me/notes/"+testAgentNoteID {
			t.Fatalf("create %d status=%d location=%q body=%s", index, response.Code, response.Header().Get("Location"), response.Body.String())
		}
	}

	mutations := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/v2/students/me/notes/" + testAgentNoteID + "/document", `{"mutationId":"` + testAgentMutationID + `","expectedHeadRevision":1,"title":"Plan v2","content":"Study graphs"}`},
		{http.MethodPost, "/api/v2/students/me/notes/" + testAgentNoteID + "/archive", `{"mutationId":"` + testAgentMutationID + `","expectedHeadRevision":2}`},
		{http.MethodPost, "/api/v2/students/me/notes/" + testAgentNoteID + "/restore", `{"mutationId":"` + testAgentMutationID + `","expectedHeadRevision":3}`},
	}
	for _, mutation := range mutations {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, agentNotesRequest(mutation.method, mutation.path, mutation.body))
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", mutation.path, response.Code, response.Body.String())
		}
	}
	if createCalls != 2 || replaceCalls != 1 || archiveCalls != 1 || restoreCalls != 1 {
		t.Fatalf("calls create=%d replace=%d archive=%d restore=%d", createCalls, replaceCalls, archiveCalls, restoreCalls)
	}
}

func TestAgentNoteMutationBoundaryRejectsBeforeService(t *testing.T) {
	t.Parallel()
	calls := 0
	service := agentNotesServiceStub{
		create: func(context.Context, string, agentnotes.CreateInput) (agentnotes.MutationResult, error) {
			calls++
			return agentnotes.MutationResult{}, nil
		},
		replace: func(context.Context, string, string, agentnotes.ReplaceInput) (agentnotes.MutationResult, error) {
			calls++
			return agentnotes.MutationResult{}, nil
		},
		archive: func(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
			calls++
			return agentnotes.MutationResult{}, nil
		},
		restore: func(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
			calls++
			return agentnotes.MutationResult{}, nil
		},
	}
	handler := newAgentNotesTestHandler(t, service, true)
	valid := `{"mutationId":"` + testAgentMutationID + `","expectedHeadRevision":0,"title":"Plan","content":"body"}`
	for _, test := range []struct {
		method      string
		path        string
		body        string
		authorize   bool
		contentType string
		wantStatus  int
	}{
		{http.MethodPost, "/api/v2/students/me/notes?x=1", valid, true, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/api/v2/students/me/notes", valid, false, "application/json", http.StatusUnauthorized},
		{http.MethodPost, "/api/v2/students/me/notes", `{}`, true, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/api/v2/students/me/notes", strings.Replace(valid, `}`, `,"extra":1}`, 1), true, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/api/v2/students/me/notes", strings.Replace(valid, `"title":"Plan"`, `"title":"Plan","title":"Again"`, 1), true, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/api/v2/students/me/notes", valid, true, "text/plain", http.StatusUnsupportedMediaType},
		{http.MethodPut, "/api/v2/students/me/notes/invalid/document", valid, true, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/api/v2/students/me/notes/" + testAgentNoteID + "/archive", `{"mutationId":"` + testAgentMutationID + `"}`, true, "application/json", http.StatusBadRequest},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.RemoteAddr = "192.0.2.1:44000"
		if test.authorize {
			request.Header.Set("Authorization", "Bearer student-token")
		}
		request.Header.Set("Content-Type", test.contentType)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Fatalf("path=%s status=%d want=%d body=%s", test.path, response.Code, test.wantStatus, response.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid mutations reached service: %d", calls)
	}

	disabled := newAgentNotesTestHandler(t, service, false)
	response := newTestResponseRecorder()
	disabled.ServeHTTP(response, agentNotesRequest(http.MethodPost, "/api/v2/students/me/notes", valid))
	if response.Code != http.StatusServiceUnavailable || calls != 0 {
		t.Fatalf("disabled status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestAgentNotesErrorsUseStableOpaqueMapping(t *testing.T) {
	t.Parallel()
	secret := "database-secret"
	for _, test := range []struct {
		code       agentnotes.ErrorCode
		wantStatus int
	}{
		{agentnotes.ErrorInvalidQuery, http.StatusBadRequest},
		{agentnotes.ErrorPrincipalRejected, http.StatusForbidden},
		{agentnotes.ErrorNotFound, http.StatusNotFound},
		{agentnotes.ErrorCursorInvalid, http.StatusBadRequest},
		{agentnotes.ErrorHeadConflict, http.StatusConflict},
		{agentnotes.ErrorStateConflict, http.StatusConflict},
		{agentnotes.ErrorIdempotencyConflict, http.StatusConflict},
		{agentnotes.ErrorDatabase, http.StatusInternalServerError},
	} {
		service := agentNotesServiceStub{get: func(context.Context, string, string) (agentnotes.Note, bool, error) {
			return agentnotes.Note{}, false, &agentnotes.Error{Code: test.code, Op: "read", Cause: errors.New(secret)}
		}}
		handler := newAgentNotesTestHandler(t, service, true)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, agentNotesRequest(http.MethodGet, "/api/v2/students/me/notes/"+testAgentNoteID, ""))
		if response.Code != test.wantStatus || strings.Contains(response.Body.String(), secret) {
			t.Fatalf("code=%s status=%d body=%s", test.code, response.Code, response.Body.String())
		}
	}
}

func TestAgentNotesCORSPreflightIsMethodAware(t *testing.T) {
	t.Parallel()
	handler := newAgentNotesTestHandler(t, unusedAgentNotesService{}, true)
	for _, test := range []struct {
		method  string
		path    string
		headers string
	}{
		{http.MethodGet, "/api/v2/students/me/notes", "Authorization"},
		{http.MethodPost, "/api/v2/students/me/notes", "Authorization, Content-Type"},
		{http.MethodGet, "/api/v2/students/me/notes/" + testAgentNoteID, "Authorization"},
		{http.MethodPut, "/api/v2/students/me/notes/" + testAgentNoteID + "/document", "Authorization, Content-Type"},
		{http.MethodPost, "/api/v2/students/me/notes/" + testAgentNoteID + "/archive", "Authorization, Content-Type"},
		{http.MethodPost, "/api/v2/students/me/notes/" + testAgentNoteID + "/restore", "Authorization, Content-Type"},
	} {
		request := httptest.NewRequest(http.MethodOptions, test.path, nil)
		request.RemoteAddr = "192.0.2.1:44000"
		request.Header.Set("Origin", "https://ascendany.example")
		request.Header.Set("Access-Control-Request-Method", test.method)
		request.Header.Set("Access-Control-Request-Headers", test.headers)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != test.method {
			t.Fatalf("path=%s method=%s status=%d allow=%q body=%s", test.path, test.method, response.Code, response.Header().Get("Access-Control-Allow-Methods"), response.Body.String())
		}
	}
}

func assertAgentNoteStateCall(t *testing.T, access, noteID string, input agentnotes.StateInput, expectedHead int64) {
	t.Helper()
	if access != "student-token" || noteID != testAgentNoteID || input.MutationID != testAgentMutationID || input.ExpectedHeadRevision != expectedHead {
		t.Fatalf("state access=%q note=%q input=%#v", access, noteID, input)
	}
}

func newAgentNotesTestHandler(t *testing.T, service AgentNotesService, writes bool) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.AgentNotes = service
	options.Capabilities = testCapabilities(writes)
	if !writes {
		options.Artifacts = nil
		options.Imports = nil
		options.ModelProbe = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func agentNotesRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer student-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func testAgentNote() agentnotes.Note {
	now := time.Date(2026, 7, 11, 10, 30, 0, 0, time.UTC)
	return agentnotes.Note{
		Summary: agentnotes.Summary{
			ID: testAgentNoteID, HeadRevision: 1, State: agentnotes.StateActive,
			Title: "Plan", ContentSHA256: strings.Repeat("a", 64),
			CurrentMutationID: testAgentMutationID, CurrentOperation: agentnotes.OperationCreate,
			CurrentRevisionCreatedAt: now, CreatedAt: now, UpdatedAt: now,
		},
		Content: "Study trees",
	}
}
