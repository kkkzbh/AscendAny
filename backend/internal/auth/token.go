package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	refreshTokenVersion = "v1"
	secretBytes         = 32
)

type RefreshCredential struct {
	TokenID string
	Secret  [secretBytes]byte
}

type IssuedRefreshCredential struct {
	Serialized   string
	TokenID      string
	SecretDigest [sha256.Size]byte
}

type IssuedCSRFToken struct {
	Serialized string
	Digest     [sha256.Size]byte
}

func issueRefreshCredential(random io.Reader) (IssuedRefreshCredential, error) {
	tokenID, err := newUUIDv4(random)
	if err != nil {
		return IssuedRefreshCredential{}, err
	}
	secret, err := randomBytes(random, secretBytes)
	if err != nil {
		return IssuedRefreshCredential{}, err
	}
	encodedSecret := base64.RawURLEncoding.EncodeToString(secret)
	return IssuedRefreshCredential{
		Serialized:   refreshTokenVersion + "." + tokenID + "." + encodedSecret,
		TokenID:      tokenID,
		SecretDigest: sha256.Sum256(secret),
	}, nil
}

func parseRefreshCredential(serialized string) (RefreshCredential, error) {
	parts := strings.Split(serialized, ".")
	if len(parts) != 3 || parts[0] != refreshTokenVersion {
		return RefreshCredential{}, errors.New("refresh token envelope is invalid")
	}
	if _, err := parseUUIDv4(parts[1]); err != nil {
		return RefreshCredential{}, fmt.Errorf("refresh token ID: %w", err)
	}
	secret, err := decodeCanonicalURLToken(parts[2])
	if err != nil {
		return RefreshCredential{}, fmt.Errorf("refresh token secret: %w", err)
	}
	var fixed [secretBytes]byte
	copy(fixed[:], secret)
	return RefreshCredential{TokenID: parts[1], Secret: fixed}, nil
}

func issueCSRFToken(random io.Reader) (IssuedCSRFToken, error) {
	raw, err := randomBytes(random, secretBytes)
	if err != nil {
		return IssuedCSRFToken{}, err
	}
	return IssuedCSRFToken{
		Serialized: base64.RawURLEncoding.EncodeToString(raw),
		Digest:     sha256.Sum256(raw),
	}, nil
}

func parseCSRFToken(serialized string) ([sha256.Size]byte, error) {
	raw, err := decodeCanonicalURLToken(serialized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func decodeCanonicalURLToken(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(decoded) != secretBytes || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("token must be canonical base64url encoding of 32 bytes")
	}
	return decoded, nil
}

func randomBytes(random io.Reader, count int) ([]byte, error) {
	if random == nil {
		return nil, authError(ErrorInvalidConfiguration, "Random source is required.", nil)
	}
	value := make([]byte, count)
	if _, err := io.ReadFull(random, value); err != nil {
		return nil, authError(ErrorInternal, "Cryptographic random generation failed.", err)
	}
	return value, nil
}

func newUUIDv4(random io.Reader) (string, error) {
	raw, err := randomBytes(random, 16)
	if err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return formatUUID(raw), nil
}

func parseUUIDv4(value string) ([16]byte, error) {
	var parsed [16]byte
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return parsed, errors.New("UUID is not canonical")
	}
	compact := value[0:8] + value[9:13] + value[14:18] + value[19:23] + value[24:36]
	if strings.ToLower(compact) != compact {
		return parsed, errors.New("UUID must use lowercase hexadecimal")
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		return parsed, errors.New("UUID contains invalid hexadecimal")
	}
	copy(parsed[:], decoded)
	if parsed[6]>>4 != 4 || parsed[8]>>6 != 2 {
		return [16]byte{}, errors.New("UUID must be RFC 4122 version 4")
	}
	if formatUUID(parsed[:]) != value {
		return [16]byte{}, errors.New("UUID is not canonical")
	}
	return parsed, nil
}

func formatUUID(raw []byte) string {
	hexadecimal := hex.EncodeToString(raw)
	return hexadecimal[0:8] + "-" + hexadecimal[8:12] + "-" + hexadecimal[12:16] + "-" + hexadecimal[16:20] + "-" + hexadecimal[20:32]
}
