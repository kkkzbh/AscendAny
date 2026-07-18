package auth

import "time"

const (
	MinPasswordBytes      = 8
	MaxPasswordBytes      = 128
	MinUsernameBytes      = 3
	MaxUsernameBytes      = 32
	MinDisplayNameBytes   = 1
	MaxDisplayNameBytes   = 64
	MinStudentNumberBytes = 1
	MaxStudentNumberBytes = 64
	MinPTANicknameBytes   = 1
	MaxPTANicknameBytes   = 256
)

type Role string

const (
	RoleStudent Role = "student"
	RoleAdmin   Role = "admin"
)

type Account struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"displayName"`
	StudentNumber *string `json:"studentNumber"`
	PTANickname   *string `json:"-"`
	Role          Role    `json:"role"`
	AuthRevision  int64   `json:"authRevision"`
}

type AccountRecord struct {
	Account
	PasswordPHC string     `json:"-"`
	DisabledAt  *time.Time `json:"-"`
}

type SessionRecord struct {
	DatabaseID   int64
	ID           string
	AccountID    string
	AuthRevision int64
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastSeenAt   time.Time
	RevokedAt    *time.Time
}

type LoginInput struct {
	Username string
	Password string
}

type RegistrationInput struct {
	Username      string
	Password      string
	StudentNumber string
	PTANickname   string
}

type SSOExchangeInput struct {
	Token string
}

type LocalPasswordBootstrapInput struct {
	AccessToken string
	NewPassword string
}

type RefreshInput struct {
	RefreshToken string
	CSRFToken    string
}

type LogoutInput struct {
	AccessToken  string
	RefreshToken string
	CSRFToken    string
}

type AuthResult struct {
	AccessToken        string    `json:"accessToken"`
	ExpiresAt          time.Time `json:"expiresAt"`
	CSRFToken          string    `json:"csrfToken"`
	RefreshCookieValue string    `json:"-"`
	Account            Account   `json:"account"`
}

type AccessPrincipal struct {
	AccountID    string
	SessionID    string
	Role         Role
	AuthRevision int64
	JWTID        string
	ExpiresAt    time.Time
}

// AuthenticatedAccount binds the database-authoritative account row to the
// exact access-token principal that selected its active session. Downstream
// services use this value instead of accepting caller-supplied account,
// session, role, or authorization-revision fields.
type AuthenticatedAccount struct {
	Account   Account
	Principal AccessPrincipal
}
