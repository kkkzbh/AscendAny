package chatagent

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "chat_agent_invalid_configuration"
	ErrorInvalidInput         ErrorCode = "chat_agent_invalid_input"
	ErrorPrincipalRejected    ErrorCode = "chat_agent_principal_rejected"
	ErrorNotFound             ErrorCode = "chat_agent_not_found"
	ErrorThreadCursorInvalid  ErrorCode = "chat_agent_thread_cursor_invalid"
	ErrorEventCursorInvalid   ErrorCode = "chat_agent_event_cursor_invalid"
	ErrorIdempotencyConflict  ErrorCode = "chat_agent_idempotency_conflict"
	ErrorThreadKindConflict   ErrorCode = "chat_agent_thread_kind_conflict"
	ErrorAutoAnalysisConflict ErrorCode = "chat_agent_auto_analysis_configuration_conflict"
	ErrorConfigurationMissing ErrorCode = "chat_agent_configuration_missing"
	ErrorAnalyticsConflict    ErrorCode = "chat_agent_analytics_conflict"
	ErrorLeaseLost            ErrorCode = "chat_agent_lease_lost"
	ErrorProvider             ErrorCode = "chat_agent_provider_failure"
	ErrorStoredDataInvalid    ErrorCode = "chat_agent_stored_data_invalid"
	ErrorDatabase             ErrorCode = "chat_agent_database_failure"
	ErrorCanceled             ErrorCode = "chat_agent_canceled"
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

func databaseFailure(operation string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return domainError(ErrorCanceled, false, operation, cause)
	}
	return domainError(ErrorDatabase, false, operation, cause)
}
