package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      uint32 = 19_456
	argonIterations  uint32 = 2
	argonParallelism uint8  = 1
	argonSaltBytes          = 16
	argonKeyBytes           = 32
	maxPHCBytes             = 256
)

type PasswordHasher struct {
	pepper []byte
	random io.Reader
}

func NewPasswordHasher(pepper []byte, random io.Reader) (*PasswordHasher, error) {
	if len(pepper) < 32 {
		return nil, authError(ErrorInvalidConfiguration, "Password pepper must contain at least 32 bytes.", nil)
	}
	if random == nil {
		return nil, authError(ErrorInvalidConfiguration, "Password random source is required.", nil)
	}
	return &PasswordHasher{pepper: append([]byte(nil), pepper...), random: random}, nil
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	if h == nil {
		return "", authError(ErrorInvalidConfiguration, "Password hasher is required.", nil)
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltBytes)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", authError(ErrorInternal, "Password salt generation failed.", err)
	}
	key := h.derive(password, salt)
	defer clear(key)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h *PasswordHasher) Verify(password, phc string) (bool, error) {
	if h == nil {
		return false, authError(ErrorInvalidConfiguration, "Password hasher is required.", nil)
	}
	if err := validatePassword(password); err != nil {
		return false, nil
	}
	salt, expected, err := parsePHC(phc)
	if err != nil {
		return false, authError(ErrorInternal, "Stored password hash is invalid.", err)
	}
	actual := h.derive(password, salt)
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func (h *PasswordHasher) derive(password string, salt []byte) []byte {
	mac := hmac.New(sha256.New, h.pepper)
	_, _ = mac.Write([]byte(password))
	prehash := mac.Sum(nil)
	defer clear(prehash)
	return argon2.IDKey(prehash, salt, argonIterations, argonMemory, argonParallelism, argonKeyBytes)
}

func parsePHC(phc string) ([]byte, []byte, error) {
	if len(phc) == 0 || len(phc) > maxPHCBytes {
		return nil, nil, errors.New("PHC length is invalid")
	}
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return nil, nil, errors.New("PHC envelope is invalid")
	}
	expectedParameters := fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonIterations, argonParallelism)
	if parts[3] != expectedParameters {
		return nil, nil, errors.New("PHC parameters are invalid")
	}
	salt, err := decodeCanonicalPHCBase64(parts[4], argonSaltBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("decode PHC salt: %w", err)
	}
	key, err := decodeCanonicalPHCBase64(parts[5], argonKeyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("decode PHC key: %w", err)
	}
	return salt, key, nil
}

func decodeCanonicalPHCBase64(value string, exactBytes int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(decoded) != exactBytes || base64.RawStdEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("non-canonical base64 or wrong decoded length")
	}
	return decoded, nil
}
