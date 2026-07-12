package chatagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"golang.org/x/text/unicode/norm"
)

const (
	ToolAnalyticsGetSelf     = "analytics.get_self"
	ToolAgentNotesListActive = "agent_notes.list_active"
	ToolAgentNotesGetActive  = "agent_notes.get_active"

	AnalyticsGetSelfArgumentsSchema     = "ascendany.agent_tool.analytics_get_self_arguments.v1"
	AnalyticsGetSelfResultSchema        = "ascendany.agent_tool.analytics_get_self_result.v1"
	AgentNotesListActiveArgumentsSchema = "ascendany.agent_tool.agent_notes_list_active_arguments.v1"
	AgentNotesListActiveResultSchema    = "ascendany.agent_tool.agent_notes_list_active_result.v1"
	AgentNotesGetActiveArgumentsSchema  = "ascendany.agent_tool.agent_notes_get_active_arguments.v1"
	AgentNotesGetActiveResultSchema     = "ascendany.agent_tool.agent_notes_get_active_result.v1"

	maximumAnalyticsToolHistory = 50
	maximumNotesToolPage        = 20
)

type runtimeToolDefinition struct {
	ArgumentsSchema string
	ResultSchema    string
	Description     string
	Parameters      json.RawMessage
}

var runtimeToolDefinitions = map[string]runtimeToolDefinition{
	ToolAnalyticsGetSelf: {
		ArgumentsSchema: AnalyticsGetSelfArgumentsSchema,
		ResultSchema:    AnalyticsGetSelfResultSchema,
		Description:     "Read the authenticated student's immutable analytics snapshot bound to this agent run.",
		Parameters:      json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"historyLimit":{"type":"integer","minimum":1,"maximum":50}},"required":["historyLimit"]}`),
	},
	ToolAgentNotesListActive: {
		ArgumentsSchema: AgentNotesListActiveArgumentsSchema,
		ResultSchema:    AgentNotesListActiveResultSchema,
		Description:     "List bounded summaries of the authenticated student's active durable notes.",
		Parameters:      json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["limit"]}`),
	},
	ToolAgentNotesGetActive: {
		ArgumentsSchema: AgentNotesGetActiveArgumentsSchema,
		ResultSchema:    AgentNotesGetActiveResultSchema,
		Description:     "Read one active durable note owned by the authenticated student.",
		Parameters:      json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"noteId":{"type":"string","pattern":"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"}},"required":["noteId"]}`),
	},
}

type BoundToolQuery struct {
	RunID         string
	StudentNumber string
}

type BoundAnalyticsQuery struct {
	BoundToolQuery
	GenerationDatabaseID int64
	HeadRevision         int64
	HistoryLimit         int
}

type AnalyticsToolResult struct {
	State        string                `json:"state"`
	HeadRevision int64                 `json:"headRevision"`
	Rating       *int64                `json:"rating"`
	Metrics      *AnalyticsToolMetrics `json:"metrics"`
}

type AnalyticsToolMetrics struct {
	ReferenceTime time.Time                   `json:"referenceTime"`
	Current       analytics.MetricValues      `json:"current"`
	History       []AnalyticsToolHistoryPoint `json:"history"`
}

type AnalyticsToolHistoryPoint struct {
	EventTime   time.Time              `json:"eventTime"`
	Values      analytics.MetricValues `json:"values"`
	Rank        int64                  `json:"rank"`
	OldRating   int64                  `json:"oldRating"`
	Delta       int64                  `json:"delta"`
	NewRating   int64                  `json:"newRating"`
	Seed        float64                `json:"seed"`
	Performance float64                `json:"performance"`
}

type NoteToolSummary struct {
	ID            string    `json:"id"`
	HeadRevision  int64     `json:"headRevision"`
	Title         string    `json:"title"`
	ContentSHA256 string    `json:"contentSha256"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type NoteToolDetail struct {
	NoteToolSummary
	Content string `json:"content"`
}

type RuntimeToolReader interface {
	LoadBoundAnalytics(context.Context, BoundAnalyticsQuery) (AnalyticsToolResult, error)
	ListBoundActiveNotes(context.Context, BoundToolQuery, int) ([]NoteToolSummary, error)
	LoadBoundActiveNote(context.Context, BoundToolQuery, string) (NoteToolDetail, bool, error)
}

type RuntimeToolExecutor struct {
	reader RuntimeToolReader
}

func NewRuntimeToolExecutor(reader RuntimeToolReader) (*RuntimeToolExecutor, error) {
	if reader == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct agent runtime tool executor", errors.New("runtime tool reader is required"))
	}
	return &RuntimeToolExecutor{reader: reader}, nil
}

func (executor *RuntimeToolExecutor) Execute(ctx context.Context, request ToolRequest) (ToolExecution, error) {
	if ctx == nil || !canonicalUUIDv4.MatchString(request.RunID) || strings.TrimSpace(request.StudentNumber) != request.StudentNumber || request.StudentNumber == "" ||
		len(request.StudentNumber) > auth.MaxStudentNumberBytes || !utf8.ValidString(request.StudentNumber) || !toolCallKeyPattern.MatchString(request.Key) {
		return ToolExecution{}, domainError(ErrorInvalidInput, true, "execute agent runtime tool", errors.New("tool ownership identity is invalid"))
	}
	definition, exists := runtimeToolDefinitions[request.Name]
	if !exists || request.ArgumentsSchema != definition.ArgumentsSchema {
		return deniedTool("tool_not_allowed"), nil
	}
	arguments, _, err := canonicaljson.Object(request.Arguments, MaxToolDocumentBytes)
	if err != nil {
		return failedTool("tool_arguments_invalid"), nil
	}
	switch request.Name {
	case ToolAnalyticsGetSelf:
		if request.Analytics == nil || request.Analytics.GenerationDatabaseID < 1 || request.Analytics.HeadRevision < 1 {
			return deniedTool("analytics_snapshot_unavailable"), nil
		}
		var input struct {
			HistoryLimit *int `json:"historyLimit"`
		}
		if !decodeExactToolArguments(arguments, &input) || input.HistoryLimit == nil || *input.HistoryLimit < 1 || *input.HistoryLimit > maximumAnalyticsToolHistory {
			return failedTool("tool_arguments_invalid"), nil
		}
		result, err := executor.reader.LoadBoundAnalytics(ctx, BoundAnalyticsQuery{
			BoundToolQuery:       BoundToolQuery{RunID: request.RunID, StudentNumber: request.StudentNumber},
			GenerationDatabaseID: request.Analytics.GenerationDatabaseID, HeadRevision: request.Analytics.HeadRevision,
			HistoryLimit: *input.HistoryLimit,
		})
		if err != nil {
			return ToolExecution{}, err
		}
		if err := validateAnalyticsToolResult(result, request.Analytics.HeadRevision, *input.HistoryLimit); err != nil {
			return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "validate analytics tool result", err)
		}
		return successfulTool(AnalyticsGetSelfResultSchema, result)
	case ToolAgentNotesListActive:
		var input struct {
			Limit *int `json:"limit"`
		}
		if !decodeExactToolArguments(arguments, &input) || input.Limit == nil || *input.Limit < 1 || *input.Limit > maximumNotesToolPage {
			return failedTool("tool_arguments_invalid"), nil
		}
		items, err := executor.reader.ListBoundActiveNotes(ctx, BoundToolQuery{RunID: request.RunID, StudentNumber: request.StudentNumber}, *input.Limit)
		if err != nil {
			return ToolExecution{}, err
		}
		if items == nil || len(items) > *input.Limit {
			return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "validate active agent note tool page", errors.New("note reader returned an invalid page"))
		}
		seen := make(map[string]struct{}, len(items))
		for index, item := range items {
			if err := validateNoteToolSummary(item); err != nil {
				return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "validate active agent note tool page", err)
			}
			if _, duplicate := seen[item.ID]; duplicate || index > 0 && item.UpdatedAt.After(items[index-1].UpdatedAt) {
				return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "validate active agent note tool page", errors.New("note page is duplicated or unordered"))
			}
			seen[item.ID] = struct{}{}
		}
		return successfulTool(AgentNotesListActiveResultSchema, struct {
			Items []NoteToolSummary `json:"items"`
		}{Items: items})
	case ToolAgentNotesGetActive:
		var input struct {
			NoteID *string `json:"noteId"`
		}
		if !decodeExactToolArguments(arguments, &input) || input.NoteID == nil || !canonicalUUIDv4.MatchString(*input.NoteID) {
			return failedTool("tool_arguments_invalid"), nil
		}
		note, found, err := executor.reader.LoadBoundActiveNote(ctx, BoundToolQuery{RunID: request.RunID, StudentNumber: request.StudentNumber}, *input.NoteID)
		if err != nil {
			return ToolExecution{}, err
		}
		if !found {
			return failedTool("note_not_found"), nil
		}
		if note.ID != *input.NoteID || validateNoteToolDetail(note) != nil {
			return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "validate active agent note tool detail", errors.New("note reader returned an invalid detail"))
		}
		encoded, err := json.Marshal(note)
		if err != nil {
			return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "encode active agent note tool result", errors.New("note result encoding failed"))
		}
		if len(encoded) > MaxToolDocumentBytes {
			return failedTool("tool_result_too_large"), nil
		}
		return successfulTool(AgentNotesGetActiveResultSchema, note)
	default:
		return deniedTool("tool_not_allowed"), nil
	}
}

func decodeExactToolArguments(data []byte, destination any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination) == nil
}

func successfulTool(schema string, value any) (ToolExecution, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "encode agent tool result", errors.New("tool result encoding failed"))
	}
	canonical, _, err := canonicaljson.Object(encoded, MaxToolDocumentBytes)
	if err != nil {
		return ToolExecution{}, domainError(ErrorStoredDataInvalid, true, "canonicalize agent tool result", err)
	}
	return ToolExecution{Outcome: ToolSucceeded, ResultSchema: runtimeStringPointer(schema), Result: canonical}, nil
}

func validateAnalyticsToolResult(result AnalyticsToolResult, expectedHeadRevision int64, historyLimit int) error {
	if result.HeadRevision != expectedHeadRevision {
		return errors.New("analytics result head revision differs from the run snapshot")
	}
	switch result.State {
	case "no_observations":
		if result.Rating != nil || result.Metrics != nil {
			return errors.New("no-observations analytics result contains a ready payload")
		}
		return nil
	case "ready":
		if result.Rating == nil || *result.Rating < 0 || result.Metrics == nil || len(result.Metrics.History) < 1 || len(result.Metrics.History) > historyLimit ||
			result.Metrics.ReferenceTime.IsZero() || !result.Metrics.ReferenceTime.Equal(result.Metrics.ReferenceTime.UTC()) || !validToolMetricValues(result.Metrics.Current) {
			return errors.New("ready analytics result violates its shape")
		}
		var previousTime time.Time
		var previousRating int64
		for index, point := range result.Metrics.History {
			if point.EventTime.IsZero() || !point.EventTime.Equal(point.EventTime.UTC()) || point.EventTime.After(result.Metrics.ReferenceTime) ||
				index > 0 && point.EventTime.Before(previousTime) || !validToolMetricValues(point.Values) || point.Rank < 1 || point.OldRating < 0 ||
				point.NewRating < 0 || point.NewRating-point.OldRating != point.Delta || math.IsNaN(point.Seed) || math.IsInf(point.Seed, 0) ||
				math.IsNaN(point.Performance) || math.IsInf(point.Performance, 0) || index > 0 && point.OldRating != previousRating {
				return errors.New("analytics history violates its shape")
			}
			previousTime = point.EventTime
			previousRating = point.NewRating
		}
		if previousRating != *result.Rating {
			return errors.New("analytics result rating differs from its history")
		}
		return nil
	default:
		return errors.New("analytics result state is invalid")
	}
}

func validToolMetricValues(values analytics.MetricValues) bool {
	for _, value := range []*float64{values.Knowledge, values.Accuracy, values.Quality, values.Flexibility, values.Proficiency} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 100) {
			return false
		}
	}
	return true
}

func validateNoteToolDetail(note NoteToolDetail) error {
	if err := validateNoteToolSummary(note.NoteToolSummary); err != nil {
		return err
	}
	if len(note.Content) > 131072 || !utf8.ValidString(note.Content) || !norm.NFC.IsNormalString(note.Content) ||
		strings.ContainsRune(note.Content, '\x00') || strings.ContainsRune(note.Content, '\r') || digestToolContent(note.Content) != note.ContentSHA256 {
		return errors.New("note content violates its storage contract")
	}
	return nil
}

func failedTool(code string) ToolExecution {
	return ToolExecution{Outcome: ToolFailed, ErrorCode: runtimeStringPointer(code)}
}
func deniedTool(code string) ToolExecution {
	return ToolExecution{Outcome: ToolDenied, ErrorCode: runtimeStringPointer(code)}
}

func (repository *PostgresRepository) LoadBoundAnalytics(ctx context.Context, query BoundAnalyticsQuery) (result AnalyticsToolResult, resultErr error) {
	if err := validateBoundAnalyticsQuery(query); err != nil {
		return AnalyticsToolResult{}, err
	}
	resultErr = repository.transaction(ctx, "load run-bound analytics tool data", readOnlyOptions(), func(tx postgresTx) error {
		var ratingText *string
		var metricsText *string
		err := tx.QueryRow(ctx, `
SELECT student.rating::text,
       student.metrics::text
FROM ascendany.agent_runs AS run
JOIN ascendany.auth_accounts AS account
  ON account.account_id = run.owner_account_id
 AND account.role = 'student'
 AND account.student_number = $2
 AND account.disabled_at IS NULL
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = run.analytics_generation_id
 AND generation.status = 'succeeded'
JOIN ascendany.agent_run_events AS queued
  ON queued.agent_run_id = run.agent_run_id
 AND queued.event_sequence = 1
 AND queued.event_type = 'queued'
LEFT JOIN ascendany.student_analytics AS student
  ON student.analytics_generation_id = generation.analytics_generation_id
 AND student.actor_id = account.actor_id
WHERE run.public_id = $1::uuid
  AND run.analytics_generation_id = $3
  AND NULLIF(queued.payload ->> 'analyticsHeadRevision', '')::bigint = $4`, query.RunID, query.StudentNumber, query.GenerationDatabaseID, query.HeadRevision).Scan(&ratingText, &metricsText)
		if errors.Is(err, pgx.ErrNoRows) {
			return domainError(ErrorPrincipalRejected, true, "authorize run-bound analytics tool data", errors.New("run owner or analytics snapshot no longer matches"))
		}
		if err != nil {
			return databaseFailure("load run-bound analytics tool data", err)
		}
		result = AnalyticsToolResult{State: "no_observations", HeadRevision: query.HeadRevision}
		if ratingText == nil && metricsText == nil {
			return nil
		}
		if ratingText == nil || metricsText == nil {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound analytics tool data", errors.New("rating and metrics nullability differs"))
		}
		rating, err := parseToolRating(*ratingText)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound analytics rating", err)
		}
		metrics, err := analytics.DecodeStoredStudentMetrics([]byte(*metricsText))
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound analytics metrics", err)
		}
		if len(metrics.RatingHistory) == 0 || metrics.RatingHistory[len(metrics.RatingHistory)-1].NewRating != rating || len(metrics.ExamHistory) != len(metrics.RatingHistory) {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound analytics history", errors.New("rating or history alignment is invalid"))
		}
		if err := validateToolAnalyticsMembership(ctx, tx, query.GenerationDatabaseID, metrics); err != nil {
			return err
		}
		start := len(metrics.ExamHistory) - query.HistoryLimit
		if start < 0 {
			start = 0
		}
		history := make([]AnalyticsToolHistoryPoint, 0, len(metrics.ExamHistory)-start)
		for index := start; index < len(metrics.ExamHistory); index++ {
			exam := metrics.ExamHistory[index]
			ratingPoint := metrics.RatingHistory[index]
			history = append(history, AnalyticsToolHistoryPoint{
				EventTime: exam.EventTime, Values: exam.Values, Rank: ratingPoint.Rank,
				OldRating: ratingPoint.OldRating, Delta: ratingPoint.Delta, NewRating: ratingPoint.NewRating,
				Seed: ratingPoint.Seed, Performance: ratingPoint.Performance,
			})
		}
		toolMetrics := AnalyticsToolMetrics{ReferenceTime: metrics.ReferenceTime, Current: metrics.Current, History: history}
		result = AnalyticsToolResult{State: "ready", HeadRevision: query.HeadRevision, Rating: &rating, Metrics: &toolMetrics}
		return nil
	})
	return result, resultErr
}

func validateToolAnalyticsMembership(ctx context.Context, tx postgresTx, generationDatabaseID int64, metrics analytics.StudentMetrics) error {
	rows, err := tx.Query(ctx, `
SELECT exam_id, snapshot_id
FROM ascendany.analytics_generation_snapshots
WHERE analytics_generation_id = $1`, generationDatabaseID)
	if err != nil {
		return databaseFailure("load run-bound analytics generation membership", err)
	}
	defer rows.Close()
	type membershipKey struct{ examID, snapshotID int64 }
	membership := make(map[membershipKey]struct{})
	for rows.Next() {
		var key membershipKey
		if err := rows.Scan(&key.examID, &key.snapshotID); err != nil {
			return databaseFailure("scan run-bound analytics generation membership", err)
		}
		if key.examID < 1 || key.snapshotID < 1 {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound analytics generation membership", errors.New("generation membership identity is invalid"))
		}
		membership[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return databaseFailure("iterate run-bound analytics generation membership", err)
	}
	for index := range metrics.ExamHistory {
		examPoint := metrics.ExamHistory[index]
		ratingPoint := metrics.RatingHistory[index]
		key := membershipKey{examID: examPoint.ExamID, snapshotID: examPoint.SnapshotID}
		if _, exists := membership[key]; !exists || ratingPoint.ExamID != examPoint.ExamID || ratingPoint.SnapshotID != examPoint.SnapshotID {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound analytics generation membership", errors.New("student history references a snapshot outside its generation"))
		}
	}
	return nil
}

func (repository *PostgresRepository) ListBoundActiveNotes(ctx context.Context, query BoundToolQuery, limit int) (result []NoteToolSummary, resultErr error) {
	if err := validateBoundToolQuery(query); err != nil || limit < 1 || limit > maximumNotesToolPage {
		if err == nil {
			err = errors.New("note limit is invalid")
		}
		return nil, domainError(ErrorInvalidInput, true, "validate run-bound agent note query", err)
	}
	resultErr = repository.transaction(ctx, "list run-bound active agent notes", readOnlyOptions(), func(tx postgresTx) error {
		rows, err := tx.Query(ctx, `
SELECT note.public_id::text,
       note.head_revision,
       revision.title,
       revision.content_sha256,
       note.updated_at
FROM ascendany.agent_runs AS run
JOIN ascendany.auth_accounts AS account
  ON account.account_id = run.owner_account_id
 AND account.role = 'student'
 AND account.student_number = $2
 AND account.disabled_at IS NULL
JOIN ascendany.agent_notes AS note
  ON note.owner_account_id = run.owner_account_id
JOIN ascendany.agent_note_revisions AS revision
  ON revision.agent_note_revision_id = note.current_revision_id
 AND revision.agent_note_id = note.agent_note_id
 AND revision.owner_account_id = note.owner_account_id
 AND revision.revision_number = note.head_revision
 AND revision.note_state = 'active'
WHERE run.public_id = $1::uuid
ORDER BY note.updated_at DESC, note.agent_note_id DESC
LIMIT $3`, query.RunID, query.StudentNumber, limit)
		if err != nil {
			return databaseFailure("list run-bound active agent notes", err)
		}
		defer rows.Close()
		result = make([]NoteToolSummary, 0, limit)
		for rows.Next() {
			var item NoteToolSummary
			if err := rows.Scan(&item.ID, &item.HeadRevision, &item.Title, &item.ContentSHA256, &item.UpdatedAt); err != nil {
				return databaseFailure("scan run-bound active agent note", err)
			}
			item.UpdatedAt = item.UpdatedAt.UTC()
			if err := validateNoteToolSummary(item); err != nil {
				return domainError(ErrorStoredDataInvalid, true, "validate run-bound active agent note", err)
			}
			result = append(result, item)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate run-bound active agent notes", err)
		}
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) LoadBoundActiveNote(ctx context.Context, query BoundToolQuery, noteID string) (result NoteToolDetail, found bool, resultErr error) {
	if err := validateBoundToolQuery(query); err != nil || !canonicalUUIDv4.MatchString(noteID) {
		if err == nil {
			err = errors.New("note ID is invalid")
		}
		return NoteToolDetail{}, false, domainError(ErrorInvalidInput, true, "validate run-bound active agent note detail", err)
	}
	resultErr = repository.transaction(ctx, "load run-bound active agent note", readOnlyOptions(), func(tx postgresTx) error {
		err := tx.QueryRow(ctx, `
SELECT note.public_id::text,
       note.head_revision,
       revision.title,
       revision.content_sha256,
       note.updated_at,
       revision.content
FROM ascendany.agent_runs AS run
JOIN ascendany.auth_accounts AS account
  ON account.account_id = run.owner_account_id
 AND account.role = 'student'
 AND account.student_number = $2
 AND account.disabled_at IS NULL
JOIN ascendany.agent_notes AS note
  ON note.owner_account_id = run.owner_account_id
 AND note.public_id = $3::uuid
JOIN ascendany.agent_note_revisions AS revision
  ON revision.agent_note_revision_id = note.current_revision_id
 AND revision.agent_note_id = note.agent_note_id
 AND revision.owner_account_id = note.owner_account_id
 AND revision.revision_number = note.head_revision
 AND revision.note_state = 'active'
WHERE run.public_id = $1::uuid`, query.RunID, query.StudentNumber, noteID).Scan(
			&result.ID, &result.HeadRevision, &result.Title, &result.ContentSHA256, &result.UpdatedAt, &result.Content,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return databaseFailure("load run-bound active agent note", err)
		}
		result.UpdatedAt = result.UpdatedAt.UTC()
		if err := validateNoteToolDetail(result); err != nil {
			return domainError(ErrorStoredDataInvalid, true, "validate run-bound active agent note", err)
		}
		found = true
		return nil
	})
	return result, found, resultErr
}

func validateBoundAnalyticsQuery(query BoundAnalyticsQuery) error {
	if err := validateBoundToolQuery(query.BoundToolQuery); err != nil || query.GenerationDatabaseID < 1 || query.HeadRevision < 1 || query.HistoryLimit < 1 || query.HistoryLimit > maximumAnalyticsToolHistory {
		if err == nil {
			err = errors.New("analytics snapshot or history limit is invalid")
		}
		return domainError(ErrorInvalidInput, true, "validate run-bound analytics tool query", err)
	}
	return nil
}

func validateBoundToolQuery(query BoundToolQuery) error {
	if !canonicalUUIDv4.MatchString(query.RunID) || query.StudentNumber == "" || len(query.StudentNumber) > auth.MaxStudentNumberBytes ||
		strings.TrimSpace(query.StudentNumber) != query.StudentNumber || !utf8.ValidString(query.StudentNumber) {
		return errors.New("run ID and student number are required")
	}
	return nil
}

func validateNoteToolSummary(item NoteToolSummary) error {
	if !canonicalUUIDv4.MatchString(item.ID) || item.HeadRevision < 1 || len(item.Title) < 1 || len(item.Title) > 512 || strings.TrimSpace(item.Title) != item.Title ||
		!utf8.ValidString(item.Title) || !norm.NFC.IsNormalString(item.Title) || containsControl(item.Title) || !sha256Pattern.MatchString(item.ContentSHA256) ||
		item.UpdatedAt.IsZero() || !item.UpdatedAt.Equal(item.UpdatedAt.UTC()) {
		return errors.New("note summary violates its storage contract")
	}
	return nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func parseToolRating(value string) (int64, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("rating is not canonical")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("rating is not canonical")
	}
	return parsed, nil
}

func digestToolContent(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

var _ ToolExecutor = (*RuntimeToolExecutor)(nil)
var _ RuntimeToolReader = (*PostgresRepository)(nil)
