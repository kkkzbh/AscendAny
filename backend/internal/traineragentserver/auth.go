package traineragentserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	trainerAgentTokenEnvironmentPrefix = "ASCENDANY_TRAINER_AGENT_TOKEN_FILE_AGENT_HEX_"
	maximumTokenFilePathBytes          = 4096
	maximumBearerTokenBytes            = 512
)

var (
	agentIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	bearerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{32,512}$`)
)

type EnvironmentLookup func(string) (string, bool)
type SecretFileReader func(string) ([]byte, error)

type TokenSecretResolver interface {
	Resolve(context.Context, string) (string, error)
}

type EnvironmentFileTokenResolver struct {
	lookup   EnvironmentLookup
	readFile SecretFileReader
}

func NewEnvironmentFileTokenResolver(
	lookup EnvironmentLookup,
	readFile SecretFileReader,
) (*EnvironmentFileTokenResolver, error) {
	if lookup == nil || readFile == nil {
		return nil, errors.New("trainer-agent token environment lookup and secret file reader are required")
	}
	return &EnvironmentFileTokenResolver{lookup: lookup, readFile: readFile}, nil
}

func TrainerAgentTokenFileEnvironmentVariable(agentID string) (string, error) {
	if !agentIDPattern.MatchString(agentID) {
		return "", errors.New("trainer-agent ID is invalid")
	}
	return trainerAgentTokenEnvironmentPrefix + strings.ToUpper(hex.EncodeToString([]byte(agentID))), nil
}

func (resolver *EnvironmentFileTokenResolver) Resolve(ctx context.Context, agentID string) (string, error) {
	if ctx == nil {
		return "", errors.New("trainer-agent token resolution context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	environmentName, err := TrainerAgentTokenFileEnvironmentVariable(agentID)
	if err != nil {
		return "", err
	}
	path, present := resolver.lookup(environmentName)
	if !present || path == "" || len(path) > maximumTokenFilePathBytes || path != strings.TrimSpace(path) ||
		strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return "", fmt.Errorf("%s must contain one canonical absolute credential path", environmentName)
	}
	secret, err := resolver.readFile(path)
	if err != nil {
		return "", fmt.Errorf("credential referenced by %s cannot be read", environmentName)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(secret) == 0 || len(secret) > maximumBearerTokenBytes || !utf8.Valid(secret) ||
		string(secret) != strings.TrimSpace(string(secret)) || !bearerTokenPattern.Match(secret) {
		return "", fmt.Errorf("credential referenced by %s is invalid", environmentName)
	}
	return string(secret), nil
}

type ScopedBearerVerifier struct {
	agentID  string
	resolver TokenSecretResolver
}

func NewScopedBearerVerifier(agentID string, resolver TokenSecretResolver) (*ScopedBearerVerifier, error) {
	if !agentIDPattern.MatchString(agentID) || resolver == nil {
		return nil, errors.New("one canonical trainer-agent ID and token resolver are required")
	}
	return &ScopedBearerVerifier{agentID: agentID, resolver: resolver}, nil
}

func (verifier *ScopedBearerVerifier) Verify(ctx context.Context, presented string) (string, error) {
	if ctx == nil {
		return "", errorValue(ErrorAuthenticationRejected, "Authentication was rejected.", false, errors.New("context is required"))
	}
	expected, err := verifier.resolver.Resolve(ctx, verifier.agentID)
	if err != nil {
		return "", errorValue(ErrorCredentialUnavailable, "Trainer-agent credential verification is unavailable.", true, err)
	}
	expectedDigest := sha256.Sum256([]byte(expected))
	presentedDigest := sha256.Sum256([]byte(presented))
	matched := subtle.ConstantTimeCompare(expectedDigest[:], presentedDigest[:]) == 1
	if !matched || !bearerTokenPattern.MatchString(presented) {
		return "", errorValue(ErrorAuthenticationRejected, "Authentication was rejected.", false, errors.New("bearer digest differs"))
	}
	return verifier.agentID, nil
}
