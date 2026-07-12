package traineragentserver

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorAuthenticationRejected ErrorCode = "authentication_rejected"
	ErrorCredentialUnavailable  ErrorCode = "credential_unavailable"
	ErrorInvalidRequest         ErrorCode = "invalid_request"
	ErrorUnsupportedProtocol    ErrorCode = "unsupported_protocol"
	ErrorLeaseLost              ErrorCode = "lease_lost"
	ErrorOutputRejected         ErrorCode = "output_rejected"
	ErrorStorageUnavailable     ErrorCode = "storage_unavailable"
	ErrorServiceUnavailable     ErrorCode = "service_unavailable"
)

type Error struct {
	Code      ErrorCode
	Detail    string
	Retryable bool
	Cause     error
}

func (value *Error) Error() string {
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", value.Code, value.Detail)
}

func (value *Error) Unwrap() error {
	if value == nil {
		return nil
	}
	return value.Cause
}

func CodeOf(err error) ErrorCode {
	var owned *Error
	if errors.As(err, &owned) {
		return owned.Code
	}
	return ""
}

func errorValue(code ErrorCode, detail string, retryable bool, cause error) error {
	return &Error{Code: code, Detail: detail, Retryable: retryable, Cause: cause}
}
