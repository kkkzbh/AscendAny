package oj

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100

	ProblemSchemaV1      = "ascendany.oj.problem.v1"
	JudgeResultSchemaV1  = "ascendany.oj.judge-result.v1"
	TestBundleMediaType  = "application/vnd.ascendany.oj-test-bundle.v1+tar"
	CPP20SourceMediaType = "text/x-c++src; charset=utf-8"
	PlainTextMediaType   = "text/plain; charset=utf-8"
	JudgeOutputMediaType = "application/octet-stream"
	LanguageCPP20        = "cpp20"
)

type Policy struct {
	MaximumTitleBytes       int
	MaximumStatementBytes   int
	MaximumSolutionBytes    int
	MaximumProblemSpecBytes int
	MaximumTestBundleBytes  int64
	MaximumSourceBytes      int64
	MaximumStdinBytes       int64
	MaximumTimeLimitMS      int
	MaximumMemoryBytes      int64
	MaximumOutputBytes      int64
}

func DefaultPolicy() Policy {
	return Policy{
		MaximumTitleBytes:       512,
		MaximumStatementBytes:   1 << 20,
		MaximumSolutionBytes:    1 << 20,
		MaximumProblemSpecBytes: 256 << 10,
		MaximumTestBundleBytes:  256 << 20,
		MaximumSourceBytes:      1 << 20,
		MaximumStdinBytes:       1 << 20,
		MaximumTimeLimitMS:      120_000,
		MaximumMemoryBytes:      2 << 30,
		MaximumOutputBytes:      16 << 20,
	}
}

type Artifact struct {
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	MediaType  string `json:"mediaType"`
	StorageKey string `json:"storageKey"`
}

type Lifecycle string

const (
	LifecycleActive   Lifecycle = "active"
	LifecycleArchived Lifecycle = "archived"
)

type CreateProblemVersionInput struct {
	Principal            auth.AccessPrincipal
	Slug                 string
	ExpectedHeadRevision int64
	Lifecycle            Lifecycle
	Title                string
	StatementMarkdown    string
	SolutionMarkdown     *string
	KnowledgeTags        []string
	TimeLimitMS          int
	MemoryLimitBytes     int64
	OutputLimitBytes     int64
	ProblemSpec          json.RawMessage
	TestBundle           Artifact
}

type CreateProblemVersionCommand struct {
	CreateProblemVersionInput
	ProblemPublicID   string
	ProblemSchema     string
	ProblemSpecSHA256 string
	ContentSHA256     string
}

type ProblemQuery struct {
	Principal auth.AccessPrincipal
	ProblemID string
}

type ProblemListQuery struct {
	Principal       auth.AccessPrincipal
	AfterSlug       *string
	Limit           int
	IncludeArchived bool
}

type ProblemPage struct {
	Items      []Problem `json:"items"`
	NextCursor *string   `json:"nextCursor"`
}

type Problem struct {
	ID             string          `json:"id"`
	Slug           string          `json:"slug"`
	HeadRevision   int64           `json:"headRevision"`
	CurrentVersion *ProblemVersion `json:"currentVersion"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type ProblemVersion struct {
	Number             int64           `json:"number"`
	Lifecycle          Lifecycle       `json:"lifecycle"`
	Title              string          `json:"title"`
	StatementMarkdown  string          `json:"statementMarkdown"`
	SolutionMarkdown   *string         `json:"solutionMarkdown,omitempty"`
	KnowledgeTags      []string        `json:"knowledgeTags"`
	TimeLimitMS        int             `json:"timeLimitMs"`
	MemoryLimitBytes   int64           `json:"memoryLimitBytes"`
	OutputLimitBytes   int64           `json:"outputLimitBytes"`
	ProblemSchema      string          `json:"problemSchema"`
	ProblemSpec        json.RawMessage `json:"problemSpec,omitempty"`
	ProblemSpecSHA256  string          `json:"problemSpecSha256,omitempty"`
	TestBundle         *Artifact       `json:"testBundle,omitempty"`
	ContentSHA256      string          `json:"contentSha256"`
	CreatedByAccountID string          `json:"createdByAccountId"`
	CreatedAt          time.Time       `json:"createdAt"`
}

type CreateProblemVersionResult struct {
	Problem    Problem `json:"problem"`
	Idempotent bool    `json:"idempotent"`
}

type SubmissionMode string

const (
	SubmissionRun    SubmissionMode = "run"
	SubmissionSubmit SubmissionMode = "submit"
)

type CreateSubmissionInput struct {
	Principal                   auth.AccessPrincipal
	ClientRequestID             string
	ProblemID                   string
	ExpectedProblemHeadRevision int64
	Mode                        SubmissionMode
	LanguageID                  string
	Source                      Artifact
	Stdin                       *Artifact
}

type CreateSubmissionCommand struct {
	CreateSubmissionInput
	SubmissionPublicID string
	JudgeJobPublicID   string
}

type Submission struct {
	ID             string         `json:"id"`
	JudgeJobID     string         `json:"judgeJobId"`
	ProblemID      string         `json:"problemId"`
	ProblemVersion int64          `json:"problemVersion"`
	Mode           SubmissionMode `json:"mode"`
	LanguageID     string         `json:"languageId"`
	CreatedAt      time.Time      `json:"createdAt"`
}

type CreateSubmissionResult struct {
	Submission Submission `json:"submission"`
	Created    bool       `json:"created"`
}

type SubmissionQuery struct {
	Principal    auth.AccessPrincipal
	SubmissionID string
}

type JudgeEventQuery struct {
	Principal     auth.AccessPrincipal
	SubmissionID  string
	AfterSequence int64
	Limit         int
}

type JobStatus string

const (
	JobQueued      JobStatus = "queued"
	JobRunning     JobStatus = "running"
	JobCompleted   JobStatus = "completed"
	JobSystemError JobStatus = "system_error"
)

type SubmissionDetail struct {
	Submission
	Status       JobStatus    `json:"status"`
	AttemptCount int32        `json:"attemptCount"`
	FailureCode  *string      `json:"failureCode,omitempty"`
	Result       *JudgeResult `json:"result,omitempty"`
	UpdatedAt    time.Time    `json:"updatedAt"`
}

type JudgeEvent struct {
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

type JudgeEventBatch struct {
	Events       []JudgeEvent `json:"events"`
	LastSequence int64        `json:"lastSequence"`
	Terminal     bool         `json:"terminal"`
}

type JudgeClaim struct {
	DatabaseID     int64
	ID             string
	AttemptCount   int32
	AttemptToken   string
	LeaseOwner     string
	LeaseExpiresAt time.Time
	Reclaimed      bool
}

type Verdict string

const (
	VerdictAccepted            Verdict = "accepted"
	VerdictWrongAnswer         Verdict = "wrong_answer"
	VerdictCompileError        Verdict = "compile_error"
	VerdictRuntimeError        Verdict = "runtime_error"
	VerdictTimeLimitExceeded   Verdict = "time_limit_exceeded"
	VerdictMemoryLimitExceeded Verdict = "memory_limit_exceeded"
	VerdictOutputLimitExceeded Verdict = "output_limit_exceeded"
)

type JudgeResultInput struct {
	Verdict         Verdict
	ScoreFraction   float64
	PassedCaseCount int64
	TotalCaseCount  int64
	MaxTimeMS       int64
	MaxMemoryBytes  int64
	Output          *Artifact
	ResultManifest  json.RawMessage
}

type PublishedOutput struct {
	Artifact Artifact
	Release  func() error
}

type CompleteJudgeCommand struct {
	Claim JudgeClaim
	JudgeResultInput
	ResultSchema string
	ResultSHA256 string
}

type JudgeResult struct {
	Verdict         Verdict         `json:"verdict"`
	ScoreFraction   float64         `json:"scoreFraction"`
	PassedCaseCount int64           `json:"passedCaseCount"`
	TotalCaseCount  int64           `json:"totalCaseCount"`
	MaxTimeMS       int64           `json:"maxTimeMs"`
	MaxMemoryBytes  int64           `json:"maxMemoryBytes"`
	Output          *Artifact       `json:"output,omitempty"`
	ResultSchema    string          `json:"resultSchema"`
	ResultManifest  json.RawMessage `json:"resultManifest"`
	ResultSHA256    string          `json:"resultSha256"`
	CreatedAt       time.Time       `json:"createdAt"`
}

type JudgeOutcome struct {
	JobID       string       `json:"jobId"`
	Disposition string       `json:"disposition"`
	Result      *JudgeResult `json:"result,omitempty"`
	FailureCode *string      `json:"failureCode,omitempty"`
}
