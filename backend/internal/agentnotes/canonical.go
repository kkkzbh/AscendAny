package agentnotes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func canonicalizeDocument(title, content string) (string, string, string, error) {
	if !utf8.ValidString(title) || !norm.NFC.IsNormalString(title) || strings.TrimSpace(title) != title ||
		len(title) < 1 || len(title) > MaxTitleBytes || strings.ContainsRune(title, '\x00') {
		return "", "", "", errors.New("title must be trimmed NFC UTF-8 within the byte limit")
	}
	for _, character := range title {
		if unicode.IsControl(character) {
			return "", "", "", errors.New("title must be a single printable line")
		}
	}
	if !utf8.ValidString(content) || !norm.NFC.IsNormalString(content) || len(content) > MaxContentBytes ||
		strings.ContainsRune(content, '\x00') || strings.ContainsRune(content, '\r') {
		return "", "", "", errors.New("content must be NFC UTF-8 with LF line endings within the byte limit")
	}
	digest := sha256.Sum256([]byte(content))
	return title, content, hex.EncodeToString(digest[:]), nil
}
