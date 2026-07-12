package auth

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

func validateUsername(username string) error {
	if !IsCanonicalUsername(username) {
		return authError(ErrorInvalidInput, "Username must match [a-z0-9_]{3,32}.", nil)
	}
	return nil
}

// IsCanonicalUsername reports whether a username is already in the only form
// accepted by the authentication service.
func IsCanonicalUsername(username string) bool {
	return usernamePattern.MatchString(username)
}

func validatePassword(password string) error {
	if !utf8.ValidString(password) {
		return authError(ErrorInvalidInput, "Password must be valid UTF-8.", nil)
	}
	if len(password) < MinPasswordBytes || len(password) > MaxPasswordBytes {
		return authError(
			ErrorInvalidInput,
			fmt.Sprintf("Password must contain %d to %d UTF-8 bytes.", MinPasswordBytes, MaxPasswordBytes),
			nil,
		)
	}
	return nil
}

func validateTrimmedField(name, value string, minimum, maximum int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !utf8.ValidString(trimmed) || strings.IndexByte(trimmed, 0) >= 0 || len(trimmed) < minimum || len(trimmed) > maximum {
		return "", authError(
			ErrorInvalidInput,
			fmt.Sprintf("%s must contain %d to %d UTF-8 bytes after trimming and cannot contain NUL.", name, minimum, maximum),
			nil,
		)
	}
	return trimmed, nil
}

func validRole(role Role) bool {
	return role == RoleStudent || role == RoleAdmin
}
