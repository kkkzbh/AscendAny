package credential

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFileEnvironmentVariableIsCollisionFreeAndAuthorityBound(t *testing.T) {
	t.Parallel()
	name, err := FileEnvironmentVariable("feedback.delivery-token_value", "feedback.example.test:443")
	if err != nil {
		t.Fatal(err)
	}
	const expected = "ASCENDANY_CREDENTIAL_FILE_REF_HEX_666565646261636B2E64656C69766572792D746F6B656E5F76616C7565_AUTHORITY_HEX_666565646261636B2E6578616D706C652E746573743A343433"
	if name != expected {
		t.Fatalf("name=%q", name)
	}
	for _, input := range [][2]string{{"INVALID", "api.example:443"}, {"models.primary", "api.example"}, {"models.primary", "API.example:443"}, {"models.primary", "api.example:0443"}, {"models.primary", "127.000.000.001:443"}} {
		if _, err := FileEnvironmentVariable(input[0], input[1]); err == nil {
			t.Fatalf("accepted %#v", input)
		}
	}
}

func TestEnvironmentFileResolverReadsExactBoundedOpaqueSecret(t *testing.T) {
	t.Parallel()
	name, err := FileEnvironmentVariable("models.primary", "models.example:443")
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewEnvironmentFileResolver(func(got string) (string, bool) {
		if got != name {
			t.Fatalf("environment=%q", got)
		}
		return "/run/credentials/ascendanyd/model", true
	}, func(path string) ([]byte, error) {
		if path != "/run/credentials/ascendanyd/model" {
			t.Fatalf("path=%q", path)
		}
		return []byte(`{"username":"sender@example.test","password":"secret with spaces"}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := resolver.Resolve(context.Background(), "models.primary", "models.example:443")
	if err != nil || secret != `{"username":"sender@example.test","password":"secret with spaces"}` {
		t.Fatalf("secret=%q error=%v", secret, err)
	}
}

func TestEnvironmentFileResolverFailsClosedWithoutLeakingReadErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewEnvironmentFileResolver(nil, func(string) ([]byte, error) { return nil, nil }); err == nil {
		t.Fatal("nil lookup accepted")
	}
	for name, path := range map[string]string{"empty": "", "relative": "token", "padded": " /secret", "unclean": "/a/../secret", "nul": "/sec\x00ret", "long": "/" + strings.Repeat("a", maxFilePathBytes)} {
		name, path := name, path
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := false
			resolver, _ := NewEnvironmentFileResolver(func(string) (string, bool) { return path, true }, func(string) ([]byte, error) {
				called = true
				return []byte("token"), nil
			})
			if _, err := resolver.Resolve(context.Background(), "models.primary", "models.example:443"); err == nil || called {
				t.Fatalf("error=%v called=%t", err, called)
			}
		})
	}
	resolver, _ := NewEnvironmentFileResolver(func(string) (string, bool) { return "/secret", true }, func(string) ([]byte, error) {
		return nil, errors.New("private filesystem detail")
	})
	if _, err := resolver.Resolve(context.Background(), "models.primary", "models.example:443"); err == nil || strings.Contains(err.Error(), "private filesystem detail") {
		t.Fatalf("error=%v", err)
	}
}

func TestEnvironmentFileResolverHonorsCancellationAndOpaqueSecretBound(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	resolver, _ := NewEnvironmentFileResolver(func(string) (string, bool) {
		called = true
		return "/secret", true
	}, func(string) ([]byte, error) { return []byte("token"), nil })
	if _, err := resolver.Resolve(ctx, "models.primary", "models.example:443"); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error=%v called=%t", err, called)
	}
	for _, secret := range [][]byte{nil, []byte(strings.Repeat("s", MaxSecretBytes+1))} {
		resolver, _ := NewEnvironmentFileResolver(func(string) (string, bool) { return "/secret", true }, func(string) ([]byte, error) {
			return secret, nil
		})
		if _, err := resolver.Resolve(context.Background(), "models.primary", "models.example:443"); err == nil {
			t.Fatalf("accepted opaque secret with %d bytes", len(secret))
		}
	}
}

func TestValidBearerOwnsBearerGrammar(t *testing.T) {
	t.Parallel()
	for _, secret := range [][]byte{nil, []byte("="), []byte("===="), []byte(" token"), []byte("to ken"), []byte("令牌"), []byte(strings.Repeat("t", MaxBearerBytes+1))} {
		if ValidBearer(secret) {
			t.Fatalf("accepted %q", secret)
		}
	}
}
