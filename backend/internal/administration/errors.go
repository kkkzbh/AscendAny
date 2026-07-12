package administration

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "administration_invalid_configuration"
	ErrorInvalidQuery         ErrorCode = "administration_invalid_query"
	ErrorCursorInvalid        ErrorCode = "administration_cursor_invalid"
	ErrorPrincipalRejected    ErrorCode = "administration_principal_rejected"
	ErrorTargetNotFound       ErrorCode = "administration_target_not_found"
	ErrorSelfDisable          ErrorCode = "administration_self_disable_rejected"
	ErrorConcurrentMutation   ErrorCode = "administration_concurrent_mutation"
	ErrorStoredDataInvalid    ErrorCode = "administration_stored_data_invalid"
	ErrorDatabase             ErrorCode = "administration_database_failure"
	ErrorCanceled             ErrorCode = "administration_canceled"
)

type Error struct {
	Code  ErrorCode
	Op    string
	Cause error
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

func adminError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return adminError(ErrorCanceled, operation, err)
	}
	return adminError(ErrorDatabase, operation, err)
}
