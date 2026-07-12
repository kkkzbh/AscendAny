package feedback

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	credentialdomain "github.com/kkkzbh/AscendAny/backend/internal/credential"
)

type credentialResolverFunc func(context.Context, string, string) (string, error)

func (resolver credentialResolverFunc) Resolve(ctx context.Context, reference, authority string) (string, error) {
	return resolver(ctx, reference, authority)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (transport roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type failingResponseBody struct{}

func (failingResponseBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingResponseBody) Close() error             { return nil }

type callbackResponseBody struct {
	read func() error
}

func (body callbackResponseBody) Read([]byte) (int, error) { return 0, body.read() }
func (callbackResponseBody) Close() error                  { return nil }

type timeoutNetworkError struct{}

func (timeoutNetworkError) Error() string   { return "timeout" }
func (timeoutNetworkError) Timeout() bool   { return true }
func (timeoutNetworkError) Temporary() bool { return true }

func TestWebhookDeliveryProviderSendsAuthenticatedBoundedPayloadAndReceipt(t *testing.T) {
	t.Parallel()
	platform := "desktop"
	appVersion := "2.0.0"
	userAgent := "AscendAny Desktop"
	delivery := validWebhookDelivery("https://feedback.example.test/hooks/ascendany")
	delivery.Platform = &platform
	delivery.AppVersion = &appVersion
	delivery.UserAgent = &userAgent

	provider, err := newWebhookDeliveryProvider(
		credentialResolverFunc(func(_ context.Context, reference, authority string) (string, error) {
			if reference != "feedback.delivery.token" {
				t.Fatalf("credential reference=%q", reference)
			}
			if authority != "feedback.example.test:443" {
				t.Fatalf("credential authority=%q", authority)
			}
			return "exact-token_123", nil
		}),
		roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.URL.String() != "https://feedback.example.test:443/hooks/ascendany" {
				t.Fatalf("method=%q URL=%q", request.Method, request.URL)
			}
			if request.Header.Get("Authorization") != "Bearer exact-token_123" ||
				request.Header.Get("Content-Type") != "application/json" ||
				request.Header.Get("Accept") != "application/json" ||
				request.Header.Get("Idempotency-Key") != testFeedbackID ||
				request.Header.Get("User-Agent") != "AscendAny-Feedback-Delivery/2" {
				t.Fatalf("headers=%v", request.Header)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload webhookPayload
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Schema != webhookPayloadSchema || payload.FeedbackID != testFeedbackID ||
				payload.Title != "Desktop feedback" || payload.Content != "The import completed." ||
				payload.Platform == nil || *payload.Platform != platform ||
				payload.AppVersion == nil || *payload.AppVersion != appVersion ||
				payload.UserAgent == nil || *payload.UserAgent != userAgent {
				t.Fatalf("payload=%#v", payload)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"receiptId":"accepted-1"}`)),
				Request:    request,
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptBytes, err := provider.Deliver(context.Background(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	if len(receiptBytes) == 0 || len(receiptBytes) > maxWebhookReceiptBytes {
		t.Fatalf("receipt bytes=%d", len(receiptBytes))
	}
	var receipt webhookReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatal(err)
	}
	responseBody := []byte(`{"receiptId":"accepted-1"}`)
	digest := sha256.Sum256(responseBody)
	if receipt.StatusCode != http.StatusCreated || receipt.ResponseBodySHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("receipt=%#v", receipt)
	}
}

func TestWebhookDeliveryProviderRejectsRedirectWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	var redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/first":
			http.Redirect(writer, request, "/second", http.StatusTemporaryRedirect)
		case "/second":
			redirected.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	provider, err := newWebhookDeliveryProvider(
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil }),
		server.Client().Transport,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Deliver(context.Background(), validWebhookDelivery(server.URL+"/first"))
	assertProviderFailure(t, err, "webhook_redirect_rejected", true)
	if redirected.Load() != 0 {
		t.Fatalf("redirect target called %d times", redirected.Load())
	}
}

func TestWebhookDeliveryProviderRejectsNonExactConfiguration(t *testing.T) {
	t.Parallel()
	validDocument := `{"url":"https://feedback.example.test/hook","timeoutMilliseconds":1000}`
	tests := map[string]func(*DeliveryRequest){
		"wrong schema": func(delivery *DeliveryRequest) {
			delivery.ConfigurationSchema = "ascendany.feedback_delivery.webhook.v2"
		},
		"empty document": func(delivery *DeliveryRequest) { delivery.Configuration = nil },
		"oversized document": func(delivery *DeliveryRequest) {
			delivery.Configuration = json.RawMessage(`{"url":"https://` + strings.Repeat("a", maxWebhookConfigurationBytes) + `","timeoutMilliseconds":1000}`)
		},
		"array root": func(delivery *DeliveryRequest) { delivery.Configuration = json.RawMessage(`[]`) },
		"duplicate field": func(delivery *DeliveryRequest) {
			delivery.Configuration = json.RawMessage(`{"url":"https://a.test","url":"https://b.test","timeoutMilliseconds":1000}`)
		},
		"unknown field": func(delivery *DeliveryRequest) {
			delivery.Configuration = json.RawMessage(`{"url":"https://a.test","timeoutMilliseconds":1000,"provider":"webhook"}`)
		},
		"missing URL": func(delivery *DeliveryRequest) {
			delivery.Configuration = json.RawMessage(`{"timeoutMilliseconds":1000}`)
		},
		"missing timeout": func(delivery *DeliveryRequest) { delivery.Configuration = json.RawMessage(`{"url":"https://a.test"}`) },
		"URL type": func(delivery *DeliveryRequest) {
			delivery.Configuration = json.RawMessage(`{"url":1,"timeoutMilliseconds":1000}`)
		},
		"timeout type": func(delivery *DeliveryRequest) {
			delivery.Configuration = json.RawMessage(`{"url":"https://a.test","timeoutMilliseconds":1.5}`)
		},
		"trailing data":     func(delivery *DeliveryRequest) { delivery.Configuration = json.RawMessage(validDocument + `{}`) },
		"incomplete object": func(delivery *DeliveryRequest) { delivery.Configuration = json.RawMessage(`{"url":"https://a.test"`) },
		"HTTP URL": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("http://a.test/hook", 1000)
		},
		"URL userinfo": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://user@a.test/hook", 1000)
		},
		"URL query": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test/hook?token=x", 1000)
		},
		"empty URL query": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test/hook?", 1000)
		},
		"URL fragment": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test/hook#fragment", 1000)
		},
		"URL empty port": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test:/hook", 1000)
		},
		"URL zero port": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test:0/hook", 1000)
		},
		"URL large port": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test:99999/hook", 1000)
		},
		"relative URL": func(delivery *DeliveryRequest) { delivery.Configuration = webhookConfigurationJSON("/hook", 1000) },
		"URL outer space": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON(" https://a.test/hook", 1000)
		},
		"timeout too short": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test/hook", minWebhookTimeoutMilliseconds-1)
		},
		"timeout too long": func(delivery *DeliveryRequest) {
			delivery.Configuration = webhookConfigurationJSON("https://a.test/hook", maxWebhookTimeoutMilliseconds+1)
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := atomic.Bool{}
			provider := mustWebhookProvider(t,
				credentialResolverFunc(func(context.Context, string, string) (string, error) {
					called.Store(true)
					return "token", nil
				}),
				roundTripperFunc(func(*http.Request) (*http.Response, error) {
					called.Store(true)
					return nil, errors.New("must not be called")
				}),
			)
			delivery := validWebhookDelivery("https://feedback.example.test/hook")
			mutate(&delivery)
			_, err := provider.Deliver(context.Background(), delivery)
			assertProviderFailure(t, err, "webhook_configuration_invalid", true)
			if called.Load() {
				t.Fatal("credential resolver or transport called for invalid configuration")
			}
		})
	}
}

func TestWebhookDeliveryProviderRejectsInvalidStoredDelivery(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*DeliveryRequest){
		"feedback ID":        func(delivery *DeliveryRequest) { delivery.FeedbackID = "invalid" },
		"configuration ID":   func(delivery *DeliveryRequest) { delivery.ConfigurationID = 0 },
		"missing credential": func(delivery *DeliveryRequest) { delivery.CredentialRef = nil },
		"invalid credential": func(delivery *DeliveryRequest) { invalid := "INVALID"; delivery.CredentialRef = &invalid },
		"empty title":        func(delivery *DeliveryRequest) { delivery.Title = "" },
		"trimmed title":      func(delivery *DeliveryRequest) { delivery.Title = " title" },
		"large title":        func(delivery *DeliveryRequest) { delivery.Title = strings.Repeat("x", MaxTitleBytes+1) },
		"empty content":      func(delivery *DeliveryRequest) { delivery.Content = "" },
		"trimmed content":    func(delivery *DeliveryRequest) { delivery.Content = "content " },
		"large content":      func(delivery *DeliveryRequest) { delivery.Content = strings.Repeat("x", MaxContentBytes+1) },
		"large platform": func(delivery *DeliveryRequest) {
			value := strings.Repeat("x", MaxPlatformBytes+1)
			delivery.Platform = &value
		},
		"trimmed version": func(delivery *DeliveryRequest) { value := " 2.0"; delivery.AppVersion = &value },
		"large user agent": func(delivery *DeliveryRequest) {
			value := strings.Repeat("x", MaxUserAgentBytes+1)
			delivery.UserAgent = &value
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := atomic.Bool{}
			provider := mustWebhookProvider(t,
				credentialResolverFunc(func(context.Context, string, string) (string, error) {
					called.Store(true)
					return "token", nil
				}),
				roundTripperFunc(func(*http.Request) (*http.Response, error) {
					called.Store(true)
					return nil, errors.New("must not be called")
				}),
			)
			delivery := validWebhookDelivery("https://feedback.example.test/hook")
			mutate(&delivery)
			_, err := provider.Deliver(context.Background(), delivery)
			assertProviderFailure(t, err, "webhook_request_invalid", true)
			if called.Load() {
				t.Fatal("credential resolver or transport called for invalid delivery")
			}
		})
	}
}

func TestWebhookDeliveryProviderRejectsUnboundedEncodedPayload(t *testing.T) {
	t.Parallel()
	provider := mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil }),
		roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("transport called for unbounded payload")
			return nil, nil
		}),
	)
	delivery := validWebhookDelivery("https://feedback.example.test/hook")
	delivery.Title = strings.Repeat("\x01", MaxTitleBytes)
	delivery.Content = strings.Repeat("\x01", MaxContentBytes)
	platform := strings.Repeat("\x01", MaxPlatformBytes)
	appVersion := strings.Repeat("\x01", MaxAppVersionBytes)
	userAgent := strings.Repeat("\x01", MaxUserAgentBytes)
	delivery.Platform = &platform
	delivery.AppVersion = &appVersion
	delivery.UserAgent = &userAgent
	_, err := provider.Deliver(context.Background(), delivery)
	assertProviderFailure(t, err, "webhook_request_invalid", true)
}

func TestWebhookDeliveryProviderClassifiesCredentialAndContextFailures(t *testing.T) {
	t.Parallel()
	provider := mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "", errors.New("missing secret") }),
		roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("must not run") }),
	)
	_, err := provider.Deliver(context.Background(), validWebhookDelivery("https://feedback.example.test/hook"))
	assertProviderFailure(t, err, "webhook_credential_unavailable", true)
	for _, credential := range []string{"", "token with space", "token=middle"} {
		credential := credential
		invalid := mustWebhookProvider(t,
			credentialResolverFunc(func(context.Context, string, string) (string, error) { return credential, nil }),
			roundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("transport called for invalid credential")
				return nil, nil
			}),
		)
		_, err := invalid.Deliver(context.Background(), validWebhookDelivery("https://feedback.example.test/hook"))
		assertProviderFailure(t, err, "webhook_credential_invalid", true)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Deliver(canceled, validWebhookDelivery("https://feedback.example.test/hook"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
	if _, err := provider.Deliver(nil, validWebhookDelivery("https://feedback.example.test/hook")); err == nil {
		t.Fatal("nil context accepted")
	}

	resolverContext, cancelResolver := context.WithCancel(context.Background())
	cancelingResolver := mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) {
			cancelResolver()
			return "", errors.New("resolver interrupted")
		}),
		roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("must not run") }),
	)
	_, err = cancelingResolver.Deliver(resolverContext, validWebhookDelivery("https://feedback.example.test/hook"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("resolver cancellation error=%v", err)
	}
}

func TestWebhookDeliveryProviderClassifiesResponseStatuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status    int
		code      string
		permanent bool
	}{
		{http.StatusMovedPermanently, "webhook_redirect_rejected", true},
		{http.StatusTemporaryRedirect, "webhook_redirect_rejected", true},
		{http.StatusRequestTimeout, "webhook_temporarily_unavailable", false},
		{http.StatusTooEarly, "webhook_temporarily_unavailable", false},
		{http.StatusTooManyRequests, "webhook_temporarily_unavailable", false},
		{http.StatusUnauthorized, "webhook_auth_rejected", true},
		{http.StatusForbidden, "webhook_auth_rejected", true},
		{http.StatusBadRequest, "webhook_request_rejected", true},
		{http.StatusConflict, "webhook_request_rejected", true},
		{http.StatusInternalServerError, "webhook_temporarily_unavailable", false},
		{http.StatusServiceUnavailable, "webhook_temporarily_unavailable", false},
		{http.StatusSwitchingProtocols, "webhook_protocol_rejected", true},
	}
	for _, test := range tests {
		test := test
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			provider := providerReturning(t, test.status, strings.NewReader(strings.Repeat("x", maxWebhookResponseBodyBytes+1)))
			_, err := provider.Deliver(context.Background(), validWebhookDelivery("https://feedback.example.test/hook"))
			assertProviderFailure(t, err, test.code, test.permanent)
		})
	}
}

func TestWebhookDeliveryProviderBoundsSuccessfulResponseAndClassifiesReadFailure(t *testing.T) {
	t.Parallel()
	tooLarge := providerReturning(t, http.StatusOK, strings.NewReader(strings.Repeat("x", maxWebhookResponseBodyBytes+1)))
	_, err := tooLarge.Deliver(context.Background(), validWebhookDelivery("https://feedback.example.test/hook"))
	assertProviderFailure(t, err, "webhook_response_too_large", true)

	readFailure := providerReturning(t, http.StatusOK, failingResponseBody{})
	_, err = readFailure.Deliver(context.Background(), validWebhookDelivery("https://feedback.example.test/hook"))
	assertProviderFailure(t, err, "webhook_response_read_failure", false)
}

func TestWebhookDeliveryProviderEnforcesConfiguredTimeout(t *testing.T) {
	t.Parallel()
	provider := mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil }),
		roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	)
	delivery := validWebhookDelivery("https://feedback.example.test/hook")
	delivery.Configuration = webhookConfigurationJSON("https://feedback.example.test/hook", minWebhookTimeoutMilliseconds)
	started := time.Now()
	_, err := provider.Deliver(context.Background(), delivery)
	assertProviderFailure(t, err, "webhook_timeout", false)
	if elapsed := time.Since(started); elapsed < time.Duration(minWebhookTimeoutMilliseconds)*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("timeout elapsed=%s", elapsed)
	}
}

func TestWebhookDeliveryProviderPreservesParentCancellationAndClassifiesTransportFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancelingTransport := mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil }),
		roundTripperFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, context.Canceled
		}),
	)
	_, err := cancelingTransport.Deliver(ctx, validWebhookDelivery("https://feedback.example.test/hook"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("transport cancellation error=%v", err)
	}

	failedTransport := mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil }),
		roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }),
	)
	_, err = failedTransport.Deliver(context.Background(), validWebhookDelivery("https://feedback.example.test/hook"))
	assertProviderFailure(t, err, "webhook_transport_failure", false)
}

func TestWebhookDeliveryProviderAppliesTimeoutAndParentCancellationWhileReading(t *testing.T) {
	t.Parallel()
	readTimeout := providerReturning(t, http.StatusOK, callbackResponseBody{read: func() error {
		time.Sleep(time.Duration(minWebhookTimeoutMilliseconds+20) * time.Millisecond)
		return errors.New("late response")
	}})
	delivery := validWebhookDelivery("https://feedback.example.test/hook")
	delivery.Configuration = webhookConfigurationJSON("https://feedback.example.test/hook", minWebhookTimeoutMilliseconds)
	_, err := readTimeout.Deliver(context.Background(), delivery)
	assertProviderFailure(t, err, "webhook_timeout", false)

	ctx, cancel := context.WithCancel(context.Background())
	readCancellation := providerReturning(t, http.StatusOK, callbackResponseBody{read: func() error {
		cancel()
		return errors.New("parent canceled")
	}})
	_, err = readCancellation.Deliver(ctx, validWebhookDelivery("https://feedback.example.test/hook"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation error=%v", err)
	}
}

func TestWebhookTransportFailuresHaveExplicitDisposition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		code      string
		permanent bool
	}{
		{"TLS", x509.UnknownAuthorityError{}, "webhook_tls_rejected", true},
		{"DNS", &net.DNSError{IsNotFound: true}, "webhook_dns_not_found", true},
		{"timeout", timeoutNetworkError{}, "webhook_timeout", false},
		{"transport", errors.New("connection refused"), "webhook_transport_failure", false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failure := classifyWebhookTransportFailure(test.err)
			assertProviderFailure(t, failure, test.code, test.permanent)
		})
	}
}

func TestWebhookDeliveryProviderRequiresDependencies(t *testing.T) {
	t.Parallel()
	validResolver := credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil })
	validTransport := roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil })
	if _, err := newWebhookDeliveryProvider(nil, validTransport); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil resolver error=%v code=%q", err, CodeOf(err))
	}
	if _, err := newWebhookDeliveryProvider(validResolver, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil transport error=%v code=%q", err, CodeOf(err))
	}
	provider, err := NewWebhookDeliveryProvider(validResolver)
	if err != nil || provider.client.Transport == nil || provider.client.CheckRedirect == nil {
		t.Fatalf("provider=%#v error=%v", provider, err)
	}
	transport, ok := provider.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("production transport=%#v proxy must be disabled", provider.client.Transport)
	}
}

func TestCanonicalWebhookAuthorityNormalizesOneDestinationIdentity(t *testing.T) {
	t.Parallel()
	for endpoint, expected := range map[string]string{
		"https://Feedback.Example.Test./hook":     "feedback.example.test:443",
		"https://feedback.example.test:0443/hook": "feedback.example.test:443",
		"https://feedback.example.test:8443/hook": "feedback.example.test:8443",
		"https://[2001:0db8:0:0::1]/hook":         "[2001:db8::1]:443",
	} {
		authority, err := CanonicalWebhookAuthority(endpoint)
		if err != nil || authority != expected {
			t.Fatalf("endpoint=%q authority=%q expected=%q error=%v", endpoint, authority, expected, err)
		}
	}
	for _, endpoint := range []string{
		"http://feedback.example.test/hook",
		"https://bad_host.test/hook",
		"https://-bad.test/hook",
		"https://bad-.test/hook",
		"https://feedback.example.test:0/hook",
		"https://feedback.example.test:99999/hook",
	} {
		if authority, err := CanonicalWebhookAuthority(endpoint); err == nil {
			t.Fatalf("invalid endpoint=%q authority=%q", endpoint, authority)
		}
	}
}

func validWebhookDelivery(endpoint string) DeliveryRequest {
	credentialReference := "feedback.delivery.token"
	return DeliveryRequest{
		FeedbackID:          testFeedbackID,
		Title:               "Desktop feedback",
		Content:             "The import completed.",
		ConfigurationID:     1,
		ConfigurationSchema: WebhookConfigurationSchema,
		Configuration:       webhookConfigurationJSON(endpoint, 1000),
		CredentialRef:       &credentialReference,
	}
}

func webhookConfigurationJSON(endpoint string, timeoutMilliseconds int64) json.RawMessage {
	encoded, err := json.Marshal(map[string]any{
		"url":                 endpoint,
		"timeoutMilliseconds": timeoutMilliseconds,
	})
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustWebhookProvider(t *testing.T, resolver credentialdomain.Resolver, transport http.RoundTripper) *WebhookDeliveryProvider {
	t.Helper()
	provider, err := newWebhookDeliveryProvider(resolver, transport)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func providerReturning(t *testing.T, status int, body io.Reader) *WebhookDeliveryProvider {
	t.Helper()
	return mustWebhookProvider(t,
		credentialResolverFunc(func(context.Context, string, string) (string, error) { return "token", nil }),
		roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Header:     make(http.Header),
				Body:       io.NopCloser(body),
				Request:    request,
			}, nil
		}),
	)
}

func assertProviderFailure(t *testing.T, err error, code string, permanent bool) {
	t.Helper()
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Code != code || failure.Permanent != permanent || failure.Cause == nil {
		t.Fatalf("error=%v failure=%#v expected code=%q permanent=%t", err, failure, code, permanent)
	}
}
