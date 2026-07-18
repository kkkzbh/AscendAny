package achievement

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "achievement_invalid_configuration"
	ErrorInvalidQuery         ErrorCode = "achievement_invalid_query"
	ErrorForbidden            ErrorCode = "achievement_forbidden"
	ErrorStudentNotFound      ErrorCode = "achievement_student_not_found"
	ErrorPrincipalRejected    ErrorCode = "achievement_principal_rejected"
	ErrorStoredDataInvalid    ErrorCode = "achievement_stored_data_invalid"
	ErrorDatabase             ErrorCode = "achievement_database_failure"
	ErrorCanceled             ErrorCode = "achievement_canceled"
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

func achievementError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return achievementError(ErrorCanceled, operation, err)
	}
	return achievementError(ErrorDatabase, operation, err)
}

func storedDataFailure(operation string, err error) error {
	return achievementError(ErrorStoredDataInvalid, operation, err)
}
