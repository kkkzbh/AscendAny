package analytics

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "invalid_configuration"
	ErrorInvalidManifest      ErrorCode = "invalid_manifest"
	ErrorAlgorithmMismatch    ErrorCode = "algorithm_mismatch"
	ErrorConfigMismatch       ErrorCode = "config_mismatch"
	ErrorInvalidDataset       ErrorCode = "invalid_dataset"
	ErrorLeaseLost            ErrorCode = "lease_lost"
	ErrorStateConflict        ErrorCode = "state_conflict"
	ErrorDatabase             ErrorCode = "database_failure"
	ErrorCanceled             ErrorCode = "canceled"
)

type AnalyticsError struct {
	Code      ErrorCode
	Permanent bool
	Op        string
	Err       error
}

func (e *AnalyticsError) Error() string {
	return fmt.Sprintf("analytics %s during %s: %v", e.Code, e.Op, e.Err)
}

func (e *AnalyticsError) Unwrap() error {
	return e.Err
}

func CodeOf(err error) (ErrorCode, bool) {
	var analyticsErr *AnalyticsError
	if !errors.As(err, &analyticsErr) {
		return "", false
	}
	return analyticsErr.Code, true
}

func IsPermanent(err error) bool {
	var analyticsErr *AnalyticsError
	return errors.As(err, &analyticsErr) && analyticsErr.Permanent
}

func analyticsError(code ErrorCode, permanent bool, op string, err error) error {
	return &AnalyticsError{Code: code, Permanent: permanent, Op: op, Err: err}
}
