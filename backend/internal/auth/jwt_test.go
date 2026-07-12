package auth

import (
	"bytes"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAccountID = "123e4567-e89b-42d3-a456-426614174000"
	testSessionID = "123e4567-e89b-42d3-a456-426614174001"
	testJWTID     = "123e4567-e89b-42d3-a456-426614174002"
)

func TestJWTManagerIssuesAndParsesFixedHS256Claims(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 1, 2, 3, 456_000_000, time.UTC)
	manager, err := NewJWTManager("ascendany", "ascendany-v2", bytes.Repeat([]byte{0x71}, 32), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := AccessPrincipal{
		AccountID:    testAccountID,
		SessionID:    testSessionID,
		Role:         RoleStudent,
		AuthRevision: 4,
		JWTID:        testJWTID,
	}
	serialized, expiresAt, err := manager.Issue(want, now)
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Equal(now.Truncate(time.Second).Add(15 * time.Minute)) {
		t.Fatalf("unexpected expiry: %v", expiresAt)
	}
	got, err := manager.ParseAt(serialized, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("principal mismatch: got %#v want %#v", got, want)
	}
}

func TestJWTManagerRejectsAlgorithmIssuerAudienceAndTimeConfusion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	key := bytes.Repeat([]byte{0x61}, 32)
	manager, err := NewJWTManager("ascendany", "ascendany-v2", key, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := AccessClaims{
		SessionID:    testSessionID,
		Role:         RoleStudent,
		AuthRevision: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "ascendany",
			Subject:   testAccountID,
			Audience:  jwt.ClaimStrings{"ascendany-v2"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        testJWTID,
		},
	}
	cases := []struct {
		name   string
		method jwt.SigningMethod
		claims AccessClaims
		key    any
	}{
		{name: "HS512", method: jwt.SigningMethodHS512, claims: base, key: key},
		{name: "wrong issuer", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.Issuer = "other" }), key: key},
		{name: "wrong audience", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.Audience = jwt.ClaimStrings{"other"} }), key: key},
		{name: "extra audience", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.Audience = append(c.Audience, "other") }), key: key},
		{name: "expired", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second)) }), key: key},
		{name: "missing expiry", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.ExpiresAt = nil }), key: key},
		{name: "missing issued at", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.IssuedAt = nil }), key: key},
		{name: "missing subject", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.Subject = "" }), key: key},
		{name: "future not-before", method: jwt.SigningMethodHS256, claims: mutateClaims(base, func(c *AccessClaims) { c.NotBefore = jwt.NewNumericDate(now.Add(time.Minute)) }), key: key},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			token := jwt.NewWithClaims(testCase.method, testCase.claims)
			serialized, err := token.SignedString(testCase.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.ParseAt(serialized, now); errorCode(err) != ErrorAuthentication {
				t.Fatalf("invalid JWT was accepted or mapped incorrectly: %v", err)
			}
		})
	}

	none := jwt.NewWithClaims(jwt.SigningMethodNone, base)
	serialized, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseAt(serialized, now); errorCode(err) != ErrorAuthentication {
		t.Fatalf("alg=none was accepted or mapped incorrectly: %v", err)
	}
}

func mutateClaims(source AccessClaims, mutate func(*AccessClaims)) AccessClaims {
	result := source
	result.Audience = append(jwt.ClaimStrings(nil), source.Audience...)
	mutate(&result)
	return result
}
