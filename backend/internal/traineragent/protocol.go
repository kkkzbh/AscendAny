// Package traineragent owns the authenticated client and local runtime for the
// RTX recommendation trainer agent. It never receives PostgreSQL or km6
// artifact-store capabilities. Only the isolated Python child performs model
// computation.
package traineragent

import (
	"context"
	"encoding/json"
	"time"
)

type LeaseReference struct {
	RunID        string
	AttemptToken string
}

type Claim struct {
	LeaseReference
	LeaseDuration       time.Duration
	LeaseExpiresAt      time.Time
	ClaimedAt           time.Time
	InputManifestSHA256 string
	InputBundleSHA256   string
	InputBundle         json.RawMessage
}

type Heartbeat struct {
	LeaseExpiresAt time.Time
	SucceededAt    time.Time
}

type PublicationDisposition string

const (
	PublicationActivated  PublicationDisposition = "activated"
	PublicationSuperseded PublicationDisposition = "superseded"
)

type Publication struct {
	Disposition               PublicationDisposition
	ModelID                   string
	RequestSHA256             string
	OutputBundleSHA256        string
	RuntimeConstructionSHA256 string
	RuntimeProvenanceSHA256   string
	RuntimeTreeSHA256         string
	HostCapabilitySHA256      string
	RuntimeAttestationSHA256  string
	UploadedAt                time.Time
}

type FailureDisposition string

const (
	FailureRecorded FailureDisposition = "failed"
	FailureRequeued FailureDisposition = "requeued"
)

type FailureReport struct {
	Code      string
	Detail    string
	Retryable bool
}

type RemoteError struct {
	StatusCode int
	Code       string
	Detail     string
	Retryable  bool
}

func (failure *RemoteError) Error() string {
	if failure == nil {
		return "<nil>"
	}
	return failure.Code + ": " + failure.Detail
}

type Transport interface {
	Claim(context.Context) (*Claim, error)
	Heartbeat(context.Context, LeaseReference) (Heartbeat, error)
	Publish(context.Context, Claim, []byte) (Publication, error)
	ReportFailure(context.Context, Claim, FailureReport) (FailureDisposition, error)
}
