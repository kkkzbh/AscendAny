package chatagent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func TestPostgresFrontendNotesMutationsRemainReplayableAcrossLeaseReclaim(t *testing.T) {
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
	promptKey, _ := seedAgentConfiguration(t, ctx, pool, adminAccountID, adminSessionID, suffix, "prompt", nil,
		json.RawMessage(`{"instruction":"mutate the bound frontend notes"}`))
	modelKey, _ := seedAgentConfiguration(t, ctx, pool, adminAccountID, adminSessionID, suffix, "model_connection", stringPointer("agent.integration.credential"),
		json.RawMessage(`{"model":"deterministic"}`))
	_, analyticsHeadRevision, _ := seedPublishedAnalytics(t, ctx, pool, suffix)
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

	var frontendDocument agentFrontendV1RunContextDocument
	frontendDocument.Schema = AgentFrontendV1ContextSchema
	frontendDocument.CurrentUser.Content = "Update these notes."
	frontendDocument.Messages = []agentFrontendV1ContextMessage{{Content: "Update these notes.", Role: "user"}}
	frontendDocument.Notes.Content = "A"
	frontendDocument.Notes.Title = "Integration notebook"
	rawFrontendDocument, err := json.Marshal(frontendDocument)
	if err != nil {
		t.Fatal(err)
	}
	canonicalFrontendDocument, _, err := canonicaljson.Object(rawFrontendDocument, MaxFrontendContextDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := service.Enqueue(ctx, EnqueueInput{
		Principal: principal, ThreadID: thread.ID, ClientRequestID: mustIntegrationUUID(t), Kind: RunReply,
		Content: string(canonicalFrontendDocument), PromptConfigurationKey: promptKey, ModelConfigurationKey: modelKey,
		ExpectedAnalyticsHeadRevision: chatAgentTestInt64Pointer(analyticsHeadRevision),
	})
	if err != nil || !enqueued.Created {
		t.Fatalf("enqueue=%#v error=%v", enqueued, err)
	}
	claim, err := repository.Claim(ctx, "frontend-notes-first", mustIntegrationUUID(t), time.Minute)
	if err != nil || claim == nil || claim.ID != enqueued.Run.ID {
		t.Fatalf("claim=%#v error=%v", claim, err)
	}
	work, err := repository.LoadWork(ctx, *claim, 100)
	if err != nil || work.FrontendNotes == nil || work.FrontendNotes.Content != "A" ||
		work.FrontendNotes.Title != "Integration notebook" || len(work.ToolCalls) != 0 {
		t.Fatalf("work=%#v error=%v", work, err)
	}
	runtimeTools, err := NewRuntimeToolExecutor(repository)
	if err != nil {
		t.Fatal(err)
	}
	recordMutation := func(activeClaim Claim, state *FrontendNotesState, key string, arguments json.RawMessage) ToolCallRecord {
		t.Helper()
		normalized, err := normalizeProviderToolCall(ProviderToolCall{
			Key: key, Name: ToolUpdateNotes, ArgumentsSchema: UpdateNotesArgumentsSchema, Arguments: arguments,
		})
		if err != nil {
			t.Fatal(err)
		}
		execution, err := runtimeTools.Execute(ctx, ToolRequest{
			RunID: activeClaim.ID, StudentNumber: "chat-" + suffix, FrontendNotes: cloneFrontendNotesState(state),
			Key: normalized.Key, Name: normalized.Name, ArgumentsSchema: normalized.ArgumentsSchema, Arguments: normalized.Arguments,
		})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		record, err := normalizeToolExecution(normalized, execution, now, now)
		if err != nil {
			t.Fatal(err)
		}
		_, update, err := notesUpdateForNewRecord(state, record)
		if err != nil || update == nil {
			t.Fatalf("update=%#v error=%v", update, err)
		}
		stored, err := repository.RecordToolCall(ctx, activeClaim, record, update)
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}

	firstRecord := recordMutation(*claim, work.FrontendNotes, "notes:integration:1", json.RawMessage(`{"content":"A\nB","mode":"replace"}`))
	if firstRecord.Sequence != 1 {
		t.Fatalf("first record=%#v", firstRecord)
	}
	if tag, err := pool.Exec(ctx, `
UPDATE ascendany.agent_runs
SET lease_expires_at = clock_timestamp() - interval '1 second', updated_at = clock_timestamp()
WHERE agent_run_id = $1`, claim.DatabaseID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expire first notes lease rows=%d error=%v", tag.RowsAffected(), err)
	}
	reclaimed, err := repository.Claim(ctx, "frontend-notes-reclaimed", mustIntegrationUUID(t), time.Minute)
	if err != nil || reclaimed == nil || reclaimed.ID != claim.ID || !reclaimed.Reclaimed || reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaimed=%#v error=%v", reclaimed, err)
	}
	restartedWork, err := repository.LoadWork(ctx, *reclaimed, 100)
	if err != nil || restartedWork.FrontendNotes == nil || restartedWork.FrontendNotes.Content != "A" || len(restartedWork.ToolCalls) != 1 {
		t.Fatalf("restarted work=%#v error=%v", restartedWork, err)
	}
	replayedNotes, err := replayFrontendNotes(restartedWork.FrontendNotes, restartedWork.ToolCalls)
	if err != nil || replayedNotes == nil || replayedNotes.Content != "A\nB" {
		t.Fatalf("replayed notes=%#v error=%v", replayedNotes, err)
	}
	secondRecord := recordMutation(*reclaimed, replayedNotes, "notes:integration:2", json.RawMessage(
		`{"mode":"patch","patch":"--- notes.md\n+++ notes.md\n@@ -1,2 +1,3 @@\n A\n B\n+C"}`,
	))
	if secondRecord.Sequence != 2 {
		t.Fatalf("second record=%#v", secondRecord)
	}
	if err := repository.Complete(ctx, *reclaimed, Completion{
		MessageID: mustIntegrationUUID(t), Output: AssistantOutput{Content: "Frontend notes updated."},
	}); err != nil {
		t.Fatal(err)
	}

	batch, err := service.ReadRunEvents(ctx, EventQuery{Principal: principal, RunID: enqueued.Run.ID, Limit: 20})
	wantTypes := []string{"queued", "claimed", "notes_update", "tool.succeeded", "reclaimed", "notes_update", "tool.succeeded", "completed"}
	if err != nil || !batch.Terminal || batch.LastSequence != int64(len(wantTypes)) || len(batch.Events) != len(wantTypes) {
		t.Fatalf("events=%#v error=%v", batch, err)
	}
	for index, wantType := range wantTypes {
		if batch.Events[index].Sequence != int64(index+1) || batch.Events[index].Type != wantType {
			t.Fatalf("events=%#v", batch.Events)
		}
	}
	if string(batch.Events[2].Payload) != `{"mode":"replace","next":"A\nB","patch":null,"previous":"A","toolCallKey":"notes:integration:1","toolName":"update_notes","toolSequence":1}` ||
		string(batch.Events[5].Payload) != `{"mode":"patch","next":"A\nB\nC","patch":"--- notes.md\n+++ notes.md\n@@ -1,2 +1,3 @@\n A\n B\n+C","previous":"A\nB","toolCallKey":"notes:integration:2","toolName":"update_notes","toolSequence":2}` {
		t.Fatalf("notes events=%s / %s", batch.Events[2].Payload, batch.Events[5].Payload)
	}
}
