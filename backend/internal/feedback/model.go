package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	MaxTitleBytes              = 800
	MaxContentBytes            = 40000
	MaxPlatformBytes           = 320
	MaxAppVersionBytes         = 320
	MaxUserAgentBytes          = 2048
	MaxImages                  = 8
	MaxImageBytes              = 8 << 20
	MaxImageDataURLBytes       = 12 << 20
	MaxImageNameRunes          = 160
	MaxAttachmentFilenameBytes = 640
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
	Attachments     []Attachment
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

// ImageInput is the frozen Agent frontend image object before data-URL decoding.
type ImageInput struct {
	Name    string
	DataURL string
}

// Attachment is the immutable content-addressed manifest persisted with one
// feedback submission. Sequence is one-based and preserves frontend order.
type Attachment struct {
	Sequence   int16  `json:"sequence"`
	Filename   string `json:"filename"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	MediaType  string `json:"mediaType"`
	StorageKey string `json:"storageKey"`
}

func (command SubmitCommand) subjectHex() string {
	return hex.EncodeToString(command.SubjectDigest[:])
}
