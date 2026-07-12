package feedback

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "feedback_invalid_configuration"
	ErrorInvalidInput         ErrorCode = "feedback_invalid_input"
	ErrorPrincipalRejected    ErrorCode = "feedback_principal_rejected"
	ErrorRateLimited          ErrorCode = "feedback_rate_limited"
	ErrorIdempotencyConflict  ErrorCode = "feedback_idempotency_conflict"
	ErrorDeliveryUnavailable  ErrorCode = "feedback_delivery_unavailable"
	ErrorLeaseLost            ErrorCode = "feedback_lease_lost"
	ErrorProvider             ErrorCode = "feedback_provider_failure"
	ErrorStoredDataInvalid    ErrorCode = "feedback_stored_data_invalid"
	ErrorDatabase             ErrorCode = "feedback_database_failure"
	ErrorCanceled             ErrorCode = "feedback_canceled"
)

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

func feedbackError(code ErrorCode, permanent bool, operation string, cause error) error {
	return &Error{Code: code, Permanent: permanent, Op: operation, Cause: cause}
}

func databaseFailure(operation string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return feedbackError(ErrorCanceled, false, operation, cause)
	}
	return feedbackError(ErrorDatabase, false, operation, cause)
}
