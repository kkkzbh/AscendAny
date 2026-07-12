package recommendation

import (
	"context"
	"errors"
	"fmt"

	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration             ErrorCode = "recommendation_invalid_configuration"
	ErrorInvalidInput                     ErrorCode = "recommendation_invalid_input"
	ErrorPrincipalRejected                ErrorCode = "recommendation_principal_rejected"
	ErrorAnalyticsUnavailable             ErrorCode = "recommendation_analytics_unavailable"
	ErrorTrainingConfigurationUnavailable ErrorCode = "recommendation_training_configuration_unavailable"
	ErrorPreflightFailed                  ErrorCode = "recommendation_preflight_failed"
	ErrorInvalidBundle                    ErrorCode = "recommendation_invalid_bundle"
	ErrorInvalidArtifact                  ErrorCode = "recommendation_invalid_artifact"
	ErrorStateConflict                    ErrorCode = "recommendation_state_conflict"
	ErrorLeaseLost                        ErrorCode = "recommendation_lease_lost"
	ErrorTrainer                          ErrorCode = "recommendation_trainer_failure"
	ErrorStoredDataInvalid                ErrorCode = "recommendation_stored_data_invalid"
	ErrorDatabase                         ErrorCode = "recommendation_database_failure"
	ErrorCanceled                         ErrorCode = "recommendation_canceled"
)

type PreflightFailure struct {
	IssueCode   string
	ProblemKeys []string
}

func (failure *PreflightFailure) Error() string {
	return "recommendation preflight failed: " + failure.IssueCode
}

type AnalyticsHeadConflict struct {
	ExpectedGenerationID int64
	ExpectedHeadRevision int64
	CurrentGenerationID  int64
	CurrentHeadRevision  int64
}

func (conflict *AnalyticsHeadConflict) Error() string {
	return "analytics head differs from the reviewed head"
}

func preflightFailure(issueCode string, problemKeys []string) error {
	return domainError(ErrorPreflightFailed, true, "preflight recommendation training", &PreflightFailure{
		IssueCode: issueCode, ProblemKeys: problemKeys,
	})
}

type Error struct {
	Code      ErrorCode
	Permanent bool
	Op        string
	Cause     error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s during %s: %v", err.Code, err.Op, err.Cause)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func CodeOf(err error) ErrorCode {
	var owned *Error
	if errors.As(err, &owned) {
		return owned.Code
	}
	return ""
}

func IsPermanent(err error) bool {
	var owned *Error
	return errors.As(err, &owned) && owned.Permanent
}

func domainError(code ErrorCode, permanent bool, operation string, cause error) error {
	return &Error{Code: code, Permanent: permanent, Op: operation, Cause: cause}
}

func databaseError(operation string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return domainError(ErrorCanceled, false, operation, cause)
	}
	return domainError(ErrorDatabase, false, operation, cause)
}

func mapPrincipalError(operation string, cause error) error {
	switch principalguard.CodeOf(cause) {
	case principalguard.ErrorInvalidPrincipal, principalguard.ErrorRejected:
		return domainError(ErrorPrincipalRejected, true, operation, cause)
	case principalguard.ErrorStoredData:
		return domainError(ErrorStoredDataInvalid, true, operation, cause)
	case principalguard.ErrorCanceled:
		return domainError(ErrorCanceled, false, operation, cause)
	case principalguard.ErrorDatabase:
		return domainError(ErrorDatabase, false, operation, cause)
	default:
		return domainError(ErrorStoredDataInvalid, true, operation, cause)
	}
}
