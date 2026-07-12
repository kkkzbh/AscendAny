package importing

import (
	"fmt"
	"strings"
	"time"
)

type claimAttempt struct {
	owner string
	count int32
}

func validateState(status JobStatus, stage JobStage) error {
	valid := false
	switch status {
	case JobQueued:
		valid = stage == StageReceived
	case JobRunning:
		valid = stage == StageValidating || stage == StageImporting || stage == StageAnalyzing
	case JobSucceeded:
		valid = stage == StageCompleted
	case JobFailed:
		valid = stage == StageFailed
	case JobSuperseded:
		valid = stage == StageSuperseded
	}
	if !valid {
		return importError(ErrorStateConflict, false, "validate state", fmt.Errorf("status %q cannot use stage %q", status, stage))
	}
	return nil
}

func validateClaim(claim Claim, expectedStage JobStage, now time.Time) error {
	if err := validateState(claim.Status, claim.Stage); err != nil {
		return err
	}
	if claim.Status != JobRunning || claim.Stage != expectedStage {
		return importError(
			ErrorStateConflict,
			false,
			"validate claim",
			fmt.Errorf("job is %s/%s, expected running/%s", claim.Status, claim.Stage, expectedStage),
		)
	}
	if _, err := requireClaimAttempt(claim, "validate claim"); err != nil {
		return err
	}
	if claim.LeaseExpiresAt == nil || !claim.LeaseExpiresAt.After(now) {
		return importError(ErrorLeaseLost, false, "validate claim", fmt.Errorf("job has no active lease"))
	}
	return nil
}

func requireClaimAttempt(claim Claim, operation string) (claimAttempt, error) {
	if claim.ID <= 0 || claim.ArtifactID <= 0 || !ValidPublicID(claim.PublicID) {
		return claimAttempt{}, importError(ErrorStateConflict, false, operation, fmt.Errorf("claim job identity is invalid"))
	}
	if claim.LeaseOwner == nil || strings.TrimSpace(*claim.LeaseOwner) == "" || claim.AttemptCount <= 0 {
		return claimAttempt{}, importError(ErrorStateConflict, false, operation, fmt.Errorf("claim attempt identity is invalid"))
	}
	return claimAttempt{owner: *claim.LeaseOwner, count: claim.AttemptCount}, nil
}
