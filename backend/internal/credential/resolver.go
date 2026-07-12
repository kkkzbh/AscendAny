// Package credential owns resolution of secret references into process-local
// credentials. Callers persist only a reference and bind it to one canonical
// remote authority; environment variables contain secret-file paths only.
package credential

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	fileEnvironmentPrefix = "ASCENDANY_CREDENTIAL_FILE_REF_HEX_"
	fileAuthorityMarker   = "_AUTHORITY_HEX_"
	maxFilePathBytes      = 4096
	MaxBearerBytes        = 8192
)

var referencePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type EnvironmentLookup func(string) (string, bool)

type SecretFileReader func(string) ([]byte, error)

type Resolver interface {
	Resolve(context.Context, string, string) (string, error)
}

type EnvironmentFileResolver struct {
	lookup   EnvironmentLookup
	readFile SecretFileReader
}

func NewEnvironmentFileResolver(lookup EnvironmentLookup, readFile SecretFileReader) (*EnvironmentFileResolver, error) {
	if lookup == nil || readFile == nil {
		return nil, errors.New("environment lookup and secret file reader are required")
	}
	return &EnvironmentFileResolver{lookup: lookup, readFile: readFile}, nil
}

// FileEnvironmentVariable returns the collision-free environment variable
// which owns the secret-file path for one reference and canonical authority.
func FileEnvironmentVariable(reference, authority string) (string, error) {
	if !referencePattern.MatchString(reference) || !ValidAuthority(authority) {
		return "", errors.New("credential reference and authority must be canonical")
	}
	return fileEnvironmentPrefix + strings.ToUpper(hex.EncodeToString([]byte(reference))) +
		fileAuthorityMarker + strings.ToUpper(hex.EncodeToString([]byte(authority))), nil
}

func (resolver *EnvironmentFileResolver) Resolve(ctx context.Context, reference, authority string) (string, error) {
	if ctx == nil {
		return "", errors.New("credential resolution context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	environmentName, err := FileEnvironmentVariable(reference, authority)
	if err != nil {
		return "", errors.New("credential reference or authority is invalid")
	}
	path, present := resolver.lookup(environmentName)
	if !present {
		return "", fmt.Errorf("credential file environment variable %s is required", environmentName)
	}
	if path == "" || len(path) > maxFilePathBytes || path != strings.TrimSpace(path) ||
		strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("credential file environment variable %s must contain one canonical absolute path", environmentName)
	}
	secret, err := resolver.readFile(path)
	if err != nil {
		return "", fmt.Errorf("credential file referenced by %s cannot be read", environmentName)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !ValidBearer(secret) {
		return "", fmt.Errorf("credential file referenced by %s must contain one bounded bearer credential", environmentName)
	}
	return string(secret), nil
}

func ValidAuthority(authority string) bool {
	host, rawPort, err := net.SplitHostPort(authority)
	if err != nil || host == "" || rawPort == "" {
		return false
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != rawPort {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String() == host && net.JoinHostPort(host, rawPort) == authority
	}
	return validDNSHostname(host) && net.JoinHostPort(host, rawPort) == authority
}

func ValidBearer(value []byte) bool {
	if len(value) == 0 || len(value) > MaxBearerBytes || string(value) != strings.TrimSpace(string(value)) {
		return false
	}
	padding := false
	dataCharacters := 0
	for _, character := range value {
		if character == '=' {
			padding = true
			continue
		}
		if padding || !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("-._~+/", rune(character))) {
			return false
		}
		dataCharacters++
	}
	return dataCharacters > 0
}

func validDNSHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || host != strings.ToLower(host) {
		return false
	}
	hasNonNumericLabelCharacter := false
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !asciiLetterOrDigit(label[0]) || !asciiLetterOrDigit(label[len(label)-1]) {
			return false
		}
		for index := 1; index < len(label)-1; index++ {
			if !asciiLetterOrDigit(label[index]) && label[index] != '-' {
				return false
			}
		}
		for index := range len(label) {
			if label[index] < '0' || label[index] > '9' {
				hasNonNumericLabelCharacter = true
			}
		}
	}
	return hasNonNumericLabelCharacter
}

func asciiLetterOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
