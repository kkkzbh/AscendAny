package feedback

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type deliveryRepositoryStub struct {
	request        DeliveryRequest
	loadErr        error
	completedHash  string
	requeuedReason string
	failedCode     string
	failedDetail   string
}

func (repository *deliveryRepositoryStub) ClaimDelivery(context.Context, string, string, time.Duration) (*DeliveryClaim, error) {
	return nil, nil
}

func (repository *deliveryRepositoryStub) RenewDeliveryLease(context.Context, DeliveryClaim, time.Duration) error {
	return nil
}

func (repository *deliveryRepositoryStub) LoadDelivery(context.Context, DeliveryClaim) (DeliveryRequest, error) {
	return repository.request, repository.loadErr
}

func (repository *deliveryRepositoryStub) CompleteDelivery(_ context.Context, _ DeliveryClaim, receiptHash string) error {
	repository.completedHash = receiptHash
	return nil
}

func (repository *deliveryRepositoryStub) RequeueDelivery(_ context.Context, _ DeliveryClaim, _ time.Duration, reason string) error {
	repository.requeuedReason = reason
	return nil
}

func (repository *deliveryRepositoryStub) FailDelivery(_ context.Context, _ DeliveryClaim, code, detail string) error {
	repository.failedCode = code
	repository.failedDetail = detail
	return nil
}

func TestDeliveryWorkerOwnsSuccessRetryAndPermanentFailureTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		provider    DeliveryProvider
		disposition string
		assert      func(*testing.T, *deliveryRepositoryStub, DeliveryOutcome)
	}{
		{
			name: "success",
			provider: deliveryProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) {
				return []byte("receipt"), nil
			}),
			disposition: DeliverySucceeded,
			assert: func(t *testing.T, repository *deliveryRepositoryStub, outcome DeliveryOutcome) {
				t.Helper()
				if len(repository.completedHash) != 64 || outcome.ReceiptSHA256 == nil || *outcome.ReceiptSHA256 != repository.completedHash {
					t.Fatalf("repository=%#v outcome=%#v", repository, outcome)
				}
			},
		},
		{
			name: "retry",
			provider: deliveryProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) {
				return nil, &ProviderFailure{Code: "provider_unavailable", Permanent: false, Cause: errors.New("down")}
			}),
			disposition: DeliveryRetry,
			assert: func(t *testing.T, repository *deliveryRepositoryStub, _ DeliveryOutcome) {
				t.Helper()
				if repository.requeuedReason != "provider_unavailable" {
					t.Fatalf("repository=%#v", repository)
				}
			},
		},
		{
			name: "permanent",
			provider: deliveryProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) {
				return nil, &ProviderFailure{Code: "recipient_rejected", Permanent: true, Cause: errors.New(strings.Repeat("x", 5000))}
			}),
			disposition: DeliveryFailed,
			assert: func(t *testing.T, repository *deliveryRepositoryStub, _ DeliveryOutcome) {
				t.Helper()
				if repository.failedCode != "recipient_rejected" || len(repository.failedDetail) != 4096 {
					t.Fatalf("repository=%#v", repository)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &deliveryRepositoryStub{request: DeliveryRequest{FeedbackID: testFeedbackID}}
			worker, err := newDeliveryWorker(repository, test.provider, DeliveryWorkerConfig{
				Owner: "worker", LeaseDuration: time.Minute, RetryDelay: time.Minute,
			}, func() (string, error) { return testRequestID, nil })
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := worker.Process(context.Background(), validDeliveryClaim())
			if err != nil || outcome.Disposition != test.disposition {
				t.Fatalf("outcome=%#v error=%v", outcome, err)
			}
			test.assert(t, repository, outcome)
		})
	}
}

func TestDeliveryWorkerRejectsOpaqueProviderErrorAndBadReceipt(t *testing.T) {
	t.Parallel()
	for name, provider := range map[string]DeliveryProvider{
		"opaque": deliveryProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) {
			return nil, errors.New("opaque provider error")
		}),
		"empty receipt": deliveryProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) {
			return nil, nil
		}),
	} {
		name := name
		provider := provider
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := &deliveryRepositoryStub{request: DeliveryRequest{FeedbackID: testFeedbackID}}
			worker, err := NewDeliveryWorker(repository, provider, DeliveryWorkerConfig{
				Owner: "worker", LeaseDuration: time.Minute, RetryDelay: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := worker.Process(context.Background(), validDeliveryClaim()); CodeOf(err) != ErrorProvider {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
			if repository.completedHash != "" || repository.requeuedReason != "" || repository.failedCode != "" {
				t.Fatalf("repository transitioned invalid provider result: %#v", repository)
			}
		})
	}
}

func validDeliveryClaim() DeliveryClaim {
	return DeliveryClaim{
		DatabaseID:     1,
		ID:             testDeliveryID,
		AttemptCount:   1,
		AttemptToken:   testRequestID,
		LeaseOwner:     "worker",
		LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}
}
