package principalguard

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type ErrorCode string

const (
	ErrorInvalidPrincipal ErrorCode = "principal_guard_invalid_principal"
	ErrorRejected         ErrorCode = "principal_guard_rejected"
	ErrorStoredData       ErrorCode = "principal_guard_stored_data_invalid"
	ErrorDatabase         ErrorCode = "principal_guard_database_failure"
	ErrorCanceled         ErrorCode = "principal_guard_canceled"
)

type Error struct {
	Code  ErrorCode
	Cause error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %v", err.Code, err.Cause)
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

type RowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type RoleSet map[auth.Role]struct{}

func Roles(values ...auth.Role) RoleSet {
	result := make(RoleSet, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

type Resolved struct {
	AccountDatabaseID int64
	AccountID         string
	SessionDatabaseID int64
	SessionID         string
	Role              auth.Role
	AuthRevision      int64
	ActorID           *int64
	StudentNumber     *string
}

func Resolve(
	ctx context.Context,
	queryer RowQuerier,
	principal auth.AccessPrincipal,
	allowedRoles RoleSet,
) (Resolved, error) {
	return resolve(ctx, queryer, principal, allowedRoles, false)
}

// ResolveForUpdate locks the exact account and session rows while applying the
// same principal and actor-binding checks as Resolve. Product mutations call it
// inside their owning transaction before reading or changing mutable state.
func ResolveForUpdate(
	ctx context.Context,
	queryer RowQuerier,
	principal auth.AccessPrincipal,
	allowedRoles RoleSet,
) (Resolved, error) {
	return resolve(ctx, queryer, principal, allowedRoles, true)
}

func resolve(
	ctx context.Context,
	queryer RowQuerier,
	principal auth.AccessPrincipal,
	allowedRoles RoleSet,
	lock bool,
) (Resolved, error) {
	if ctx == nil || queryer == nil || len(allowedRoles) == 0 ||
		!canonicalUUIDv4.MatchString(principal.AccountID) ||
		!canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision <= 0 {
		return Resolved{}, &Error{Code: ErrorInvalidPrincipal, Cause: errors.New("principal, role policy, context, and query owner are required")}
	}
	if _, allowed := allowedRoles[principal.Role]; !allowed {
		return Resolved{}, &Error{Code: ErrorRejected, Cause: errors.New("principal role is outside the operation policy")}
	}
	var resolved Resolved
	var role string
	var sessionRevision int64
	var identifierActorID *int64
	var identifierValue *string
	query := `
SELECT account.account_id,
       account.public_id::text,
       account.role,
       account.auth_revision,
       account.actor_id,
       account.student_number,
       session.session_id,
       session.public_id::text,
       session.auth_revision,
       identifier.actor_id,
       identifier.identifier_value
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
LEFT JOIN ascendany.pintia_actor_identifiers AS identifier
  ON identifier.actor_id = account.actor_id
 AND identifier.identifier_kind = 'student_number'
 AND identifier.identifier_value = account.student_number
WHERE account.public_id = $1::uuid
  AND account.auth_revision = $2
  AND account.role = $3
  AND account.disabled_at IS NULL
  AND session.public_id = $4::uuid
  AND session.auth_revision = $2
  AND session.revoked_at IS NULL
	AND session.expires_at > transaction_timestamp()`
	if lock {
		query += `
FOR UPDATE OF account, session`
	}
	err := queryer.QueryRow(ctx, query,
		principal.AccountID,
		principal.AuthRevision,
		string(principal.Role),
		principal.SessionID,
	).Scan(
		&resolved.AccountDatabaseID,
		&resolved.AccountID,
		&role,
		&resolved.AuthRevision,
		&resolved.ActorID,
		&resolved.StudentNumber,
		&resolved.SessionDatabaseID,
		&resolved.SessionID,
		&sessionRevision,
		&identifierActorID,
		&identifierValue,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolved{}, &Error{Code: ErrorRejected, Cause: err}
	}
	if err != nil {
		code := ErrorDatabase
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = ErrorCanceled
		}
		return Resolved{}, &Error{Code: code, Cause: err}
	}
	resolved.Role = auth.Role(role)
	if resolved.AccountDatabaseID <= 0 || resolved.SessionDatabaseID <= 0 ||
		resolved.AccountID != principal.AccountID || resolved.SessionID != principal.SessionID ||
		resolved.Role != principal.Role || resolved.AuthRevision != principal.AuthRevision ||
		sessionRevision != principal.AuthRevision {
		return Resolved{}, &Error{Code: ErrorStoredData, Cause: errors.New("resolved account and session differ from the immutable principal")}
	}
	switch resolved.Role {
	case auth.RoleStudent:
		if resolved.ActorID == nil || *resolved.ActorID <= 0 || resolved.StudentNumber == nil ||
			strings.TrimSpace(*resolved.StudentNumber) != *resolved.StudentNumber || *resolved.StudentNumber == "" ||
			identifierActorID == nil || *identifierActorID != *resolved.ActorID || identifierValue == nil ||
			*identifierValue != *resolved.StudentNumber {
			return Resolved{}, &Error{Code: ErrorStoredData, Cause: errors.New("student account and imported actor binding are inconsistent")}
		}
	case auth.RoleAdmin:
		if resolved.ActorID != nil || resolved.StudentNumber != nil || identifierActorID != nil || identifierValue != nil {
			return Resolved{}, &Error{Code: ErrorStoredData, Cause: errors.New("administrator account unexpectedly owns a student actor binding")}
		}
	default:
		return Resolved{}, &Error{Code: ErrorStoredData, Cause: errors.New("resolved account role is invalid")}
	}
	return resolved, nil
}
