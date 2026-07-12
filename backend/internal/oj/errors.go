package oj

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "oj_invalid_configuration"
	ErrorInvalidInput         ErrorCode = "oj_invalid_input"
	ErrorPrincipalRejected    ErrorCode = "oj_principal_rejected"
	ErrorNotFound             ErrorCode = "oj_not_found"
	ErrorHeadConflict         ErrorCode = "oj_head_conflict"
	ErrorIdempotencyConflict  ErrorCode = "oj_idempotency_conflict"
	ErrorArtifactConflict     ErrorCode = "oj_artifact_conflict"
	ErrorArtifactFailure      ErrorCode = "oj_artifact_failure"
	ErrorPayloadTooLarge      ErrorCode = "oj_payload_too_large"
	ErrorLeaseLost            ErrorCode = "oj_lease_lost"
	ErrorStoredDataInvalid    ErrorCode = "oj_stored_data_invalid"
	ErrorDatabase             ErrorCode = "oj_database_failure"
	ErrorCanceled             ErrorCode = "oj_canceled"
)

type Error struct {
	Code      ErrorCode
	Permanent bool
	Operation string
	Cause     error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s: %v", err.Code, err.Operation, err.Cause)
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

func ojError(code ErrorCode, permanent bool, operation string, cause error) error {
	return &Error{Code: code, Permanent: permanent, Operation: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ojError(ErrorCanceled, false, operation, err)
	}
	return ojError(ErrorDatabase, false, operation, err)
}
