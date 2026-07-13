package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"time"
)

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type ServiceConfig struct {
	Issuer               string
	Audience             string
	JWTSigningPrivateKey ed25519.PrivateKey
	PasswordPepper       []byte
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	Clock                Clock
	Random               io.Reader
	passwordWork         *passwordWorkLimiter
}

type Service struct {
	repository   Repository
	passwords    *PasswordHasher
	jwt          *JWTManager
	clock        Clock
	random       io.Reader
	refreshTTL   time.Duration
	dummyPHC     string
	passwordWork *passwordWorkLimiter
}

func NewService(repository Repository, config ServiceConfig) (*Service, error) {
	if repository == nil {
		return nil, authError(ErrorInvalidConfiguration, "Auth repository is required.", nil)
	}
	if config.Clock == nil {
		return nil, authError(ErrorInvalidConfiguration, "Auth clock is required.", nil)
	}
	if config.Random == nil {
		return nil, authError(ErrorInvalidConfiguration, "Auth random source is required.", nil)
	}
	if config.RefreshTTL <= 0 {
		return nil, authError(ErrorInvalidConfiguration, "Refresh lifetime must be positive.", nil)
	}
	if config.passwordWork == nil {
		return nil, authError(ErrorInvalidConfiguration, "Password work limiter is required.", nil)
	}
	passwords, err := NewPasswordHasher(config.PasswordPepper, config.Random)
	if err != nil {
		return nil, err
	}
	jwtManager, err := NewJWTManager(config.Issuer, config.Audience, config.JWTSigningPrivateKey, config.AccessTTL)
	if err != nil {
		return nil, err
	}
	release, acquired := config.passwordWork.tryAcquire()
	if !acquired {
		return nil, passwordWorkSaturated()
	}
	dummyPHC, err := passwords.Hash("ascendany-dummy-password")
	release()
	if err != nil {
		return nil, err
	}
	return &Service{
		repository:   repository,
		passwords:    passwords,
		jwt:          jwtManager,
		clock:        config.Clock,
		random:       config.Random,
		refreshTTL:   config.RefreshTTL,
		dummyPHC:     dummyPHC,
		passwordWork: config.passwordWork,
	}, nil
}

func ProductionConfig(
	issuer, audience string,
	jwtSigningPrivateKey ed25519.PrivateKey,
	passwordPepper []byte,
	accessTTL, refreshTTL time.Duration,
) ServiceConfig {
	return ServiceConfig{
		Issuer:               issuer,
		Audience:             audience,
		JWTSigningPrivateKey: jwtSigningPrivateKey,
		PasswordPepper:       passwordPepper,
		AccessTTL:            accessTTL,
		RefreshTTL:           refreshTTL,
		Clock:                systemClock{},
		Random:               rand.Reader,
		passwordWork:         productionPasswordWorkLimiter,
	}
}

func (s *Service) Login(ctx context.Context, input LoginInput) (AuthResult, error) {
	now := s.clock.Now()
	if err := validateUsername(input.Username); err != nil {
		return AuthResult{}, authenticationRejected(err)
	}
	if err := validatePassword(input.Password); err != nil {
		return AuthResult{}, authenticationRejected(err)
	}
	account, found, err := s.repository.FindLoginAccount(ctx, input.Username)
	if err != nil {
		return AuthResult{}, err
	}
	phc := s.dummyPHC
	if found {
		phc = account.PasswordPHC
	}
	verified, verifyErr := s.verifyLoginPassword(input.Password, phc)
	if verifyErr != nil {
		return AuthResult{}, verifyErr
	}
	if !found || !verified || account.DisabledAt != nil {
		return AuthResult{}, authenticationRejected(nil)
	}
	sessionID, err := newUUIDv4(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	credential, err := issueRefreshCredential(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	csrf, err := issueCSRFToken(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	jwtID, err := newUUIDv4(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	sessionExpiry := now.Add(s.refreshTTL)
	created, err := s.repository.CreateSession(ctx, CreateSessionCommand{
		AccountID:            account.ID,
		ExpectedAuthRevision: account.AuthRevision,
		SessionID:            sessionID,
		Now:                  now,
		SessionExpiry:        sessionExpiry,
		RefreshToken:         newRefreshToken(credential, csrf, now, sessionExpiry),
	})
	if err != nil {
		return AuthResult{}, err
	}
	if created.Status != SessionCreated {
		return AuthResult{}, authenticationRejected(nil)
	}
	return s.authResult(created.Account.Account, sessionID, jwtID, credential, csrf, now)
}

func (s *Service) verifyLoginPassword(password, phc string) (bool, error) {
	release, acquired := s.passwordWork.tryAcquire()
	if !acquired {
		return false, passwordWorkSaturated()
	}
	defer release()
	verified, err := s.passwords.Verify(password, phc)
	if err != nil {
		_, _ = s.passwords.Verify(password, s.dummyPHC)
		return false, err
	}
	return verified, nil
}

func (s *Service) Refresh(ctx context.Context, input RefreshInput) (AuthResult, error) {
	now := s.clock.Now()
	presented, csrfDigest, err := parsePresentedRefresh(input.RefreshToken, input.CSRFToken)
	if err != nil {
		return AuthResult{}, authenticationRejected(err)
	}
	nextCredential, err := issueRefreshCredential(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	nextCSRF, err := issueCSRFToken(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	jwtID, err := newUUIDv4(s.random)
	if err != nil {
		return AuthResult{}, err
	}
	var accepted RefreshSnapshot
	decision, err := s.repository.TransactRefresh(ctx, presented.TokenID, now, func(snapshot RefreshSnapshot) RefreshDecision {
		if !refreshCredentialsMatch(snapshot, presented, csrfDigest) {
			return RefreshDecision{Kind: RefreshReject}
		}
		if snapshot.UsedAt != nil {
			return RefreshDecision{Kind: RefreshRevokeReuse}
		}
		if !refreshSnapshotActive(snapshot, now) {
			return RefreshDecision{Kind: RefreshReject}
		}
		accepted = snapshot
		next := newRefreshToken(nextCredential, nextCSRF, now, snapshot.Session.ExpiresAt)
		return RefreshDecision{Kind: RefreshRotate, NextToken: &next}
	})
	if err != nil {
		return AuthResult{}, err
	}
	switch decision {
	case RefreshRevokeReuse:
		return AuthResult{}, refreshReuseDetected()
	case RefreshRotate:
		return s.authResult(accepted.Account.Account, accepted.Session.ID, jwtID, nextCredential, nextCSRF, now)
	default:
		return AuthResult{}, authenticationRejected(nil)
	}
}

func (s *Service) Logout(ctx context.Context, input LogoutInput) error {
	now := s.clock.Now()
	principal, err := s.jwt.ParseAt(input.AccessToken, now)
	if err != nil {
		return err
	}
	presented, csrfDigest, err := parsePresentedRefresh(input.RefreshToken, input.CSRFToken)
	if err != nil {
		return authenticationRejected(err)
	}
	decision, err := s.repository.TransactRefresh(ctx, presented.TokenID, now, func(snapshot RefreshSnapshot) RefreshDecision {
		if !refreshCredentialsMatch(snapshot, presented, csrfDigest) {
			return RefreshDecision{Kind: RefreshReject}
		}
		if snapshot.UsedAt != nil {
			return RefreshDecision{Kind: RefreshRevokeReuse}
		}
		if !refreshSnapshotActive(snapshot, now) || !principalMatchesSnapshot(principal, snapshot) {
			return RefreshDecision{Kind: RefreshReject}
		}
		return RefreshDecision{Kind: RefreshLogout}
	})
	if err != nil {
		return err
	}
	if decision == RefreshRevokeReuse {
		return refreshReuseDetected()
	}
	if decision != RefreshLogout {
		return authenticationRejected(nil)
	}
	return nil
}

func (s *Service) Me(ctx context.Context, accessToken string) (Account, error) {
	authenticated, err := s.Authenticate(ctx, accessToken)
	if err != nil {
		return Account{}, err
	}
	return authenticated.Account, nil
}

// VerifyAccessToken verifies the signed access credential and returns its
// immutable principal without performing a database read. Product repositories
// that need authorization and data from one database snapshot use this method,
// then revalidate the complete principal inside their own transaction.
func (s *Service) VerifyAccessToken(accessToken string) (AccessPrincipal, error) {
	return s.verifyAccessTokenAt(accessToken, s.clock.Now())
}

func (s *Service) verifyAccessTokenAt(accessToken string, now time.Time) (AccessPrincipal, error) {
	return s.jwt.ParseAt(accessToken, now)
}

// Authenticate parses the access token and revalidates its complete account
// and session binding against the current database state. It is the sole
// authentication boundary for authorized product services that also need the
// session identity carried by the access token.
func (s *Service) Authenticate(ctx context.Context, accessToken string) (AuthenticatedAccount, error) {
	now := s.clock.Now()
	principal, err := s.verifyAccessTokenAt(accessToken, now)
	if err != nil {
		return AuthenticatedAccount{}, err
	}
	snapshot, err := s.repository.LoadPrincipal(ctx, principal.AccountID, principal.SessionID, now)
	if err != nil {
		return AuthenticatedAccount{}, err
	}
	if !snapshot.Found || !principalSnapshotActive(snapshot, principal, now) {
		return AuthenticatedAccount{}, authenticationRejected(nil)
	}
	return AuthenticatedAccount{
		Account:   snapshot.Account.Account,
		Principal: principal,
	}, nil
}

func (s *Service) issueSessionCredentials() (string, string, IssuedRefreshCredential, IssuedCSRFToken, string, error) {
	accountID, err := newUUIDv4(s.random)
	if err != nil {
		return "", "", IssuedRefreshCredential{}, IssuedCSRFToken{}, "", err
	}
	sessionID, err := newUUIDv4(s.random)
	if err != nil {
		return "", "", IssuedRefreshCredential{}, IssuedCSRFToken{}, "", err
	}
	credential, err := issueRefreshCredential(s.random)
	if err != nil {
		return "", "", IssuedRefreshCredential{}, IssuedCSRFToken{}, "", err
	}
	csrf, err := issueCSRFToken(s.random)
	if err != nil {
		return "", "", IssuedRefreshCredential{}, IssuedCSRFToken{}, "", err
	}
	jwtID, err := newUUIDv4(s.random)
	if err != nil {
		return "", "", IssuedRefreshCredential{}, IssuedCSRFToken{}, "", err
	}
	return accountID, sessionID, credential, csrf, jwtID, nil
}

func (s *Service) authResult(
	account Account,
	sessionID string,
	jwtID string,
	credential IssuedRefreshCredential,
	csrf IssuedCSRFToken,
	now time.Time,
) (AuthResult, error) {
	access, expiresAt, err := s.jwt.Issue(AccessPrincipal{
		AccountID:    account.ID,
		SessionID:    sessionID,
		Role:         account.Role,
		AuthRevision: account.AuthRevision,
		JWTID:        jwtID,
	}, now)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		AccessToken:        access,
		ExpiresAt:          expiresAt,
		CSRFToken:          csrf.Serialized,
		RefreshCookieValue: credential.Serialized,
		Account:            account,
	}, nil
}

func parsePresentedRefresh(refreshToken, csrfToken string) (RefreshCredential, [32]byte, error) {
	credential, err := parseRefreshCredential(refreshToken)
	if err != nil {
		return RefreshCredential{}, [32]byte{}, err
	}
	csrfDigest, err := parseCSRFToken(csrfToken)
	if err != nil {
		return RefreshCredential{}, [32]byte{}, err
	}
	return credential, csrfDigest, nil
}

func newRefreshToken(
	credential IssuedRefreshCredential,
	csrf IssuedCSRFToken,
	createdAt time.Time,
	expiresAt time.Time,
) NewRefreshToken {
	return NewRefreshToken{
		ID:           credential.TokenID,
		SecretDigest: credential.SecretDigest,
		CSRFDigest:   csrf.Digest,
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
	}
}

func refreshCredentialsMatch(snapshot RefreshSnapshot, presented RefreshCredential, csrfDigest [sha256.Size]byte) bool {
	if !snapshot.Found || snapshot.TokenID != presented.TokenID {
		return false
	}
	secretDigest := sha256.Sum256(presented.Secret[:])
	secretMatches := subtle.ConstantTimeCompare(secretDigest[:], snapshot.SecretDigest[:])
	csrfMatches := subtle.ConstantTimeCompare(csrfDigest[:], snapshot.CSRFDigest[:])
	return secretMatches&csrfMatches == 1
}

func refreshSnapshotActive(snapshot RefreshSnapshot, now time.Time) bool {
	return snapshot.Found &&
		snapshot.TokenRevokedAt == nil &&
		now.Before(snapshot.TokenExpiresAt) &&
		snapshot.Session.RevokedAt == nil &&
		now.Before(snapshot.Session.ExpiresAt) &&
		snapshot.Account.DisabledAt == nil &&
		snapshot.Account.AuthRevision == snapshot.Session.AuthRevision
}

func principalMatchesSnapshot(principal AccessPrincipal, snapshot RefreshSnapshot) bool {
	return principal.AccountID == snapshot.Account.ID &&
		principal.SessionID == snapshot.Session.ID &&
		principal.Role == snapshot.Account.Role &&
		principal.AuthRevision == snapshot.Account.AuthRevision
}

func principalSnapshotActive(snapshot PrincipalSnapshot, principal AccessPrincipal, now time.Time) bool {
	return snapshot.Found &&
		snapshot.Account.DisabledAt == nil &&
		snapshot.Session.RevokedAt == nil &&
		now.Before(snapshot.Session.ExpiresAt) &&
		snapshot.Session.AccountID == snapshot.Account.ID &&
		principal.AccountID == snapshot.Account.ID &&
		principal.SessionID == snapshot.Session.ID &&
		principal.Role == snapshot.Account.Role &&
		principal.AuthRevision == snapshot.Account.AuthRevision &&
		snapshot.Session.AuthRevision == snapshot.Account.AuthRevision
}
