package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

const MaxEnrollmentLifetime = 7 * 24 * time.Hour

type EnrollmentIssueInput struct {
	Username      string
	DisplayName   string
	StudentNumber string
	ExpiresAt     time.Time
}

type EnrollmentClaimInput struct {
	Token    string
	Password string
}

type EnrollmentGrant struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"displayName"`
	StudentNumber   string    `json:"studentNumber"`
	IssuerAccountID string    `json:"issuerAccountId"`
	IssuedAt        time.Time `json:"issuedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type IssuedEnrollment struct {
	Grant EnrollmentGrant `json:"grant"`
	Token string          `json:"token"`
}

type IssueEnrollmentCommand struct {
	Grant                      EnrollmentGrant
	SecretDigest               [sha256.Size]byte
	ExpectedIssuerAuthRevision int64
	IssuerSessionID            string
}

type IssueEnrollmentStatus uint8

const (
	EnrollmentIssued IssueEnrollmentStatus = iota + 1
	EnrollmentIssueIdentityUnavailable
	EnrollmentIssueIssuerRejected
	EnrollmentIssueExpired
)

type IssueEnrollmentResult struct {
	Status IssueEnrollmentStatus
	Grant  EnrollmentGrant
}

type RevokeEnrollmentCommand struct {
	GrantID                     string
	RevokerAccountID            string
	ExpectedRevokerAuthRevision int64
	RevokerSessionID            string
	Now                         time.Time
}

type RevokeEnrollmentStatus uint8

const (
	EnrollmentRevoked RevokeEnrollmentStatus = iota + 1
	EnrollmentRevokeNotRevocable
	EnrollmentRevokeIssuerRejected
)

type ClaimEnrollmentCommand struct {
	SecretDigest  [sha256.Size]byte
	AccountID     string
	PasswordPHC   string
	SessionID     string
	RefreshToken  NewRefreshToken
	Now           time.Time
	SessionExpiry time.Time
}

type ClaimEnrollmentStatus uint8

const (
	EnrollmentClaimed ClaimEnrollmentStatus = iota + 1
	EnrollmentClaimRejected
)

type ClaimEnrollmentResult struct {
	Status          ClaimEnrollmentStatus
	Account         AccountRecord
	AuthenticatedAt time.Time
}

func (s *Service) IssueEnrollment(
	ctx context.Context,
	accessToken string,
	input EnrollmentIssueInput,
) (IssuedEnrollment, error) {
	if err := validateAuthContext(ctx); err != nil {
		return IssuedEnrollment{}, err
	}
	now := canonicalAuthTime(s.clock.Now())
	issuer, issuerSessionID, err := s.authorizeEnrollmentAdmin(ctx, accessToken, now)
	if err != nil {
		return IssuedEnrollment{}, err
	}

	identity, expiresAt, err := validateEnrollmentIssueInput(input, now)
	if err != nil {
		return IssuedEnrollment{}, err
	}
	grantID, err := newUUIDv4(s.random)
	if err != nil {
		return IssuedEnrollment{}, err
	}
	issuedToken, err := issueEnrollmentToken(s.random)
	if err != nil {
		return IssuedEnrollment{}, err
	}
	grant := EnrollmentGrant{
		ID:              grantID,
		Username:        identity.Username,
		DisplayName:     identity.DisplayName,
		StudentNumber:   identity.StudentNumber,
		IssuerAccountID: issuer.ID,
		IssuedAt:        now,
		ExpiresAt:       expiresAt,
	}
	result, err := s.repository.IssueEnrollment(ctx, IssueEnrollmentCommand{
		Grant:                      grant,
		SecretDigest:               issuedToken.Digest,
		ExpectedIssuerAuthRevision: issuer.AuthRevision,
		IssuerSessionID:            issuerSessionID,
	})
	if err != nil {
		return IssuedEnrollment{}, err
	}
	switch result.Status {
	case EnrollmentIssued:
		if !sameIssuedEnrollmentGrant(result.Grant, grant) {
			return IssuedEnrollment{}, authError(ErrorInternal, "Issued enrollment grant does not match its command.", nil)
		}
		return IssuedEnrollment{Grant: result.Grant, Token: issuedToken.Serialized}, nil
	case EnrollmentIssueIdentityUnavailable:
		return IssuedEnrollment{}, enrollmentIdentityUnavailable()
	case EnrollmentIssueIssuerRejected:
		return IssuedEnrollment{}, authenticationRejected(nil)
	case EnrollmentIssueExpired:
		return IssuedEnrollment{}, authError(ErrorInvalidInput, "Enrollment expiry elapsed before the grant was issued.", nil)
	default:
		return IssuedEnrollment{}, authError(ErrorInternal, "Enrollment issue result is invalid.", nil)
	}
}

func (s *Service) RevokeEnrollment(ctx context.Context, accessToken, grantID string) error {
	if err := validateAuthContext(ctx); err != nil {
		return err
	}
	now := canonicalAuthTime(s.clock.Now())
	revoker, revokerSessionID, err := s.authorizeEnrollmentAdmin(ctx, accessToken, now)
	if err != nil {
		return err
	}
	if _, err := parseUUIDv4(grantID); err != nil {
		return authError(ErrorInvalidInput, "Enrollment grant ID must be a canonical UUIDv4.", err)
	}
	status, err := s.repository.RevokeEnrollment(ctx, RevokeEnrollmentCommand{
		GrantID:                     grantID,
		RevokerAccountID:            revoker.ID,
		ExpectedRevokerAuthRevision: revoker.AuthRevision,
		RevokerSessionID:            revokerSessionID,
		Now:                         now,
	})
	if err != nil {
		return err
	}
	switch status {
	case EnrollmentRevoked:
		return nil
	case EnrollmentRevokeNotRevocable:
		return enrollmentNotRevocable()
	case EnrollmentRevokeIssuerRejected:
		return authenticationRejected(nil)
	default:
		return authError(ErrorInternal, "Enrollment revoke result is invalid.", nil)
	}
}

func (s *Service) authorizeEnrollmentAdmin(
	ctx context.Context,
	accessToken string,
	now time.Time,
) (Account, string, error) {
	principal, err := s.jwt.ParseAt(accessToken, now)
	if err != nil {
		return Account{}, "", err
	}
	snapshot, err := s.repository.LoadPrincipal(ctx, principal.AccountID, principal.SessionID, now)
	if err != nil {
		return Account{}, "", err
	}
	if !snapshot.Found || !principalSnapshotActive(snapshot, principal, now) {
		return Account{}, "", authenticationRejected(nil)
	}
	if snapshot.Account.Role != RoleAdmin {
		return Account{}, "", forbidden()
	}
	return snapshot.Account.Account, snapshot.Session.ID, nil
}

func (s *Service) ClaimEnrollment(ctx context.Context, input EnrollmentClaimInput) (AuthResult, error) {
	if err := validateAuthContext(ctx); err != nil {
		return AuthResult{}, err
	}
	digest, err := parseEnrollmentToken(input.Token)
	if err != nil {
		return AuthResult{}, enrollmentRejected(err)
	}
	if err := validatePassword(input.Password); err != nil {
		return AuthResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return AuthResult{}, canceled(err)
	}

	release, acquired := s.passwordWork.tryAcquire()
	if !acquired {
		return AuthResult{}, passwordWorkSaturated()
	}
	passwordPHC, hashErr := s.passwords.Hash(input.Password)
	release()
	if hashErr != nil {
		return AuthResult{}, hashErr
	}
	if err := ctx.Err(); err != nil {
		return AuthResult{}, canceled(err)
	}

	accountID, sessionID, credential, csrf, jwtID, err := s.issueSessionCredentials()
	if err != nil {
		return AuthResult{}, err
	}
	now := canonicalAuthTime(s.clock.Now())
	sessionExpiry := now.Add(s.refreshTTL)
	result, err := s.repository.ClaimEnrollment(ctx, ClaimEnrollmentCommand{
		SecretDigest:  digest,
		AccountID:     accountID,
		PasswordPHC:   passwordPHC,
		SessionID:     sessionID,
		RefreshToken:  newRefreshToken(credential, csrf, now, sessionExpiry),
		Now:           now,
		SessionExpiry: sessionExpiry,
	})
	if err != nil {
		return AuthResult{}, err
	}
	if result.Status != EnrollmentClaimed {
		return AuthResult{}, enrollmentRejected(nil)
	}
	if err := validateAccountRecord(result.Account); err != nil ||
		result.Account.ID != accountID ||
		result.Account.Role != RoleStudent ||
		result.Account.PasswordPHC != passwordPHC ||
		result.AuthenticatedAt.IsZero() {
		return AuthResult{}, authError(ErrorInternal, "Claimed enrollment account is invalid.", err)
	}
	return s.authResult(result.Account.Account, sessionID, jwtID, credential, csrf, result.AuthenticatedAt)
}

func sameIssuedEnrollmentGrant(actual, command EnrollmentGrant) bool {
	return actual.ID == command.ID &&
		actual.Username == command.Username &&
		actual.DisplayName == command.DisplayName &&
		actual.StudentNumber == command.StudentNumber &&
		actual.IssuerAccountID == command.IssuerAccountID &&
		actual.ExpiresAt.Equal(command.ExpiresAt) &&
		!actual.IssuedAt.IsZero() &&
		actual.IssuedAt.Before(actual.ExpiresAt)
}

type enrollmentIdentity struct {
	Username      string
	DisplayName   string
	StudentNumber string
}

func validateEnrollmentIssueInput(input EnrollmentIssueInput, now time.Time) (enrollmentIdentity, time.Time, error) {
	if err := validateUsername(input.Username); err != nil {
		return enrollmentIdentity{}, time.Time{}, err
	}
	displayName, err := validateTrimmedField(
		"Display name",
		input.DisplayName,
		MinDisplayNameBytes,
		MaxDisplayNameBytes,
	)
	if err != nil {
		return enrollmentIdentity{}, time.Time{}, err
	}
	studentNumber, err := validateTrimmedField(
		"Student number",
		input.StudentNumber,
		MinStudentNumberBytes,
		MaxStudentNumberBytes,
	)
	if err != nil {
		return enrollmentIdentity{}, time.Time{}, err
	}
	expiresAt := canonicalAuthTime(input.ExpiresAt)
	if input.ExpiresAt.IsZero() || !expiresAt.After(now) || expiresAt.After(now.Add(MaxEnrollmentLifetime)) {
		return enrollmentIdentity{}, time.Time{}, authError(
			ErrorInvalidInput,
			"Enrollment expiry must be after issuance and no more than seven days later.",
			nil,
		)
	}
	return enrollmentIdentity{
		Username:      input.Username,
		DisplayName:   displayName,
		StudentNumber: studentNumber,
	}, expiresAt, nil
}

func validateAuthContext(ctx context.Context) error {
	if ctx == nil {
		return authError(ErrorInvalidConfiguration, "Authentication context is required.", nil)
	}
	if err := ctx.Err(); err != nil {
		return canceled(err)
	}
	return nil
}

func canceled(cause error) error {
	if !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return authError(ErrorInternal, "Authentication cancellation cause is invalid.", cause)
	}
	return authError(ErrorCanceled, "Authentication operation was canceled.", cause)
}

func canonicalAuthTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return time.UnixMicro(value.UnixMicro()).UTC()
}
