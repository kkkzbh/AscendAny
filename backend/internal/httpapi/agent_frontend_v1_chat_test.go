package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

const (
	testAgentFrontendV1ConversationThreadID = "223e4567-e89b-42d3-a456-426614174050"
	testAgentFrontendV1AutoThreadID         = "223e4567-e89b-42d3-a456-426614174051"
	testAgentFrontendV1OutputMessageID      = "223e4567-e89b-42d3-a456-426614174052"
)

type agentFrontendV1AnalyticsStub struct {
	getSelf func(context.Context, string, int) (studentanalytics.Result, error)
}

func (stub agentFrontendV1AnalyticsStub) GetSelf(ctx context.Context, token string, limit int) (studentanalytics.Result, error) {
	return stub.getSelf(ctx, token, limit)
}

func (agentFrontendV1AnalyticsStub) GetLeaderboard(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
	panic("unexpected leaderboard read")
}

func TestAgentFrontendV1ReplyPreservesFullLocalHistoryAndStreamsDurableRun(t *testing.T) {
	now := testChatTime()
	analyticsRead := false
	thread := chatagent.Thread{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation}
	inputMessage := chatagent.Message{
		ID: testChatMessageID, ThreadID: thread.ID, Sequence: 1, Kind: chatagent.MessageUser, CreatedAt: now,
	}
	reasoning := "checked the current analytics"
	summary := "next context"
	runID := testAgentRunID
	output := chatagent.Message{
		ID: testAgentFrontendV1OutputMessageID, ThreadID: thread.ID, Sequence: 2,
		Kind: chatagent.MessageAssistant, Content: "Final answer.", ReasoningContent: &reasoning,
		ContextSummary: &summary, RunID: &runID, CreatedAt: now,
	}
	service := chatAgentServiceStub{
		createThread: func(_ context.Context, token string) (chatagent.Thread, error) {
			if token != "chat-token" || !analyticsRead {
				t.Fatalf("create token=%q", token)
			}
			return thread, nil
		},
		enqueue: func(_ context.Context, token, threadID string, input chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
			const wantContent = `{"currentUser":{"content":"Next question","messageIndex":2,"ptaNickname":"pta-user","studentId":"student-1"},"messages":[{"content":"First question","reasoningContent":null,"role":"user"},{"content":"Prior answer","reasoningContent":"Prior reasoning","role":"assistant"},{"content":"Next question","reasoningContent":null,"role":"user"}],"notes":{"content":"note","locked":false,"title":"Notebook"},"role":{"id":"mentor","name":"Mentor","systemPrompt":"Be concise."},"schema":"ascendany.agent.frontend-context.v1","summary":"prior"}`
			if token != "chat-token" || threadID != thread.ID || !chatagent.ValidPublicID(input.ClientRequestID) ||
				input.Kind != chatagent.RunReply || input.Content != wantContent ||
				input.PromptConfigurationKey != agentFrontendV1PromptConfigurationKey ||
				input.ModelConfigurationKey != agentFrontendV1ModelConfigurationKey ||
				(input.ExpectedAnalyticsHeadRevision == nil || *input.ExpectedAnalyticsHeadRevision != 7) {
				t.Fatalf("enqueue token=%q thread=%q input=%#v", token, threadID, input)
			}
			inputMessage.Content = input.Content
			return agentFrontendV1QueuedResult(thread.ID, inputMessage), nil
		},
		readRunEvents: func(_ context.Context, token, runID string, after int64, limit int) (chatagent.RunEventBatch, error) {
			if token != "chat-token" || runID != testAgentRunID || after != 0 || limit != agentEventBatchSize {
				t.Fatalf("read events token=%q run=%q after=%d limit=%d", token, runID, after, limit)
			}
			return chatagent.RunEventBatch{
				Events: []chatagent.RunEvent{
					{Sequence: 1, Type: "queued", Payload: []byte(`{"analyticsHeadRevision":7,"messageSequence":1,"model":"deepseek-chat","provider":"openai_compatible","requestMode":"chat_completions","runKind":"reply"}`), CreatedAt: now},
					{Sequence: 2, Type: "claimed", Payload: []byte(`{"attemptCount":1,"leaseOwner":"worker"}`), CreatedAt: now},
					{Sequence: 3, Type: "tool.succeeded", Payload: []byte(`{"toolCallKey":"call-1","toolName":"analytics.current","toolSequence":1}`), CreatedAt: now},
					{Sequence: 4, Type: "notes_update", Payload: []byte(`{"mode":"replace","next":"updated note","patch":null,"previous":"note","toolCallKey":"call-notes","toolName":"update_notes","toolSequence":2}`), CreatedAt: now},
					{Sequence: 5, Type: "tool.succeeded", Payload: []byte(`{"toolCallKey":"call-notes","toolName":"update_notes","toolSequence":2}`), CreatedAt: now},
					{Sequence: 6, Type: "completed", Payload: []byte(`{"messageId":"` + output.ID + `","messageSequence":2}`), CreatedAt: now},
				},
				LastSequence: 6,
				Terminal:     true,
			}, nil
		},
		getRun: func(_ context.Context, token, requestedRunID string) (chatagent.Run, bool, error) {
			if token != "chat-token" || requestedRunID != testAgentRunID {
				t.Fatalf("get run token=%q run=%q", token, requestedRunID)
			}
			outputID := output.ID
			return chatagent.Run{
				ID: testAgentRunID, ThreadID: thread.ID, InputMessageID: inputMessage.ID,
				Status: chatagent.RunSucceeded, OutputMessageID: &outputID,
			}, true, nil
		},
		listMessages: func(_ context.Context, token, threadID string, after int64, limit int) ([]chatagent.Message, error) {
			if token != "chat-token" || threadID != thread.ID || after != 1 || limit != 1 {
				t.Fatalf("messages token=%q thread=%q after=%d limit=%d", token, threadID, after, limit)
			}
			return []chatagent.Message{output}, nil
		},
	}
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(_ context.Context, token string, limit int) (studentanalytics.Result, error) {
		if token != "chat-token" || limit != 1 {
			t.Fatalf("analytics token=%q limit=%d", token, limit)
		}
		analyticsRead = true
		return studentanalytics.Result{
			State: studentanalytics.StateReady, HeadRevision: 7, Ready: &studentanalytics.ReadyResult{},
		}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, service, analytics)
	request := chatAgentRequest(http.MethodPost, "/api/v1/chat/reply/stream", `{
		"studentId":"student-1",
		"ptaNickname":"pta-user",
		"messages":[
			{"role":"user","content":"First question"},
			{"role":"assistant","content":"Prior answer","reasoningContent":"Prior reasoning"},
			{"role":"user","content":"Next question"}
		],
		"summary":"prior",
		"roleId":"mentor",
		"roleName":"Mentor",
		"roleSystemPrompt":"Be concise.",
		"notes":"note",
		"notesTitle":"Notebook",
		"notesLocked":false
	}`)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
	body := response.Body.String()
	assertOrderedSubstrings(t, body,
		`"type":"meta","summary":"prior","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"`,
		`"type":"tool_activity_start","activityId":"call-1","label":"analytics.current","status":"running"`,
		`"type":"tool_activity_done","activityId":"call-1","label":"analytics.current","status":"done"`,
		`"type":"tool_activity_start","activityId":"call-notes","label":"更新学习笔记","status":"running"`,
		`"type":"notes_update","mode":"replace","previous":"note","next":"updated note","patch":null`,
		`"type":"tool_activity_done","activityId":"call-notes","label":"更新学习笔记","status":"done"`,
		`"type":"reasoning_delta","text":"checked the current analytics"`,
		`"type":"delta","text":"Final answer."`,
		`"type":"done","reply":"Final answer.","summary":"next context","runId":"123e4567-e89b-42d3-a456-426614174051","threadId":"223e4567-e89b-42d3-a456-426614174050","inputMessageId":"123e4567-e89b-42d3-a456-426614174052","outputMessageId":"223e4567-e89b-42d3-a456-426614174052","created":true,"updatedNotes":"updated note","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"`,
	)
}

func TestAgentFrontendV1ReplyCreatesFreshThreadForEveryRequest(t *testing.T) {
	now := testChatTime()
	created := chatagent.Thread{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation}
	input := chatagent.Message{ID: testChatMessageID, ThreadID: created.ID, Sequence: 1, Kind: chatagent.MessageUser, CreatedAt: now}
	output := agentFrontendV1OutputMessage(created.ID, "Created thread reply.", nil)
	service := completedAgentFrontendV1ChatService(now, created.ID, input, output)
	service.listThreads = func(context.Context, string, *string, int) (chatagent.ThreadPage, error) {
		panic("Agent frontend bridge must not list account threads")
	}
	createCalls := 0
	service.createThread = func(_ context.Context, token string) (chatagent.Thread, error) {
		if token != "chat-token" {
			t.Fatalf("create token=%q", token)
		}
		createCalls++
		return created, nil
	}
	handler := newAgentFrontendV1ChatTestHandler(t, service, nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(http.MethodPost, "/api/v1/chat/reply/stream", `{"messages":[{"role":"user","content":"First"}],"summary":""}`))
	if response.Code != http.StatusOK || createCalls != 1 || !strings.Contains(response.Body.String(), `"type":"done","reply":"Created thread reply."`) {
		t.Fatalf("status=%d createCalls=%d body=%s", response.Code, createCalls, response.Body.String())
	}
}

func TestAgentFrontendV1ReplyRunsWithoutPublishedAnalytics(t *testing.T) {
	now := testChatTime()
	thread := chatagent.Thread{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation}
	input := chatagent.Message{ID: testChatMessageID, ThreadID: thread.ID, Sequence: 1, Kind: chatagent.MessageUser, CreatedAt: now}
	output := agentFrontendV1OutputMessage(thread.ID, "General answer.", nil)
	service := completedAgentFrontendV1ChatService(now, thread.ID, input, output)
	service.createThread = func(context.Context, string) (chatagent.Thread, error) { return thread, nil }
	service.enqueue = func(_ context.Context, _ string, requestedThreadID string, request chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
		if request.ExpectedAnalyticsHeadRevision != nil {
			t.Fatalf("unexpected analytics binding=%d", *request.ExpectedAnalyticsHeadRevision)
		}
		input.Content = request.Content
		return agentFrontendV1QueuedResult(requestedThreadID, input), nil
	}
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(_ context.Context, token string, limit int) (studentanalytics.Result, error) {
		if token != "chat-token" || limit != 1 {
			t.Fatalf("analytics token=%q limit=%d", token, limit)
		}
		return studentanalytics.Result{State: studentanalytics.StateNoObservations, HeadRevision: 7}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, service, analytics)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(
		http.MethodPost,
		"/api/v1/chat/reply/stream",
		`{"messages":[{"role":"user","content":"First"}],"summary":""}`,
	))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reply":"General answer."`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentFrontendV1ReplyNonStreamReturnsDurableProviderMetadata(t *testing.T) {
	now := testChatTime()
	thread := chatagent.Thread{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation}
	input := chatagent.Message{ID: testChatMessageID, ThreadID: thread.ID, Sequence: 1, Kind: chatagent.MessageUser, CreatedAt: now}
	summary := "durable summary"
	output := agentFrontendV1OutputMessage(thread.ID, "Durable JSON reply.", &summary)
	service := completedAgentFrontendV1ChatService(now, thread.ID, input, output)
	service.createThread = func(context.Context, string) (chatagent.Thread, error) { return thread, nil }
	service.readRunEvents = func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
		return chatagent.RunEventBatch{Events: []chatagent.RunEvent{
			{Sequence: 1, Type: "queued", Payload: []byte(`{"analyticsHeadRevision":7,"messageSequence":1,"model":"deepseek-chat","provider":"openai_compatible","requestMode":"chat_completions","runKind":"reply"}`), CreatedAt: now},
			{Sequence: 2, Type: "completed", Payload: []byte(`{"messageId":"` + output.ID + `","messageSequence":2}`), CreatedAt: now},
		}, LastSequence: 2, Terminal: true}, nil
	}
	handler := newAgentFrontendV1ChatTestHandler(t, service, nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(
		http.MethodPost,
		"/api/v1/chat/reply",
		`{"messages":[{"role":"user","content":"Question"}],"summary":"prior"}`,
	))
	want := `{"reply":"Durable JSON reply.","summary":"durable summary","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("status=%d body=%s want=%s", response.Code, response.Body.String(), want)
	}
}

func TestAgentFrontendV1ReplyKeepsLocalSessionsOnIndependentDurableThreads(t *testing.T) {
	threads := []chatagent.Thread{
		{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation},
		{ID: "323e4567-e89b-42d3-a456-426614174050", Kind: chatagent.ThreadConversation},
	}
	createIndex := 0
	enqueuedThreads := make([]string, 0, len(threads))
	enqueuedContent := make([]string, 0, len(threads))
	service := chatAgentServiceStub{
		listThreads: func(context.Context, string, *string, int) (chatagent.ThreadPage, error) {
			panic("Agent frontend bridge must not list account threads")
		},
		createThread: func(_ context.Context, token string) (chatagent.Thread, error) {
			if token != "chat-token" || createIndex >= len(threads) {
				t.Fatalf("create token=%q index=%d", token, createIndex)
			}
			thread := threads[createIndex]
			createIndex++
			return thread, nil
		},
		enqueue: func(_ context.Context, token, threadID string, input chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
			if token != "chat-token" || !chatagent.ValidPublicID(input.ClientRequestID) {
				t.Fatalf("enqueue token=%q input=%#v", token, input)
			}
			enqueuedThreads = append(enqueuedThreads, threadID)
			enqueuedContent = append(enqueuedContent, input.Content)
			return chatagent.EnqueueResult{}, nil
		},
	}
	requestIDs, err := newRequestIDGenerator(bytes.NewReader([]byte("12345678")))
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{chatAgent: service, requestIDs: requestIDs}
	if _, err := handler.agentFrontendV1EnqueueFreshReply(context.Background(), "chat-token", "session-a", int64Pointer(7)); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.agentFrontendV1EnqueueFreshReply(context.Background(), "chat-token", "session-b", int64Pointer(7)); err != nil {
		t.Fatal(err)
	}
	if createIndex != 2 || len(enqueuedThreads) != 2 || enqueuedThreads[0] != threads[0].ID || enqueuedThreads[1] != threads[1].ID ||
		enqueuedContent[0] != "session-a" || enqueuedContent[1] != "session-b" {
		t.Fatalf("createIndex=%d threads=%#v content=%#v", createIndex, enqueuedThreads, enqueuedContent)
	}
}

func TestAgentFrontendV1ContextPreservesFrozenLocalHistory(t *testing.T) {
	summary := "persisted local summary"
	studentID := "student-7"
	ptaNickname := "pta-7"
	roleID := "sakiko"
	roleName := "Sakiko"
	rolePrompt := "Keep the original role semantics."
	notes := "persisted notes"
	notesTitle := "Local notebook"
	notesLocked := true
	reasoning := "prior assistant reasoning"
	encoded, err := canonicalAgentFrontendV1RunInput(agentFrontendV1ChatReplyRequest{
		StudentID:   &studentID,
		PTANickname: &ptaNickname,
		Messages: []agentFrontendV1ChatMessage{
			{Role: "system", Content: "persisted local system marker"},
			{Role: "user", Content: "old question"},
			{Role: "assistant", Content: "old answer", ReasoningContent: &reasoning},
			{Role: "user", Content: "current question"},
		},
		Summary:          &summary,
		RoleID:           &roleID,
		RoleName:         &roleName,
		RoleSystemPrompt: &rolePrompt,
		Notes:            &notes,
		NotesTitle:       &notesTitle,
		NotesLocked:      &notesLocked,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > chatagent.MaxMessageBytes {
		t.Fatalf("encoded bytes=%d", len(encoded))
	}
	var document agentFrontendV1RunContext
	if err := json.Unmarshal([]byte(encoded), &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != agentFrontendV1ContextSchema || document.Summary != summary ||
		document.CurrentUser.StudentID != studentID || document.CurrentUser.PTANickname != ptaNickname ||
		document.CurrentUser.Content != "current question" || document.CurrentUser.MessageIndex != 3 ||
		document.Role.ID != roleID || document.Role.Name != roleName ||
		document.Role.SystemPrompt != rolePrompt || document.Notes.Content != notes ||
		document.Notes.Title != notesTitle || !document.Notes.Locked || len(document.Messages) != 4 {
		t.Fatalf("document=%#v", document)
	}
	for index, expectedRole := range []string{"system", "user", "assistant", "user"} {
		if document.Messages[index].Role != expectedRole {
			t.Fatalf("message[%d]=%#v", index, document.Messages[index])
		}
	}
	if document.Messages[2].ReasoningContent == nil || *document.Messages[2].ReasoningContent != reasoning ||
		document.Messages[0].ReasoningContent != nil || document.Messages[3].Content != "current question" {
		t.Fatalf("messages=%#v", document.Messages)
	}
}

func TestAgentFrontendV1ContextAcceptsExactUnicodeNotesCharacterLimit(t *testing.T) {
	t.Parallel()
	summary := ""
	notes := strings.Repeat("😀", chatagent.MaxFrontendNotesCharacters)
	encoded, err := canonicalAgentFrontendV1RunInput(agentFrontendV1ChatReplyRequest{
		Messages: []agentFrontendV1ChatMessage{{Role: "user", Content: "summarize these notes"}},
		Summary:  &summary,
		Notes:    &notes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= chatagent.MaxMessageBytes || len(encoded) > chatagent.MaxFrontendContextDocumentBytes {
		t.Fatalf("encoded bytes=%d", len(encoded))
	}
	var document agentFrontendV1RunContext
	if err := json.Unmarshal([]byte(encoded), &document); err != nil || document.Notes.Content != notes {
		t.Fatalf("notes bytes=%d error=%v", len(document.Notes.Content), err)
	}
}

func TestAgentFrontendV1ReplyRejectsContextAboveMessageBoundary(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "current question"}},
		"summary":  "",
		"notes":    strings.Repeat("n", chatagent.MaxMessageBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newAgentFrontendV1ChatTestHandler(t, unusedChatAgentService{}, nil)
	request := chatAgentRequest(http.MethodPost, "/api/v1/chat/reply/stream", string(payload))
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_chat_request"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentFrontendV1AutoAnalysisBindsPublishedHeadAndFrozenFrontendContext(t *testing.T) {
	now := testChatTime()
	input := chatagent.Message{
		ID: testChatMessageID, ThreadID: testAgentFrontendV1AutoThreadID, Sequence: 1,
		Kind: chatagent.MessageAutoAnalysisRequest, Content: chatagent.AutoAnalysisInputContent, CreatedAt: now,
	}
	output := agentFrontendV1OutputMessage(testAgentFrontendV1AutoThreadID, "Automatic analysis.", nil)
	service := completedAgentFrontendV1ChatService(now, testAgentFrontendV1AutoThreadID, input, output)
	service.enqueue = nil
	service.autoAnalysis = func(_ context.Context, token string, request chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
		wantContext := chatagent.AutoAnalysisFrontendContext{
			StudentID:        "student-1",
			PTANickname:      "pta-user",
			RoleID:           chatagent.DefaultAutoAnalysisRoleID,
			RoleName:         "Mentor",
			RoleSystemPrompt: "Be concise.",
			LatestExamID:     testExamID,
			Notes:            "note",
			NotesTitle:       "Notebook",
			NotesLocked:      true,
		}
		if token != "chat-token" || request.PromptConfigurationKey != agentFrontendV1PromptConfigurationKey ||
			request.ModelConfigurationKey != agentFrontendV1ModelConfigurationKey || request.ExpectedAnalyticsHeadRevision != 7 ||
			request.Identity != (chatagent.AutoAnalysisIdentity{ExamID: testExamID, RoleID: chatagent.DefaultAutoAnalysisRoleID}) ||
			request.FrontendContext != wantContext {
			t.Fatalf("auto analysis token=%q request=%#v", token, request)
		}
		result := agentFrontendV1QueuedResult(testAgentFrontendV1AutoThreadID, input)
		result.Created = false
		return result, nil
	}
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(_ context.Context, token string, limit int) (studentanalytics.Result, error) {
		if token != "chat-token" || limit != 1 {
			t.Fatalf("analytics token=%q limit=%d", token, limit)
		}
		return studentanalytics.Result{
			State: studentanalytics.StateReady, HeadRevision: 7, Ready: &studentanalytics.ReadyResult{
				ExamHistory: []studentanalytics.ExamHistoryPoint{{ExamID: testExamID}},
			},
		}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, service, analytics)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(http.MethodPost, "/api/v1/chat/auto-analysis/stream", `{
		"studentId":"student-1","ptaNickname":"pta-user","roleName":"Mentor",
		"roleSystemPrompt":"Be concise.","latestExamId":"`+testExamID+`","notes":"note",
		"notesTitle":"Notebook","notesLocked":true
	}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reply":""`) ||
		!strings.Contains(response.Body.String(), `"provider":"none"`) ||
		strings.Contains(response.Body.String(), "Automatic analysis.") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentFrontendV1AutoAnalysisDefaultsCurrentExamAndRole(t *testing.T) {
	service := chatAgentServiceStub{autoAnalysis: func(_ context.Context, token string, request chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
		if token != "chat-token" || request.Identity.ExamID != testExamID ||
			request.Identity.RoleID != chatagent.DefaultAutoAnalysisRoleID ||
			request.FrontendContext.LatestExamID != testExamID ||
			request.FrontendContext.RoleID != chatagent.DefaultAutoAnalysisRoleID {
			t.Fatalf("token=%q request=%#v", token, request)
		}
		result := agentFrontendV1QueuedResult(testAgentFrontendV1AutoThreadID, chatagent.Message{
			ID: testChatMessageID, ThreadID: testAgentFrontendV1AutoThreadID, Sequence: 1,
			Kind: chatagent.MessageAutoAnalysisRequest, Content: chatagent.AutoAnalysisInputContent,
		})
		result.Run.Kind = chatagent.RunAutoAnalysis
		result.Created = false
		return result, nil
	}}
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
		return studentanalytics.Result{
			State: studentanalytics.StateReady, HeadRevision: 7,
			Ready: &studentanalytics.ReadyResult{ExamHistory: []studentanalytics.ExamHistoryPoint{{ExamID: testExamID}}},
		}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, service, analytics)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(http.MethodPost, "/api/v1/chat/auto-analysis/stream", `{}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"done"`) ||
		!strings.Contains(response.Body.String(), `"reply":""`) || !strings.Contains(response.Body.String(), `"provider":"none"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentFrontendV1AutoAnalysisDecisionSkipsMissingOrStaleExam(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		analytics studentanalytics.Result
		requested *string
	}{
		{name: "not generated", analytics: studentanalytics.Result{State: studentanalytics.StateNotGenerated}},
		{name: "no observations", analytics: studentanalytics.Result{State: studentanalytics.StateNoObservations, HeadRevision: 7}},
		{name: "ready without exam", analytics: studentanalytics.Result{State: studentanalytics.StateReady, HeadRevision: 7, Ready: &studentanalytics.ReadyResult{}}},
		{name: "stale client exam", analytics: studentanalytics.Result{
			State: studentanalytics.StateReady, HeadRevision: 7,
			Ready: &studentanalytics.ReadyResult{ExamHistory: []studentanalytics.ExamHistoryPoint{{ExamID: testExamID}}},
		}, requested: stringPointer("323e4567-e89b-42d3-a456-426614174050")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := &Handler{studentAnalytics: agentFrontendV1AnalyticsStub{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
				return test.analytics, nil
			}}}
			identity, revision, shouldRun, err := handler.agentFrontendV1AutoAnalysisDecision(
				context.Background(), "chat-token", agentFrontendV1AutoAnalysisRequest{LatestExamID: test.requested},
			)
			if err != nil || shouldRun || revision != 0 || identity != (chatagent.AutoAnalysisIdentity{}) {
				t.Fatalf("identity=%#v revision=%d shouldRun=%t error=%v", identity, revision, shouldRun, err)
			}
		})
	}
}

func TestAgentFrontendV1AutoAnalysisNonStreamEmptyUsesServerDefault(t *testing.T) {
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
		return studentanalytics.Result{State: studentanalytics.StateNoObservations, HeadRevision: 7}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, unusedChatAgentService{}, analytics)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(http.MethodPost, "/api/v1/chat/auto-analysis", `{}`))
	if response.Code != http.StatusOK || response.Body.String() != `{"reply":"","provider":"server_default"}`+"\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentFrontendV1AutoAnalysisNonStreamReturnsDurableProviderMetadata(t *testing.T) {
	now := testChatTime()
	input := chatagent.Message{
		ID: testChatMessageID, ThreadID: testAgentFrontendV1AutoThreadID, Sequence: 1,
		Kind: chatagent.MessageAutoAnalysisRequest, Content: chatagent.AutoAnalysisInputContent, CreatedAt: now,
	}
	output := agentFrontendV1OutputMessage(testAgentFrontendV1AutoThreadID, "Durable automatic analysis.", nil)
	service := chatAgentServiceStub{
		autoAnalysis: func(_ context.Context, _ string, request chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
			if request.Identity != (chatagent.AutoAnalysisIdentity{ExamID: testExamID, RoleID: "mentor"}) {
				t.Fatalf("request=%#v", request)
			}
			return chatagent.EnqueueResult{
				Run: chatagent.Run{
					ID: testAgentRunID, ThreadID: input.ThreadID, ClientRequestID: testChatRequestID,
					Kind: chatagent.RunAutoAnalysis, InputMessageID: input.ID, Status: chatagent.RunQueued,
				},
				Message: input, Created: true,
			}, nil
		},
		readRunEvents: func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
			return chatagent.RunEventBatch{Events: []chatagent.RunEvent{
				{Sequence: 1, Type: "queued", Payload: []byte(`{"analyticsHeadRevision":7,"autoAnalysisExamId":"` + testExamID + `","autoAnalysisRoleId":"mentor","messageSequence":1,"model":"deepseek-chat","provider":"openai_compatible","requestMode":"chat_completions","runKind":"auto_analysis"}`), CreatedAt: now},
				{Sequence: 2, Type: "completed", Payload: []byte(`{"messageId":"` + output.ID + `","messageSequence":2}`), CreatedAt: now},
			}, LastSequence: 2, Terminal: true}, nil
		},
		getRun: func(context.Context, string, string) (chatagent.Run, bool, error) {
			outputID := output.ID
			return chatagent.Run{
				ID: testAgentRunID, ThreadID: input.ThreadID, InputMessageID: input.ID,
				Status: chatagent.RunSucceeded, OutputMessageID: &outputID,
			}, true, nil
		},
		listMessages: func(context.Context, string, string, int64, int) ([]chatagent.Message, error) {
			return []chatagent.Message{output}, nil
		},
	}
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
		return studentanalytics.Result{
			State: studentanalytics.StateReady, HeadRevision: 7,
			Ready: &studentanalytics.ReadyResult{ExamHistory: []studentanalytics.ExamHistoryPoint{{ExamID: testExamID}}},
		}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, service, analytics)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(
		http.MethodPost,
		"/api/v1/chat/auto-analysis",
		`{"roleId":"mentor","latestExamId":"`+testExamID+`"}`,
	))
	want := `{"reply":"Durable automatic analysis.","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("status=%d body=%s want=%s", response.Code, response.Body.String(), want)
	}
}

func TestAgentFrontendV1AutoAnalysisStreamProjectsDurableProviderMetadata(t *testing.T) {
	now := testChatTime()
	input := chatagent.Message{
		ID: testChatMessageID, ThreadID: testAgentFrontendV1AutoThreadID, Sequence: 1,
		Kind: chatagent.MessageAutoAnalysisRequest, Content: chatagent.AutoAnalysisInputContent, CreatedAt: now,
	}
	output := agentFrontendV1OutputMessage(testAgentFrontendV1AutoThreadID, "Streamed automatic analysis.", nil)
	service := chatAgentServiceStub{
		autoAnalysis: func(_ context.Context, _ string, request chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
			if request.Identity != (chatagent.AutoAnalysisIdentity{ExamID: testExamID, RoleID: "mentor"}) {
				t.Fatalf("request=%#v", request)
			}
			return chatagent.EnqueueResult{
				Run: chatagent.Run{
					ID: testAgentRunID, ThreadID: input.ThreadID, ClientRequestID: testChatRequestID,
					Kind: chatagent.RunAutoAnalysis, InputMessageID: input.ID, Status: chatagent.RunQueued,
				},
				Message: input, Created: true,
			}, nil
		},
		readRunEvents: func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
			return chatagent.RunEventBatch{Events: []chatagent.RunEvent{
				{Sequence: 1, Type: "queued", Payload: []byte(`{"analyticsHeadRevision":7,"autoAnalysisExamId":"` + testExamID + `","autoAnalysisRoleId":"mentor","messageSequence":1,"model":"deepseek-chat","provider":"openai_compatible","requestMode":"chat_completions","runKind":"auto_analysis"}`), CreatedAt: now},
				{Sequence: 2, Type: "completed", Payload: []byte(`{"messageId":"` + output.ID + `","messageSequence":2}`), CreatedAt: now},
			}, LastSequence: 2, Terminal: true}, nil
		},
		getRun: func(context.Context, string, string) (chatagent.Run, bool, error) {
			outputID := output.ID
			return chatagent.Run{
				ID: testAgentRunID, ThreadID: input.ThreadID, InputMessageID: input.ID,
				Status: chatagent.RunSucceeded, OutputMessageID: &outputID,
			}, true, nil
		},
		listMessages: func(context.Context, string, string, int64, int) ([]chatagent.Message, error) {
			return []chatagent.Message{output}, nil
		},
	}
	analytics := agentFrontendV1AnalyticsStub{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
		return studentanalytics.Result{
			State: studentanalytics.StateReady, HeadRevision: 7,
			Ready: &studentanalytics.ReadyResult{ExamHistory: []studentanalytics.ExamHistoryPoint{{ExamID: testExamID}}},
		}, nil
	}}
	handler := newAgentFrontendV1ChatTestHandler(t, service, analytics)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(
		http.MethodPost,
		"/api/v1/chat/auto-analysis/stream",
		`{"roleId":"mentor","latestExamId":"`+testExamID+`"}`,
	))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertOrderedSubstrings(t, response.Body.String(),
		`"type":"meta","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"`,
		`"type":"delta","text":"Streamed automatic analysis."`,
		`"type":"done","reply":"Streamed automatic analysis.","runId":"123e4567-e89b-42d3-a456-426614174051","threadId":"223e4567-e89b-42d3-a456-426614174051","inputMessageId":"123e4567-e89b-42d3-a456-426614174052","outputMessageId":"223e4567-e89b-42d3-a456-426614174052","created":true,"provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"`,
	)
}

func TestAgentFrontendV1ReplyProjectsTerminalFailure(t *testing.T) {
	now := testChatTime()
	thread := chatagent.Thread{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation}
	input := chatagent.Message{ID: testChatMessageID, ThreadID: thread.ID, Sequence: 1, Kind: chatagent.MessageUser, CreatedAt: now}
	failureCode := "provider_unavailable"
	service := chatAgentServiceStub{
		createThread: func(context.Context, string) (chatagent.Thread, error) { return thread, nil },
		enqueue: func(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
			return agentFrontendV1QueuedResult(thread.ID, input), nil
		},
		readRunEvents: func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
			return chatagent.RunEventBatch{Events: []chatagent.RunEvent{
				{Sequence: 1, Type: "queued", Payload: []byte(`{"messageSequence":1,"runKind":"reply"}`), CreatedAt: now},
				{Sequence: 2, Type: "failed", Payload: []byte(`{"errorCode":"provider_unavailable"}`), CreatedAt: now},
			}, LastSequence: 2, Terminal: true}, nil
		},
		getRun: func(context.Context, string, string) (chatagent.Run, bool, error) {
			return chatagent.Run{ID: testAgentRunID, ThreadID: thread.ID, Status: chatagent.RunFailed, ErrorCode: &failureCode}, true, nil
		},
	}
	handler := newAgentFrontendV1ChatTestHandler(t, service, nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, chatAgentRequest(http.MethodPost, "/api/v1/chat/reply/stream", `{"messages":[{"role":"user","content":"First"}],"summary":""}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"error","code":"provider_unavailable","message":"Agent run failed."`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentFrontendV1ChatRejectsMalformedPayloads(t *testing.T) {
	handler := newAgentFrontendV1ChatTestHandler(t, unusedChatAgentService{}, nil)
	for _, body := range []string{
		`{"messages":[],"summary":""}`,
		`{"messages":[{"role":"user","content":"Question"}]}`,
		`{"messages":[{"role":"assistant","content":"No user"}],"summary":""}`,
		`{"messages":[{"role":"user","content":"Question","extra":true}],"summary":""}`,
		`{"messages":[{"role":"user","content":"Question"}],"summary":"","extra":true}`,
	} {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, chatAgentRequest(http.MethodPost, "/api/v1/chat/reply/stream", body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}

	preflight := chatAgentRequest(http.MethodOptions, "/api/v1/chat/reply/stream", "")
	preflight.Header.Del("Authorization")
	preflight.Header.Set("Origin", testWebOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	preflightResponse := newTestResponseRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Methods") != http.MethodPost {
		t.Fatalf("preflight status=%d headers=%#v body=%s", preflightResponse.Code, preflightResponse.Header(), preflightResponse.Body.String())
	}
}

func TestAgentFrontendV1ChatStopsWhenClientCancels(t *testing.T) {
	now := testChatTime()
	thread := chatagent.Thread{ID: testAgentFrontendV1ConversationThreadID, Kind: chatagent.ThreadConversation}
	input := chatagent.Message{ID: testChatMessageID, ThreadID: thread.ID, Sequence: 1, Kind: chatagent.MessageUser, CreatedAt: now}
	readStarted := make(chan struct{})
	service := chatAgentServiceStub{
		createThread: func(context.Context, string) (chatagent.Thread, error) { return thread, nil },
		enqueue: func(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
			return agentFrontendV1QueuedResult(thread.ID, input), nil
		},
		readRunEvents: func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
			select {
			case <-readStarted:
			default:
				close(readStarted)
			}
			return chatagent.RunEventBatch{Events: []chatagent.RunEvent{
				{Sequence: 1, Type: "queued", Payload: []byte(`{"messageSequence":1,"runKind":"reply"}`), CreatedAt: now},
			}, LastSequence: 1, Terminal: false}, nil
		},
	}
	handler := newAgentFrontendV1ChatTestHandler(t, service, nil)
	ctx, cancel := context.WithCancel(context.Background())
	request := chatAgentRequest(http.MethodPost, "/api/v1/chat/reply/stream", `{"messages":[{"role":"user","content":"First"}],"summary":""}`).WithContext(ctx)
	response := newTestResponseRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("event read did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after cancellation")
	}
	if strings.Contains(response.Body.String(), `"type":"done"`) {
		t.Fatalf("canceled stream completed: %s", response.Body.String())
	}
}

func newAgentFrontendV1ChatTestHandler(
	t *testing.T,
	service ChatAgentService,
	analytics StudentAnalyticsService,
) http.Handler {
	t.Helper()
	options := testHandlerOptions(healthReadyReport())
	options.ChatAgent = service
	if analytics == nil {
		analytics = agentFrontendV1AnalyticsStub{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
			return studentanalytics.Result{
				State: studentanalytics.StateReady, HeadRevision: 7, Ready: &studentanalytics.ReadyResult{},
			}, nil
		}}
	}
	options.StudentAnalytics = analytics
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func completedAgentFrontendV1ChatService(
	now time.Time,
	threadID string,
	input chatagent.Message,
	output chatagent.Message,
) chatAgentServiceStub {
	return chatAgentServiceStub{
		enqueue: func(_ context.Context, _ string, requestedThreadID string, request chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
			if request.ExpectedAnalyticsHeadRevision == nil || *request.ExpectedAnalyticsHeadRevision != 7 {
				return chatagent.EnqueueResult{}, errors.New("reply analytics head revision was not bound")
			}
			input.Content = request.Content
			return agentFrontendV1QueuedResult(requestedThreadID, input), nil
		},
		readRunEvents: func(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
			return chatagent.RunEventBatch{Events: []chatagent.RunEvent{
				{Sequence: 1, Type: "queued", Payload: []byte(`{"messageSequence":1,"runKind":"reply"}`), CreatedAt: now},
				{Sequence: 2, Type: "completed", Payload: []byte(`{"messageId":"` + output.ID + `","messageSequence":2}`), CreatedAt: now},
			}, LastSequence: 2, Terminal: true}, nil
		},
		getRun: func(context.Context, string, string) (chatagent.Run, bool, error) {
			outputID := output.ID
			return chatagent.Run{
				ID: testAgentRunID, ThreadID: threadID, InputMessageID: input.ID,
				Status: chatagent.RunSucceeded, OutputMessageID: &outputID,
			}, true, nil
		},
		listMessages: func(context.Context, string, string, int64, int) ([]chatagent.Message, error) {
			return []chatagent.Message{output}, nil
		},
	}
}

func agentFrontendV1QueuedResult(threadID string, input chatagent.Message) chatagent.EnqueueResult {
	return chatagent.EnqueueResult{
		Run: chatagent.Run{
			ID: testAgentRunID, ThreadID: threadID, ClientRequestID: testChatRequestID,
			Kind: chatagent.RunReply, InputMessageID: input.ID, Status: chatagent.RunQueued,
		},
		Message: input,
		Created: true,
	}
}

func agentFrontendV1OutputMessage(threadID, content string, summary *string) chatagent.Message {
	runID := testAgentRunID
	return chatagent.Message{
		ID: testAgentFrontendV1OutputMessageID, ThreadID: threadID, Sequence: 2,
		Kind: chatagent.MessageAssistant, Content: content, ContextSummary: summary,
		RunID: &runID, CreatedAt: testChatTime(),
	}
}

func assertOrderedSubstrings(t *testing.T, value string, substrings ...string) {
	t.Helper()
	position := 0
	for _, substring := range substrings {
		index := strings.Index(value[position:], substring)
		if index < 0 {
			t.Fatalf("missing ordered substring %q in %s", substring, value)
		}
		position += index + len(substring)
	}
}
