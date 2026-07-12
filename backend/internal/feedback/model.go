package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	MaxTitleBytes      = 800
	MaxContentBytes    = 40000
	MaxPlatformBytes   = 320
	MaxAppVersionBytes = 320
	MaxUserAgentBytes  = 2048
)

type Policy struct {
	Window                   time.Duration
	MaximumSubmissions       int
	DeliveryConfigurationKey string
}

type SubmitInput struct {
	Principal       auth.AccessPrincipal
	ClientRequestID string
	Title           string
	Content         string
	Platform        *string
	AppVersion      *string
	UserAgent       *string
}

type SubmitCommand struct {
	SubmitInput
	FeedbackPublicID string
	DeliveryPublicID string
	SubjectDigest    [sha256.Size]byte
	Policy           Policy
}

type Submission struct {
	ID            string    `json:"id"`
	DeliveryJobID string    `json:"deliveryJobId"`
	CreatedAt     time.Time `json:"createdAt"`
}

type SubmitResult struct {
	Submission Submission `json:"submission"`
	Created    bool       `json:"created"`
}

func (command SubmitCommand) subjectHex() string {
	return hex.EncodeToString(command.SubjectDigest[:])
}
