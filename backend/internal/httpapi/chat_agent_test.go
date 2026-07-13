package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

const (
	testChatThreadID  = "123e4567-e89b-42d3-a456-426614174050"
	testAgentRunID    = "123e4567-e89b-42d3-a456-426614174051"
	testChatMessageID = "123e4567-e89b-42d3-a456-426614174052"
	testChatRequestID = "123e4567-e89b-42d3-a456-426614174053"
)

type chatAgentServiceStub struct {
	createThread  func(context.Context, string) (chatagent.Thread, error)
	listThreads   func(context.Context, string, *string, int) (chatagent.ThreadPage, error)
	listMessages  func(context.Context, string, string, int64, int) ([]chatagent.Message, error)
	getRun        func(context.Context, string, string) (chatagent.Run, bool, error)
	readRunEvents func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error)
	enqueue       func(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error)
	autoAnalysis  func(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error)
}

func (stub chatAgentServiceStub) CreateThread(ctx context.Context, token string) (chatagent.Thread, error) {
	return stub.createThread(ctx, token)
}

func (stub chatAgentServiceStub) ListThreads(ctx context.Context, token string, cursor *string, limit int) (chatagent.ThreadPage, error) {
	return stub.listThreads(ctx, token, cursor, limit)
}

func (stub chatAgentServiceStub) ListMessages(ctx context.Context, token, threadID string, after int64, limit int) ([]chatagent.Message, error) {
	return stub.listMessages(ctx, token, threadID, after, limit)
}

func (stub chatAgentServiceStub) GetRun(ctx context.Context, token, runID string) (chatagent.Run, bool, error) {
	return stub.getRun(ctx, token, runID)
}

func (stub chatAgentServiceStub) ReadRunEvents(ctx context.Context, token, runID string, after int64, limit int) (chatagent.RunEventBatch, error) {
	return stub.readRunEvents(ctx, token, runID, after, limit)
}

func (stub chatAgentServiceStub) Enqueue(ctx context.Context, token, threadID string, input chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
	return stub.enqueue(ctx, token, threadID, input)
}

func (stub chatAgentServiceStub) EnqueueAutoAnalysis(ctx context.Context, token string, input chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
	return stub.autoAnalysis(ctx, token, input)
}

func TestChatAgentHTTPReadAndMutationContracts(t *testing.T) {
	now := testChatTime()
	thread := chatagent.Thread{ID: testChatThreadID, Kind: chatagent.ThreadConversation, HeadRevision: 1, CreatedAt: now, UpdatedAt: now}
	message := chatagent.Message{
		ID: testChatMessageID, ThreadID: testChatThreadID, Sequence: 3,
		Kind: chatagent.MessageUser, Content: "Explain this.", CreatedAt: now,
	}
	run := chatagent.Run{
		ID: testAgentRunID, ThreadID: testChatThreadID, ClientRequestID: testChatRequestID,
		Kind: chatagent.RunReply, InputMessageID: testChatMessageID, Status: chatagent.RunQueued,
		CreatedAt: now, UpdatedAt: now,
	}
	service := chatAgentServiceStub{
		listThreads: func(_ context.Context, token string, cursor *string, limit int) (chatagent.ThreadPage, error) {
			if token != "chat-token" || cursor != nil || limit != 7 {
				t.Fatalf("thread list token=%q cursor=%v limit=%d", token, cursor, limit)
			}
			return chatagent.ThreadPage{Items: []chatagent.Thread{thread}}, nil
		},
		createThread: func(_ context.Context, token string) (chatagent.Thread, error) {
			if token != "chat-token" {
				t.Fatalf("create token=%q", token)
			}
			return thread, nil
		},
		listMessages: func(_ context.Context, token, threadID string, after int64, limit int) ([]chatagent.Message, error) {
			if token != "chat-token" || threadID != testChatThreadID || after != 2 || limit != 3 {
				t.Fatalf("message query token=%q thread=%q after=%d limit=%d", token, threadID, after, limit)
			}
			return []chatagent.Message{message}, nil
		},
		enqueue: func(_ context.Context, token, threadID string, input chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
			if token != "chat-token" || threadID != testChatThreadID || input.ClientRequestID != testChatRequestID ||
				input.Kind != chatagent.RunReply || input.Content != "Explain this." ||
				input.PromptConfigurationKey != "agent.prompt.default" || input.ModelConfigurationKey != "agent.model.default" ||
				input.ExpectedAnalyticsHeadRevision != nil {
				t.Fatalf("enqueue token=%q thread=%q input=%#v", token, threadID, input)
			}
			return chatagent.EnqueueResult{Run: run, Message: message, Created: true}, nil
		},
		autoAnalysis: func(_ context.Context, token string, input chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
			if token != "chat-token" || input.PromptConfigurationKey != "agent.prompt.default" ||
				input.ModelConfigurationKey != "agent.model.default" || input.ExpectedAnalyticsHeadRevision != 7 {
				t.Fatalf("auto-analysis token=%q input=%#v", token, input)
			}
			autoRun := run
			autoRun.Kind = chatagent.RunAutoAnalysis
			autoMessage := message
			autoMessage.Kind = chatagent.MessageAutoAnalysisRequest
			autoMessage.Content = chatagent.AutoAnalysisInputContent
			return chatagent.EnqueueResult{Run: autoRun, Message: autoMessage, Created: true}, nil
		},
		getRun: func(_ context.Context, token, runID string) (chatagent.Run, bool, error) {
			if token != "chat-token" || runID != testAgentRunID {
				t.Fatalf("run read token=%q run=%q", token, runID)
			}
			return run, true, nil
		},
	}
	handler := newChatAgentTestHandler(t, service, true)

	listResponse := newTestResponseRecorder()
	handler.ServeHTTP(listResponse, chatAgentRequest(http.MethodGet, "/api/v2/students/me/chat/threads?limit=7", ""))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), testChatThreadID) ||
		!strings.Contains(listResponse.Body.String(), `"kind":"conversation"`) {
		t.Fatalf("thread list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	createResponse := newTestResponseRecorder()
	handler.ServeHTTP(createResponse, chatAgentRequest(http.MethodPost, "/api/v2/students/me/chat/threads", ""))
	if createResponse.Code != http.StatusCreated || createResponse.Header().Get("Location") != "/api/v2/students/me/chat/threads/"+testChatThreadID+"/messages" {
		t.Fatalf("thread create status=%d headers=%#v body=%s", createResponse.Code, createResponse.Header(), createResponse.Body.String())
	}
	messageResponse := newTestResponseRecorder()
	handler.ServeHTTP(messageResponse, chatAgentRequest(http.MethodGet,
		"/api/v2/students/me/chat/threads/"+testChatThreadID+"/messages?afterSequence=2&limit=3", ""))
	if messageResponse.Code != http.StatusOK || !strings.Contains(messageResponse.Body.String(), `"lastSequence":3`) {
		t.Fatalf("message list status=%d body=%s", messageResponse.Code, messageResponse.Body.String())
	}
	enqueueBody := `{"clientRequestId":"` + testChatRequestID + `","kind":"reply","content":"Explain this.","promptConfigurationKey":"agent.prompt.default","modelConfigurationKey":"agent.model.default","expectedAnalyticsHeadRevision":null}`
	enqueueResponse := newTestResponseRecorder()
	handler.ServeHTTP(enqueueResponse, chatAgentRequest(http.MethodPost,
		"/api/v2/students/me/chat/threads/"+testChatThreadID+"/runs", enqueueBody))
	if enqueueResponse.Code != http.StatusAccepted || enqueueResponse.Header().Get("Location") != "/api/v2/students/me/agent-runs/"+testAgentRunID {
		t.Fatalf("enqueue status=%d headers=%#v body=%s", enqueueResponse.Code, enqueueResponse.Header(), enqueueResponse.Body.String())
	}
	autoBody := `{"promptConfigurationKey":"agent.prompt.default","modelConfigurationKey":"agent.model.default","expectedAnalyticsHeadRevision":7}`
	autoResponse := newTestResponseRecorder()
	handler.ServeHTTP(autoResponse, chatAgentRequest(http.MethodPost, "/api/v2/students/me/auto-analysis", autoBody))
	if autoResponse.Code != http.StatusAccepted || autoResponse.Header().Get("Location") != "/api/v2/students/me/agent-runs/"+testAgentRunID ||
		!strings.Contains(autoResponse.Body.String(), `"created":true`) {
		t.Fatalf("auto-analysis status=%d headers=%#v body=%s", autoResponse.Code, autoResponse.Header(), autoResponse.Body.String())
	}
	runResponse := newTestResponseRecorder()
	handler.ServeHTTP(runResponse, chatAgentRequest(http.MethodGet, "/api/v2/students/me/agent-runs/"+testAgentRunID, ""))
	if runResponse.Code != http.StatusOK || !strings.Contains(runResponse.Body.String(), `"status":"queued"`) {
		t.Fatalf("run status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
}

func TestChatAgentSSEPagesFullTerminalBatchBeforeClosing(t *testing.T) {
	calls := 0
	service := chatAgentServiceStub{
		readRunEvents: func(_ context.Context, token, runID string, after int64, limit int) (chatagent.RunEventBatch, error) {
			if token != "chat-token" || runID != testAgentRunID || limit != agentEventBatchSize {
				t.Fatalf("event query token=%q run=%q after=%d limit=%d", token, runID, after, limit)
			}
			calls++
			if calls == 1 {
				events := make([]chatagent.RunEvent, agentEventBatchSize)
				for index := range events {
					sequence := int64(index + 1)
					events[index] = chatagent.RunEvent{Sequence: sequence, Type: "running", Payload: json.RawMessage(`{"status":"running"}`), CreatedAt: testChatTime()}
				}
				return chatagent.RunEventBatch{Events: events, LastSequence: int64(agentEventBatchSize + 1), Terminal: true}, nil
			}
			if after != int64(agentEventBatchSize) {
				t.Fatalf("second after=%d", after)
			}
			return chatagent.RunEventBatch{Events: []chatagent.RunEvent{{
				Sequence: int64(agentEventBatchSize + 1), Type: "completed",
				Payload: json.RawMessage(`{"messageId":"` + testChatMessageID + `"}`), CreatedAt: testChatTime(),
			}}, LastSequence: int64(agentEventBatchSize + 1), Terminal: true}, nil
		},
	}
	handler := newChatAgentTestHandler(t, service, true)
	request := chatAgentRequest(http.MethodGet, "/api/v2/students/me/agent-runs/"+testAgentRunID+"/events", "")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 2 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "id: 200\nevent: running") ||
		!strings.Contains(response.Body.String(), "id: 201\nevent: completed") {
		t.Fatalf("terminal page was not fully streamed: %s", response.Body.String())
	}
}

func TestAutomaticAnalysisHTTPReplayAndOpaqueConfigurationConflict(t *testing.T) {
	requestBody := `{"promptConfigurationKey":"agent.prompt.default","modelConfigurationKey":"agent.model.default","expectedAnalyticsHeadRevision":7}`
	now := testChatTime()
	result := chatagent.EnqueueResult{
		Run: chatagent.Run{
			ID: testAgentRunID, ThreadID: testChatThreadID, ClientRequestID: testChatRequestID,
			Kind: chatagent.RunAutoAnalysis, InputMessageID: testChatMessageID, Status: chatagent.RunQueued,
			CreatedAt: now, UpdatedAt: now,
		},
		Message: chatagent.Message{
			ID: testChatMessageID, ThreadID: testChatThreadID, Sequence: 1,
			Kind: chatagent.MessageAutoAnalysisRequest, Content: chatagent.AutoAnalysisInputContent, CreatedAt: now,
		},
		Created: false,
	}
	handler := newChatAgentTestHandler(t, chatAgentServiceStub{
		autoAnalysis: func(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
			return result, nil
		},
	}, true)
	replay := newTestResponseRecorder()
	handler.ServeHTTP(replay, chatAgentRequest(http.MethodPost, "/api/v2/students/me/auto-analysis", requestBody))
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"created":false`) {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}

	secret := "stored-configuration-secret"
	conflictHandler := newChatAgentTestHandler(t, chatAgentServiceStub{
		autoAnalysis: func(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
			return chatagent.EnqueueResult{}, &chatagent.Error{
				Code: chatagent.ErrorAutoAnalysisConflict, Permanent: true, Op: "replay", Cause: errors.New(secret),
			}
		},
	}, true)
	conflict := newTestResponseRecorder()
	conflictHandler.ServeHTTP(conflict, chatAgentRequest(http.MethodPost, "/api/v2/students/me/auto-analysis", requestBody))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"auto_analysis_configuration_conflict"`) ||
		strings.Contains(conflict.Body.String(), secret) {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	unknown := newTestResponseRecorder()
	handler.ServeHTTP(unknown, chatAgentRequest(http.MethodPost, "/api/v2/students/me/auto-analysis",
		`{"promptConfigurationKey":"agent.prompt.default","modelConfigurationKey":"agent.model.default","expectedAnalyticsHeadRevision":7,"clientRequestId":"`+testChatRequestID+`"}`))
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestChatAgentHTTPRejectsNonCanonicalRequestsAndOpaqueErrors(t *testing.T) {
	secret := "database-secret-must-not-leak"
	service := chatAgentServiceStub{
		listThreads: func(context.Context, string, *string, int) (chatagent.ThreadPage, error) {
			return chatagent.ThreadPage{}, &chatagent.Error{Code: chatagent.ErrorDatabase, Op: "list", Cause: errors.New(secret)}
		},
		readRunEvents: func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
			return chatagent.RunEventBatch{}, &chatagent.Error{Code: chatagent.ErrorEventCursorInvalid, Op: "events", Cause: errors.New(secret)}
		},
	}
	handler := newChatAgentTestHandler(t, service, true)

	for _, path := range []string{
		"/api/v2/students/me/chat/threads?",
		"/api/v2/students/me/chat/threads?limit=01",
		"/api/v2/students/me/chat/threads?limit=1&limit=2",
		"/api/v2/students/me/chat/threads?cursor=invalid",
		"/api/v2/students/me/chat/threads?unknown=1",
		"/api/v2/students/me/chat/threads/invalid/messages",
		"/api/v2/students/me/chat/threads/" + testChatThreadID + "/messages?afterSequence=01",
		"/api/v2/students/me/agent-runs/invalid",
	} {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, chatAgentRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	internalResponse := newTestResponseRecorder()
	handler.ServeHTTP(internalResponse, chatAgentRequest(http.MethodGet, "/api/v2/students/me/chat/threads", ""))
	if internalResponse.Code != http.StatusInternalServerError || strings.Contains(internalResponse.Body.String(), secret) {
		t.Fatalf("internal status=%d body=%s", internalResponse.Code, internalResponse.Body.String())
	}
	cursorRequest := chatAgentRequest(http.MethodGet, "/api/v2/students/me/agent-runs/"+testAgentRunID+"/events", "")
	cursorRequest.Header.Set("Last-Event-ID", "99")
	cursorResponse := newTestResponseRecorder()
	handler.ServeHTTP(cursorResponse, cursorRequest)
	if cursorResponse.Code != http.StatusBadRequest || !strings.Contains(cursorResponse.Body.String(), `"code":"invalid_event_cursor"`) || strings.Contains(cursorResponse.Body.String(), secret) {
		t.Fatalf("cursor status=%d body=%s", cursorResponse.Code, cursorResponse.Body.String())
	}
}

func TestChatAgentCORSPreflightIsClosedPerMethod(t *testing.T) {
	handler := newChatAgentTestHandler(t, unusedChatAgentService{}, true)
	tests := []struct {
		path    string
		method  string
		headers string
	}{
		{"/api/v2/students/me/chat/threads", http.MethodGet, "Authorization"},
		{"/api/v2/students/me/chat/threads", http.MethodPost, "Authorization"},
		{"/api/v2/students/me/chat/threads/" + testChatThreadID + "/messages", http.MethodGet, "Authorization"},
		{"/api/v2/students/me/chat/threads/" + testChatThreadID + "/runs", http.MethodPost, "Authorization, Content-Type"},
		{"/api/v2/students/me/auto-analysis", http.MethodPost, "Authorization, Content-Type"},
		{"/api/v2/students/me/agent-runs/" + testAgentRunID, http.MethodGet, "Authorization"},
		{"/api/v2/students/me/agent-runs/" + testAgentRunID + "/events", http.MethodGet, "Authorization, Last-Event-ID"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodOptions, test.path, nil)
		request.RemoteAddr = "192.0.2.1:44000"
		request.Header.Set("Origin", testWebOrigin)
		request.Header.Set("Access-Control-Request-Method", test.method)
		request.Header.Set("Access-Control-Request-Headers", test.headers)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != test.method {
			t.Fatalf("path=%s status=%d headers=%#v body=%s", test.path, response.Code, response.Header(), response.Body.String())
		}
	}
}

func newChatAgentTestHandler(t *testing.T, service ChatAgentService, writes bool) http.Handler {
	t.Helper()
	options := testHandlerOptions(healthReadyReport())
	options.ChatAgent = service
	options.Capabilities = testCapabilities(writes)
	if !writes {
		options.Artifacts = nil
		options.Imports = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func chatAgentRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer chat-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func testChatTime() time.Time {
	return time.Date(2026, 7, 11, 2, 3, 4, 0, time.UTC)
}

func healthReadyReport() health.Report {
	return health.Report{Status: health.StatusReady}
}
