package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestPasswordHasherUsesExactArgon2idPHCAndPepper(t *testing.T) {
	t.Parallel()
	pepperA := bytes.Repeat([]byte{0x41}, 32)
	pepperB := bytes.Repeat([]byte{0x42}, 32)
	password := "correct horse battery staple"

	hasherA, err := NewPasswordHasher(pepperA, bytes.NewReader(bytes.Repeat([]byte{0x11}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	hashA, err := hasherA.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hashA, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected PHC: %q", hashA)
	}
	verified, err := hasherA.Verify(password, hashA)
	if err != nil || !verified {
		t.Fatalf("correct password was rejected: verified=%v err=%v", verified, err)
	}
	verified, err = hasherA.Verify("wrong password value", hashA)
	if err != nil || verified {
		t.Fatalf("wrong password result: verified=%v err=%v", verified, err)
	}

	hasherB, err := NewPasswordHasher(pepperB, bytes.NewReader(bytes.Repeat([]byte{0x11}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := hasherB.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if hashA == hashB {
		t.Fatal("changing the HMAC pepper did not change the PHC")
	}
}

func TestPasswordHasherRejectsMalformedPHC(t *testing.T) {
	t.Parallel()
	hasher, err := NewPasswordHasher(bytes.Repeat([]byte{0x31}, 32), bytes.NewReader(bytes.Repeat([]byte{0x22}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := hasher.Hash("valid password bytes")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(valid, "$")
	malformed := []string{
		"",
		valid + "$duplicate",
		strings.Replace(valid, "argon2id", "argon2i", 1),
		strings.Replace(valid, "v=19", "v=16", 1),
		strings.Replace(valid, "m=19456,t=2,p=1", "t=2,m=19456,p=1", 1),
		strings.Replace(valid, "m=19456,t=2,p=1", "m=19456,t=2,p=1,m=19456", 1),
		strings.Replace(valid, parts[4], parts[4]+"=", 1),
		strings.Replace(valid, parts[5], parts[5][:len(parts[5])-1], 1),
		strings.Repeat("x", maxPHCBytes+1),
	}
	for _, phc := range malformed {
		phc := phc
		t.Run(phcName(phc), func(t *testing.T) {
			verified, err := hasher.Verify("valid password bytes", phc)
			if verified || errorCode(err) != ErrorInternal {
				t.Fatalf("malformed PHC result: verified=%v err=%v", verified, err)
			}
		})
	}
}

func TestPasswordValidationUsesRawUTF8ByteLengthWithoutTrimming(t *testing.T) {
	t.Parallel()
	hasher, err := NewPasswordHasher(bytes.Repeat([]byte{0x51}, 32), bytes.NewReader(bytes.Repeat([]byte{0x33}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	rawSpaces := strings.Repeat(" ", MinPasswordBytes)
	phc, err := hasher.Hash(rawSpaces)
	if err != nil {
		t.Fatalf("raw spaces should satisfy byte length without trimming: %v", err)
	}
	verified, err := hasher.Verify(rawSpaces, phc)
	if err != nil || !verified {
		t.Fatalf("raw-space password did not round trip: verified=%v err=%v", verified, err)
	}
	for _, password := range []string{
		strings.Repeat("a", MinPasswordBytes-1),
		strings.Repeat("a", MaxPasswordBytes+1),
		strings.Repeat("界", 43),
		string([]byte{0xff, 0xfe}) + strings.Repeat("a", MinPasswordBytes),
	} {
		if _, err := hasher.Hash(password); errorCode(err) != ErrorInvalidInput {
			t.Fatalf("invalid password %q was accepted: %v", password, err)
		}
	}
}

func phcName(value string) string {
	if value == "" {
		return "empty"
	}
	if len(value) > 48 {
		return value[:48]
	}
	return value
}
