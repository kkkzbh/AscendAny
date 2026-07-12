package traineragent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

const (
	maximumTransportEnvelopeBytes = 1 << 20
	maximumTransportBundleBytes   = 1 << 30
	maximumTransportErrorBytes    = 16 << 10
	maximumFailureDetailBytes     = 2048
	maximumLeaseDuration          = 24 * time.Hour
	maximumTokenBytes             = 512
	maximumSmallResponseBytes     = 64 << 10
	trainerAgentUserAgent         = "AscendAny-Trainer-Agent/1"
)

var (
	uuidV4Pattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	sha256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	agentIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	bearerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{32,512}$`)
	remoteCodePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

type HTTPClientConfig struct {
	BaseURL                  string
	BearerToken              string
	AgentID                  string
	LeaseDuration            time.Duration
	RequestTimeout           time.Duration
	MaximumInputBundleBytes  int
	MaximumOutputBundleBytes int
}

type HTTPClient struct {
	config HTTPClientConfig
	client *http.Client
	now    func() time.Time
}

var _ Transport = (*HTTPClient)(nil)

func NewHTTPClient(config HTTPClientConfig) (*HTTPClient, error) {
	if err := validateHTTPClientConfig(config); err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: config.RequestTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxConnsPerHost:       4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   config.RequestTimeout,
		ResponseHeaderTimeout: config.RequestTimeout,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS13},
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("trainer-agent redirects are forbidden")
		},
	}
	return newHTTPClient(config, client)
}

func newHTTPClient(config HTTPClientConfig, client *http.Client) (*HTTPClient, error) {
	if err := validateHTTPClientConfig(config); err != nil {
		return nil, err
	}
	if client == nil || client.Transport == nil {
		return nil, errors.New("trainer-agent HTTP transport is required")
	}
	return &HTTPClient{config: config, client: client, now: time.Now}, nil
}

func validateHTTPClientConfig(config HTTPClientConfig) error {
	if err := validateHTTPOrigin(config.BaseURL); err != nil {
		return err
	}
	if !bearerTokenPattern.MatchString(config.BearerToken) {
		return errors.New("trainer-agent bearer token must contain 32 to 512 canonical token characters")
	}
	if !agentIDPattern.MatchString(config.AgentID) {
		return errors.New("trainer-agent ID must contain 1 to 128 canonical ASCII characters")
	}
	if _, err := durationMilliseconds(config.LeaseDuration); err != nil {
		return fmt.Errorf("trainer-agent lease duration: %w", err)
	}
	if config.LeaseDuration > maximumLeaseDuration {
		return errors.New("trainer-agent lease duration must not exceed 24 hours")
	}
	interval, err := workerlease.ValidateDuration(config.LeaseDuration)
	if err != nil {
		return fmt.Errorf("trainer-agent lease duration: %w", err)
	}
	if config.RequestTimeout < 100*time.Millisecond || config.RequestTimeout > interval {
		return errors.New("trainer-agent request timeout must be between 100 milliseconds and one lease renewal interval")
	}
	if config.MaximumInputBundleBytes <= 0 || config.MaximumInputBundleBytes > maximumTransportBundleBytes ||
		config.MaximumOutputBundleBytes <= 0 || config.MaximumOutputBundleBytes > maximumTransportBundleBytes {
		return errors.New("trainer-agent input and output bundle limits must be between 1 byte and 1 GiB")
	}
	return nil
}

func validateHTTPOrigin(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawPath != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.Host != strings.ToLower(parsed.Host) || parsed.String() != baseURL {
		return errors.New("trainer-agent base URL must be one canonical HTTPS origin without credentials, path, query, fragment, or explicit port 443")
	}
	hostname := parsed.Hostname()
	if hostname == "" || len(parsed.Host) > 255 || strings.HasSuffix(hostname, ".") || strings.Contains(hostname, "%") ||
		strings.IndexFunc(parsed.Host, func(value rune) bool { return value > 127 }) >= 0 {
		return errors.New("trainer-agent base URL host must be bounded ASCII")
	}
	if port := parsed.Port(); port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port || value == 443 {
			return errors.New("trainer-agent base URL port must be canonical, non-default, and between 1 and 65535")
		}
	}
	return nil
}

func (client *HTTPClient) Claim(ctx context.Context) (*Claim, error) {
	leaseMilliseconds, _ := durationMilliseconds(client.config.LeaseDuration)
	request := traineragentprotocol.ClaimRequestV1{
		Protocol: traineragentprotocol.ClaimRequestProtocolV1, AgentID: client.config.AgentID,
		LeaseDurationMilliseconds: leaseMilliseconds,
	}
	response, raw, err := client.execute(
		ctx, http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims", traineragentprotocol.ClaimMediaTypeV1, request,
		client.config.MaximumInputBundleBytes+maximumTransportEnvelopeBytes,
	)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNoContent {
		if len(raw) != 0 || response.Header.Get("Content-Type") != "" {
			return nil, errors.New("empty trainer-agent claim must have no body or content type")
		}
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, client.remoteError(response, raw)
	}
	if err := requireMediaType(response, traineragentprotocol.ClaimMediaTypeV1); err != nil {
		return nil, err
	}
	var wire traineragentprotocol.ClaimResponseV1
	if err := decodeCanonicalResponse(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode trainer-agent claim: %w", err)
	}
	return client.parseClaim(wire)
}

func (client *HTTPClient) Heartbeat(ctx context.Context, lease LeaseReference) (Heartbeat, error) {
	if err := validateLeaseReference(lease); err != nil {
		return Heartbeat{}, err
	}
	request := traineragentprotocol.HeartbeatRequestV1{
		Protocol: traineragentprotocol.HeartbeatRequestProtocolV1, AgentID: client.config.AgentID, AttemptToken: lease.AttemptToken,
	}
	response, raw, err := client.execute(
		ctx, http.MethodPost, claimPath(lease.RunID)+"/heartbeats", traineragentprotocol.HeartbeatMediaTypeV1, request,
		maximumSmallResponseBytes,
	)
	if err != nil {
		return Heartbeat{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Heartbeat{}, client.remoteError(response, raw)
	}
	if err := requireMediaType(response, traineragentprotocol.HeartbeatMediaTypeV1); err != nil {
		return Heartbeat{}, err
	}
	var wire traineragentprotocol.HeartbeatResponseV1
	if err := decodeCanonicalResponse(raw, &wire); err != nil {
		return Heartbeat{}, fmt.Errorf("decode trainer-agent heartbeat: %w", err)
	}
	if wire.Protocol != traineragentprotocol.HeartbeatResponseProtocolV1 {
		return Heartbeat{}, errors.New("trainer-agent heartbeat protocol is unsupported")
	}
	expiresAt, err := parseCanonicalTimestamp(wire.LeaseExpiresAt)
	if err != nil {
		return Heartbeat{}, err
	}
	return Heartbeat{LeaseExpiresAt: expiresAt, SucceededAt: expiresAt.Add(-client.config.LeaseDuration)}, nil
}

func (client *HTTPClient) Publish(ctx context.Context, claim Claim, output []byte) (Publication, error) {
	if err := client.validateClaim(claim); err != nil {
		return Publication{}, err
	}
	canonical, digest, err := requireCanonicalBundle(output, client.config.MaximumOutputBundleBytes, "training output")
	if err != nil {
		return Publication{}, err
	}
	request := traineragentprotocol.OutputRequestV1{
		Protocol: traineragentprotocol.OutputRequestProtocolV1, AgentID: client.config.AgentID, AttemptToken: claim.AttemptToken,
		InputManifestSHA256: claim.InputManifestSHA256, OutputBundleSHA256: digest, OutputBundle: canonical,
	}
	requestSHA256, err := canonicalRequestSHA256(request)
	if err != nil {
		return Publication{}, err
	}
	response, raw, err := client.execute(
		ctx, http.MethodPost, claimPath(claim.RunID)+"/output", traineragentprotocol.OutputMediaTypeV1, request,
		maximumSmallResponseBytes,
	)
	if err != nil {
		return Publication{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Publication{}, client.remoteError(response, raw)
	}
	if err := requireMediaType(response, traineragentprotocol.OutputMediaTypeV1); err != nil {
		return Publication{}, err
	}
	var wire traineragentprotocol.OutputResponseV1
	if err := decodeCanonicalResponse(raw, &wire); err != nil {
		return Publication{}, fmt.Errorf("decode trainer-agent publication: %w", err)
	}
	if wire.Protocol != traineragentprotocol.OutputResponseProtocolV1 || !uuidV4Pattern.MatchString(wire.ModelID) ||
		!sha256Pattern.MatchString(wire.RuntimeConstructionSHA256) ||
		!sha256Pattern.MatchString(wire.RuntimeProvenanceSHA256) ||
		!sha256Pattern.MatchString(wire.RuntimeTreeSHA256) ||
		!sha256Pattern.MatchString(wire.HostCapabilitySHA256) ||
		!sha256Pattern.MatchString(wire.RuntimeAttestationSHA256) {
		return Publication{}, errors.New("trainer-agent publication response is invalid")
	}
	disposition := PublicationDisposition(wire.Disposition)
	if disposition != PublicationActivated && disposition != PublicationSuperseded {
		return Publication{}, errors.New("trainer-agent publication disposition is unsupported")
	}
	return Publication{
		Disposition: disposition, ModelID: wire.ModelID, RequestSHA256: requestSHA256,
		OutputBundleSHA256:        digest,
		RuntimeConstructionSHA256: wire.RuntimeConstructionSHA256,
		RuntimeProvenanceSHA256:   wire.RuntimeProvenanceSHA256,
		RuntimeTreeSHA256:         wire.RuntimeTreeSHA256,
		HostCapabilitySHA256:      wire.HostCapabilitySHA256,
		RuntimeAttestationSHA256:  wire.RuntimeAttestationSHA256,
		UploadedAt:                client.now().UTC(),
	}, nil
}

func (client *HTTPClient) ReportFailure(ctx context.Context, claim Claim, failure FailureReport) (FailureDisposition, error) {
	if err := client.validateClaim(claim); err != nil {
		return "", err
	}
	if err := validateFailureReport(failure); err != nil {
		return "", err
	}
	request := traineragentprotocol.FailureRequestV1{
		Protocol: traineragentprotocol.FailureRequestProtocolV1, AgentID: client.config.AgentID, AttemptToken: claim.AttemptToken,
		Code: failure.Code, Detail: failure.Detail, Retryable: failure.Retryable,
	}
	response, raw, err := client.execute(
		ctx, http.MethodPost, claimPath(claim.RunID)+"/failures", traineragentprotocol.FailureMediaTypeV1, request,
		maximumSmallResponseBytes,
	)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", client.remoteError(response, raw)
	}
	if err := requireMediaType(response, traineragentprotocol.FailureMediaTypeV1); err != nil {
		return "", err
	}
	var wire traineragentprotocol.FailureResponseV1
	if err := decodeCanonicalResponse(raw, &wire); err != nil {
		return "", fmt.Errorf("decode trainer-agent failure response: %w", err)
	}
	if wire.Protocol != traineragentprotocol.FailureResponseProtocolV1 {
		return "", errors.New("trainer-agent failure response protocol is unsupported")
	}
	disposition := FailureDisposition(wire.Disposition)
	if disposition != FailureRecorded && disposition != FailureRequeued {
		return "", errors.New("trainer-agent failure disposition is unsupported")
	}
	return disposition, nil
}

func (client *HTTPClient) execute(
	ctx context.Context,
	method, path, mediaType string,
	requestValue any,
	maximumResponseBytes int,
) (*http.Response, []byte, error) {
	if ctx == nil {
		return nil, nil, errors.New("trainer-agent request context is required")
	}
	requestBody, err := encodeCanonicalRequest(requestValue)
	if err != nil {
		return nil, nil, err
	}
	requestContext, cancel := context.WithTimeout(ctx, client.config.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext, method, client.config.BaseURL+path, bytes.NewReader(requestBody),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("construct trainer-agent request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.config.BearerToken)
	request.Header.Set("Accept", mediaType)
	request.Header.Set("Content-Type", mediaType)
	request.Header.Set("User-Agent", trainerAgentUserAgent)
	response, err := client.client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("execute trainer-agent request: %w", err)
	}
	if response.Body == nil {
		return nil, nil, errors.New("trainer-agent response body is missing")
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, int64(maximumResponseBytes)+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, nil, fmt.Errorf("read trainer-agent response: %w", readErr)
	}
	if closeErr != nil {
		return nil, nil, fmt.Errorf("close trainer-agent response: %w", closeErr)
	}
	if len(raw) > maximumResponseBytes {
		return nil, nil, fmt.Errorf("trainer-agent response exceeds %d bytes", maximumResponseBytes)
	}
	if response.Header.Get("Content-Encoding") != "" {
		return nil, nil, errors.New("trainer-agent response content encoding is forbidden")
	}
	return response, raw, nil
}

func (client *HTTPClient) parseClaim(wire traineragentprotocol.ClaimResponseV1) (*Claim, error) {
	leaseMilliseconds, _ := durationMilliseconds(client.config.LeaseDuration)
	if wire.Protocol != traineragentprotocol.ClaimResponseProtocolV1 || !uuidV4Pattern.MatchString(wire.RunID) ||
		!uuidV4Pattern.MatchString(wire.AttemptToken) || wire.LeaseDurationMilliseconds != leaseMilliseconds ||
		!sha256Pattern.MatchString(wire.InputManifestSHA256) || !sha256Pattern.MatchString(wire.InputBundleSHA256) {
		return nil, errors.New("trainer-agent claim metadata is invalid")
	}
	expiresAt, err := parseCanonicalTimestamp(wire.LeaseExpiresAt)
	if err != nil {
		return nil, err
	}
	bundle, digest, err := requireCanonicalBundle(wire.InputBundle, client.config.MaximumInputBundleBytes, "training input")
	if err != nil || digest != wire.InputBundleSHA256 {
		return nil, errors.New("trainer-agent claim input bundle differs from its digest")
	}
	claim := &Claim{
		LeaseReference: LeaseReference{RunID: wire.RunID, AttemptToken: wire.AttemptToken},
		LeaseDuration:  client.config.LeaseDuration, LeaseExpiresAt: expiresAt,
		ClaimedAt:           expiresAt.Add(-client.config.LeaseDuration),
		InputManifestSHA256: wire.InputManifestSHA256, InputBundleSHA256: wire.InputBundleSHA256,
		InputBundle: bundle,
	}
	return claim, nil
}

func (client *HTTPClient) validateClaim(claim Claim) error {
	if err := validateLeaseReference(claim.LeaseReference); err != nil {
		return err
	}
	if claim.LeaseDuration != client.config.LeaseDuration || claim.ClaimedAt.IsZero() ||
		claim.LeaseExpiresAt.Sub(claim.ClaimedAt) != claim.LeaseDuration ||
		!sha256Pattern.MatchString(claim.InputManifestSHA256) ||
		!sha256Pattern.MatchString(claim.InputBundleSHA256) {
		return errors.New("trainer-agent claim provenance is invalid")
	}
	_, digest, err := requireCanonicalBundle(claim.InputBundle, client.config.MaximumInputBundleBytes, "training input")
	if err != nil || digest != claim.InputBundleSHA256 {
		return errors.New("trainer-agent claim input bundle differs from its digest")
	}
	return nil
}

func (client *HTTPClient) remoteError(response *http.Response, raw []byte) error {
	if err := requireMediaType(response, traineragentprotocol.ErrorMediaTypeV1); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > maximumTransportErrorBytes {
		return errors.New("trainer-agent error response is empty or oversized")
	}
	var wire traineragentprotocol.ErrorResponseV1
	if err := decodeCanonicalResponse(raw, &wire); err != nil {
		return fmt.Errorf("decode trainer-agent error response: %w", err)
	}
	if wire.Protocol != traineragentprotocol.ErrorResponseProtocolV1 || !remoteCodePattern.MatchString(wire.Code) ||
		wire.Detail == "" || strings.TrimSpace(wire.Detail) != wire.Detail || len(wire.Detail) > maximumFailureDetailBytes ||
		!utf8.ValidString(wire.Detail) || strings.ContainsRune(wire.Detail, 0) {
		return errors.New("trainer-agent error response violates its schema")
	}
	return &RemoteError{
		StatusCode: response.StatusCode, Code: wire.Code, Detail: wire.Detail, Retryable: wire.Retryable,
	}
}

func encodeCanonicalRequest(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode trainer-agent request: %w", err)
	}
	canonical, _, err := canonicaljson.Object(raw, maximumTransportBundleBytes+maximumTransportEnvelopeBytes)
	if err != nil {
		return nil, fmt.Errorf("canonicalize trainer-agent request: %w", err)
	}
	return canonical, nil
}

func canonicalRequestSHA256(value any) (string, error) {
	canonical, err := encodeCanonicalRequest(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func decodeCanonicalResponse(raw []byte, destination any) error {
	canonical, _, err := canonicaljson.Object(raw, len(raw))
	if err != nil || !bytes.Equal(canonical, raw) {
		return errors.New("response must be one canonical JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains a trailing JSON value")
		}
		return err
	}
	return nil
}

func requireCanonicalBundle(raw []byte, maximumBytes int, label string) (json.RawMessage, string, error) {
	canonical, digest, err := canonicaljson.Object(raw, maximumBytes)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, "", fmt.Errorf("%s bundle must be a bounded canonical JSON object", label)
	}
	return canonical, digest, nil
}

func requireMediaType(response *http.Response, expected string) error {
	if response.Header.Get("Content-Type") != expected {
		return fmt.Errorf("trainer-agent response content type must be %s", expected)
	}
	return nil
}

func claimPath(runID string) string {
	return traineragentprotocol.HTTPBasePathV1 + "/claims/" + runID
}

func validateLeaseReference(lease LeaseReference) error {
	if !uuidV4Pattern.MatchString(lease.RunID) || !uuidV4Pattern.MatchString(lease.AttemptToken) {
		return errors.New("trainer-agent lease requires canonical run and attempt UUIDv4 values")
	}
	return nil
}

func validateFailureReport(failure FailureReport) error {
	if !remoteCodePattern.MatchString(failure.Code) || failure.Detail == "" ||
		strings.TrimSpace(failure.Detail) != failure.Detail || len(failure.Detail) > maximumFailureDetailBytes ||
		!utf8.ValidString(failure.Detail) || strings.ContainsRune(failure.Detail, 0) {
		return errors.New("trainer-agent failure report is invalid")
	}
	return nil
}

func parseCanonicalTimestamp(raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != raw {
		return time.Time{}, errors.New("trainer-agent lease timestamp must be canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func durationMilliseconds(duration time.Duration) (int64, error) {
	if duration <= 0 || duration%time.Millisecond != 0 {
		return 0, errors.New("duration must be a positive whole number of milliseconds")
	}
	return int64(duration / time.Millisecond), nil
}

func parsePositiveInt(raw, label string) (int, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 || value > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("%s must be a positive platform integer", label)
	}
	return int(value), nil
}
