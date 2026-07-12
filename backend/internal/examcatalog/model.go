package examcatalog

import (
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type ListQuery struct {
	Principal auth.AccessPrincipal
	Cursor    *string
	Limit     int
}

type DetailQuery struct {
	Principal auth.AccessPrincipal
	ExamID    string
}

type Page struct {
	Items      []ExamSummary `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

type ExamSummary struct {
	ID               string     `json:"id"`
	SnapshotID       string     `json:"snapshotId"`
	Platform         string     `json:"platform"`
	ProblemSetID     string     `json:"problemSetId"`
	Title            string     `json:"title"`
	SourceURL        string     `json:"sourceUrl"`
	StartsAt         *time.Time `json:"startsAt"`
	EndsAt           *time.Time `json:"endsAt"`
	TotalScore       *string    `json:"totalScore"`
	ProblemCount     int64      `json:"problemCount"`
	ParticipantCount int64      `json:"participantCount"`
	RankingCount     int64      `json:"rankingCount"`
	SubmissionCount  int64      `json:"submissionCount"`
	SnapshotSequence int64      `json:"snapshotSequence"`
	HeadRevision     int64      `json:"headRevision"`
	ExporterVersion  string     `json:"exporterVersion"`
	ExportedAt       time.Time  `json:"exportedAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type Detail struct {
	ExamSummary
	Problems []Problem `json:"problems"`
}

type Problem struct {
	ID                         string  `json:"id"`
	ProblemID                  string  `json:"problemId"`
	Label                      *string `json:"label"`
	Title                      string  `json:"title"`
	MaxScore                   *string `json:"maxScore"`
	TimeLimitMS                *int64  `json:"timeLimitMs"`
	MemoryLimitBytes           *int64  `json:"memoryLimitBytes"`
	SubmissionCount            int64   `json:"submissionCount"`
	SubmittingParticipantCount int64   `json:"submittingParticipantCount"`
	PassedParticipantCount     int64   `json:"passedParticipantCount"`
}
