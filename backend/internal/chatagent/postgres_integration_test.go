package chatagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func TestPostgresAgentRunIsIdempotentFencedAndAtomicallyPublished(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var dispatchable int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.agent_runs
WHERE (status = 'queued' AND next_attempt_at <= clock_timestamp())
   OR (status = 'running' AND lease_expires_at <= clock_timestamp())`).Scan(&dispatchable); err != nil {
		t.Fatal(err)
	}
	if dispatchable != 0 {
		t.Skipf("database owns %d pre-existing dispatchable agent runs", dispatchable)
	}

	principal, adminAccountID, adminSessionID, suffix := seedChatAgentPrincipals(t, ctx, pool)
	promptKey, promptItemID := seedAgentConfiguration(t, ctx, pool, adminAccountID, adminSessionID, suffix, "prompt", nil,
		json.RawMessage(`{"instruction":"version-one"}`))
	modelKey, _ := seedAgentConfiguration(t, ctx, pool, adminAccountID, adminSessionID, suffix, "model_connection", stringPointer("agent.integration.credential"),
		json.RawMessage(`{"model":"deterministic"}`))
	analyticsGenerationID, analyticsHeadRevision := seedPublishedAnalytics(t, ctx, pool, suffix)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := service.CreateThread(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	input := EnqueueInput{
		Principal: principal, ThreadID: thread.ID, ClientRequestID: mustIntegrationUUID(t), Kind: RunReply,
		Content: "Explain my latest progress.", PromptConfigurationKey: promptKey, ModelConfigurationKey: modelKey,
	}

	type enqueueAttempt struct {
		result EnqueueResult
		err    error
	}
	attempts := make(chan enqueueAttempt, 2)
	for range 2 {
		go func() {
			result, err := service.Enqueue(ctx, input)
			attempts <- enqueueAttempt{result: result, err: err}
		}()
	}
	first := <-attempts
	second := <-attempts
	if first.err != nil || second.err != nil || first.result.Run.ID != second.result.Run.ID || first.result.Message.ID != second.result.Message.ID ||
		first.result.Created == second.result.Created {
		t.Fatalf("first=%#v err=%v second=%#v err=%v", first.result, first.err, second.result, second.err)
	}
	enqueued := first.result
	if !enqueued.Created {
		enqueued = second.result
	}
	notesRepository, err := agentnotes.NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	notesService, err := agentnotes.NewService(notesRepository)
	if err != nil {
		t.Fatal(err)
	}
	createdNote, err := notesService.Create(ctx, agentnotes.CreateCommand{
		Principal: principal, MutationID: mustIntegrationUUID(t), ExpectedHeadRevision: 0,
		Title: "Integration goal", Content: "Practice graph traversal.",
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeTools, err := NewRuntimeToolExecutor(repository)
	if err != nil {
		t.Fatal(err)
	}
	listedNotes, err := runtimeTools.Execute(ctx, ToolRequest{
		RunID: enqueued.Run.ID, StudentNumber: "chat-" + suffix, Key: "integration:notes:list",
		Name: ToolAgentNotesListActive, ArgumentsSchema: AgentNotesListActiveArgumentsSchema,
		Arguments: json.RawMessage(`{"limit":10}`),
	})
	if err != nil || listedNotes.Outcome != ToolSucceeded || !strings.Contains(string(listedNotes.Result), createdNote.Note.ID) {
		t.Fatalf("listed notes=%#v error=%v", listedNotes, err)
	}
	foreignNotes, err := runtimeTools.Execute(ctx, ToolRequest{
		RunID: enqueued.Run.ID, StudentNumber: "foreign-student", Key: "integration:notes:foreign",
		Name: ToolAgentNotesListActive, ArgumentsSchema: AgentNotesListActiveArgumentsSchema,
		Arguments: json.RawMessage(`{"limit":10}`),
	})
	if err != nil || foreignNotes.Outcome != ToolSucceeded || string(foreignNotes.Result) != `{"items":[]}` {
		t.Fatalf("foreign notes=%#v error=%v", foreignNotes, err)
	}
	changed := input
	changed.Content = "A different immutable request."
	if _, err := service.Enqueue(ctx, changed); CodeOf(err) != ErrorIdempotencyConflict {
		t.Fatalf("changed replay error=%v code=%q", err, CodeOf(err))
	}

	mismatchedRevision := analyticsHeadRevision + 1
	autoInput := AutoAnalysisInput{
		Principal: principal, PromptConfigurationKey: promptKey, ModelConfigurationKey: modelKey,
		ExpectedAnalyticsHeadRevision: mismatchedRevision,
	}
	if _, err := service.EnqueueAutoAnalysis(ctx, autoInput); CodeOf(err) != ErrorAnalyticsConflict {
		t.Fatalf("auto-analysis mismatch error=%v code=%q", err, CodeOf(err))
	}

	activateSecondAgentConfiguration(t, ctx, pool, promptItemID, adminAccountID, adminSessionID,
		"prompt", json.RawMessage(`{"instruction":"version-two"}`))

	staleClaim, err := repository.Claim(ctx, "chat-agent-stale", mustIntegrationUUID(t), time.Minute)
	if err != nil || staleClaim == nil || staleClaim.ID != enqueued.Run.ID || staleClaim.AttemptCount != 1 {
		t.Fatalf("stale claim=%#v error=%v", staleClaim, err)
	}
	if err := repository.RenewLease(ctx, *staleClaim, time.Minute); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.agent_runs
SET lease_expires_at = clock_timestamp() - interval '1 second',
    updated_at = clock_timestamp()
WHERE agent_run_id = $1`, staleClaim.DatabaseID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expire stale claim rows=%d error=%v", tag.RowsAffected(), err)
	}
	activeClaim, err := repository.Claim(ctx, "chat-agent-active", mustIntegrationUUID(t), time.Minute)
	if err != nil || activeClaim == nil || activeClaim.ID != staleClaim.ID || !activeClaim.Reclaimed || activeClaim.AttemptCount != 2 {
		t.Fatalf("active claim=%#v error=%v", activeClaim, err)
	}
	if err := repository.Complete(ctx, *staleClaim, Completion{
		MessageID: mustIntegrationUUID(t), Output: AssistantOutput{Content: "stale output"},
	}); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("stale completion error=%v code=%q", err, CodeOf(err))
	}

	provider, err := NewDeterministicProvider([]DeterministicProviderStep{
		{Response: ProviderResponse{ToolCalls: []ProviderToolCall{{
			Key: "progress:1", Name: "progress.lookup", ArgumentsSchema: "ascendany.agent.tool-arguments.v1",
			Arguments: json.RawMessage(`{"student":"current"}`),
		}}}},
		{Response: ProviderResponse{Assistant: &AssistantOutput{Content: "Your latest progress is durable."}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resultSchema := "ascendany.agent.tool-result.v1"
	tools, err := NewDeterministicToolExecutor(map[string]ToolExecution{
		"progress.lookup":   {Outcome: ToolSucceeded, ResultSchema: &resultSchema, Result: json.RawMessage(`{"rating":1500}`)},
		"restricted.lookup": {Outcome: ToolDenied, ErrorCode: stringPointer("policy_denied")},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repository, provider, tools, WorkerConfig{
		Owner: "chat-agent-active", LeaseDuration: time.Minute, MaximumContextItems: 100, MaximumToolRounds: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(ctx, *activeClaim)
	if err != nil || outcome.Disposition != WorkerSucceeded || outcome.MessageID == nil {
		t.Fatalf("outcome=%#v error=%v", outcome, err)
	}
	requests := provider.Requests()
	if len(requests) != 2 || string(requests[0].Prompt.Document) != `{"instruction":"version-one"}` || len(requests[0].Conversation) != 1 ||
		len(requests[1].ToolCalls) != 1 || requests[1].ToolCalls[0].Sequence != 1 {
		t.Fatalf("provider requests=%#v", requests)
	}

	storedRun, found, err := service.GetRun(ctx, RunQuery{Principal: principal, RunID: enqueued.Run.ID})
	if err != nil || !found || storedRun.Status != RunSucceeded || storedRun.OutputMessageID == nil || *storedRun.OutputMessageID != *outcome.MessageID {
		t.Fatalf("stored run=%#v found=%t error=%v", storedRun, found, err)
	}
	messages, err := service.ListMessages(ctx, MessageQuery{Principal: principal, ThreadID: thread.ID, Limit: 10})
	if err != nil || len(messages) != 2 || messages[0].Kind != MessageUser || messages[1].Kind != MessageAssistant || messages[1].RunID == nil || *messages[1].RunID != storedRun.ID {
		t.Fatalf("messages=%#v error=%v", messages, err)
	}
	eventBatch, err := service.ReadRunEvents(ctx, EventQuery{Principal: principal, RunID: enqueued.Run.ID, Limit: 10})
	events := eventBatch.Events
	if err != nil || len(events) != 5 || !eventBatch.Terminal || eventBatch.LastSequence != 5 {
		t.Fatalf("event batch=%#v error=%v", eventBatch, err)
	}
	wantEvents := []string{"queued", "claimed", "reclaimed", "tool.succeeded", "completed"}
	for index, event := range events {
		if event.Sequence != int64(index+1) || event.Type != wantEvents[index] {
			t.Fatalf("events=%#v", events)
		}
	}
	autoHeadRevision := analyticsHeadRevision
	autoInput.ExpectedAnalyticsHeadRevision = autoHeadRevision
	secondDeviceInput := autoInput
	secondDeviceInput.Principal = seedAdditionalStudentSession(t, ctx, pool, principal)
	autoAttempts := make(chan enqueueAttempt, 2)
	for _, concurrentInput := range []AutoAnalysisInput{autoInput, secondDeviceInput} {
		concurrentInput := concurrentInput
		go func() {
			result, err := service.EnqueueAutoAnalysis(ctx, concurrentInput)
			autoAttempts <- enqueueAttempt{result: result, err: err}
		}()
	}
	firstAuto := <-autoAttempts
	secondAuto := <-autoAttempts
	if firstAuto.err != nil || secondAuto.err != nil || firstAuto.result.Run.ID != secondAuto.result.Run.ID ||
		firstAuto.result.Message.ID != secondAuto.result.Message.ID || firstAuto.result.Run.ThreadID != secondAuto.result.Run.ThreadID ||
		firstAuto.result.Created == secondAuto.result.Created {
		t.Fatalf("first auto=%#v err=%v second auto=%#v err=%v", firstAuto.result, firstAuto.err, secondAuto.result, secondAuto.err)
	}
	autoResult := firstAuto.result
	if !autoResult.Created {
		autoResult = secondAuto.result
	}
	var autoThreadCount, autoRunCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.chat_threads AS thread
JOIN ascendany.auth_accounts AS account
  ON account.account_id = thread.owner_account_id
WHERE account.public_id = $1::uuid
  AND thread.thread_kind = 'auto_analysis'`, principal.AccountID).Scan(&autoThreadCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.agent_runs AS run
JOIN ascendany.auth_accounts AS account
  ON account.account_id = run.owner_account_id
WHERE account.public_id = $1::uuid
  AND run.analytics_generation_id = $2
  AND run.run_kind = 'auto_analysis'`, principal.AccountID, analyticsGenerationID).Scan(&autoRunCount); err != nil {
		t.Fatal(err)
	}
	if autoThreadCount != 1 || autoRunCount != 1 || autoResult.Message.Content != AutoAnalysisInputContent {
		t.Fatalf("auto thread count=%d run count=%d result=%#v", autoThreadCount, autoRunCount, autoResult)
	}
	_, err = pool.Exec(ctx, `
INSERT INTO ascendany.chat_messages (
    public_id, chat_thread_id, owner_account_id, message_sequence,
    message_kind, content, author_session_id
)
SELECT $1::uuid, thread.chat_thread_id, account.account_id, thread.head_revision + 1,
       'auto_analysis_request', $5, session.session_id
FROM ascendany.chat_threads AS thread
JOIN ascendany.auth_accounts AS account
  ON account.account_id = thread.owner_account_id
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
WHERE thread.public_id = $2::uuid
  AND account.public_id = $3::uuid
  AND session.public_id = $4::uuid`, mustIntegrationUUID(t), thread.ID, principal.AccountID, principal.SessionID, AutoAnalysisInputContent)
	assertChatPostgresCode(t, err, "23514")
	_, err = pool.Exec(ctx, `
INSERT INTO ascendany.chat_messages (
    public_id, chat_thread_id, owner_account_id, message_sequence,
    message_kind, content, author_session_id
)
SELECT $1::uuid, thread.chat_thread_id, account.account_id, thread.head_revision + 1,
       'auto_analysis_request', 'client-owned automatic analysis content', session.session_id
FROM ascendany.chat_threads AS thread
JOIN ascendany.auth_accounts AS account
  ON account.account_id = thread.owner_account_id
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
WHERE thread.public_id = $2::uuid
  AND account.public_id = $3::uuid
  AND session.public_id = $4::uuid`, mustIntegrationUUID(t), autoResult.Run.ThreadID, principal.AccountID, principal.SessionID)
	assertChatPostgresCode(t, err, "23514")
	boundAnalytics, err := runtimeTools.Execute(ctx, ToolRequest{
		RunID: autoResult.Run.ID, StudentNumber: "chat-" + suffix,
		Analytics: &AnalyticsSnapshot{GenerationDatabaseID: analyticsGenerationID, HeadRevision: analyticsHeadRevision},
		Key:       "integration:analytics:self", Name: ToolAnalyticsGetSelf, ArgumentsSchema: AnalyticsGetSelfArgumentsSchema,
		Arguments: json.RawMessage(`{"historyLimit":10}`),
	})
	if err != nil || boundAnalytics.Outcome != ToolSucceeded || !strings.Contains(string(boundAnalytics.Result), fmt.Sprintf(`"headRevision":%d`, analyticsHeadRevision)) ||
		!strings.Contains(string(boundAnalytics.Result), `"rating":1500`) || !strings.Contains(string(boundAnalytics.Result), `"state":"ready"`) ||
		strings.Contains(string(boundAnalytics.Result), `"examId"`) || strings.Contains(string(boundAnalytics.Result), `"snapshotId"`) {
		t.Fatalf("bound analytics=%#v error=%v", boundAnalytics, err)
	}
	if _, err := runtimeTools.Execute(ctx, ToolRequest{
		RunID: autoResult.Run.ID, StudentNumber: "foreign-student",
		Analytics: &AnalyticsSnapshot{GenerationDatabaseID: analyticsGenerationID, HeadRevision: analyticsHeadRevision},
		Key:       "integration:analytics:foreign", Name: ToolAnalyticsGetSelf, ArgumentsSchema: AnalyticsGetSelfArgumentsSchema,
		Arguments: json.RawMessage(`{"historyLimit":10}`),
	}); CodeOf(err) != ErrorPrincipalRejected {
		t.Fatalf("foreign analytics error=%v code=%q", err, CodeOf(err))
	}
	changedAutoRevision := autoHeadRevision + 1
	changedAuto := autoInput
	changedAuto.ExpectedAnalyticsHeadRevision = changedAutoRevision
	if _, err := service.EnqueueAutoAnalysis(ctx, changedAuto); CodeOf(err) != ErrorAnalyticsConflict {
		t.Fatalf("changed auto-analysis replay error=%v code=%q", err, CodeOf(err))
	}
	changedAuto = autoInput
	changedAuto.PromptConfigurationKey = "agent.prompt.different"
	if _, err := service.EnqueueAutoAnalysis(ctx, changedAuto); CodeOf(err) != ErrorAutoAnalysisConflict {
		t.Fatalf("changed auto-analysis configuration error=%v code=%q", err, CodeOf(err))
	}
	autoClaim, err := repository.Claim(ctx, "chat-agent-auto", mustIntegrationUUID(t), time.Minute)
	if err != nil || autoClaim == nil || autoClaim.ID != autoResult.Run.ID {
		t.Fatalf("auto-analysis claim=%#v error=%v", autoClaim, err)
	}
	autoProvider, err := NewDeterministicProvider([]DeterministicProviderStep{
		{Response: ProviderResponse{ToolCalls: []ProviderToolCall{{
			Key: "restricted:1", Name: "restricted.lookup", ArgumentsSchema: "ascendany.agent.tool-arguments.v1",
			Arguments: json.RawMessage(`{"scope":"private"}`),
		}}}},
		{Response: ProviderResponse{Assistant: &AssistantOutput{Content: "The published analytics snapshot is attached."}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	autoWorker, err := NewWorker(repository, autoProvider, tools, WorkerConfig{
		Owner: "chat-agent-auto", LeaseDuration: time.Minute, MaximumContextItems: 100, MaximumToolRounds: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if autoOutcome, err := autoWorker.Process(ctx, *autoClaim); err != nil || autoOutcome.Disposition != WorkerSucceeded {
		t.Fatalf("auto-analysis outcome=%#v error=%v", autoOutcome, err)
	}
	autoRequests := autoProvider.Requests()
	if len(autoRequests) != 2 || autoRequests[0].Kind != RunAutoAnalysis || autoRequests[0].Analytics == nil ||
		autoRequests[0].Analytics.GenerationDatabaseID != analyticsGenerationID ||
		autoRequests[0].Analytics.HeadRevision != analyticsHeadRevision || len(autoRequests[1].ToolCalls) != 1 ||
		autoRequests[1].ToolCalls[0].Outcome != ToolDenied {
		t.Fatalf("auto-analysis provider requests=%#v", autoRequests)
	}
	autoMessages, err := service.ListMessages(ctx, MessageQuery{Principal: principal, ThreadID: autoResult.Run.ThreadID, Limit: 10})
	if err != nil || len(autoMessages) != 2 || autoMessages[0].Kind != MessageAutoAnalysisRequest || autoMessages[1].Kind != MessageAssistant {
		t.Fatalf("messages after auto-analysis=%#v error=%v", autoMessages, err)
	}
	failedInput := input
	failedInput.ClientRequestID = mustIntegrationUUID(t)
	failedInput.Content = "Fail this durable run explicitly."
	failedResult, err := service.Enqueue(ctx, failedInput)
	if err != nil || !failedResult.Created {
		t.Fatalf("failed run enqueue=%#v error=%v", failedResult, err)
	}
	blockedInput := failedInput
	blockedInput.ClientRequestID = mustIntegrationUUID(t)
	blockedInput.Content = "This run must wait for the earlier thread run."
	blockedResult, err := service.Enqueue(ctx, blockedInput)
	if err != nil || !blockedResult.Created {
		t.Fatalf("blocked run enqueue=%#v error=%v", blockedResult, err)
	}
	failedClaim, err := repository.Claim(ctx, "chat-agent-failure", mustIntegrationUUID(t), time.Minute)
	if err != nil || failedClaim == nil || failedClaim.ID != failedResult.Run.ID {
		t.Fatalf("failed run claim=%#v error=%v", failedClaim, err)
	}
	blockedClaim, err := repository.Claim(ctx, "chat-agent-blocked", mustIntegrationUUID(t), time.Minute)
	if err != nil || blockedClaim != nil {
		t.Fatalf("same-thread later run claimed early=%#v error=%v", blockedClaim, err)
	}
	if err := repository.Fail(ctx, *failedClaim, "model_rejected", "provider rejected the request"); err != nil {
		t.Fatal(err)
	}
	if err := repository.RenewLease(ctx, *failedClaim, time.Minute); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("terminal renewal error=%v code=%q", err, CodeOf(err))
	}
	failedRun, found, err := service.GetRun(ctx, RunQuery{Principal: principal, RunID: failedResult.Run.ID})
	if err != nil || !found || failedRun.Status != RunFailed || failedRun.ErrorCode == nil || *failedRun.ErrorCode != "model_rejected" {
		t.Fatalf("failed run=%#v found=%t error=%v", failedRun, found, err)
	}
	blockedClaim, err = repository.Claim(ctx, "chat-agent-blocked", mustIntegrationUUID(t), time.Minute)
	if err != nil || blockedClaim == nil || blockedClaim.ID != blockedResult.Run.ID {
		t.Fatalf("same-thread later run claim=%#v error=%v", blockedClaim, err)
	}
	if err := repository.Fail(ctx, *blockedClaim, "test_finished", "integration run finished"); err != nil {
		t.Fatal(err)
	}
	messages, err = service.ListMessages(ctx, MessageQuery{Principal: principal, ThreadID: thread.ID, Limit: 10})
	if err != nil || len(messages) != 4 || messages[2].Kind != MessageUser || messages[3].Kind != MessageUser {
		t.Fatalf("messages after failed run=%#v error=%v", messages, err)
	}
	contextFirst := input
	contextFirst.ClientRequestID = mustIntegrationUUID(t)
	contextFirst.Content = "First interleaved context input."
	contextFirstResult, err := service.Enqueue(ctx, contextFirst)
	if err != nil || !contextFirstResult.Created {
		t.Fatalf("first context enqueue=%#v error=%v", contextFirstResult, err)
	}
	contextSecond := input
	contextSecond.ClientRequestID = mustIntegrationUUID(t)
	contextSecond.Content = "Second interleaved context input."
	contextSecondResult, err := service.Enqueue(ctx, contextSecond)
	if err != nil || !contextSecondResult.Created {
		t.Fatalf("second context enqueue=%#v error=%v", contextSecondResult, err)
	}
	contextFirstClaim, err := repository.Claim(ctx, "chat-agent-context-first", mustIntegrationUUID(t), time.Minute)
	if err != nil || contextFirstClaim == nil || contextFirstClaim.ID != contextFirstResult.Run.ID {
		t.Fatalf("first context claim=%#v error=%v", contextFirstClaim, err)
	}
	contextAssistantID := mustIntegrationUUID(t)
	if err := repository.Complete(ctx, *contextFirstClaim, Completion{
		MessageID: contextAssistantID,
		Output:    AssistantOutput{Content: "Assistant published after the second input."},
	}); err != nil {
		t.Fatal(err)
	}
	contextSecondClaim, err := repository.Claim(ctx, "chat-agent-context-second", mustIntegrationUUID(t), time.Minute)
	if err != nil || contextSecondClaim == nil || contextSecondClaim.ID != contextSecondResult.Run.ID {
		t.Fatalf("second context claim=%#v error=%v", contextSecondClaim, err)
	}
	minimalContext, err := repository.LoadWork(ctx, *contextSecondClaim, 1)
	if err != nil || len(minimalContext.Conversation) != 1 || minimalContext.Conversation[0].ID != contextSecondResult.Message.ID {
		t.Fatalf("minimal context=%#v error=%v", minimalContext.Conversation, err)
	}
	contextWork, err := repository.LoadWork(ctx, *contextSecondClaim, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundInterleavedAssistant := false
	for _, message := range contextWork.Conversation {
		if message.ID == contextAssistantID {
			foundInterleavedAssistant = true
		}
	}
	if !foundInterleavedAssistant {
		t.Fatalf("interleaved assistant missing from context=%#v", contextWork.Conversation)
	}
	if err := repository.Fail(ctx, *contextSecondClaim, "test_finished", "context integration run finished"); err != nil {
		t.Fatal(err)
	}
	messages, err = service.ListMessages(ctx, MessageQuery{Principal: principal, ThreadID: thread.ID, Limit: 20})
	if err != nil || len(messages) != 7 || messages[6].ID != contextAssistantID {
		t.Fatalf("messages after context interleave=%#v error=%v", messages, err)
	}
	threadPage, err := service.ListThreads(ctx, ThreadQuery{Principal: principal, Limit: 10})
	if err != nil || len(threadPage.Items) != 2 || threadPage.Items[0].ID != thread.ID ||
		threadPage.Items[0].Kind != ThreadConversation || threadPage.Items[0].HeadRevision != 7 ||
		threadPage.Items[1].ID != autoResult.Run.ThreadID || threadPage.Items[1].Kind != ThreadAutoAnalysis ||
		threadPage.Items[1].HeadRevision != 2 || threadPage.NextCursor != nil {
		t.Fatalf("thread page=%#v error=%v", threadPage, err)
	}
	secondThread, err := service.CreateThread(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := service.ListThreads(ctx, ThreadQuery{Principal: principal, Limit: 1})
	if err != nil || len(firstPage.Items) != 1 || firstPage.Items[0].ID != secondThread.ID || firstPage.NextCursor == nil {
		t.Fatalf("first paged threads=%#v error=%v", firstPage, err)
	}
	secondPage, err := service.ListThreads(ctx, ThreadQuery{Principal: principal, Cursor: firstPage.NextCursor, Limit: 1})
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].ID != thread.ID || secondPage.NextCursor == nil {
		t.Fatalf("second paged threads=%#v error=%v", secondPage, err)
	}
	thirdPage, err := service.ListThreads(ctx, ThreadQuery{Principal: principal, Cursor: secondPage.NextCursor, Limit: 1})
	if err != nil || len(thirdPage.Items) != 1 || thirdPage.Items[0].ID != autoResult.Run.ThreadID || thirdPage.NextCursor != nil {
		t.Fatalf("third paged threads=%#v error=%v", thirdPage, err)
	}
}

func seedPublishedAnalytics(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (int64, int64) {
	t.Helper()
	artifactDigest := sha256Hex("chat-agent-artifact-" + suffix)
	var artifactID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/json', 'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, artifactDigest).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	var importJobID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (public_id, artifact_id, job_kind, status, stage)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, mustIntegrationUUID(t), artifactID).Scan(&importJobID); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'running', stage = 'validating', attempt_count = 1,
    lease_owner = 'chat-agent-integration', lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE import_job_id = $1`, importJobID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("claim import fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'importing', updated_at = clock_timestamp()
WHERE import_job_id = $1`, importJobID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("advance import fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	var examID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', $2)
RETURNING exam_id`, mustIntegrationUUID(t), "chat-agent-exam-"+suffix).Scan(&examID); err != nil {
		t.Fatal(err)
	}
	domainDigest := sha256Hex("chat-agent-domain-" + suffix)
	var snapshotID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.exam_snapshots (
    public_id, exam_id, snapshot_sequence, source_artifact_id, import_job_id,
    contract_schema, contract_schema_sha256, domain_hash_protocol, domain_hash,
    exporter_name, exporter_version, exported_at, title, source_url,
    problems_source_count, problems_observed_count, problems_exported_count, problems_pagination_exhausted,
    rankings_source_count, rankings_observed_count, rankings_exported_count, rankings_pagination_exhausted,
    submissions_source_count, submissions_observed_count, submissions_exported_count, submissions_pagination_exhausted,
    participants_exported_count
)
VALUES (
    $1::uuid, $2, 1, $3, $4,
    'ascendany.pintia.snapshot.v2', $5, 'domain_hash_proto_v1', $6,
    'ascendany-pintia-exporter', 'integration', clock_timestamp(), 'Chat agent analytics fixture',
    'https://pintia.cn/problem-sets/chat-agent-integration',
    1, 1, 1, true,
    0, 0, 0, true,
    0, 0, 0, true,
    0
)
RETURNING snapshot_id`, mustIntegrationUUID(t), examID, artifactID, importJobID,
		sha256Hex("ascendany.pintia.snapshot.v2"), domainDigest).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE exam_id = $1`, examID, snapshotID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("publish exam fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'analyzing', lease_owner = NULL, lease_expires_at = NULL, updated_at = clock_timestamp()
WHERE import_job_id = $1`, importJobID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("handoff import fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'succeeded', stage = 'completed', snapshot_id = $2,
    finished_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE import_job_id = $1`, importJobID, snapshotID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("complete import fixture rows=%d error=%v", tag.RowsAffected(), err)
	}

	var baseGenerationID *int64
	var baseRevision int64
	if err := pool.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton`).Scan(&baseGenerationID, &baseRevision); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256Hex("chat-agent-manifest-" + suffix)
	configDigest := sha256Hex("chat-agent-config-" + suffix)
	var generationID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.analytics_generations (
    status, base_analytics_generation_id, base_head_revision,
    target_exam_id, target_snapshot_id, target_exam_head_revision,
    input_manifest, input_manifest_sha256, algorithm_version, config_sha256
)
VALUES ('queued', $1, $2, $3, $4, 1, '{}'::jsonb, $5, 'chat-agent-integration', $6)
	RETURNING analytics_generation_id`, baseGenerationID, baseRevision, examID, snapshotID, manifestDigest, configDigest).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id, exam_id, snapshot_id, domain_hash
)
VALUES ($1, $2, $3, $4)`, generationID, examID, snapshotID, domainDigest); err != nil {
		t.Fatal(err)
	}
	var studentActorID int64
	if err := pool.QueryRow(ctx, `
SELECT actor_id
FROM ascendany.auth_accounts
WHERE student_number = $1
  AND role = 'student'`, "chat-"+suffix).Scan(&studentActorID); err != nil {
		t.Fatal(err)
	}
	referenceTime := time.Now().UTC().Truncate(time.Microsecond)
	metric := 80.0
	values := analytics.MetricValues{Knowledge: &metric, Accuracy: &metric, Quality: &metric, Flexibility: &metric, Proficiency: &metric}
	metrics, err := json.Marshal(analytics.StudentMetrics{
		Protocol: analytics.StudentMetricsProtocolV1, ReferenceTime: referenceTime, Current: values,
		ExamHistory: []analytics.ExamMetricPoint{{ExamID: examID, SnapshotID: snapshotID, EventTime: referenceTime, Values: values}},
		RatingHistory: []analytics.RatingHistoryPoint{{
			ExamID: examID, SnapshotID: snapshotID, EventTime: referenceTime, Rank: 1,
			OldRating: 800, Delta: 700, NewRating: 1500, Seed: 1, Performance: 1500,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.student_analytics (analytics_generation_id, actor_id, rating, metrics)
VALUES ($1, $2, 1500, $3::jsonb)`, generationID, studentActorID, string(metrics)); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running', attempt_count = 1, lease_owner = 'chat-agent-integration',
    lease_expires_at = clock_timestamp() + interval '1 hour', started_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("claim analytics fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL, finished_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("complete analytics fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	newRevision := baseRevision + 1
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $1, head_revision = $2, updated_at = clock_timestamp()
WHERE singleton AND head_revision = $3`, generationID, newRevision, baseRevision); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("publish analytics fixture rows=%d error=%v", tag.RowsAffected(), err)
	}
	return generationID, newRevision
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func seedChatAgentPrincipals(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (auth.AccessPrincipal, int64, int64, string) {
	t.Helper()
	suffix := strings.ReplaceAll(mustIntegrationUUID(t), "-", "")[:10]
	now := time.Now().UTC().Truncate(time.Microsecond)
	studentNumber := "chat-" + suffix
	var actorID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "chat-user-"+suffix).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', $1, $2)`, studentNumber, actorID); err != nil {
		t.Fatal(err)
	}
	studentAccountPublicID := mustIntegrationUUID(t)
	var studentAccountID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, student_number, actor_id,
    role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, 'integration-unused', $3, $4, $5, 'student', 1, $6, $6)
RETURNING account_id`, studentAccountPublicID, "chat_"+suffix, "Chat "+suffix, studentNumber, actorID, now).Scan(&studentAccountID); err != nil {
		t.Fatal(err)
	}
	studentSessionPublicID := mustIntegrationUUID(t)
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (public_id, account_id, auth_revision, created_at, expires_at, last_seen_at)
VALUES ($1::uuid, $2, 1, $3, $4, $3)`, studentSessionPublicID, studentAccountID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	adminPublicID := mustIntegrationUUID(t)
	var adminAccountID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, 'integration-unused', $3, 'admin', 1, $4, $4)
RETURNING account_id`, adminPublicID, "chata_"+suffix, "Chat Admin "+suffix, now).Scan(&adminAccountID); err != nil {
		t.Fatal(err)
	}
	adminSessionPublicID := mustIntegrationUUID(t)
	var adminSessionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_sessions (public_id, account_id, auth_revision, created_at, expires_at, last_seen_at)
VALUES ($1::uuid, $2, 1, $3, $4, $3)
RETURNING session_id`, adminSessionPublicID, adminAccountID, now, now.Add(time.Hour)).Scan(&adminSessionID); err != nil {
		t.Fatal(err)
	}
	return auth.AccessPrincipal{
		AccountID: studentAccountPublicID, SessionID: studentSessionPublicID, JWTID: mustIntegrationUUID(t),
		Role: auth.RoleStudent, AuthRevision: 1,
	}, adminAccountID, adminSessionID, suffix
}

func seedAdditionalStudentSession(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	principal auth.AccessPrincipal,
) auth.AccessPrincipal {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	publicID := mustIntegrationUUID(t)
	if tag, err := pool.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (public_id, account_id, auth_revision, created_at, expires_at, last_seen_at)
SELECT $1::uuid, account_id, auth_revision, $3, $4, $3
FROM ascendany.auth_accounts
WHERE public_id = $2::uuid`, publicID, principal.AccountID, now, now.Add(time.Hour)); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("additional student session rows=%d error=%v", tag.RowsAffected(), err)
	}
	principal.SessionID = publicID
	principal.JWTID = mustIntegrationUUID(t)
	return principal
}

func seedAgentConfiguration(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	adminAccountID, adminSessionID int64,
	suffix, kind string,
	credentialRef *string,
	document json.RawMessage,
) (string, int64) {
	t.Helper()
	key := "agent." + kind + "." + suffix
	var itemID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, $3)
RETURNING configuration_item_id`, mustIntegrationUUID(t), key, kind).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	canonical, digest, err := canonicaljson.Object(document, MaxConfigurationBytes)
	if err != nil {
		t.Fatal(err)
	}
	var versionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, credential_ref, created_by_account_id, created_by_session_id
)
VALUES ($1, $2, 1, $3, $4::jsonb, $5, $6, $7, $8)
RETURNING configuration_version_id`, itemID, kind, "ascendany."+kind+".integration.v1", string(canonical), digest,
		credentialRef, adminAccountID, adminSessionID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE configuration_item_id = $1 AND head_revision = 0`, itemID, versionID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("activate configuration rows=%d error=%v", tag.RowsAffected(), err)
	}
	return key, itemID
}

func activateSecondAgentConfiguration(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	itemID, adminAccountID, adminSessionID int64,
	kind string,
	document json.RawMessage,
) {
	t.Helper()
	canonical, digest, err := canonicaljson.Object(document, MaxConfigurationBytes)
	if err != nil {
		t.Fatal(err)
	}
	var versionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, created_by_account_id, created_by_session_id
)
VALUES ($1, $2, 2, $3, $4::jsonb, $5, $6, $7)
RETURNING configuration_version_id`, itemID, kind, "ascendany."+kind+".integration.v1", string(canonical), digest,
		adminAccountID, adminSessionID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 2, updated_at = clock_timestamp()
WHERE configuration_item_id = $1 AND head_revision = 1`, itemID, versionID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("activate second configuration rows=%d error=%v", tag.RowsAffected(), err)
	}
}

func mustIntegrationUUID(t *testing.T) string {
	t.Helper()
	value, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertChatPostgresCode(t *testing.T, err error, want string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != want {
		t.Fatalf("PostgreSQL error = %v, want SQLSTATE %s", err, want)
	}
}
