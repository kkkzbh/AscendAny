package auth

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestRefreshAndCSRFTokensHaveStrictSelfDescribingFormat(t *testing.T) {
	t.Parallel()
	random := bytes.NewReader(bytes.Repeat([]byte{0x7a}, 128))
	issued, err := issueRefreshCredential(random)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(issued.Serialized, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] != issued.TokenID {
		t.Fatalf("unexpected refresh format: %q", issued.Serialized)
	}
	parsed, err := parseRefreshCredential(issued.Serialized)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TokenID != issued.TokenID || sha256.Sum256(parsed.Secret[:]) != issued.SecretDigest {
		t.Fatal("parsed refresh credential does not reproduce its stored digest")
	}
	csrf, err := issueCSRFToken(random)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := parseCSRFToken(csrf.Serialized)
	if err != nil || digest != csrf.Digest {
		t.Fatalf("CSRF token did not round trip: digest=%x err=%v", digest, err)
	}
}

func TestRefreshAndCSRFTokensRejectNonCanonicalInput(t *testing.T) {
	t.Parallel()
	random := bytes.NewReader(bytes.Repeat([]byte{0x2b}, 128))
	issued, err := issueRefreshCredential(random)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(issued.Serialized, ".")
	malformed := []string{
		"v2." + parts[1] + "." + parts[2],
		"v1." + strings.ToUpper(parts[1]) + "." + parts[2],
		"v1." + parts[1] + "." + parts[2] + "=",
		"v1." + parts[1] + "." + parts[2] + ".extra",
		"v1.00000000-0000-0000-0000-000000000000." + parts[2],
	}
	for _, value := range malformed {
		if _, err := parseRefreshCredential(value); err == nil {
			t.Fatalf("malformed refresh credential was accepted: %q", value)
		}
	}
	for _, value := range []string{"", parts[2] + "=", parts[2][:len(parts[2])-1]} {
		if _, err := parseCSRFToken(value); err == nil {
			t.Fatalf("malformed CSRF token was accepted: %q", value)
		}
	}
}
