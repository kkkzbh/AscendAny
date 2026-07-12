package importing

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "invalid_configuration"
	ErrorInvalidMediaType     ErrorCode = "invalid_media_type"
	ErrorInvalidPublication   ErrorCode = "invalid_publication"
	ErrorJobCursorInvalid     ErrorCode = "job_cursor_invalid"
	ErrorEventCursorAhead     ErrorCode = "event_cursor_ahead"
	ErrorUUIDGeneration       ErrorCode = "uuid_generation_failed"
	ErrorDatabase             ErrorCode = "database_failure"
	ErrorArtifactMetadata     ErrorCode = "artifact_metadata_conflict"
	ErrorArtifactVerification ErrorCode = "artifact_verification_failed"
	ErrorValidation           ErrorCode = "snapshot_validation_failed"
	ErrorStateConflict        ErrorCode = "job_state_conflict"
	ErrorLeaseLost            ErrorCode = "job_lease_lost"
	ErrorIdentityConflict     ErrorCode = "identity_conflict"
	ErrorSubmissionConflict   ErrorCode = "submission_identity_conflict"
	ErrorHeadConflict         ErrorCode = "exam_head_conflict"
	ErrorManifest             ErrorCode = "analytics_manifest_failed"
	ErrorCanceled             ErrorCode = "canceled"
)

type ImportError struct {
	Code      ErrorCode
	Permanent bool
	Op        string
	Err       error
}

func (e *ImportError) Error() string {
	return fmt.Sprintf("import %s during %s: %v", e.Code, e.Op, e.Err)
}

func (e *ImportError) Unwrap() error {
	return e.Err
}

func CodeOf(err error) (ErrorCode, bool) {
	var importErr *ImportError
	if !errors.As(err, &importErr) {
		return "", false
	}
	return importErr.Code, true
}

func IsPermanent(err error) bool {
	var importErr *ImportError
	return errors.As(err, &importErr) && importErr.Permanent
}

func importError(code ErrorCode, permanent bool, op string, err error) error {
	return &ImportError{Code: code, Permanent: permanent, Op: op, Err: err}
}
