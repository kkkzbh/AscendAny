package chatagent

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize             = 50
	MaxPageSize                 = 200
	MaxMessageBytes             = 131072
	MaxReasoningBytes           = 262144
	MaxContextSummaryBytes      = 65536
	MaxConfigurationBytes       = 256 << 10
	MaxToolDocumentBytes        = 256 << 10
	MaxFailureDetailBytes       = 4096
	MaxProviderToolCallsPerTurn = 32
	AutoAnalysisInputContent    = "Analyze the student's current published analytics snapshot and provide a concise, actionable progress review."
)

type MessageKind string

const (
	MessageUser                MessageKind = "user"
	MessageAutoAnalysisRequest MessageKind = "auto_analysis_request"
	MessageAssistant           MessageKind = "assistant"
)

type RunKind string

const (
	RunReply        RunKind = "reply"
	RunAutoAnalysis RunKind = "auto_analysis"
)

type RunStatus string

const (
	RunQueued     RunStatus = "queued"
	RunRunning    RunStatus = "running"
	RunSucceeded  RunStatus = "succeeded"
	RunFailed     RunStatus = "failed"
	RunSuperseded RunStatus = "superseded"
)

type ThreadKind string

const (
	ThreadConversation ThreadKind = "conversation"
	ThreadAutoAnalysis ThreadKind = "auto_analysis"
)

type Thread struct {
	ID           string     `json:"id"`
	Kind         ThreadKind `json:"kind"`
	HeadRevision int64      `json:"headRevision"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type ThreadPage struct {
	Items      []Thread `json:"items"`
	NextCursor *string  `json:"nextCursor"`
}

type Message struct {
	ID               string      `json:"id"`
	ThreadID         string      `json:"threadId"`
	Sequence         int64       `json:"sequence"`
	Kind             MessageKind `json:"kind"`
	Content          string      `json:"content"`
	ReasoningContent *string     `json:"reasoningContent,omitempty"`
	ContextSummary   *string     `json:"contextSummary,omitempty"`
	RunID            *string     `json:"runId,omitempty"`
	CreatedAt        time.Time   `json:"createdAt"`
}

type Run struct {
	ID              string     `json:"id"`
	ThreadID        string     `json:"threadId"`
	ClientRequestID string     `json:"clientRequestId"`
	Kind            RunKind    `json:"kind"`
	InputMessageID  string     `json:"inputMessageId"`
	OutputMessageID *string    `json:"outputMessageId,omitempty"`
	Status          RunStatus  `json:"status"`
	AttemptCount    int32      `json:"attemptCount"`
	ErrorCode       *string    `json:"errorCode,omitempty"`
	ErrorDetail     *string    `json:"errorDetail,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type RunEvent struct {
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

type RunEventBatch struct {
	Events       []RunEvent `json:"events"`
	LastSequence int64      `json:"lastSequence"`
	Terminal     bool       `json:"terminal"`
}

type ThreadQuery struct {
	Principal auth.AccessPrincipal
	Cursor    *string
	Limit     int
}

type MessageQuery struct {
	Principal     auth.AccessPrincipal
	ThreadID      string
	AfterSequence int64
	Limit         int
}

type RunQuery struct {
	Principal auth.AccessPrincipal
	RunID     string
}

type EventQuery struct {
	Principal     auth.AccessPrincipal
	RunID         string
	AfterSequence int64
	Limit         int
}

type EnqueueInput struct {
	Principal                     auth.AccessPrincipal
	ThreadID                      string
	ClientRequestID               string
	Kind                          RunKind
	Content                       string
	PromptConfigurationKey        string
	ModelConfigurationKey         string
	ExpectedAnalyticsHeadRevision *int64
}

type EnqueueRequest struct {
	ClientRequestID               string  `json:"clientRequestId"`
	Kind                          RunKind `json:"kind"`
	Content                       string  `json:"content"`
	PromptConfigurationKey        string  `json:"promptConfigurationKey"`
	ModelConfigurationKey         string  `json:"modelConfigurationKey"`
	ExpectedAnalyticsHeadRevision *int64  `json:"expectedAnalyticsHeadRevision,omitempty"`
}

type EnqueueCommand struct {
	EnqueueInput
	RunID     string
	MessageID string
}

type EnqueueResult struct {
	Run                 Run                          `json:"run"`
	Message             Message                      `json:"message"`
	Created             bool                         `json:"created"`
	AutoAnalysisContext *AutoAnalysisFrontendContext `json:"-"`
}

type CreateThreadCommand struct {
	Principal auth.AccessPrincipal
	ThreadID  string
	Kind      ThreadKind
}

type AutoAnalysisRequest struct {
	PromptConfigurationKey        string                      `json:"promptConfigurationKey"`
	ModelConfigurationKey         string                      `json:"modelConfigurationKey"`
	ExpectedAnalyticsHeadRevision int64                       `json:"expectedAnalyticsHeadRevision"`
	Identity                      AutoAnalysisIdentity        `json:"-"`
	FrontendContext               AutoAnalysisFrontendContext `json:"frontendContext,omitempty"`
}

type AutoAnalysisIdentity struct {
	ExamID string `json:"examId"`
	RoleID string `json:"roleId"`
}

type AutoAnalysisFrontendContext struct {
	StudentID        string `json:"studentId"`
	PTANickname      string `json:"ptaNickname"`
	RoleID           string `json:"roleId"`
	RoleName         string `json:"roleName"`
	RoleSystemPrompt string `json:"roleSystemPrompt"`
	LatestExamID     string `json:"latestExamId"`
	Notes            string `json:"notes"`
	NotesTitle       string `json:"notesTitle"`
	NotesLocked      bool   `json:"notesLocked"`
}

type AutoAnalysisInput struct {
	Principal                     auth.AccessPrincipal
	PromptConfigurationKey        string
	ModelConfigurationKey         string
	ExpectedAnalyticsHeadRevision int64
	Identity                      AutoAnalysisIdentity
	FrontendContext               AutoAnalysisFrontendContext
}

type AutoAnalysisCommand struct {
	AutoAnalysisInput
	ThreadID        string
	RunID           string
	MessageID       string
	ClientRequestID string
}

type ConfigurationSnapshot struct {
	VersionDatabaseID int64
	Key               string
	SchemaID          string
	Document          json.RawMessage
	DocumentSHA256    string
	CredentialRef     *string
}

type AnalyticsSnapshot struct {
	GenerationDatabaseID int64
	HeadRevision         int64
}

type Claim struct {
	DatabaseID     int64
	ID             string
	AttemptCount   int32
	AttemptToken   string
	LeaseOwner     string
	LeaseExpiresAt time.Time
	Reclaimed      bool
}

type ToolOutcome string

const (
	ToolSucceeded ToolOutcome = "succeeded"
	ToolFailed    ToolOutcome = "failed"
	ToolDenied    ToolOutcome = "denied"
)

type ToolCallRecord struct {
	Sequence        int64
	Key             string
	Name            string
	ArgumentsSchema string
	Arguments       json.RawMessage
	ArgumentsSHA256 string
	ResultSchema    *string
	Result          json.RawMessage
	ResultSHA256    *string
	Outcome         ToolOutcome
	ErrorCode       *string
	StartedAt       time.Time
	FinishedAt      time.Time
}

type Work struct {
	RunID               string
	Kind                RunKind
	ThreadID            string
	StudentNumber       string
	InputMessageID      string
	Analytics           *AnalyticsSnapshot
	AutoAnalysisContext *AutoAnalysisFrontendContext
	FrontendNotes       *FrontendNotesState
	Prompt              ConfigurationSnapshot
	Model               ConfigurationSnapshot
	Conversation        []Message
	ToolCalls           []ToolCallRecord
}

type AssistantOutput struct {
	Content          string
	ReasoningContent *string
	ContextSummary   *string
}

type Completion struct {
	MessageID string
	Output    AssistantOutput
}

type WorkerDisposition string

const (
	WorkerSucceeded WorkerDisposition = "succeeded"
	WorkerFailed    WorkerDisposition = "failed"
)

type WorkerOutcome struct {
	RunID       string            `json:"runId"`
	Disposition WorkerDisposition `json:"disposition"`
	MessageID   *string           `json:"messageId,omitempty"`
	FailureCode *string           `json:"failureCode,omitempty"`
}
