package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
)

type runtimeToolReaderStub struct {
	analyticsResult AnalyticsToolResult
	analyticsError  error
	analyticsQuery  BoundAnalyticsQuery
	notes           []NoteToolSummary
	notesError      error
	notesQuery      BoundToolQuery
	notesLimit      int
	note            NoteToolDetail
	noteFound       bool
	noteError       error
	noteQuery       BoundToolQuery
	noteID          string
}

func (reader *runtimeToolReaderStub) LoadBoundAnalytics(_ context.Context, query BoundAnalyticsQuery) (AnalyticsToolResult, error) {
	reader.analyticsQuery = query
	return reader.analyticsResult, reader.analyticsError
}

func (reader *runtimeToolReaderStub) ListBoundActiveNotes(_ context.Context, query BoundToolQuery, limit int) ([]NoteToolSummary, error) {
	reader.notesQuery = query
	reader.notesLimit = limit
	return reader.notes, reader.notesError
}

func (reader *runtimeToolReaderStub) LoadBoundActiveNote(_ context.Context, query BoundToolQuery, noteID string) (NoteToolDetail, bool, error) {
	reader.noteQuery = query
	reader.noteID = noteID
	return reader.note, reader.noteFound, reader.noteError
}

func TestRuntimeToolExecutorBindsAnalyticsToRunStudentAndImmutableGeneration(t *testing.T) {
	t.Parallel()
	rating := int64(1510)
	reader := &runtimeToolReaderStub{analyticsResult: AnalyticsToolResult{
		State: "ready", HeadRevision: 7, Rating: &rating,
		Metrics: &AnalyticsToolMetrics{
			ReferenceTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Current: analytics.MetricValues{},
			History: []AnalyticsToolHistoryPoint{{
				EventTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Values: analytics.MetricValues{},
				Rank: 1, OldRating: 1500, Delta: 10, NewRating: 1510, Seed: 1, Performance: 1510,
			}},
		},
	}}
	executor, err := NewRuntimeToolExecutor(reader)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executor.Execute(context.Background(), ToolRequest{
		RunID: testRunID, StudentNumber: "20260001", Analytics: &AnalyticsSnapshot{GenerationDatabaseID: 88, HeadRevision: 7},
		Key: "call:analytics", Name: ToolAnalyticsGetSelf, ArgumentsSchema: AnalyticsGetSelfArgumentsSchema,
		Arguments: json.RawMessage(`{"historyLimit":12}`),
	})
	if err != nil || execution.Outcome != ToolSucceeded || execution.ResultSchema == nil || *execution.ResultSchema != AnalyticsGetSelfResultSchema {
		t.Fatalf("execution=%#v error=%v", execution, err)
	}
	want := BoundAnalyticsQuery{BoundToolQuery: BoundToolQuery{RunID: testRunID, StudentNumber: "20260001"}, GenerationDatabaseID: 88, HeadRevision: 7, HistoryLimit: 12}
	if reader.analyticsQuery != want || string(execution.Result) != `{"headRevision":7,"metrics":{"current":{"accuracy":null,"flexibility":null,"knowledge":null,"proficiency":null,"quality":null},"history":[{"delta":10,"eventTime":"2026-07-01T00:00:00Z","newRating":1510,"oldRating":1500,"performance":1510,"rank":1,"seed":1,"values":{"accuracy":null,"flexibility":null,"knowledge":null,"proficiency":null,"quality":null}}],"referenceTime":"2026-07-01T00:00:00Z"},"rating":1510,"state":"ready"}` {
		t.Fatalf("query=%#v result=%s", reader.analyticsQuery, execution.Result)
	}
}

func TestRuntimeToolCatalogOwnsExactBoundedJSONSchemas(t *testing.T) {
	t.Parallel()
	if len(runtimeToolDefinitions) != 3 {
		t.Fatalf("definitions=%d", len(runtimeToolDefinitions))
	}
	for name, definition := range runtimeToolDefinitions {
		if !identifierPattern.MatchString(name) || !schemaIDPattern.MatchString(definition.ArgumentsSchema) || !schemaIDPattern.MatchString(definition.ResultSchema) || strings.TrimSpace(definition.Description) == "" {
			t.Fatalf("name=%q definition=%#v", name, definition)
		}
		var schema struct {
			Type                 string                     `json:"type"`
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(definition.Parameters)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&schema); err != nil || schema.Type != "object" || schema.AdditionalProperties || len(schema.Properties) != 1 || len(schema.Required) != 1 {
			t.Fatalf("name=%q schema=%s decoded=%#v error=%v", name, definition.Parameters, schema, err)
		}
	}
}

func TestRuntimeToolExecutorDeniesAnalyticsWithoutEnqueueSnapshot(t *testing.T) {
	t.Parallel()
	reader := &runtimeToolReaderStub{}
	executor, _ := NewRuntimeToolExecutor(reader)
	execution, err := executor.Execute(context.Background(), ToolRequest{
		RunID: testRunID, StudentNumber: "20260001", Key: "call:analytics", Name: ToolAnalyticsGetSelf,
		ArgumentsSchema: AnalyticsGetSelfArgumentsSchema, Arguments: json.RawMessage(`{"historyLimit":10}`),
	})
	if err != nil || execution.Outcome != ToolDenied || execution.ErrorCode == nil || *execution.ErrorCode != "analytics_snapshot_unavailable" || reader.analyticsQuery.RunID != "" {
		t.Fatalf("execution=%#v reader=%#v error=%v", execution, reader, err)
	}
}

func TestRuntimeToolExecutorListsAndReadsOnlyRunBoundActiveNotes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	noteID := "88888888-8888-4888-8888-888888888888"
	summary := NoteToolSummary{ID: noteID, HeadRevision: 2, Title: "Goal", ContentSHA256: digestToolContent("Practice graphs"), UpdatedAt: now}
	reader := &runtimeToolReaderStub{notes: []NoteToolSummary{summary}, note: NoteToolDetail{NoteToolSummary: summary, Content: "Practice graphs"}, noteFound: true}
	executor, _ := NewRuntimeToolExecutor(reader)
	list, err := executor.Execute(context.Background(), ToolRequest{
		RunID: testRunID, StudentNumber: "20260001", Key: "notes:list", Name: ToolAgentNotesListActive,
		ArgumentsSchema: AgentNotesListActiveArgumentsSchema, Arguments: json.RawMessage(`{"limit":5}`),
	})
	if err != nil || list.Outcome != ToolSucceeded || reader.notesQuery != (BoundToolQuery{RunID: testRunID, StudentNumber: "20260001"}) || reader.notesLimit != 5 {
		t.Fatalf("list=%#v reader=%#v error=%v", list, reader, err)
	}
	detail, err := executor.Execute(context.Background(), ToolRequest{
		RunID: testRunID, StudentNumber: "20260001", Key: "notes:get", Name: ToolAgentNotesGetActive,
		ArgumentsSchema: AgentNotesGetActiveArgumentsSchema, Arguments: json.RawMessage(`{"noteId":"` + noteID + `"}`),
	})
	if err != nil || detail.Outcome != ToolSucceeded || reader.noteQuery != (BoundToolQuery{RunID: testRunID, StudentNumber: "20260001"}) || reader.noteID != noteID || !json.Valid(detail.Result) {
		t.Fatalf("detail=%#v reader=%#v error=%v", detail, reader, err)
	}
}

func TestRuntimeToolExecutorReturnsExplicitToolOutcomesForModelMistakes(t *testing.T) {
	t.Parallel()
	reader := &runtimeToolReaderStub{}
	executor, _ := NewRuntimeToolExecutor(reader)
	fixtures := []struct {
		request ToolRequest
		outcome ToolOutcome
		code    string
	}{
		{request: ToolRequest{RunID: testRunID, StudentNumber: "20260001", Key: "call:1", Name: "shell.execute", ArgumentsSchema: "ascendany.agent_tool.shell.v1", Arguments: json.RawMessage(`{}`)}, outcome: ToolDenied, code: "tool_not_allowed"},
		{request: ToolRequest{RunID: testRunID, StudentNumber: "20260001", Key: "call:1", Name: ToolAgentNotesListActive, ArgumentsSchema: AgentNotesListActiveArgumentsSchema, Arguments: json.RawMessage(`{"limit":0}`)}, outcome: ToolFailed, code: "tool_arguments_invalid"},
		{request: ToolRequest{RunID: testRunID, StudentNumber: "20260001", Key: "call:1", Name: ToolAgentNotesGetActive, ArgumentsSchema: AgentNotesGetActiveArgumentsSchema, Arguments: json.RawMessage(`{"noteId":"invalid"}`)}, outcome: ToolFailed, code: "tool_arguments_invalid"},
	}
	for _, fixture := range fixtures {
		execution, err := executor.Execute(context.Background(), fixture.request)
		if err != nil || execution.Outcome != fixture.outcome || execution.ErrorCode == nil || *execution.ErrorCode != fixture.code {
			t.Fatalf("request=%#v execution=%#v error=%v", fixture.request, execution, err)
		}
	}
}

func TestRuntimeToolExecutorPropagatesRepositoryFailureWithoutForgingToolResult(t *testing.T) {
	t.Parallel()
	repositoryFailure := errors.New("database unavailable")
	reader := &runtimeToolReaderStub{notesError: repositoryFailure}
	executor, _ := NewRuntimeToolExecutor(reader)
	_, err := executor.Execute(context.Background(), ToolRequest{
		RunID: testRunID, StudentNumber: "20260001", Key: "call:1", Name: ToolAgentNotesListActive,
		ArgumentsSchema: AgentNotesListActiveArgumentsSchema, Arguments: json.RawMessage(`{"limit":5}`),
	})
	if !errors.Is(err, repositoryFailure) {
		t.Fatalf("error=%v", err)
	}
}

func TestRuntimeToolExecutorReportsOversizedEncodedNoteAsToolFailure(t *testing.T) {
	t.Parallel()
	noteID := "88888888-8888-4888-8888-888888888888"
	content := strings.Repeat("\n", 131072)
	reader := &runtimeToolReaderStub{noteFound: true, note: NoteToolDetail{
		NoteToolSummary: NoteToolSummary{ID: noteID, HeadRevision: 1, Title: "Large note", ContentSHA256: digestToolContent(content), UpdatedAt: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)},
		Content:         content,
	}}
	executor, _ := NewRuntimeToolExecutor(reader)
	execution, err := executor.Execute(context.Background(), ToolRequest{
		RunID: testRunID, StudentNumber: "20260001", Key: "call:1", Name: ToolAgentNotesGetActive,
		ArgumentsSchema: AgentNotesGetActiveArgumentsSchema, Arguments: json.RawMessage(`{"noteId":"` + noteID + `"}`),
	})
	if err != nil || execution.Outcome != ToolFailed || execution.ErrorCode == nil || *execution.ErrorCode != "tool_result_too_large" {
		t.Fatalf("execution=%#v error=%v", execution, err)
	}
}
