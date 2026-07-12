package feedback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	credentialdomain "github.com/kkkzbh/AscendAny/backend/internal/credential"
)

const (
	WebhookConfigurationSchema = "ascendany.feedback_delivery.webhook.v1"
	webhookPayloadSchema       = "ascendany.feedback.webhook_payload.v1"

	minWebhookTimeoutMilliseconds = int64(100)
	maxWebhookTimeoutMilliseconds = int64(30_000)
	maxWebhookConfigurationBytes  = 4096
	maxWebhookURLBytes            = 2048
	maxWebhookRequestBodyBytes    = 192 << 10
	maxWebhookResponseBodyBytes   = 32 << 10
	maxWebhookResponseHeaderBytes = 16 << 10
	maxWebhookReceiptBytes        = 256
)

type webhookConfiguration struct {
	URL                 string
	Authority           string
	TimeoutMilliseconds int64
}

type webhookPayload struct {
	Schema     string  `json:"schema"`
	FeedbackID string  `json:"feedbackId"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Platform   *string `json:"platform,omitempty"`
	AppVersion *string `json:"appVersion,omitempty"`
	UserAgent  *string `json:"userAgent,omitempty"`
}

type webhookReceipt struct {
	StatusCode         int    `json:"statusCode"`
	ResponseBodySHA256 string `json:"responseBodySha256"`
}

type WebhookDeliveryProvider struct {
	credentials credentialdomain.Resolver
	client      *http.Client
}

func NewWebhookDeliveryProvider(credentials credentialdomain.Resolver) (*WebhookDeliveryProvider, error) {
	return newWebhookDeliveryProvider(credentials, defaultWebhookTransport())
}

func newWebhookDeliveryProvider(
	credentials credentialdomain.Resolver,
	transport http.RoundTripper,
) (*WebhookDeliveryProvider, error) {
	if credentials == nil || transport == nil {
		return nil, feedbackError(
			ErrorInvalidConfiguration,
			true,
			"construct webhook delivery provider",
			errors.New("credential resolver and HTTP transport are required"),
		)
	}
	return &WebhookDeliveryProvider{
		credentials: credentials,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func defaultWebhookTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		ResponseHeaderTimeout:  time.Duration(maxWebhookTimeoutMilliseconds) * time.Millisecond,
		MaxResponseHeaderBytes: maxWebhookResponseHeaderBytes,
		DisableCompression:     true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

func (provider *WebhookDeliveryProvider) Deliver(ctx context.Context, delivery DeliveryRequest) ([]byte, error) {
	if ctx == nil {
		return nil, providerFailure("webhook_request_invalid", true, errors.New("delivery context is required"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	configuration, failure := parseWebhookConfiguration(delivery)
	if failure != nil {
		return nil, failure
	}
	if failure := validateWebhookDelivery(delivery); failure != nil {
		return nil, failure
	}

	credential, err := provider.credentials.Resolve(ctx, *delivery.CredentialRef, configuration.Authority)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, providerFailure("webhook_credential_unavailable", true, err)
	}
	if !credentialdomain.ValidBearer([]byte(credential)) {
		return nil, providerFailure("webhook_credential_invalid", true, errors.New("resolved webhook credential violates the bearer contract"))
	}

	payload, err := json.Marshal(webhookPayload{
		Schema:     webhookPayloadSchema,
		FeedbackID: delivery.FeedbackID,
		Title:      delivery.Title,
		Content:    delivery.Content,
		Platform:   delivery.Platform,
		AppVersion: delivery.AppVersion,
		UserAgent:  delivery.UserAgent,
	})
	if err != nil || len(payload) == 0 || len(payload) > maxWebhookRequestBodyBytes {
		return nil, providerFailure("webhook_request_invalid", true, errors.New("webhook payload exceeds its encoding contract"))
	}

	requestContext, cancel := context.WithTimeout(ctx, time.Duration(configuration.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, configuration.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL cannot form an HTTP request"))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", delivery.FeedbackID)
	request.Header.Set("User-Agent", "AscendAny-Feedback-Delivery/2")

	response, err := provider.client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if requestContext.Err() != nil {
			return nil, providerFailure("webhook_timeout", false, errors.New("webhook request exceeded its configured timeout"))
		}
		return nil, classifyWebhookTransportFailure(err)
	}
	defer response.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxWebhookResponseBodyBytes+1))
	if readErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if requestContext.Err() != nil {
			return nil, providerFailure("webhook_timeout", false, errors.New("webhook response exceeded its configured timeout"))
		}
		return nil, providerFailure("webhook_response_read_failure", false, errors.New("webhook response could not be read"))
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyWebhookStatus(response.StatusCode)
	}
	if len(responseBody) > maxWebhookResponseBodyBytes {
		return nil, providerFailure("webhook_response_too_large", true, errors.New("successful webhook response exceeds the body limit"))
	}
	digest := sha256.Sum256(responseBody)
	receipt, err := json.Marshal(webhookReceipt{
		StatusCode:         response.StatusCode,
		ResponseBodySHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil || len(receipt) == 0 || len(receipt) > maxWebhookReceiptBytes {
		return nil, providerFailure("webhook_receipt_invalid", true, errors.New("webhook receipt exceeds its encoding contract"))
	}
	return receipt, nil
}

func parseWebhookConfiguration(delivery DeliveryRequest) (webhookConfiguration, *ProviderFailure) {
	if delivery.ConfigurationSchema != WebhookConfigurationSchema {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration schema is unsupported"))
	}
	if len(delivery.Configuration) == 0 || len(delivery.Configuration) > maxWebhookConfigurationBytes {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration document is unbounded"))
	}

	decoder := json.NewDecoder(bytes.NewReader(delivery.Configuration))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration must be an object"))
	}
	var configuration webhookConfiguration
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, keyOK := keyToken.(string)
		if err != nil || !keyOK {
			return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration key is invalid"))
		}
		if _, exists := seen[key]; exists {
			return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration contains a duplicate field"))
		}
		seen[key] = struct{}{}
		switch key {
		case "url":
			if err := decoder.Decode(&configuration.URL); err != nil {
				return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL must be a string"))
			}
		case "timeoutMilliseconds":
			if err := decoder.Decode(&configuration.TimeoutMilliseconds); err != nil {
				return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook timeout must be an integer"))
			}
		default:
			return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration contains an unknown field"))
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration object is incomplete"))
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			_ = token
		}
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook configuration contains trailing data"))
	}
	if _, exists := seen["url"]; !exists {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL is required"))
	}
	if _, exists := seen["timeoutMilliseconds"]; !exists {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook timeout is required"))
	}
	configuration, failure := normalizeWebhookConfiguration(configuration)
	if failure != nil {
		return webhookConfiguration{}, failure
	}
	return configuration, nil
}

func normalizeWebhookConfiguration(configuration webhookConfiguration) (webhookConfiguration, *ProviderFailure) {
	if configuration.URL == "" || len(configuration.URL) > maxWebhookURLBytes || configuration.URL != strings.TrimSpace(configuration.URL) ||
		configuration.TimeoutMilliseconds < minWebhookTimeoutMilliseconds ||
		configuration.TimeoutMilliseconds > maxWebhookTimeoutMilliseconds {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL or timeout violates its bounds"))
	}
	parsed, err := url.Parse(configuration.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL must be an absolute HTTPS URL without userinfo, query, or fragment"))
	}
	host, valid := canonicalWebhookHost(parsed.Hostname())
	if !valid {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL host is invalid"))
	}
	rawPort := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL port is invalid"))
	}
	if rawPort == "" {
		rawPort = "443"
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return webhookConfiguration{}, providerFailure("webhook_configuration_invalid", true, errors.New("webhook URL port is invalid"))
	}
	configuration.Authority = net.JoinHostPort(host, strconv.FormatUint(port, 10))
	parsed.Host = configuration.Authority
	configuration.URL = parsed.String()
	return configuration, nil
}

func canonicalWebhookHost(raw string) (string, bool) {
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String(), true
	}
	host := strings.TrimSuffix(strings.ToLower(raw), ".")
	return host, credentialdomain.ValidAuthority(net.JoinHostPort(host, "443"))
}

// CanonicalWebhookAuthority returns the destination identity used to bind a
// credential file. Default HTTPS port 443 is always explicit.
func CanonicalWebhookAuthority(endpoint string) (string, error) {
	configuration, failure := normalizeWebhookConfiguration(webhookConfiguration{
		URL:                 endpoint,
		TimeoutMilliseconds: minWebhookTimeoutMilliseconds,
	})
	if failure != nil {
		return "", failure
	}
	return configuration.Authority, nil
}

func validateWebhookDelivery(delivery DeliveryRequest) *ProviderFailure {
	if !canonicalUUIDv4.MatchString(delivery.FeedbackID) || delivery.ConfigurationID <= 0 || delivery.CredentialRef == nil ||
		!configurationKey.MatchString(*delivery.CredentialRef) || delivery.Title == "" || len(delivery.Title) > MaxTitleBytes ||
		delivery.Title != strings.TrimSpace(delivery.Title) || delivery.Content == "" || len(delivery.Content) > MaxContentBytes ||
		delivery.Content != strings.TrimSpace(delivery.Content) {
		return providerFailure("webhook_request_invalid", true, errors.New("stored feedback delivery violates its contract"))
	}
	for _, optional := range []struct {
		value *string
		limit int
	}{
		{delivery.Platform, MaxPlatformBytes},
		{delivery.AppVersion, MaxAppVersionBytes},
		{delivery.UserAgent, MaxUserAgentBytes},
	} {
		if optional.value != nil && (len(*optional.value) > optional.limit || *optional.value != strings.TrimSpace(*optional.value)) {
			return providerFailure("webhook_request_invalid", true, errors.New("stored feedback metadata violates its contract"))
		}
	}
	return nil
}

func classifyWebhookStatus(statusCode int) *ProviderFailure {
	switch {
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return providerFailure("webhook_redirect_rejected", true, fmt.Errorf("webhook returned redirect status %d", statusCode))
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooEarly || statusCode == http.StatusTooManyRequests:
		return providerFailure("webhook_temporarily_unavailable", false, fmt.Errorf("webhook returned retryable status %d", statusCode))
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return providerFailure("webhook_auth_rejected", true, fmt.Errorf("webhook returned authorization status %d", statusCode))
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return providerFailure("webhook_request_rejected", true, fmt.Errorf("webhook returned rejection status %d", statusCode))
	case statusCode >= http.StatusInternalServerError && statusCode <= 599:
		return providerFailure("webhook_temporarily_unavailable", false, fmt.Errorf("webhook returned retryable status %d", statusCode))
	default:
		return providerFailure("webhook_protocol_rejected", true, fmt.Errorf("webhook returned unsupported status %d", statusCode))
	}
}

func classifyWebhookTransportFailure(err error) *ProviderFailure {
	var certificateInvalid x509.CertificateInvalidError
	var hostnameInvalid x509.HostnameError
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &certificateInvalid) || errors.As(err, &hostnameInvalid) || errors.As(err, &unknownAuthority) {
		return providerFailure("webhook_tls_rejected", true, errors.New("webhook TLS identity was rejected"))
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) && dnsError.IsNotFound {
		return providerFailure("webhook_dns_not_found", true, errors.New("webhook host does not exist"))
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return providerFailure("webhook_timeout", false, errors.New("webhook transport timed out"))
	}
	return providerFailure("webhook_transport_failure", false, errors.New("webhook transport failed"))
}

func providerFailure(code string, permanent bool, cause error) *ProviderFailure {
	return &ProviderFailure{Code: code, Permanent: permanent, Cause: cause}
}
