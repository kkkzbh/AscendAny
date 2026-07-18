package auth

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable machine-readable authentication failure code.
type ErrorCode string

const (
	ErrorInvalidConfiguration   ErrorCode = "auth_invalid_configuration"
	ErrorInvalidInput           ErrorCode = "auth_invalid_input"
	ErrorAdminAlreadyExists     ErrorCode = "auth_admin_already_exists"
	ErrorPasswordWorkSaturated  ErrorCode = "auth_password_work_saturated"
	ErrorRegistrationUsername   ErrorCode = "auth_registration_username_unavailable"
	ErrorRegistrationIdentity   ErrorCode = "auth_registration_identity_unavailable"
	ErrorSSODisabled            ErrorCode = "AUTH_SSO_DISABLED"
	ErrorLocalPasswordEnabled   ErrorCode = "AUTH_LOCAL_PASSWORD_ALREADY_ENABLED"
	ErrorAuthentication         ErrorCode = "auth_authentication_rejected"
	ErrorRefreshReuse           ErrorCode = "auth_refresh_reuse_detected"
	ErrorForbidden              ErrorCode = "auth_forbidden"
	ErrorEnrollmentIdentity     ErrorCode = "auth_enrollment_identity_unavailable"
	ErrorEnrollmentRejected     ErrorCode = "auth_enrollment_rejected"
	ErrorEnrollmentNotRevocable ErrorCode = "auth_enrollment_not_revocable"
	ErrorSessionNotFound        ErrorCode = "auth_session_not_found"
	ErrorCanceled               ErrorCode = "auth_canceled"
	ErrorDatabase               ErrorCode = "auth_database_failure"
	ErrorInternal               ErrorCode = "auth_internal_failure"
)

// Error carries a public code and message while preserving an internal cause.
type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e != nil && e.Code == other.Code
}

func authError(code ErrorCode, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// ErrorCodeOf classifies an authentication error without exposing its cause.
// An empty code means that err is not owned by the authentication package.
func ErrorCodeOf(err error) ErrorCode {
	var authErr *Error
	if errors.As(err, &authErr) {
		return authErr.Code
	}
	return ""
}

func errorCode(err error) ErrorCode { return ErrorCodeOf(err) }

func authenticationRejected(cause error) error {
	return authError(ErrorAuthentication, "Authentication was rejected.", cause)
}

func adminAlreadyExists() error {
	return authError(ErrorAdminAlreadyExists, "An administrator account already exists.", nil)
}

func passwordWorkSaturated() error {
	return authError(ErrorPasswordWorkSaturated, "Password verification capacity is exhausted.", nil)
}

func registrationUsernameUnavailable() error {
	return authError(ErrorRegistrationUsername, "Registration username is unavailable.", nil)
}

func registrationIdentityUnavailable() error {
	return authError(ErrorRegistrationIdentity, "Registration identity is unavailable.", nil)
}

func ssoDisabled() error {
	return authError(ErrorSSODisabled, "SSO is disabled on this server.", nil)
}

func localPasswordAlreadyEnabled() error {
	return authError(ErrorLocalPasswordEnabled, "Local password login is already enabled for this account.", nil)
}

func refreshReuseDetected() error {
	return authError(ErrorRefreshReuse, "Refresh token reuse was detected and the session was revoked.", nil)
}

func forbidden() error {
	return authError(ErrorForbidden, "Administrator authorization is required.", nil)
}

func enrollmentIdentityUnavailable() error {
	return authError(ErrorEnrollmentIdentity, "The enrollment identity is unavailable.", nil)
}

func enrollmentRejected(cause error) error {
	return authError(ErrorEnrollmentRejected, "Enrollment was rejected.", cause)
}

func enrollmentNotRevocable() error {
	return authError(ErrorEnrollmentNotRevocable, "The enrollment grant cannot be revoked.", nil)
}

func sessionNotFound() error {
	return authError(ErrorSessionNotFound, "The account session does not exist.", nil)
}
