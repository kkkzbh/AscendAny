package examgeneration

import (
	"context"
	"errors"
	"fmt"

	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "exam_generation_invalid_configuration"
	ErrorInvalidInput         ErrorCode = "exam_generation_invalid_input"
	ErrorPrincipalRejected    ErrorCode = "exam_generation_principal_rejected"
	ErrorEventCursorInvalid   ErrorCode = "exam_generation_event_cursor_invalid"
	ErrorStoredDataInvalid    ErrorCode = "exam_generation_stored_data_invalid"
	ErrorDatabase             ErrorCode = "exam_generation_database_failure"
	ErrorCanceled             ErrorCode = "exam_generation_canceled"
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
	case principalguard.ErrorInvalidPrincipal:
		return domainError(ErrorInvalidInput, true, operation, cause)
	case principalguard.ErrorRejected:
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
