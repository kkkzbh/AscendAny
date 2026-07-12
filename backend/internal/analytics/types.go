package analytics

import "time"

type Dataset struct {
	Snapshots []SnapshotData
}

type SnapshotData struct {
	ExamID               int64
	SnapshotID           int64
	DomainHash           string
	StartsAt             *time.Time
	EndsAt               *time.Time
	TotalScore           *float64
	ExpectedProblems     int64
	ExpectedParticipants int64
	ExpectedRankings     int64
	ExpectedSubmissions  int64
	Problems             []ProblemData
	Participants         []ParticipantData
	Submissions          []SubmissionData
}

type ProblemData struct {
	ProblemSetProblemID string
	MaxScore            *float64
}

type ParticipantData struct {
	ActorID        int64
	Ranking        *RankingData
	ProblemResults []RankingProblemResultData
}

type RankingData struct {
	Rank            int64
	TotalScore      *float64
	TimeUsedSeconds *int64
}

type RankingProblemResultData struct {
	ProblemSetProblemID  string
	Score                *float64
	Passed               *bool
	ValidSubmissionCount *int64
}

type SubmissionData struct {
	SubmissionIdentityID int64
	ActorID              int64
	ProblemSetProblemID  string
	SubmittedAt          time.Time
	Verdict              string
	Score                *float64
	TimeMS               *int64
	MemoryBytes          *int64
}

type Result struct {
	ReferenceTime time.Time
	Students      []StudentResult
	Problems      []ProblemResult
}

type StudentResult struct {
	ActorID int64
	Rating  int64
	Metrics StudentMetrics
}

type StudentMetrics struct {
	Protocol      string               `json:"protocol"`
	ReferenceTime time.Time            `json:"referenceTime"`
	Current       MetricValues         `json:"current"`
	ExamHistory   []ExamMetricPoint    `json:"examHistory"`
	RatingHistory []RatingHistoryPoint `json:"ratingHistory"`
}

type MetricValues struct {
	Knowledge   *float64 `json:"knowledge"`
	Accuracy    *float64 `json:"accuracy"`
	Quality     *float64 `json:"quality"`
	Flexibility *float64 `json:"flexibility"`
	Proficiency *float64 `json:"proficiency"`
}

type ExamMetricPoint struct {
	ExamID     int64        `json:"examId"`
	SnapshotID int64        `json:"snapshotId"`
	EventTime  time.Time    `json:"eventTime"`
	Values     MetricValues `json:"values"`
}

type RatingHistoryPoint struct {
	ExamID      int64     `json:"examId"`
	SnapshotID  int64     `json:"snapshotId"`
	EventTime   time.Time `json:"eventTime"`
	Rank        int64     `json:"rank"`
	OldRating   int64     `json:"oldRating"`
	Delta       int64     `json:"delta"`
	NewRating   int64     `json:"newRating"`
	Seed        float64   `json:"seed"`
	Performance float64   `json:"performance"`
}

type ProblemResult struct {
	SnapshotID          int64
	ProblemSetProblemID string
	Metrics             ProblemMetrics
}

type ProblemMetrics struct {
	Protocol                 string             `json:"protocol"`
	ParticipantCount         int64              `json:"participantCount"`
	SubmissionCount          int64              `json:"submissionCount"`
	AcceptedSubmissionCount  int64              `json:"acceptedSubmissionCount"`
	AttemptingActorCount     int64              `json:"attemptingActorCount"`
	AcceptedActorCount       int64              `json:"acceptedActorCount"`
	SubmissionAcceptanceRate float64            `json:"submissionAcceptanceRate"`
	ActorAcceptanceRate      float64            `json:"actorAcceptanceRate"`
	AcceptedRuntimeMS        *DistributionStats `json:"acceptedRuntimeMs"`
	AcceptedMemoryBytes      *DistributionStats `json:"acceptedMemoryBytes"`
}

type DistributionStats struct {
	Count  int64   `json:"count"`
	Min    int64   `json:"min"`
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
	Max    int64   `json:"max"`
}

type Claim struct {
	GenerationID              int64
	LeaseOwner                string
	LeaseExpiresAt            time.Time
	AttemptCount              int32
	Reclaimed                 bool
	BaseAnalyticsGenerationID *int64
	BaseHeadRevision          int64
	TargetExamID              int64
	TargetSnapshotID          int64
	TargetExamHeadRevision    int64
	ManifestJSON              []byte
	ManifestSHA256            string
	AlgorithmVersion          string
	ConfigSHA256              string
}

type WorkItem struct {
	Claim    Claim
	Manifest ParsedManifest
	Dataset  Dataset
}

type PublishDisposition string

const (
	PublishSucceeded  PublishDisposition = "succeeded"
	PublishSuperseded PublishDisposition = "superseded"
)

type PublishResult struct {
	Disposition             PublishDisposition
	ReplacementGenerationID *int64
}

type RunDisposition string

const (
	RunSucceeded  RunDisposition = "succeeded"
	RunSuperseded RunDisposition = "superseded"
	RunFailed     RunDisposition = "failed"
)

type RunOutcome struct {
	GenerationID            int64
	Disposition             RunDisposition
	ReplacementGenerationID *int64
	FailureCode             *ErrorCode
}
