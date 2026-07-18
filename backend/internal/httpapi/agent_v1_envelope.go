package httpapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	agentV1EnvelopePrefix    = "aav1."
	agentV1EnvelopeVersion   = byte(1)
	agentV1EnvelopeAAD       = "AscendAny Agent API v1 refresh envelope"
	agentV1MaximumTokenBytes = 256
)

type agentV1Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type agentV1ErrorEnvelope struct {
	Error agentV1Error `json:"error"`
}

type agentV1RefreshCredentials struct {
	RefreshCookieValue string
	CSRFToken          string
	ExpiresAt          time.Time
}

type agentV1RefreshEnvelope struct {
	aead       cipher.AEAD
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func newAgentV1RefreshEnvelope(
	key []byte,
	accessTTL time.Duration,
	refreshTTL time.Duration,
) (*agentV1RefreshEnvelope, error) {
	if len(key) != 32 {
		return nil, errors.New("Agent API v1 refresh-envelope key must contain exactly 32 bytes")
	}
	if accessTTL <= 0 || refreshTTL <= 0 || refreshTTL < accessTTL {
		return nil, errors.New("Agent API v1 access and refresh lifetimes are invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("construct Agent API v1 refresh-envelope cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("construct Agent API v1 refresh-envelope AEAD: %w", err)
	}
	return &agentV1RefreshEnvelope{aead: aead, accessTTL: accessTTL, refreshTTL: refreshTTL}, nil
}

func (envelope *agentV1RefreshEnvelope) initialExpiry(resultExpiresAt time.Time) (time.Time, error) {
	if resultExpiresAt.IsZero() {
		return time.Time{}, errors.New("access-token expiry is missing")
	}
	return resultExpiresAt.UTC().Add(envelope.refreshTTL - envelope.accessTTL), nil
}

func (envelope *agentV1RefreshEnvelope) seal(credentials agentV1RefreshCredentials) (string, error) {
	if err := validateAgentV1RefreshCredentials(credentials); err != nil {
		return "", err
	}
	refreshBytes := []byte(credentials.RefreshCookieValue)
	csrfBytes := []byte(credentials.CSRFToken)
	plaintext := make([]byte, 1+8+2+len(refreshBytes)+1+len(csrfBytes))
	plaintext[0] = agentV1EnvelopeVersion
	binary.BigEndian.PutUint64(plaintext[1:9], uint64(credentials.ExpiresAt.Unix()))
	binary.BigEndian.PutUint16(plaintext[9:11], uint16(len(refreshBytes)))
	offset := 11
	copy(plaintext[offset:], refreshBytes)
	offset += len(refreshBytes)
	plaintext[offset] = byte(len(csrfBytes))
	offset++
	copy(plaintext[offset:], csrfBytes)

	nonce := make([]byte, envelope.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate Agent API v1 refresh-envelope nonce: %w", err)
	}
	ciphertext := envelope.aead.Seal(nil, nonce, plaintext, []byte(agentV1EnvelopeAAD))
	serialized := agentV1EnvelopePrefix + base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...))
	if len(serialized) > agentV1MaximumTokenBytes {
		return "", errors.New("Agent API v1 refresh envelope exceeds its canonical size")
	}
	return serialized, nil
}

func (envelope *agentV1RefreshEnvelope) open(serialized string) (agentV1RefreshCredentials, error) {
	if len(serialized) <= len(agentV1EnvelopePrefix) || len(serialized) > agentV1MaximumTokenBytes ||
		!strings.HasPrefix(serialized, agentV1EnvelopePrefix) {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope is invalid")
	}
	encoded := strings.TrimPrefix(serialized, agentV1EnvelopePrefix)
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope is not canonical base64url")
	}
	nonceSize := envelope.aead.NonceSize()
	if len(raw) <= nonceSize+envelope.aead.Overhead() {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope is truncated")
	}
	plaintext, err := envelope.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], []byte(agentV1EnvelopeAAD))
	if err != nil {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope authentication failed")
	}
	if len(plaintext) < 1+8+2+1 || plaintext[0] != agentV1EnvelopeVersion {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope version is invalid")
	}
	expiresUnix := int64(binary.BigEndian.Uint64(plaintext[1:9]))
	refreshLength := int(binary.BigEndian.Uint16(plaintext[9:11]))
	offset := 11
	if refreshLength < 1 || len(plaintext) < offset+refreshLength+1 {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope credential is truncated")
	}
	refreshValue := string(plaintext[offset : offset+refreshLength])
	offset += refreshLength
	csrfLength := int(plaintext[offset])
	offset++
	if csrfLength < 1 || len(plaintext) != offset+csrfLength {
		return agentV1RefreshCredentials{}, errors.New("Agent API v1 refresh envelope CSRF credential is truncated")
	}
	credentials := agentV1RefreshCredentials{
		RefreshCookieValue: refreshValue,
		CSRFToken:          string(plaintext[offset:]),
		ExpiresAt:          time.Unix(expiresUnix, 0).UTC(),
	}
	if err := validateAgentV1RefreshCredentials(credentials); err != nil {
		return agentV1RefreshCredentials{}, err
	}
	return credentials, nil
}

func validateAgentV1RefreshCredentials(credentials agentV1RefreshCredentials) error {
	if len(credentials.RefreshCookieValue) != 83 || !strings.HasPrefix(credentials.RefreshCookieValue, "v1.") ||
		strings.IndexFunc(credentials.RefreshCookieValue, invalidAgentV1TokenRune) >= 0 {
		return errors.New("Agent API v1 refresh credential has an invalid shape")
	}
	if len(credentials.CSRFToken) != 43 || strings.IndexFunc(credentials.CSRFToken, invalidAgentV1TokenRune) >= 0 {
		return errors.New("Agent API v1 CSRF credential has an invalid shape")
	}
	if credentials.ExpiresAt.IsZero() || credentials.ExpiresAt.Nanosecond() != 0 ||
		credentials.ExpiresAt.Location() != time.UTC {
		return errors.New("Agent API v1 refresh expiry is invalid")
	}
	return nil
}

func invalidAgentV1TokenRune(value rune) bool {
	return value <= ' ' || value >= 0x7f
}

func isAgentV1Path(path string) bool {
	return path == "/api/v1" || strings.HasPrefix(path, "/api/v1/")
}
