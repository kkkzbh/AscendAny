package traineragentserver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const testScopedToken = "trainer-agent-token_0123456789abcdef"

type tokenResolverFunc func(context.Context, string) (string, error)

func (function tokenResolverFunc) Resolve(ctx context.Context, agentID string) (string, error) {
	return function(ctx, agentID)
}

func TestEnvironmentFileTokenResolverBindsOneAgentToOneSecretFile(t *testing.T) {
	t.Parallel()
	name, err := TrainerAgentTokenFileEnvironmentVariable("rtx-01")
	if err != nil {
		t.Fatal(err)
	}
	if name != "ASCENDANY_TRAINER_AGENT_TOKEN_FILE_AGENT_HEX_7274782D3031" {
		t.Fatalf("environment name = %q", name)
	}
	resolver, err := NewEnvironmentFileTokenResolver(
		func(got string) (string, bool) {
			if got != name {
				t.Fatalf("environment name = %q", got)
			}
			return "/run/credentials/ascendanyd/trainer_agent_rtx_01", true
		},
		func(path string) ([]byte, error) {
			if path != "/run/credentials/ascendanyd/trainer_agent_rtx_01" {
				t.Fatalf("path = %q", path)
			}
			return []byte(testScopedToken), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := resolver.Resolve(context.Background(), "rtx-01")
	if err != nil || secret != testScopedToken {
		t.Fatalf("secret = %q error = %v", secret, err)
	}
}

func TestEnvironmentFileTokenResolverRejectsMalformedPathsAndSecrets(t *testing.T) {
	t.Parallel()
	for name, fixture := range map[string]struct {
		path    string
		secret  string
		present bool
	}{
		"missing":       {present: false, secret: testScopedToken},
		"relative path": {path: "credentials/token", present: true, secret: testScopedToken},
		"root path":     {path: "/", present: true, secret: testScopedToken},
		"newline":       {path: "/secret/token", present: true, secret: testScopedToken + "\n"},
		"short":         {path: "/secret/token", present: true, secret: "short"},
		"space":         {path: "/secret/token", present: true, secret: strings.Repeat("x", 32) + " "},
	} {
		name, fixture := name, fixture
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resolver, err := NewEnvironmentFileTokenResolver(
				func(string) (string, bool) { return fixture.path, fixture.present },
				func(string) ([]byte, error) { return []byte(fixture.secret), nil },
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolver.Resolve(context.Background(), "rtx-01"); err == nil {
				t.Fatal("malformed token source accepted")
			}
		})
	}
}

func TestScopedBearerVerifierUsesDigestAndRejectsStudentCredentials(t *testing.T) {
	t.Parallel()
	resolvedAgent := ""
	verifier, err := NewScopedBearerVerifier("rtx-01", tokenResolverFunc(func(_ context.Context, agentID string) (string, error) {
		resolvedAgent = agentID
		return testScopedToken, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID, err := verifier.Verify(context.Background(), testScopedToken)
	if err != nil || agentID != "rtx-01" || resolvedAgent != "rtx-01" {
		t.Fatalf("agent = %q resolved = %q error = %v", agentID, resolvedAgent, err)
	}
	for _, presented := range []string{
		"eyJhbGciOiJIUzI1NiJ9.student.access-token-signature",
		testScopedToken[:len(testScopedToken)-1] + "x",
		"",
	} {
		_, err := verifier.Verify(context.Background(), presented)
		if CodeOf(err) != ErrorAuthenticationRejected {
			t.Fatalf("presented = %q error = %v code = %q", presented, err, CodeOf(err))
		}
	}
}

func TestScopedBearerVerifierKeepsResolverFailureOpaque(t *testing.T) {
	t.Parallel()
	verifier, err := NewScopedBearerVerifier("rtx-01", tokenResolverFunc(func(context.Context, string) (string, error) {
		return "", errors.New("/secret/private/token: permission denied")
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier.Verify(context.Background(), testScopedToken)
	var owned *Error
	if !errors.As(err, &owned) || owned.Code != ErrorCredentialUnavailable || strings.Contains(owned.Detail, "/secret/") {
		t.Fatalf("error = %#v", err)
	}
}
