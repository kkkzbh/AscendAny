package pintia

import "fmt"

type ErrorCode string

const (
	ErrorInvalidLimits        ErrorCode = "invalid_limits"
	ErrorPayloadTooLarge      ErrorCode = "payload_too_large"
	ErrorMalformedJSON        ErrorCode = "malformed_json"
	ErrorSchemaViolation      ErrorCode = "schema_violation"
	ErrorSchemaDigestMismatch ErrorCode = "schema_digest_mismatch"
	ErrorLimitExceeded        ErrorCode = "limit_exceeded"
	ErrorSemanticViolation    ErrorCode = "semantic_violation"
)

// ValidationError provides a stable machine-readable category and a precise
// contract path without exposing implementation-specific validator details to
// callers.
type ValidationError struct {
	Code ErrorCode
	Path string
	Err  error
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("%s at %s: %v", e.Code, e.Path, e.Err)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

func validationError(code ErrorCode, path, format string, args ...any) error {
	return &ValidationError{Code: code, Path: path, Err: fmt.Errorf(format, args...)}
}
