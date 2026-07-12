package examcatalog

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "exam_catalog_invalid_configuration"
	ErrorInvalidQuery         ErrorCode = "exam_catalog_invalid_query"
	ErrorCursorInvalid        ErrorCode = "exam_catalog_cursor_invalid"
	ErrorPrincipalRejected    ErrorCode = "exam_catalog_principal_rejected"
	ErrorStoredDataInvalid    ErrorCode = "exam_catalog_stored_data_invalid"
	ErrorDatabase             ErrorCode = "exam_catalog_database_failure"
	ErrorCanceled             ErrorCode = "exam_catalog_canceled"
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

func catalogError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return catalogError(ErrorCanceled, operation, err)
	}
	return catalogError(ErrorDatabase, operation, err)
}
