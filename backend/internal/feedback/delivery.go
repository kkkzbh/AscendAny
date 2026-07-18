package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var providerFailureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
var senderUsernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

type DeliveryClaim struct {
	DatabaseID     int64
	ID             string
	AttemptCount   int32
	AttemptToken   string
	LeaseOwner     string
	LeaseExpiresAt time.Time
	Reclaimed      bool
}

type DeliveryRequest struct {
	FeedbackID          string               `json:"feedbackId"`
	Title               string               `json:"title"`
	Content             string               `json:"content"`
	Platform            *string              `json:"platform,omitempty"`
	AppVersion          *string              `json:"appVersion,omitempty"`
	UserAgent           *string              `json:"userAgent,omitempty"`
	ConfigurationID     int64                `json:"configurationVersionId"`
	ConfigurationSchema string               `json:"configurationSchema"`
	Configuration       json.RawMessage      `json:"configuration"`
	CredentialRef       *string              `json:"credentialRef,omitempty"`
	Sender              DeliverySender       `json:"sender"`
	Attachments         []DeliveryAttachment `json:"attachments"`
}

type DeliverySender struct {
	AccountID     string  `json:"accountId"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"displayName"`
	StudentNumber *string `json:"studentNumber"`
	PTANickname   *string `json:"ptaNickname"`
	Role          string  `json:"role"`
}

func validateDeliverySender(sender DeliverySender) error {
	if !canonicalUUIDv4.MatchString(sender.AccountID) || !senderUsernamePattern.MatchString(sender.Username) ||
		sender.DisplayName != strings.TrimSpace(sender.DisplayName) || len(sender.DisplayName) < 1 || len(sender.DisplayName) > 64 ||
		(sender.Role != string(auth.RoleAdmin) && sender.Role != string(auth.RoleStudent)) {
		return feedbackError(ErrorStoredDataInvalid, true, "validate feedback sender", fmt.Errorf("stored feedback sender identity is invalid"))
	}
	if sender.Role == string(auth.RoleAdmin) {
		if sender.StudentNumber != nil || sender.PTANickname != nil {
			return feedbackError(ErrorStoredDataInvalid, true, "validate feedback sender", fmt.Errorf("administrator feedback sender has student identity"))
		}
		return nil
	}
	if sender.StudentNumber == nil || *sender.StudentNumber != strings.TrimSpace(*sender.StudentNumber) ||
		len(*sender.StudentNumber) < 1 || len(*sender.StudentNumber) > 64 {
		return feedbackError(ErrorStoredDataInvalid, true, "validate feedback sender", fmt.Errorf("student feedback sender number is invalid"))
	}
	if sender.PTANickname != nil && (*sender.PTANickname != strings.TrimSpace(*sender.PTANickname) ||
		len(*sender.PTANickname) < 1 || len(*sender.PTANickname) > 256) {
		return feedbackError(ErrorStoredDataInvalid, true, "validate feedback sender", fmt.Errorf("student feedback sender PTA nickname is invalid"))
	}
	return nil
}

type DeliveryAttachment struct {
	Sequence   int16  `json:"sequence"`
	Filename   string `json:"filename"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	MediaType  string `json:"mediaType"`
	StorageKey string `json:"storageKey"`
	Content    []byte `json:"-"`
}

func validateDeliveryAttachmentManifest(attachments []DeliveryAttachment) error {
	manifest := make([]Attachment, len(attachments))
	for index, attachment := range attachments {
		if attachment.Content != nil {
			return feedbackError(ErrorStoredDataInvalid, true, "validate delivery attachment manifest", fmt.Errorf("attachment %d contains data before hydration", index+1))
		}
		manifest[index] = Attachment{
			Sequence: attachment.Sequence, Filename: attachment.Filename, SHA256: attachment.SHA256,
			SizeBytes: attachment.SizeBytes, MediaType: attachment.MediaType, StorageKey: attachment.StorageKey,
		}
	}
	return validateAttachmentManifest(manifest)
}

func validateHydratedDeliveryAttachments(attachments []DeliveryAttachment) error {
	manifest := make([]Attachment, len(attachments))
	for index, attachment := range attachments {
		manifest[index] = Attachment{
			Sequence: attachment.Sequence, Filename: attachment.Filename, SHA256: attachment.SHA256,
			SizeBytes: attachment.SizeBytes, MediaType: attachment.MediaType, StorageKey: attachment.StorageKey,
		}
		if int64(len(attachment.Content)) != attachment.SizeBytes {
			return feedbackError(ErrorStoredDataInvalid, true, "validate hydrated delivery attachments", fmt.Errorf("attachment %d byte count differs from its manifest", index+1))
		}
		digest := sha256.Sum256(attachment.Content)
		if hex.EncodeToString(digest[:]) != attachment.SHA256 {
			return feedbackError(ErrorStoredDataInvalid, true, "validate hydrated delivery attachments", fmt.Errorf("attachment %d digest differs from its manifest", index+1))
		}
	}
	return validateAttachmentManifest(manifest)
}

type DeliveryProvider interface {
	Deliver(context.Context, DeliveryRequest) ([]byte, error)
}

type ProviderFailure struct {
	Code      string
	Permanent bool
	Cause     error
}

func (failure *ProviderFailure) Error() string {
	if failure == nil {
		return "<nil>"
	}
	return fmt.Sprintf("feedback provider %s: %v", failure.Code, failure.Cause)
}

func (failure *ProviderFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

type DeliveryOutcome struct {
	JobID         string  `json:"jobId"`
	Disposition   string  `json:"disposition"`
	ReceiptSHA256 *string `json:"receiptSha256,omitempty"`
	FailureCode   *string `json:"failureCode,omitempty"`
}

const (
	DeliverySucceeded = "succeeded"
	DeliveryRetry     = "retry"
	DeliveryFailed    = "failed"
)

type DeliveryRepository interface {
	ClaimDelivery(context.Context, string, string, time.Duration) (*DeliveryClaim, error)
	RenewDeliveryLease(context.Context, DeliveryClaim, time.Duration) error
	LoadDelivery(context.Context, DeliveryClaim) (DeliveryRequest, error)
	CompleteDelivery(context.Context, DeliveryClaim, string) error
	RequeueDelivery(context.Context, DeliveryClaim, time.Duration, string) error
	FailDelivery(context.Context, DeliveryClaim, string, string) error
}
