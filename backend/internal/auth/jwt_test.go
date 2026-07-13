package auth

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAccountID = "123e4567-e89b-42d3-a456-426614174000"
	testSessionID = "123e4567-e89b-42d3-a456-426614174001"
	testJWTID     = "123e4567-e89b-42d3-a456-426614174002"
)

func TestJWTManagerIssuesAndVerifiesFixedEdDSAClaims(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 1, 2, 3, 456_000_000, time.UTC)
	privateKey := testEd25519PrivateKey(0x71)
	manager, err := NewJWTManager("ascendany", "ascendany-v2", privateKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewAccessTokenVerifier("ascendany", "ascendany-v2", privateKey.Public().(ed25519.PublicKey))
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
	got, err := verifier.VerifyAt(serialized, now)
	if err != nil {
		t.Fatal(err)
	}
	want.ExpiresAt = expiresAt
	if got != want {
		t.Fatalf("principal mismatch: got %#v want %#v", got, want)
	}
}

func TestCatalogPublicationVerifierAuthenticatesExpiredImmutableCapability(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	privateKey := testEd25519PrivateKey(0x72)
	manager, err := NewJWTManager("ascendany", "ascendany-v2", privateKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewAccessTokenVerifier("ascendany", "ascendany-v2", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	want := AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, Role: RoleAdmin,
		AuthRevision: 7, JWTID: testJWTID,
	}
	serialized, expiresAt, err := manager.Issue(want, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyAt(serialized, now); errorCode(err) != ErrorAuthentication {
		t.Fatalf("VerifyAt() accepted expired token: %v", err)
	}
	got, err := verifier.VerifyCatalogPublicationCapability(serialized)
	if err != nil {
		t.Fatal(err)
	}
	want.ExpiresAt = expiresAt
	if got != want {
		t.Fatalf("principal mismatch: got %#v want %#v", got, want)
	}

	wrongIssuerClaims := AccessClaims{
		SessionID: testSessionID, Role: RoleAdmin, AuthRevision: 7,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "other", Subject: testAccountID, Audience: jwt.ClaimStrings{"ascendany-v2"},
			ExpiresAt: jwt.NewNumericDate(expiresAt), IssuedAt: jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			NotBefore: jwt.NewNumericDate(now.Add(-2 * time.Minute)), ID: testJWTID,
		},
	}
	wrongIssuer := jwt.NewWithClaims(jwt.SigningMethodEdDSA, wrongIssuerClaims)
	wrongIssuer.Header["typ"] = "JWT"
	serializedWrongIssuer, err := wrongIssuer.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyCatalogPublicationCapability(serializedWrongIssuer); errorCode(err) != ErrorAuthentication {
		t.Fatalf("publication verifier accepted wrong issuer: %v", err)
	}
}

func TestJWTManagerRejectsAlgorithmIssuerAudienceAndTimeConfusion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	privateKey := testEd25519PrivateKey(0x61)
	manager, err := NewJWTManager("ascendany", "ascendany-v2", privateKey, 15*time.Minute)
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
		{name: "HS256", method: jwt.SigningMethodHS256, claims: base, key: bytes.Repeat([]byte{0x61}, 32)},
		{name: "wrong issuer", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.Issuer = "other" }), key: privateKey},
		{name: "wrong audience", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.Audience = jwt.ClaimStrings{"other"} }), key: privateKey},
		{name: "extra audience", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.Audience = append(c.Audience, "other") }), key: privateKey},
		{name: "expired", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second)) }), key: privateKey},
		{name: "missing expiry", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.ExpiresAt = nil }), key: privateKey},
		{name: "missing issued at", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.IssuedAt = nil }), key: privateKey},
		{name: "missing subject", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.Subject = "" }), key: privateKey},
		{name: "future not-before", method: jwt.SigningMethodEdDSA, claims: mutateClaims(base, func(c *AccessClaims) { c.NotBefore = jwt.NewNumericDate(now.Add(time.Minute)) }), key: privateKey},
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

func TestEd25519JWTConstructorsRejectMalformedKeys(t *testing.T) {
	t.Parallel()
	privateKey := testEd25519PrivateKey(0x51)
	inconsistent := append(ed25519.PrivateKey(nil), privateKey...)
	inconsistent[len(inconsistent)-1] ^= 0xff

	for _, key := range []ed25519.PrivateKey{
		privateKey[:ed25519.PrivateKeySize-1],
		append(privateKey, 0),
		inconsistent,
	} {
		if _, err := NewJWTManager("ascendany", "ascendany-v2", key, time.Minute); errorCode(err) != ErrorInvalidConfiguration {
			t.Fatalf("NewJWTManager() error = %v", err)
		}
	}
	for _, key := range []ed25519.PublicKey{
		privateKey.Public().(ed25519.PublicKey)[:ed25519.PublicKeySize-1],
		append(privateKey.Public().(ed25519.PublicKey), 0),
	} {
		if _, err := NewAccessTokenVerifier("ascendany", "ascendany-v2", key); errorCode(err) != ErrorInvalidConfiguration {
			t.Fatalf("NewAccessTokenVerifier() error = %v", err)
		}
	}
}

func TestAccessTokenVerifierRejectsAdditionalProtectedHeaderFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	privateKey := testEd25519PrivateKey(0x41)
	verifier, err := NewAccessTokenVerifier("ascendany", "ascendany-v2", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	claims := AccessClaims{
		SessionID:    testSessionID,
		Role:         RoleStudent,
		AuthRevision: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "ascendany", Subject: testAccountID, Audience: jwt.ClaimStrings{"ascendany-v2"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)), IssuedAt: jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now), ID: testJWTID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = "JWT"
	token.Header["kid"] = "unexpected"
	serialized, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyAt(serialized, now); errorCode(err) != ErrorAuthentication {
		t.Fatalf("VerifyAt() error = %v", err)
	}
}

func testEd25519PrivateKey(fill byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{fill}, ed25519.SeedSize))
}

func mutateClaims(source AccessClaims, mutate func(*AccessClaims)) AccessClaims {
	result := source
	result.Audience = append(jwt.ClaimStrings(nil), source.Audience...)
	mutate(&result)
	return result
}
