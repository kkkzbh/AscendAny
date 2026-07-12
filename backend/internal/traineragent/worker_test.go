package traineragent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/trainerprocess"
)

type transportStub struct {
	claim         *Claim
	claimErr      error
	heartbeat     func(context.Context, LeaseReference) (Heartbeat, error)
	publish       func(context.Context, Claim, []byte) (Publication, error)
	reportFailure func(context.Context, Claim, FailureReport) (FailureDisposition, error)
}

func (transport *transportStub) Claim(context.Context) (*Claim, error) {
	return transport.claim, transport.claimErr
}

func (transport *transportStub) Heartbeat(ctx context.Context, lease LeaseReference) (Heartbeat, error) {
	return transport.heartbeat(ctx, lease)
}

func (transport *transportStub) Publish(ctx context.Context, claim Claim, output []byte) (Publication, error) {
	return transport.publish(ctx, claim, output)
}

func (transport *transportStub) ReportFailure(ctx context.Context, claim Claim, failure FailureReport) (FailureDisposition, error) {
	return transport.reportFailure(ctx, claim, failure)
}

type trainerStub struct {
	train func(context.Context, trainerprocess.TrainingRequest) ([]byte, error)
}

func (trainer trainerStub) Train(ctx context.Context, request trainerprocess.TrainingRequest) ([]byte, error) {
	return trainer.train(ctx, request)
}

func TestWorkerHeartbeatsTrainsAndPublishes(t *testing.T) {
	t.Parallel()
	claim := workerTestClaim()
	var heartbeatCalls atomic.Int32
	transport := &transportStub{
		claim: &claim,
		heartbeat: func(context.Context, LeaseReference) (Heartbeat, error) {
			heartbeatCalls.Add(1)
			return workerTestHeartbeat(claim), nil
		},
		publish: func(_ context.Context, got Claim, output []byte) (Publication, error) {
			if got.RunID != claim.RunID || string(output) != `{"output":true}` {
				t.Fatalf("publish claim/output = %#v %s", got, output)
			}
			return workerTestPublication(claim, PublicationActivated), nil
		},
		reportFailure: func(context.Context, Claim, FailureReport) (FailureDisposition, error) {
			t.Fatal("unexpected failure report")
			return "", nil
		},
	}
	worker, err := NewWorker(transport, trainerStub{train: func(_ context.Context, request trainerprocess.TrainingRequest) ([]byte, error) {
		if request.RunID != claim.RunID || request.InputManifestSHA256 != claim.InputManifestSHA256 {
			t.Fatalf("training request = %#v", request)
		}
		return []byte(`{"output":true}`), nil
	}}, WorkerConfig{LeaseDuration: claim.LeaseDuration})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.RunOne(context.Background())
	if err != nil || outcome == nil || outcome.Disposition != WorkerActivated || outcome.ModelID == nil || *outcome.ModelID != testModelID {
		t.Fatalf("outcome = %#v error = %v", outcome, err)
	}
	if heartbeatCalls.Load() < 1 {
		t.Fatal("initial heartbeat was not sent")
	}
	if outcome.AttemptToken != claim.AttemptToken || outcome.RequestSHA256 != strings.Repeat("c", 64) ||
		outcome.InputManifestSHA256 != claim.InputManifestSHA256 || outcome.OutputBundleSHA256 != strings.Repeat("d", 64) ||
		outcome.RuntimeConstructionSHA256 != strings.Repeat("a", 64) ||
		outcome.RuntimeProvenanceSHA256 != strings.Repeat("b", 64) ||
		outcome.RuntimeTreeSHA256 != strings.Repeat("c", 64) ||
		outcome.HostCapabilitySHA256 != strings.Repeat("d", 64) ||
		outcome.RuntimeAttestationSHA256 != strings.Repeat("e", 64) ||
		!outcome.ClaimedAt.Equal(claim.ClaimedAt) || !outcome.HeartbeatAt.Equal(workerTestHeartbeat(claim).SucceededAt) ||
		!outcome.UploadedAt.Equal(claim.ClaimedAt.Add(200*time.Millisecond)) {
		t.Fatalf("acceptance outcome = %#v", outcome)
	}
}

func TestWorkerReportsStructuredTrainerFailure(t *testing.T) {
	t.Parallel()
	claim := workerTestClaim()
	transport := &transportStub{
		claim: &claim,
		heartbeat: func(context.Context, LeaseReference) (Heartbeat, error) {
			return workerTestHeartbeat(claim), nil
		},
		publish: func(context.Context, Claim, []byte) (Publication, error) {
			t.Fatal("unexpected publication")
			return Publication{}, nil
		},
		reportFailure: func(_ context.Context, got Claim, failure FailureReport) (FailureDisposition, error) {
			if got.RunID != claim.RunID || failure.Code != "trainer_timeout" || failure.Detail != "training exceeded limit" || !failure.Retryable {
				t.Fatalf("failure = %#v claim = %#v", failure, got)
			}
			return FailureRequeued, nil
		},
	}
	worker, err := NewWorker(transport, trainerStub{train: func(context.Context, trainerprocess.TrainingRequest) ([]byte, error) {
		return nil, &trainerprocess.TrainerFailure{
			Code: "trainer_timeout", Detail: "training exceeded limit", Retryable: true,
		}
	}}, WorkerConfig{LeaseDuration: claim.LeaseDuration})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.RunOne(context.Background())
	if err != nil || outcome == nil || outcome.Disposition != WorkerRequeued || outcome.FailureCode == nil || *outcome.FailureCode != "trainer_timeout" {
		t.Fatalf("outcome = %#v error = %v", outcome, err)
	}
}

func TestWorkerNormalizesInvalidUnclassifiedFailureDetail(t *testing.T) {
	t.Parallel()
	claim := workerTestClaim()
	transport := &transportStub{
		claim: &claim,
		heartbeat: func(context.Context, LeaseReference) (Heartbeat, error) {
			return workerTestHeartbeat(claim), nil
		},
		publish: func(context.Context, Claim, []byte) (Publication, error) {
			t.Fatal("unexpected publication")
			return Publication{}, nil
		},
		reportFailure: func(_ context.Context, _ Claim, failure FailureReport) (FailureDisposition, error) {
			if failure.Code != "trainer_unclassified" || failure.Detail != "trainer-agent execution failed" || failure.Retryable {
				t.Fatalf("failure = %#v", failure)
			}
			return FailureRecorded, nil
		},
	}
	worker, err := NewWorker(transport, trainerStub{train: func(context.Context, trainerprocess.TrainingRequest) ([]byte, error) {
		return nil, errors.New("invalid\x00detail")
	}}, WorkerConfig{LeaseDuration: claim.LeaseDuration})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.RunOne(context.Background())
	if err != nil || outcome == nil || outcome.Disposition != WorkerFailed {
		t.Fatalf("outcome = %#v error = %v", outcome, err)
	}
}

func TestWorkerCancelsTrainerWhenHeartbeatFails(t *testing.T) {
	t.Parallel()
	claim := workerTestClaim()
	var heartbeatCalls atomic.Int32
	leaseLost := errors.New("lease heartbeat rejected")
	transport := &transportStub{
		claim: &claim,
		heartbeat: func(context.Context, LeaseReference) (Heartbeat, error) {
			if heartbeatCalls.Add(1) == 1 {
				return workerTestHeartbeat(claim), nil
			}
			return Heartbeat{}, leaseLost
		},
		publish: func(context.Context, Claim, []byte) (Publication, error) {
			t.Fatal("unexpected publication")
			return Publication{}, nil
		},
		reportFailure: func(context.Context, Claim, FailureReport) (FailureDisposition, error) {
			t.Fatal("unexpected failure report")
			return "", nil
		},
	}
	worker, err := NewWorker(transport, trainerStub{train: func(ctx context.Context, _ trainerprocess.TrainingRequest) ([]byte, error) {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}}, WorkerConfig{LeaseDuration: claim.LeaseDuration})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := worker.RunOne(ctx); !errors.Is(err, leaseLost) {
		t.Fatalf("error = %v", err)
	}
}

func workerTestClaim() Claim {
	claimedAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	return Claim{
		LeaseReference: LeaseReference{RunID: testRunID, AttemptToken: testAttemptToken},
		LeaseDuration:  300 * time.Millisecond, LeaseExpiresAt: claimedAt.Add(300 * time.Millisecond),
		ClaimedAt:           claimedAt,
		InputManifestSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		InputBundleSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		InputBundle:         []byte(`{"input":true}`),
	}
}

func workerTestHeartbeat(claim Claim) Heartbeat {
	succeededAt := claim.ClaimedAt.Add(100 * time.Millisecond)
	return Heartbeat{LeaseExpiresAt: succeededAt.Add(claim.LeaseDuration), SucceededAt: succeededAt}
}

func workerTestPublication(claim Claim, disposition PublicationDisposition) Publication {
	return Publication{
		Disposition: disposition, ModelID: testModelID, RequestSHA256: strings.Repeat("c", 64),
		OutputBundleSHA256: strings.Repeat("d", 64), RuntimeConstructionSHA256: strings.Repeat("a", 64),
		RuntimeProvenanceSHA256: strings.Repeat("b", 64), RuntimeTreeSHA256: strings.Repeat("c", 64),
		HostCapabilitySHA256: strings.Repeat("d", 64), RuntimeAttestationSHA256: strings.Repeat("e", 64),
		UploadedAt: claim.ClaimedAt.Add(200 * time.Millisecond),
	}
}
