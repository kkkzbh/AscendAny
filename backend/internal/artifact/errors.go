package artifact

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable machine-readable artifact-store failure category.
type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "invalid_configuration"
	ErrorInvalidArgument      ErrorCode = "invalid_argument"
	ErrorInvalidHash          ErrorCode = "invalid_hash"
	ErrorEmptyArtifact        ErrorCode = "empty_artifact"
	ErrorPayloadTooLarge      ErrorCode = "payload_too_large"
	ErrorCanceled             ErrorCode = "canceled"
	ErrorNotFound             ErrorCode = "not_found"
	ErrorCorrupt              ErrorCode = "corrupt"
	ErrorReferenceCheck       ErrorCode = "reference_check_failed"
	ErrorIO                   ErrorCode = "io_failure"
)

// StoreError keeps callers independent from operating-system error strings.
// The wrapped error remains available to errors.Is and errors.As.
type StoreError struct {
	Code ErrorCode
	Op   string
	Err  error
}

func (e *StoreError) Error() string {
	if e.Op == "" {
		return fmt.Sprintf("artifact %s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("artifact %s during %s: %v", e.Code, e.Op, e.Err)
}

func (e *StoreError) Unwrap() error {
	return e.Err
}

// CodeOf returns the stable error code from err, including joined or wrapped
// errors.
func CodeOf(err error) (ErrorCode, bool) {
	var storeErr *StoreError
	if !errors.As(err, &storeErr) {
		return "", false
	}
	return storeErr.Code, true
}

func storeError(code ErrorCode, op string, err error) error {
	return &StoreError{Code: code, Op: op, Err: err}
}
