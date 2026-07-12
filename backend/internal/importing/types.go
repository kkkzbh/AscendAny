package importing

import (
	"context"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

const (
	PintiaSnapshotV2MediaType = "application/vnd.ascendany.pintia.snapshot.v2+json"
	PintiaSnapshotV2JobKind   = "pintia_snapshot_v2"

	AnalyticsManifestProtocolV1 = "analytics_input_manifest_v1"
)

type JobStatus string

const (
	JobQueued     JobStatus = "queued"
	JobRunning    JobStatus = "running"
	JobSucceeded  JobStatus = "succeeded"
	JobFailed     JobStatus = "failed"
	JobSuperseded JobStatus = "superseded"
)

type JobStage string

const (
	StageReceived   JobStage = "received"
	StageValidating JobStage = "validating"
	StageImporting  JobStage = "importing"
	StageAnalyzing  JobStage = "analyzing"
	StageCompleted  JobStage = "completed"
	StageFailed     JobStage = "failed"
	StageSuperseded JobStage = "superseded"
)

type Job struct {
	ID             int64
	PublicID       string
	ArtifactID     int64
	Status         JobStatus
	Stage          JobStage
	AttemptCount   int32
	LeaseOwner     *string
	LeaseExpiresAt *time.Time
	SnapshotID     *int64
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	UpdatedAt      time.Time
}

type QueueResult struct {
	Job     Job
	Created bool
}

type Claim struct {
	Job
	Reclaimed bool
}

type ArtifactMetadata struct {
	ID         int64
	Hash       string
	Size       int64
	MediaType  string
	StorageKey string
}

type ImportDisposition string

const (
	ImportCreated   ImportDisposition = "created"
	ImportDuplicate ImportDisposition = "domain_duplicate"
	ImportFailed    ImportDisposition = "failed"
	ImportRetry     ImportDisposition = "retry"
	ImportAnalyzing ImportDisposition = "analyzing"
)

type ImportOutcome struct {
	Disposition           ImportDisposition
	SnapshotID            *int64
	SnapshotPublicID      *string
	AnalyticsGenerationID *int64
	FailureCode           *ErrorCode
}

type PublicIDs struct {
	LogicalExam string
	Snapshot    string
}

type AnalyticsConfig struct {
	AlgorithmVersion string
	ConfigSHA256     string
}

type ImportRequest struct {
	Claim      Claim
	Snapshot   *pintia.Snapshot
	DomainHash string
	PublicIDs  PublicIDs
	Analytics  AnalyticsConfig
}

type serviceRepository interface {
	QueueArtifact(context.Context, artifact.Artifact, string, string) (QueueResult, error)
	Claim(context.Context, string, time.Duration) (*Claim, error)
}

type workerStore interface {
	Claim(context.Context, string, time.Duration) (*Claim, error)
	RenewLease(context.Context, Claim, time.Duration) error
	LoadArtifact(context.Context, Claim) (ArtifactMetadata, error)
	MarkImporting(context.Context, Claim, time.Duration) (Claim, error)
	Requeue(context.Context, Claim, time.Duration, ErrorCode) error
	FailPermanent(context.Context, Claim, ErrorCode, string) error
	ImportSnapshot(context.Context, ImportRequest) (ImportOutcome, error)
}

type artifactVerifier interface {
	Verify(context.Context, string, int64) (artifact.Artifact, error)
}
