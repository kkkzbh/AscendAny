package importing

import (
	"testing"
	"time"
)

func TestStrictJobStateMachine(t *testing.T) {
	valid := []struct {
		status JobStatus
		stage  JobStage
	}{
		{JobQueued, StageReceived},
		{JobRunning, StageValidating},
		{JobRunning, StageImporting},
		{JobRunning, StageAnalyzing},
		{JobSucceeded, StageCompleted},
		{JobFailed, StageFailed},
		{JobSuperseded, StageSuperseded},
	}
	for _, state := range valid {
		if err := validateState(state.status, state.stage); err != nil {
			t.Fatalf("validateState(%s,%s) error = %v", state.status, state.stage, err)
		}
	}
	for _, state := range []struct {
		status JobStatus
		stage  JobStage
	}{{JobQueued, StageImporting}, {JobRunning, StageCompleted}, {JobSucceeded, StageAnalyzing}, {"unknown", StageReceived}} {
		assertImportCode(t, validateState(state.status, state.stage), ErrorStateConflict)
	}
}

func TestClaimRequiresLiveLeaseAndExactStage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	owner := "worker-1"
	expires := now.Add(time.Minute)
	claim := Claim{Job: Job{
		ID: 1, PublicID: "11111111-1111-4111-8111-111111111111", ArtifactID: 2,
		Status: JobRunning, Stage: StageImporting, AttemptCount: 1,
		LeaseOwner: &owner, LeaseExpiresAt: &expires,
	}}
	if err := validateClaim(claim, StageImporting, now); err != nil {
		t.Fatal(err)
	}
	expires = now
	claim.LeaseExpiresAt = &expires
	assertImportCode(t, validateClaim(claim, StageImporting, now), ErrorLeaseLost)
}

func TestClaimRequiresPositiveAttemptIdentity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	owner := "worker-1"
	expires := now.Add(time.Minute)
	claim := Claim{Job: Job{
		ID: 1, PublicID: "11111111-1111-4111-8111-111111111111", ArtifactID: 2,
		Status: JobRunning, Stage: StageImporting,
		LeaseOwner: &owner, LeaseExpiresAt: &expires,
	}}
	assertImportCode(t, validateClaim(claim, StageImporting, now), ErrorStateConflict)
}
