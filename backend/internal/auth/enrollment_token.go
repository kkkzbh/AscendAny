package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

type issuedEnrollmentToken struct {
	Serialized string
	Digest     [sha256.Size]byte
}

func issueEnrollmentToken(random io.Reader) (issuedEnrollmentToken, error) {
	raw, err := randomBytes(random, secretBytes)
	if err != nil {
		return issuedEnrollmentToken{}, err
	}
	return issuedEnrollmentToken{
		Serialized: base64.RawURLEncoding.EncodeToString(raw),
		Digest:     sha256.Sum256(raw),
	}, nil
}

func parseEnrollmentToken(serialized string) ([sha256.Size]byte, error) {
	if len(serialized) != base64.RawURLEncoding.EncodedLen(secretBytes) {
		return [sha256.Size]byte{}, errors.New("enrollment token length is invalid")
	}
	raw, err := decodeCanonicalURLToken(serialized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
