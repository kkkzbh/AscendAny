package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type workerRepositoryStub struct {
	work        Work
	loadErr     error
	recorded    []ToolCallRecord
	noteUpdates []*NotesUpdate
	completion  *Completion
	failure     *struct{ code, detail string }
}

func (*workerRepositoryStub) Claim(context.Context, string, string, time.Duration) (*Claim, error) {
	return nil, nil
}
func (*workerRepositoryStub) RenewLease(context.Context, Claim, time.Duration) error { return nil }
func (repository *workerRepositoryStub) LoadWork(context.Context, Claim, int) (Work, error) {
	return repository.work, repository.loadErr
}
func (repository *workerRepositoryStub) RecordToolCall(_ context.Context, _ Claim, record ToolCallRecord, update *NotesUpdate) (ToolCallRecord, error) {
	record.Sequence = int64(len(repository.work.ToolCalls) + len(repository.recorded) + 1)
	repository.recorded = append(repository.recorded, record)
	if update == nil {
		repository.noteUpdates = append(repository.noteUpdates, nil)
	} else {
		owned := *update
		owned.Patch = cloneOptionalString(update.Patch)
		repository.noteUpdates = append(repository.noteUpdates, &owned)
	}
	return record, nil
}
func (repository *workerRepositoryStub) Complete(_ context.Context, _ Claim, completion Completion) error {
	repository.completion = &completion
	return nil
}
func (repository *workerRepositoryStub) Fail(_ context.Context, _ Claim, code, detail string) error {
	repository.failure = &struct{ code, detail string }{code, detail}
	return nil
}

func TestWorkerPersistsCanonicalToolResultBeforeAssistant(t *testing.T) {
	t.Parallel()
	resultSchema := "ascendany.agent.tool-result.v1"
	provider, err := NewDeterministicProvider([]DeterministicProviderStep{
		{Response: ProviderResponse{ToolCalls: []ProviderToolCall{{
			Key: "lookup:1", Name: "analytics.lookup", ArgumentsSchema: "ascendany.agent.tool-arguments.v1",
			Arguments: json.RawMessage(`{"b":2,"a":1}`),
		}}}},
		{Response: ProviderResponse{Assistant: &AssistantOutput{Content: "Your score trend is stable."}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := NewDeterministicToolExecutor(map[string]ToolExecution{
		"analytics.lookup": {Outcome: ToolSucceeded, ResultSchema: &resultSchema, Result: json.RawMessage(`{"value":3}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &workerRepositoryStub{work: validWork()}
	clockValues := []time.Time{
		time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 11, 1, 0, 1, 0, time.UTC),
	}
	worker, err := newWorker(repository, provider, tools, WorkerConfig{
		Owner: "unit-worker", LeaseDuration: time.Minute, MaximumContextItems: 20, MaximumToolRounds: 2,
	}, func() (string, error) { return testMessageID, nil }, func() time.Time {
		value := clockValues[0]
		clockValues = clockValues[1:]
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(context.Background(), validClaim())
	if err != nil || outcome.Disposition != WorkerSucceeded || repository.completion == nil || repository.failure != nil {
		t.Fatalf("outcome=%#v repository=%#v error=%v", outcome, repository, err)
	}
	if len(repository.recorded) != 1 || string(repository.recorded[0].Arguments) != `{"a":1,"b":2}` || repository.recorded[0].Sequence != 1 {
		t.Fatalf("recorded=%#v", repository.recorded)
	}
	requests := provider.Requests()
	if len(requests) != 2 || len(requests[0].ToolCalls) != 0 || len(requests[1].ToolCalls) != 1 || requests[1].ToolCalls[0].Sequence != 1 {
		t.Fatalf("provider requests=%#v", requests)
	}
}

type capturingToolExecutor struct {
	delegate ToolExecutor
	requests []ToolRequest
}

func (executor *capturingToolExecutor) Execute(ctx context.Context, request ToolRequest) (ToolExecution, error) {
	executor.requests = append(executor.requests, cloneToolRequest(request))
	return executor.delegate.Execute(ctx, request)
}

func TestWorkerReplaysMultipleDurableNotesMutationsAcrossRestart(t *testing.T) {
	t.Parallel()
	runtimeTools, err := NewRuntimeToolExecutor(&runtimeToolReaderStub{})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewDeterministicProvider([]DeterministicProviderStep{
		{Response: ProviderResponse{ToolCalls: []ProviderToolCall{{
			Key: "notes:1", Name: ToolUpdateNotes, ArgumentsSchema: UpdateNotesArgumentsSchema,
			Arguments: json.RawMessage(`{"mode":"replace","content":"A\nB"}`),
		}}}},
		{Response: ProviderResponse{ToolCalls: []ProviderToolCall{{
			Key: "notes:2", Name: ToolUpdateNotes, ArgumentsSchema: UpdateNotesArgumentsSchema,
			Arguments: json.RawMessage(`{"mode":"patch","patch":"--- notes.md\n+++ notes.md\n@@ -1,2 +1,3 @@\n A\n B\n+C"}`),
		}}}},
		{Response: ProviderResponse{Assistant: &AssistantOutput{Content: "updated"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	work := validWork()
	work.FrontendNotes = &FrontendNotesState{Content: "A", Title: "Notebook"}
	repository := &workerRepositoryStub{work: work}
	clockValues := []time.Time{
		time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC), time.Date(2026, 7, 18, 1, 0, 1, 0, time.UTC),
		time.Date(2026, 7, 18, 1, 0, 2, 0, time.UTC), time.Date(2026, 7, 18, 1, 0, 3, 0, time.UTC),
	}
	worker, err := newWorker(repository, provider, runtimeTools, WorkerConfig{
		Owner: "notes-worker", LeaseDuration: time.Minute, MaximumContextItems: 20, MaximumToolRounds: 3,
	}, func() (string, error) { return testMessageID, nil }, func() time.Time {
		value := clockValues[0]
		clockValues = clockValues[1:]
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := worker.Process(context.Background(), validClaim()); err != nil || outcome.Disposition != WorkerSucceeded {
		t.Fatalf("outcome=%#v error=%v repository=%#v", outcome, err, repository)
	}
	if len(repository.noteUpdates) != 2 || repository.noteUpdates[0] == nil || repository.noteUpdates[1] == nil ||
		repository.noteUpdates[0].Previous != "A" || repository.noteUpdates[0].Next != "A\nB" ||
		repository.noteUpdates[1].Previous != "A\nB" || repository.noteUpdates[1].Next != "A\nB\nC" {
		t.Fatalf("notes updates=%#v", repository.noteUpdates)
	}

	restartedWork := work
	restartedWork.ToolCalls = append([]ToolCallRecord(nil), repository.recorded...)
	restartedProvider, _ := NewDeterministicProvider([]DeterministicProviderStep{
		{Response: ProviderResponse{ToolCalls: []ProviderToolCall{{
			Key: "notes:3", Name: ToolUpdateNotes, ArgumentsSchema: UpdateNotesArgumentsSchema,
			Arguments: json.RawMessage(`{"mode":"patch","patch":"--- notes.md\n+++ notes.md\n@@ -1,3 +1,4 @@\n A\n B\n C\n+D"}`),
		}}}},
		{Response: ProviderResponse{Assistant: &AssistantOutput{Content: "resumed"}}},
	})
	capturingTools := &capturingToolExecutor{delegate: runtimeTools}
	restartedRepository := &workerRepositoryStub{work: restartedWork}
	restartClock := []time.Time{
		time.Date(2026, 7, 18, 1, 1, 0, 0, time.UTC), time.Date(2026, 7, 18, 1, 1, 1, 0, time.UTC),
	}
	restartedWorker, err := newWorker(restartedRepository, restartedProvider, capturingTools, WorkerConfig{
		Owner: "notes-worker-restarted", LeaseDuration: time.Minute, MaximumContextItems: 20, MaximumToolRounds: 2,
	}, func() (string, error) { return testMessageID, nil }, func() time.Time {
		value := restartClock[0]
		restartClock = restartClock[1:]
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := restartedWorker.Process(context.Background(), validClaim()); err != nil || outcome.Disposition != WorkerSucceeded {
		t.Fatalf("restart outcome=%#v error=%v repository=%#v", outcome, err, restartedRepository)
	}
	if len(capturingTools.requests) != 1 || capturingTools.requests[0].FrontendNotes == nil ||
		capturingTools.requests[0].FrontendNotes.Content != "A\nB\nC" || len(restartedRepository.noteUpdates) != 1 ||
		restartedRepository.noteUpdates[0] == nil || restartedRepository.noteUpdates[0].Next != "A\nB\nC\nD" {
		t.Fatalf("captured=%#v updates=%#v", capturingTools.requests, restartedRepository.noteUpdates)
	}
}

func TestWorkerTurnsProviderFailureIntoDurableTerminalFailure(t *testing.T) {
	t.Parallel()
	provider, err := NewDeterministicProvider([]DeterministicProviderStep{{
		Error: &ProviderFailure{Code: "model_rejected", Detail: "model rejected the request", Cause: errors.New("request rejected")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := NewDeterministicToolExecutor(map[string]ToolExecution{
		"unused.tool": {Outcome: ToolDenied, ErrorCode: stringPointer("unused")},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &workerRepositoryStub{work: validWork()}
	worker, err := NewWorker(repository, provider, tools, WorkerConfig{
		Owner: "unit-worker", LeaseDuration: time.Minute, MaximumContextItems: 20, MaximumToolRounds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(context.Background(), validClaim())
	if err != nil || outcome.Disposition != WorkerFailed || repository.failure == nil || repository.failure.code != "model_rejected" || repository.completion != nil {
		t.Fatalf("outcome=%#v repository=%#v error=%v", outcome, repository, err)
	}
}

func TestWorkerTerminatesPoisonedStoredWork(t *testing.T) {
	t.Parallel()
	provider, err := NewDeterministicProvider([]DeterministicProviderStep{{
		Response: ProviderResponse{Assistant: &AssistantOutput{Content: "unused"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := NewDeterministicToolExecutor(map[string]ToolExecution{
		"unused.tool": {Outcome: ToolDenied, ErrorCode: stringPointer("unused")},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &workerRepositoryStub{loadErr: domainError(
		ErrorStoredDataInvalid,
		true,
		"load agent work",
		errors.New("bad immutable snapshot"),
	)}
	worker, err := NewWorker(repository, provider, tools, WorkerConfig{
		Owner: "unit-worker", LeaseDuration: time.Minute, MaximumContextItems: 20, MaximumToolRounds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(context.Background(), validClaim())
	if err != nil || outcome.Disposition != WorkerFailed || repository.failure == nil || repository.failure.code != "stored_data_invalid" || len(provider.Requests()) != 0 {
		t.Fatalf("outcome=%#v repository=%#v requests=%#v error=%v", outcome, repository, provider.Requests(), err)
	}
}

func TestWorkerProcessesGeneralReplyWithoutBoundAnalytics(t *testing.T) {
	t.Parallel()
	provider, err := NewDeterministicProvider([]DeterministicProviderStep{{
		Response: ProviderResponse{Assistant: &AssistantOutput{Content: "unused"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := NewDeterministicToolExecutor(map[string]ToolExecution{
		"unused.tool": {Outcome: ToolDenied, ErrorCode: stringPointer("unused")},
	})
	if err != nil {
		t.Fatal(err)
	}
	work := validWork()
	work.Analytics = nil
	repository := &workerRepositoryStub{work: work}
	worker, err := NewWorker(repository, provider, tools, WorkerConfig{
		Owner: "unit-worker", LeaseDuration: time.Minute, MaximumContextItems: 20, MaximumToolRounds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(context.Background(), validClaim())
	if err != nil || outcome.Disposition != WorkerSucceeded || repository.failure != nil ||
		repository.completion == nil || len(provider.Requests()) != 1 {
		t.Fatalf("outcome=%#v repository=%#v requests=%#v error=%v", outcome, repository, provider.Requests(), err)
	}
}

func validClaim() Claim {
	return Claim{
		DatabaseID: 1, ID: testRunID, AttemptCount: 1, AttemptToken: testRequestID,
		LeaseOwner: "unit-worker", LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}
}

func validWork() Work {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	return Work{
		RunID: testRunID, Kind: RunReply, ThreadID: testThreadID, StudentNumber: "20260001", InputMessageID: testMessageID,
		Analytics:    &AnalyticsSnapshot{GenerationDatabaseID: 41, HeadRevision: 9},
		Prompt:       ConfigurationSnapshot{VersionDatabaseID: 1, Key: "agent.prompt.default", SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{"system":"help"}`)},
		Model:        ConfigurationSnapshot{VersionDatabaseID: 2, Key: "agent.model.default", SchemaID: "ascendany.model_connection.v1", Document: json.RawMessage(`{"model":"fake"}`)},
		Conversation: []Message{{ID: testMessageID, ThreadID: testThreadID, Sequence: 1, Kind: MessageUser, Content: "Help", CreatedAt: now}},
	}
}

func stringPointer(value string) *string { return &value }
