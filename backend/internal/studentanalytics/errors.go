package studentanalytics

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "student_analytics_invalid_configuration"
	ErrorInvalidQuery         ErrorCode = "student_analytics_invalid_query"
	ErrorForbidden            ErrorCode = "student_analytics_forbidden"
	ErrorPrincipalRejected    ErrorCode = "student_analytics_principal_rejected"
	ErrorStoredDataInvalid    ErrorCode = "student_analytics_stored_data_invalid"
	ErrorDatabase             ErrorCode = "student_analytics_database_failure"
	ErrorCanceled             ErrorCode = "student_analytics_canceled"
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

func studentAnalyticsError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return studentAnalyticsError(ErrorCanceled, operation, err)
	}
	return studentAnalyticsError(ErrorDatabase, operation, err)
}

func storedDataFailure(operation string, err error) error {
	return studentAnalyticsError(ErrorStoredDataInvalid, operation, err)
}
