package agentnotes

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "agent_notes_invalid_configuration"
	ErrorInvalidQuery         ErrorCode = "agent_notes_invalid_query"
	ErrorPrincipalRejected    ErrorCode = "agent_notes_principal_rejected"
	ErrorNotFound             ErrorCode = "agent_notes_not_found"
	ErrorCursorInvalid        ErrorCode = "agent_notes_cursor_invalid"
	ErrorHeadConflict         ErrorCode = "agent_notes_head_conflict"
	ErrorStateConflict        ErrorCode = "agent_notes_state_conflict"
	ErrorIdempotencyConflict  ErrorCode = "agent_notes_idempotency_conflict"
	ErrorStoredDataInvalid    ErrorCode = "agent_notes_stored_data_invalid"
	ErrorDatabase             ErrorCode = "agent_notes_database_failure"
	ErrorCanceled             ErrorCode = "agent_notes_canceled"
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

func notesError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return notesError(ErrorCanceled, operation, err)
	}
	return notesError(ErrorDatabase, operation, err)
}
