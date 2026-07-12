package configuration

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "configuration_invalid_configuration"
	ErrorInvalidQuery         ErrorCode = "configuration_invalid_query"
	ErrorDocumentInvalid      ErrorCode = "configuration_document_invalid"
	ErrorPrincipalRejected    ErrorCode = "configuration_principal_rejected"
	ErrorNotFound             ErrorCode = "configuration_not_found"
	ErrorHeadConflict         ErrorCode = "configuration_head_conflict"
	ErrorDocumentConflict     ErrorCode = "configuration_document_conflict"
	ErrorStoredDataInvalid    ErrorCode = "configuration_stored_data_invalid"
	ErrorDatabase             ErrorCode = "configuration_database_failure"
	ErrorCanceled             ErrorCode = "configuration_canceled"
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

func configurationError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return configurationError(ErrorCanceled, operation, err)
	}
	return configurationError(ErrorDatabase, operation, err)
}
