package auth

import (
	"context"
	"time"
)

type NewRefreshToken struct {
	ID           string
	SecretDigest [32]byte
	CSRFDigest   [32]byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type CreateSessionCommand struct {
	AccountID            string
	ExpectedAuthRevision int64
	SessionID            string
	RefreshToken         NewRefreshToken
	Now                  time.Time
	SessionExpiry        time.Time
}

type CreateSessionStatus uint8

const (
	SessionCreated CreateSessionStatus = iota + 1
	SessionRejected
)

type CreateSessionResult struct {
	Status  CreateSessionStatus
	Account AccountRecord
}

type RegisterStudentCommand struct {
	Account       AccountRecord
	SessionID     string
	RefreshToken  NewRefreshToken
	Now           time.Time
	SessionExpiry time.Time
}

type RegisterStudentStatus uint8

const (
	StudentRegistered RegisterStudentStatus = iota + 1
	RegistrationUsernameUnavailable
	RegistrationIdentityUnavailable
)

type RegisterStudentResult struct {
	Status          RegisterStudentStatus
	Account         AccountRecord
	AuthenticatedAt time.Time
}

type PrincipalSnapshot struct {
	Found   bool
	Account AccountRecord
	Session SessionRecord
}

type RefreshSnapshot struct {
	Found             bool
	AccountDatabaseID int64
	TokenDatabaseID   int64
	TokenID           string
	SecretDigest      [32]byte
	CSRFDigest        [32]byte
	TokenExpiresAt    time.Time
	UsedAt            *time.Time
	TokenRevokedAt    *time.Time
	Session           SessionRecord
	Account           AccountRecord
}

type RefreshDecisionKind uint8

const (
	RefreshReject RefreshDecisionKind = iota + 1
	RefreshRotate
	RefreshRevokeReuse
	RefreshLogout
)

// RefreshDecision is a transaction outcome, not an error. A repository must
// commit RefreshRevokeReuse before the service returns its authentication error.
type RefreshDecision struct {
	Kind      RefreshDecisionKind
	NextToken *NewRefreshToken
}

type RefreshDecider func(RefreshSnapshot) RefreshDecision

type Repository interface {
	RegisterStudent(context.Context, RegisterStudentCommand) (RegisterStudentResult, error)
	FindLoginAccount(context.Context, string) (AccountRecord, bool, error)
	CreateSession(context.Context, CreateSessionCommand) (CreateSessionResult, error)
	TransactRefresh(context.Context, string, time.Time, RefreshDecider) (RefreshDecisionKind, error)
	LoadPrincipal(context.Context, string, string, time.Time) (PrincipalSnapshot, error)
	IssueEnrollment(context.Context, IssueEnrollmentCommand) (IssueEnrollmentResult, error)
	RevokeEnrollment(context.Context, RevokeEnrollmentCommand) (RevokeEnrollmentStatus, error)
	ClaimEnrollment(context.Context, ClaimEnrollmentCommand) (ClaimEnrollmentResult, error)
}
