package modelprobe

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorInvalidConfiguration ErrorCode = "model_probe_invalid_configuration"
	ErrorInvalidInput         ErrorCode = "model_probe_invalid_input"
	ErrorConfigurationMissing ErrorCode = "model_probe_configuration_missing"
	ErrorConfigurationKind    ErrorCode = "model_probe_configuration_kind"
	ErrorProviderRejected     ErrorCode = "model_probe_provider_rejected"
	ErrorStoredDataInvalid    ErrorCode = "model_probe_stored_data_invalid"
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

func modelProbeError(code ErrorCode, operation string, cause error) error {
	return &Error{Code: code, Op: operation, Cause: cause}
}
