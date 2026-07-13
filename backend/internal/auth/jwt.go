package auth

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	SessionID    string `json:"sid"`
	Role         Role   `json:"role"`
	AuthRevision int64  `json:"authRevision"`
	jwt.RegisteredClaims
}

func (c AccessClaims) Validate() error {
	if c.Subject == "" || c.ID == "" || c.SessionID == "" {
		return errors.New("sub, jti, and sid claims are required")
	}
	if c.IssuedAt == nil || c.NotBefore == nil || c.ExpiresAt == nil {
		return errors.New("iat, nbf, and exp claims are required")
	}
	if !validRole(c.Role) || c.AuthRevision < 1 {
		return errors.New("role or authRevision claim is invalid")
	}
	if c.ExpiresAt.Time.Before(c.IssuedAt.Time) || c.ExpiresAt.Time.Equal(c.IssuedAt.Time) {
		return errors.New("exp must be later than iat")
	}
	return nil
}

type JWTManager struct {
	privateKey ed25519.PrivateKey
	verifier   *AccessTokenVerifier
	ttl        time.Duration
}

// AccessTokenVerifier owns only the public capability required to authenticate
// access tokens. It cannot issue a token.
type AccessTokenVerifier struct {
	issuer    string
	audience  string
	publicKey ed25519.PublicKey
}

func NewAccessTokenVerifier(issuer, audience string, publicKey ed25519.PublicKey) (*AccessTokenVerifier, error) {
	if issuer == "" || audience == "" {
		return nil, authError(ErrorInvalidConfiguration, "JWT issuer and audience are required.", nil)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, authError(ErrorInvalidConfiguration, "JWT Ed25519 verification key must contain exactly 32 bytes.", nil)
	}
	return &AccessTokenVerifier{
		issuer:    issuer,
		audience:  audience,
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
	}, nil
}

func NewJWTManager(issuer, audience string, privateKey ed25519.PrivateKey, ttl time.Duration) (*JWTManager, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, authError(ErrorInvalidConfiguration, "JWT Ed25519 signing key must contain exactly 64 bytes.", nil)
	}
	canonicalPrivateKey := ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])
	if subtle.ConstantTimeCompare(privateKey, canonicalPrivateKey) != 1 {
		return nil, authError(ErrorInvalidConfiguration, "JWT Ed25519 signing key is internally inconsistent.", nil)
	}
	if ttl <= 0 {
		return nil, authError(ErrorInvalidConfiguration, "JWT access lifetime must be positive.", nil)
	}
	verifier, err := NewAccessTokenVerifier(issuer, audience, canonicalPrivateKey.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, err
	}
	return &JWTManager{
		privateKey: append(ed25519.PrivateKey(nil), canonicalPrivateKey...),
		verifier:   verifier,
		ttl:        ttl,
	}, nil
}

func (m *JWTManager) Issue(principal AccessPrincipal, now time.Time) (string, time.Time, error) {
	if m == nil {
		return "", time.Time{}, authError(ErrorInvalidConfiguration, "JWT manager is required.", nil)
	}
	if _, err := parseUUIDv4(principal.AccountID); err != nil {
		return "", time.Time{}, authError(ErrorInternal, "Account ID cannot be encoded into an access token.", err)
	}
	if _, err := parseUUIDv4(principal.SessionID); err != nil {
		return "", time.Time{}, authError(ErrorInternal, "Session ID cannot be encoded into an access token.", err)
	}
	if _, err := parseUUIDv4(principal.JWTID); err != nil {
		return "", time.Time{}, authError(ErrorInternal, "JWT ID cannot be encoded into an access token.", err)
	}
	if !validRole(principal.Role) || principal.AuthRevision < 1 {
		return "", time.Time{}, authError(ErrorInternal, "Access principal is invalid.", nil)
	}
	issuedAt := now.UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(m.ttl)
	claims := AccessClaims{
		SessionID:    principal.SessionID,
		Role:         principal.Role,
		AuthRevision: principal.AuthRevision,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.verifier.issuer,
			Subject:   principal.AccountID,
			Audience:  jwt.ClaimStrings{m.verifier.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(issuedAt),
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ID:        principal.JWTID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = "JWT"
	serialized, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", time.Time{}, authError(ErrorInternal, "Access token signing failed.", err)
	}
	return serialized, expiresAt, nil
}

func (m *JWTManager) ParseAt(serialized string, now time.Time) (AccessPrincipal, error) {
	if m == nil {
		return AccessPrincipal{}, authError(ErrorInvalidConfiguration, "JWT manager is required.", nil)
	}
	return m.verifier.VerifyAt(serialized, now)
}

func (v *AccessTokenVerifier) VerifyAt(serialized string, now time.Time) (AccessPrincipal, error) {
	if v == nil {
		return AccessPrincipal{}, authError(ErrorInvalidConfiguration, "JWT access-token verifier is required.", nil)
	}
	return v.verify(
		serialized,
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithNotBeforeRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
}

// VerifyCatalogPublicationCapability authenticates the immutable access-token
// bytes without applying current-time claim validity. The database owns the
// first-use wall-clock/session check and permits exact recovery of an already
// consumed publication after the token or session expires.
func (v *AccessTokenVerifier) VerifyCatalogPublicationCapability(serialized string) (AccessPrincipal, error) {
	if v == nil {
		return AccessPrincipal{}, authError(ErrorInvalidConfiguration, "JWT access-token verifier is required.", nil)
	}
	return v.verify(serialized, jwt.WithoutClaimsValidation())
}

func (v *AccessTokenVerifier) verify(serialized string, options ...jwt.ParserOption) (AccessPrincipal, error) {
	claims := &AccessClaims{}
	parserOptions := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithStrictDecoding(),
	}
	parserOptions = append(parserOptions, options...)
	token, err := jwt.ParseWithClaims(
		serialized,
		claims,
		func(token *jwt.Token) (any, error) {
			if !validEdDSAJWTHeader(token) {
				return nil, errors.New("unexpected JWT signing method")
			}
			return v.publicKey, nil
		},
		parserOptions...,
	)
	if err != nil || token == nil || !token.Valid {
		return AccessPrincipal{}, authenticationRejected(err)
	}
	if !validEdDSAJWTHeader(token) {
		return AccessPrincipal{}, authenticationRejected(errors.New("JWT header is not the exact EdDSA contract"))
	}
	if err := claims.Validate(); err != nil || claims.Issuer != v.issuer ||
		len(claims.Audience) != 1 || claims.Audience[0] != v.audience {
		return AccessPrincipal{}, authenticationRejected(errors.New("JWT registered claims violate the exact access-token contract"))
	}
	for name, value := range map[string]string{
		"sub": claims.Subject,
		"sid": claims.SessionID,
		"jti": claims.ID,
	} {
		if _, err := parseUUIDv4(value); err != nil {
			return AccessPrincipal{}, authenticationRejected(fmt.Errorf("%s claim: %w", name, err))
		}
	}
	return AccessPrincipal{
		AccountID:    claims.Subject,
		SessionID:    claims.SessionID,
		Role:         claims.Role,
		AuthRevision: claims.AuthRevision,
		JWTID:        claims.ID,
		ExpiresAt:    claims.ExpiresAt.Time.UTC(),
	}, nil
}

func validEdDSAJWTHeader(token *jwt.Token) bool {
	if token == nil || token.Method != jwt.SigningMethodEdDSA || len(token.Header) != 2 {
		return false
	}
	algorithm, algorithmOK := token.Header["alg"].(string)
	typeName, typeOK := token.Header["typ"].(string)
	return algorithmOK && algorithm == jwt.SigningMethodEdDSA.Alg() && typeOK && typeName == "JWT"
}
