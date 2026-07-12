package agentnotes

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

const cursorPrefix = "agent-note.v1\x00"

type pageCursor struct {
	UpdatedAt time.Time
	NoteID    string
}

func encodeCursor(summary Summary) (string, error) {
	if !canonicalUUIDv4.MatchString(summary.ID) || !validUTCTime(summary.UpdatedAt) {
		return "", errors.New("note cursor source is invalid")
	}
	payload := cursorPrefix + summary.UpdatedAt.Format(time.RFC3339Nano) + "\x00" + summary.ID
	return base64.RawURLEncoding.EncodeToString([]byte(payload)), nil
}

func decodeCursor(value string) (pageCursor, error) {
	if value == "" || len(value) > 192 {
		return pageCursor{}, errors.New("note cursor length is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return pageCursor{}, errors.New("note cursor is not canonical base64url")
	}
	payload := string(decoded)
	if !strings.HasPrefix(payload, cursorPrefix) {
		return pageCursor{}, errors.New("note cursor protocol is invalid")
	}
	parts := strings.Split(strings.TrimPrefix(payload, cursorPrefix), "\x00")
	if len(parts) != 2 || !canonicalUUIDv4.MatchString(parts[1]) {
		return pageCursor{}, errors.New("note cursor identity is invalid")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || updatedAt.Location() != time.UTC || updatedAt.Format(time.RFC3339Nano) != parts[0] {
		return pageCursor{}, errors.New("note cursor timestamp is invalid")
	}
	return pageCursor{UpdatedAt: updatedAt, NoteID: parts[1]}, nil
}

func ValidCursor(value string) bool {
	_, err := decodeCursor(value)
	return err == nil
}
