package auth

import (
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
	issuer   string
	audience string
	key      []byte
	ttl      time.Duration
}

func NewJWTManager(issuer, audience string, key []byte, ttl time.Duration) (*JWTManager, error) {
	if issuer == "" || audience == "" {
		return nil, authError(ErrorInvalidConfiguration, "JWT issuer and audience are required.", nil)
	}
	if len(key) < 32 {
		return nil, authError(ErrorInvalidConfiguration, "JWT HS256 key must contain at least 32 bytes.", nil)
	}
	if ttl <= 0 {
		return nil, authError(ErrorInvalidConfiguration, "JWT access lifetime must be positive.", nil)
	}
	return &JWTManager{issuer: issuer, audience: audience, key: append([]byte(nil), key...), ttl: ttl}, nil
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
			Issuer:    m.issuer,
			Subject:   principal.AccountID,
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(issuedAt),
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ID:        principal.JWTID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	serialized, err := token.SignedString(m.key)
	if err != nil {
		return "", time.Time{}, authError(ErrorInternal, "Access token signing failed.", err)
	}
	return serialized, expiresAt, nil
}

func (m *JWTManager) ParseAt(serialized string, now time.Time) (AccessPrincipal, error) {
	if m == nil {
		return AccessPrincipal{}, authError(ErrorInvalidConfiguration, "JWT manager is required.", nil)
	}
	claims := &AccessClaims{}
	token, err := jwt.ParseWithClaims(
		serialized,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected JWT signing method")
			}
			return m.key, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithNotBeforeRequired(),
		jwt.WithStrictDecoding(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil || token == nil || !token.Valid {
		return AccessPrincipal{}, authenticationRejected(err)
	}
	if token.Method != jwt.SigningMethodHS256 || token.Header["alg"] != jwt.SigningMethodHS256.Alg() {
		return AccessPrincipal{}, authenticationRejected(errors.New("JWT algorithm is not HS256"))
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != m.audience {
		return AccessPrincipal{}, authenticationRejected(errors.New("JWT audience is not exact"))
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
	}, nil
}
