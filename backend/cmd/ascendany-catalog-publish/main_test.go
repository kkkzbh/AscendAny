package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestValidateCommand(t *testing.T) {
	t.Parallel()
	if err := validateCommand([]string{"publish"}); err != nil {
		t.Fatalf("validateCommand(publish) error = %v", err)
	}
	for _, args := range [][]string{nil, {}, {"unknown"}, {"publish", "extra"}} {
		if err := validateCommand(args); err == nil {
			t.Fatalf("validateCommand(%q) error = nil", args)
		}
	}
}

func TestRunRejectsCommandBeforeReadingSecretsOrBindingPort(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookupCalled := false
	status := run(
		context.Background(),
		[]string{"unknown"},
		func(string) (string, bool) {
			lookupCalled = true
			return "", false
		},
		func(string) ([]byte, error) {
			t.Fatal("readFile was called")
			return nil, nil
		},
		"",
		&stdout,
		&stderr,
		func(string, string) (net.Listener, error) {
			t.Fatal("listen was called")
			return nil, nil
		},
	)
	if status != 2 || lookupCalled || stdout.Len() != 0 || !strings.Contains(stderr.String(), "command rejected") {
		t.Fatalf("status=%d lookupCalled=%t stdout=%q stderr=%q", status, lookupCalled, stdout.String(), stderr.String())
	}
}

func TestAccessTokenVerifierAuthenticatesSignedAdministratorPrincipal(t *testing.T) {
	t.Parallel()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x6b}, ed25519.SeedSize))
	manager, err := auth.NewJWTManager("ascendany", "ascendany-v2", privateKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.AccessPrincipal{
		AccountID:    "11111111-1111-4111-8111-111111111111",
		SessionID:    "22222222-2222-4222-8222-222222222222",
		JWTID:        "33333333-3333-4333-8333-333333333333",
		Role:         auth.RoleAdmin,
		AuthRevision: 1,
	}
	token, expiresAt, err := manager.Issue(principal, time.Now().UTC().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	publicVerifier, err := auth.NewAccessTokenVerifier("ascendany", "ascendany-v2", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := (accessTokenVerifier{verifier: publicVerifier}).VerifyAccessToken(token)
	principal.ExpiresAt = expiresAt
	if err != nil || verified != principal {
		t.Fatalf("VerifyAccessToken() = %#v, %v", verified, err)
	}
	if _, err := (accessTokenVerifier{verifier: publicVerifier}).VerifyAccessToken("header.payload.signature"); err == nil {
		t.Fatal("VerifyAccessToken() accepted an invalid signature")
	}
}

func TestAccessTokenVerifierAllowsExpiredCommittedPublicationRecovery(t *testing.T) {
	t.Parallel()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x6c}, ed25519.SeedSize))
	manager, err := auth.NewJWTManager("ascendany", "ascendany-v2", privateKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.AccessPrincipal{
		AccountID:    "11111111-1111-4111-8111-111111111111",
		SessionID:    "22222222-2222-4222-8222-222222222222",
		JWTID:        "33333333-3333-4333-8333-333333333333",
		Role:         auth.RoleAdmin,
		AuthRevision: 1,
	}
	token, expiresAt, err := manager.Issue(principal, time.Now().UTC().Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	publicVerifier, err := auth.NewAccessTokenVerifier("ascendany", "ascendany-v2", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := (accessTokenVerifier{verifier: publicVerifier}).VerifyAccessToken(token)
	principal.ExpiresAt = expiresAt
	if err != nil || verified != principal {
		t.Fatalf("VerifyAccessToken() = %#v, %v", verified, err)
	}
}
