package traineragent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHTTPClientClaimsAuthenticatedCanonicalBundle(t *testing.T) {
	t.Parallel()
	bundle := canonicalTrainerAgentObject(t, map[string]any{"protocol": "ascendany.recommendation.training-bundle.v2"})
	_, bundleSHA256, err := canonicaljson.Object(bundle, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	responseBody := canonicalTrainerAgentObject(t, traineragentprotocol.ClaimResponseV1{
		Protocol: traineragentprotocol.ClaimResponseProtocolV1, RunID: testRunID, AttemptToken: testAttemptToken,
		LeaseDurationMilliseconds: 300, LeaseExpiresAt: "2030-01-02T03:04:05Z",
		InputManifestSHA256: strings.Repeat("a", 64), InputBundleSHA256: bundleSHA256, InputBundle: bundle,
	})
	client := testHTTPClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://trainer.example"+traineragentprotocol.HTTPBasePathV1+"/claims" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer "+testBearerToken ||
			request.Header.Get("Content-Type") != traineragentprotocol.ClaimMediaTypeV1 || request.Header.Get("Accept") != traineragentprotocol.ClaimMediaTypeV1 ||
			request.Header.Get("User-Agent") != trainerAgentUserAgent {
			t.Fatalf("headers = %#v", request.Header)
		}
		raw, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var claimRequest traineragentprotocol.ClaimRequestV1
		if err := decodeCanonicalResponse(raw, &claimRequest); err != nil {
			t.Fatal(err)
		}
		if claimRequest.Protocol != traineragentprotocol.ClaimRequestProtocolV1 || claimRequest.AgentID != "rtx-01" || claimRequest.LeaseDurationMilliseconds != 300 {
			t.Fatalf("claim request = %#v", claimRequest)
		}
		return trainerAgentResponse(http.StatusOK, traineragentprotocol.ClaimMediaTypeV1, responseBody), nil
	})
	claim, err := client.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if claim.RunID != testRunID || claim.AttemptToken != testAttemptToken || claim.InputBundleSHA256 != bundleSHA256 ||
		!bytes.Equal(claim.InputBundle, bundle) || claim.LeaseDuration != 300*time.Millisecond ||
		claim.ClaimedAt.Format(time.RFC3339Nano) != "2030-01-02T03:04:04.7Z" {
		t.Fatalf("claim = %#v", claim)
	}
}

func TestHTTPClientAcceptsOnlyBodylessNoContentClaim(t *testing.T) {
	t.Parallel()
	client := testHTTPClient(t, func(*http.Request) (*http.Response, error) {
		return trainerAgentResponse(http.StatusNoContent, "", nil), nil
	})
	claim, err := client.Claim(context.Background())
	if err != nil || claim != nil {
		t.Fatalf("claim = %#v error = %v", claim, err)
	}

	client = testHTTPClient(t, func(*http.Request) (*http.Response, error) {
		return trainerAgentResponse(http.StatusNoContent, traineragentprotocol.ClaimMediaTypeV1, nil), nil
	})
	if _, err := client.Claim(context.Background()); err == nil || !strings.Contains(err.Error(), "no body or content type") {
		t.Fatalf("error = %v", err)
	}
}

func TestHTTPClientHeartbeatPublishAndFailureContracts(t *testing.T) {
	t.Parallel()
	input := canonicalTrainerAgentObject(t, map[string]any{"input": true})
	_, inputSHA256, _ := canonicaljson.Object(input, 1<<20)
	claim := Claim{
		LeaseReference: LeaseReference{RunID: testRunID, AttemptToken: testAttemptToken},
		LeaseDuration:  300 * time.Millisecond, LeaseExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		ClaimedAt:           time.Date(2030, 1, 2, 3, 4, 4, 700_000_000, time.UTC),
		InputManifestSHA256: strings.Repeat("a", 64), InputBundleSHA256: inputSHA256, InputBundle: input,
	}
	output := canonicalTrainerAgentObject(t, map[string]any{"output": true})
	_, outputBundleSHA256, err := canonicaljson.Object(output, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	outputRequestSHA256 := ""
	client := testHTTPClient(t, func(request *http.Request) (*http.Response, error) {
		calls++
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch calls {
		case 1:
			if request.URL.Path != claimPath(testRunID)+"/heartbeats" || request.Header.Get("Content-Type") != traineragentprotocol.HeartbeatMediaTypeV1 {
				t.Fatalf("heartbeat request = %s %#v", request.URL.Path, request.Header)
			}
			var value traineragentprotocol.HeartbeatRequestV1
			if err := decodeCanonicalResponse(raw, &value); err != nil || value.AttemptToken != testAttemptToken {
				t.Fatalf("heartbeat = %#v error = %v", value, err)
			}
			return trainerAgentResponse(http.StatusOK, traineragentprotocol.HeartbeatMediaTypeV1, canonicalTrainerAgentObject(t, traineragentprotocol.HeartbeatResponseV1{
				Protocol: traineragentprotocol.HeartbeatResponseProtocolV1, LeaseExpiresAt: "2030-01-02T03:04:06Z",
			})), nil
		case 2:
			if request.URL.Path != claimPath(testRunID)+"/output" || request.Header.Get("Content-Type") != traineragentprotocol.OutputMediaTypeV1 {
				t.Fatalf("output request = %s %#v", request.URL.Path, request.Header)
			}
			var value traineragentprotocol.OutputRequestV1
			if err := decodeCanonicalResponse(raw, &value); err != nil || !bytes.Equal(value.OutputBundle, output) ||
				value.InputManifestSHA256 != claim.InputManifestSHA256 {
				t.Fatalf("output = %#v error = %v", value, err)
			}
			_, outputRequestSHA256, err = canonicaljson.Object(raw, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			return trainerAgentResponse(http.StatusOK, traineragentprotocol.OutputMediaTypeV1, canonicalTrainerAgentObject(t, traineragentprotocol.OutputResponseV1{
				Protocol: traineragentprotocol.OutputResponseProtocolV1, Disposition: "activated", ModelID: testModelID,
				RuntimeConstructionSHA256: strings.Repeat("a", 64), RuntimeProvenanceSHA256: strings.Repeat("b", 64),
				RuntimeTreeSHA256: strings.Repeat("c", 64), HostCapabilitySHA256: strings.Repeat("d", 64),
				RuntimeAttestationSHA256: strings.Repeat("e", 64),
			})), nil
		case 3:
			if request.URL.Path != claimPath(testRunID)+"/failures" || request.Header.Get("Content-Type") != traineragentprotocol.FailureMediaTypeV1 {
				t.Fatalf("failure request = %s %#v", request.URL.Path, request.Header)
			}
			var value traineragentprotocol.FailureRequestV1
			if err := decodeCanonicalResponse(raw, &value); err != nil || value.Code != "trainer_timeout" || !value.Retryable {
				t.Fatalf("failure = %#v error = %v", value, err)
			}
			return trainerAgentResponse(http.StatusOK, traineragentprotocol.FailureMediaTypeV1, canonicalTrainerAgentObject(t, traineragentprotocol.FailureResponseV1{
				Protocol: traineragentprotocol.FailureResponseProtocolV1, Disposition: "requeued",
			})), nil
		default:
			t.Fatalf("unexpected request %d", calls)
			return nil, nil
		}
	})
	uploadedAt := time.Date(2030, 1, 2, 3, 4, 7, 0, time.UTC)
	client.now = func() time.Time { return uploadedAt }
	heartbeat, err := client.Heartbeat(context.Background(), claim.LeaseReference)
	if err != nil || heartbeat.LeaseExpiresAt.Format(time.RFC3339Nano) != "2030-01-02T03:04:06Z" ||
		heartbeat.SucceededAt.Format(time.RFC3339Nano) != "2030-01-02T03:04:05.7Z" {
		t.Fatalf("heartbeat = %#v error = %v", heartbeat, err)
	}
	publication, err := client.Publish(context.Background(), claim, output)
	if err != nil || publication.Disposition != PublicationActivated || publication.ModelID != testModelID ||
		publication.RequestSHA256 != outputRequestSHA256 || publication.OutputBundleSHA256 != outputBundleSHA256 ||
		publication.RuntimeConstructionSHA256 != strings.Repeat("a", 64) ||
		publication.RuntimeProvenanceSHA256 != strings.Repeat("b", 64) ||
		publication.RuntimeTreeSHA256 != strings.Repeat("c", 64) ||
		publication.HostCapabilitySHA256 != strings.Repeat("d", 64) ||
		publication.RuntimeAttestationSHA256 != strings.Repeat("e", 64) ||
		!publication.UploadedAt.Equal(uploadedAt) {
		t.Fatalf("publication = %#v error = %v", publication, err)
	}
	disposition, err := client.ReportFailure(context.Background(), claim, FailureReport{
		Code: "trainer_timeout", Detail: "trainer timed out", Retryable: true,
	})
	if err != nil || disposition != FailureRequeued {
		t.Fatalf("failure disposition = %q error = %v", disposition, err)
	}
}

func TestHTTPClientRejectsMalformedAndRemoteFailureResponses(t *testing.T) {
	t.Parallel()
	t.Run("noncanonical", func(t *testing.T) {
		client := testHTTPClient(t, func(*http.Request) (*http.Response, error) {
			return trainerAgentResponse(http.StatusOK, traineragentprotocol.ClaimMediaTypeV1, []byte(`{ "protocol":"invalid"}`)), nil
		})
		if _, err := client.Claim(context.Background()); err == nil || !strings.Contains(err.Error(), "canonical") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		client := testHTTPClient(t, func(*http.Request) (*http.Response, error) {
			return trainerAgentResponse(http.StatusOK, traineragentprotocol.ClaimMediaTypeV1, canonicalTrainerAgentObject(t, map[string]any{
				"protocol": traineragentprotocol.ClaimResponseProtocolV1, "unexpected": true,
			})), nil
		})
		if _, err := client.Claim(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("remote error", func(t *testing.T) {
		client := testHTTPClient(t, func(*http.Request) (*http.Response, error) {
			return trainerAgentResponse(http.StatusConflict, traineragentprotocol.ErrorMediaTypeV1, canonicalTrainerAgentObject(t, traineragentprotocol.ErrorResponseV1{
				Protocol: traineragentprotocol.ErrorResponseProtocolV1, Code: "lease_lost", Detail: "claim lease is no longer active", Retryable: false,
			})), nil
		})
		_, err := client.Claim(context.Background())
		var remote *RemoteError
		if !errors.As(err, &remote) || remote.StatusCode != http.StatusConflict || remote.Code != "lease_lost" || remote.Retryable {
			t.Fatalf("error = %#v", err)
		}
	})
}

func TestHTTPClientConfigurationRejectsNoncanonicalAuthorityAndSecrets(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*HTTPClientConfig){
		"HTTP":           func(config *HTTPClientConfig) { config.BaseURL = "http://trainer.example" },
		"path":           func(config *HTTPClientConfig) { config.BaseURL = "https://trainer.example/api" },
		"default port":   func(config *HTTPClientConfig) { config.BaseURL = "https://trainer.example:443" },
		"padded port":    func(config *HTTPClientConfig) { config.BaseURL = "https://trainer.example:0443" },
		"empty query":    func(config *HTTPClientConfig) { config.BaseURL = "https://trainer.example?" },
		"trailing dot":   func(config *HTTPClientConfig) { config.BaseURL = "https://trainer.example." },
		"short token":    func(config *HTTPClientConfig) { config.BearerToken = "short" },
		"padded agent":   func(config *HTTPClientConfig) { config.AgentID = " rtx-01" },
		"slow request":   func(config *HTTPClientConfig) { config.RequestTimeout = 101 * time.Millisecond },
		"long lease":     func(config *HTTPClientConfig) { config.LeaseDuration = 25 * time.Hour },
		"partial millis": func(config *HTTPClientConfig) { config.LeaseDuration += time.Nanosecond },
	} {
		t.Run(name, func(t *testing.T) {
			configuration := validHTTPClientConfig()
			mutate(&configuration)
			if _, err := newHTTPClient(configuration, &http.Client{Transport: roundTripFunc(nil)}); err == nil {
				t.Fatal("configuration error = nil")
			}
		})
	}
}

func TestNewHTTPClientDisablesProxyRedirectAndOldTLS(t *testing.T) {
	t.Parallel()
	client, err := NewHTTPClient(validHTTPClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.client.Transport)
	}
	if transport.Proxy != nil || !transport.DisableCompression || transport.TLSClientConfig == nil ||
		transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("transport policy = %#v", transport)
	}
	request, err := http.NewRequest(http.MethodGet, "https://trainer.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.client.CheckRedirect(request, nil); err == nil {
		t.Fatal("redirect error = nil")
	}
}

func testHTTPClient(t *testing.T, function roundTripFunc) *HTTPClient {
	t.Helper()
	client, err := newHTTPClient(validHTTPClientConfig(), &http.Client{Transport: function})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func validHTTPClientConfig() HTTPClientConfig {
	return HTTPClientConfig{
		BaseURL: "https://trainer.example", BearerToken: testBearerToken, AgentID: "rtx-01",
		LeaseDuration: 300 * time.Millisecond, RequestTimeout: 100 * time.Millisecond,
		MaximumInputBundleBytes: 1 << 20, MaximumOutputBundleBytes: 1 << 20,
	}
}

func trainerAgentResponse(status int, mediaType string, body []byte) *http.Response {
	header := make(http.Header)
	if mediaType != "" {
		header.Set("Content-Type", mediaType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func canonicalTrainerAgentObject(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

const (
	testBearerToken  = "0123456789abcdefghijklmnopqrstuvwxyzABCD"
	testRunID        = "123e4567-e89b-42d3-a456-426614174100"
	testAttemptToken = "123e4567-e89b-42d3-a456-426614174101"
	testModelID      = "123e4567-e89b-42d3-a456-426614174102"
)
