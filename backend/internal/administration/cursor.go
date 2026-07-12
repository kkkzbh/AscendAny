package administration

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const studentCursorPrefix = "student.v1\x00"

func EncodeStudentCursor(studentNumber string) (string, error) {
	if err := validateStudentNumber(studentNumber); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(studentCursorPrefix + studentNumber)), nil
}

func DecodeStudentCursor(cursor string) (string, error) {
	if cursor == "" || len(cursor) > 128 {
		return "", errors.New("student cursor length is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != cursor || !utf8.Valid(decoded) {
		return "", errors.New("student cursor is not canonical base64url")
	}
	value := string(decoded)
	if !strings.HasPrefix(value, studentCursorPrefix) {
		return "", errors.New("student cursor protocol is invalid")
	}
	studentNumber := strings.TrimPrefix(value, studentCursorPrefix)
	if err := validateStudentNumber(studentNumber); err != nil {
		return "", err
	}
	return studentNumber, nil
}

func ValidStudentCursor(cursor string) bool {
	_, err := DecodeStudentCursor(cursor)
	return err == nil
}

func validateStudentNumber(value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || len(value) < 1 || len(value) > auth.MaxStudentNumberBytes || strings.ContainsRune(value, '\x00') {
		return errors.New("student number is not canonical")
	}
	return nil
}
