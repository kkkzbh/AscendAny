package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var providerFailureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

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
	FeedbackID          string          `json:"feedbackId"`
	Title               string          `json:"title"`
	Content             string          `json:"content"`
	Platform            *string         `json:"platform,omitempty"`
	AppVersion          *string         `json:"appVersion,omitempty"`
	UserAgent           *string         `json:"userAgent,omitempty"`
	ConfigurationID     int64           `json:"configurationVersionId"`
	ConfigurationSchema string          `json:"configurationSchema"`
	Configuration       json.RawMessage `json:"configuration"`
	CredentialRef       *string         `json:"credentialRef,omitempty"`
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
