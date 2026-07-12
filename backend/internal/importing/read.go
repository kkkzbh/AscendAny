package importing

import (
	"encoding/json"
	"regexp"
	"time"
)

const (
	DefaultJobPageSize = 20
	MaxJobPageSize     = 100
	MaxEventBatchSize  = 100
)

var canonicalUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var publicEventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

type PublicJobError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Permanent bool   `json:"permanent"`
}

type PublicJob struct {
	ID             string          `json:"id"`
	ArtifactSHA256 string          `json:"artifactSha256"`
	Status         JobStatus       `json:"status"`
	Stage          JobStage        `json:"stage"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	ExamID         *string         `json:"examId"`
	SnapshotID     *string         `json:"snapshotId"`
	Error          *PublicJobError `json:"error"`
}

type PublicEvent struct {
	Sequence   int64           `json:"sequence"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
}

type JobPage struct {
	Items      []PublicJob `json:"items"`
	NextCursor *string     `json:"nextCursor"`
}

type EventBatch struct {
	Events   []PublicEvent
	Terminal bool
}

func ValidPublicID(value string) bool {
	return canonicalUUIDv4Pattern.MatchString(value)
}

func publicFailureMessage(code string) string {
	switch ErrorCode(code) {
	case ErrorValidation:
		return "The Pintia snapshot failed validation."
	case ErrorIdentityConflict, ErrorSubmissionConflict, ErrorHeadConflict:
		return "The snapshot conflicts with immutable imported identity."
	case ErrorArtifactMetadata, ErrorArtifactVerification:
		return "The uploaded artifact could not be verified."
	case ErrorManifest:
		return "The analytics input manifest could not be created."
	default:
		return "The import could not be completed."
	}
}

func terminalStatus(status JobStatus) bool {
	return status == JobSucceeded || status == JobFailed || status == JobSuperseded
}
