package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentserver"
)

const httpTrainerToken = "trainer-agent-token_0123456789abcdef"

type trainerVerifierStub struct {
	verify func(context.Context, string) (string, error)
}

func (stub trainerVerifierStub) Verify(ctx context.Context, token string) (string, error) {
	return stub.verify(ctx, token)
}

type trainerServiceStub struct {
	maximum   int64
	claim     func(context.Context, string, traineragentprotocol.ClaimRequestV1) (*traineragentprotocol.ClaimResponseV1, error)
	heartbeat func(context.Context, string, string, traineragentprotocol.HeartbeatRequestV1) (traineragentprotocol.HeartbeatResponseV1, error)
	publish   func(context.Context, string, string, string, traineragentprotocol.OutputRequestV1) (traineragentprotocol.OutputResponseV1, error)
	failure   func(context.Context, string, string, string, traineragentprotocol.FailureRequestV1) (traineragentprotocol.FailureResponseV1, error)
}

func (stub trainerServiceStub) MaximumClaimResponseBytes() int64 { return stub.maximum }
func (stub trainerServiceStub) MaximumOutputRequestBytes() int64 { return stub.maximum }

func (stub trainerServiceStub) Claim(ctx context.Context, agentID string, request traineragentprotocol.ClaimRequestV1) (*traineragentprotocol.ClaimResponseV1, error) {
	return stub.claim(ctx, agentID, request)
}

func (stub trainerServiceStub) Heartbeat(ctx context.Context, runID, agentID string, request traineragentprotocol.HeartbeatRequestV1) (traineragentprotocol.HeartbeatResponseV1, error) {
	return stub.heartbeat(ctx, runID, agentID, request)
}

func (stub trainerServiceStub) Publish(ctx context.Context, runID, agentID, digest string, request traineragentprotocol.OutputRequestV1) (traineragentprotocol.OutputResponseV1, error) {
	return stub.publish(ctx, runID, agentID, digest, request)
}

func (stub trainerServiceStub) ReportFailure(ctx context.Context, runID, agentID, digest string, request traineragentprotocol.FailureRequestV1) (traineragentprotocol.FailureResponseV1, error) {
	return stub.failure(ctx, runID, agentID, digest, request)
}

func TestTrainerAgentHandlerRejectsInvalidTransportBoundsAtConstruction(t *testing.T) {
	t.Parallel()
	for _, maximum := range []int64{0, maximumTrainerAgentOutputRequestBytes + 1} {
		options := testHandlerOptions(health.Report{Status: health.StatusReady})
		options.TrainerAgentTransportEnabled = true
		options.TrainerAgentVerifier = trainerVerifierStub{verify: func(context.Context, string) (string, error) {
			return "rtx-01", nil
		}}
		options.TrainerAgent = trainerServiceStub{maximum: maximum}
		if _, err := New(options); err == nil || !strings.Contains(err.Error(), "transport bounds") {
			t.Fatalf("maximum=%d New() error = %v", maximum, err)
		}
	}
}

func TestTrainerAgentClaimAuthenticatesAndReturnsCanonicalVersionedResponse(t *testing.T) {
	t.Parallel()
	bundle := canonicalHTTPTrainerObject(t, map[string]any{
		"protocol": "fixture", "payload": strings.Repeat("x", 128<<10),
	})
	if len(bundle) <= maximumTrainerAgentResponseBytes {
		t.Fatalf("claim fixture does not exercise the configured response bound: %d bytes", len(bundle))
	}
	verifierCalls := 0
	serviceCalls := 0
	handler := newTrainerAgentHandler(t, trainerVerifierStub{verify: func(_ context.Context, token string) (string, error) {
		verifierCalls++
		if token != httpTrainerToken {
			t.Fatalf("token = %q", token)
		}
		return "rtx-01", nil
	}}, trainerServiceStub{maximum: 1 << 20, claim: func(_ context.Context, agentID string, request traineragentprotocol.ClaimRequestV1) (*traineragentprotocol.ClaimResponseV1, error) {
		serviceCalls++
		if agentID != "rtx-01" || request.Protocol != traineragentprotocol.ClaimRequestProtocolV1 || request.LeaseDurationMilliseconds != 30000 {
			t.Fatalf("agent/request = %q %#v", agentID, request)
		}
		return &traineragentprotocol.ClaimResponseV1{
			Protocol: traineragentprotocol.ClaimResponseProtocolV1,
			RunID:    "11111111-1111-4111-8111-111111111111", AttemptToken: "22222222-2222-4222-8222-222222222222",
			LeaseDurationMilliseconds: 30000, LeaseExpiresAt: "2030-01-02T03:04:05Z",
			InputManifestSHA256: strings.Repeat("a", 64), InputBundleSHA256: strings.Repeat("b", 64), InputBundle: bundle,
		}, nil
	}})
	requestBody := canonicalHTTPTrainerObject(t, traineragentprotocol.ClaimRequestV1{
		Protocol: traineragentprotocol.ClaimRequestProtocolV1, AgentID: "rtx-01", LeaseDurationMilliseconds: 30000,
	})
	request := trainerAgentHTTPRequest(http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims", traineragentprotocol.ClaimMediaTypeV1, requestBody)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != traineragentprotocol.ClaimMediaTypeV1 ||
		verifierCalls != 1 || serviceCalls != 1 {
		t.Fatalf("status=%d headers=%#v verifier=%d service=%d body=%s", response.Code, response.Header(), verifierCalls, serviceCalls, response.Body.String())
	}
	canonical, _, err := canonicaljson.Object(response.Body.Bytes(), 1<<20)
	if err != nil || !bytes.Equal(canonical, response.Body.Bytes()) || bytes.HasSuffix(response.Body.Bytes(), []byte("\n")) {
		t.Fatalf("response is not exact canonical JSON: %s error=%v", response.Body.String(), err)
	}
}

func TestTrainerAgentEmptyClaimIsExactBodylessNoContent(t *testing.T) {
	t.Parallel()
	handler := newTrainerAgentHandler(t, trainerVerifierStub{verify: func(context.Context, string) (string, error) {
		return "rtx-01", nil
	}}, trainerServiceStub{maximum: 1 << 20, claim: func(context.Context, string, traineragentprotocol.ClaimRequestV1) (*traineragentprotocol.ClaimResponseV1, error) {
		return nil, nil
	}})
	body := canonicalHTTPTrainerObject(t, traineragentprotocol.ClaimRequestV1{
		Protocol: traineragentprotocol.ClaimRequestProtocolV1, AgentID: "rtx-01", LeaseDurationMilliseconds: 30000,
	})
	request := trainerAgentHTTPRequest(http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims", traineragentprotocol.ClaimMediaTypeV1, body)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Content-Type") != "" ||
		response.Header().Get("Cache-Control") != "no-store" || response.Body.Len() != 0 {
		t.Fatalf("status=%d headers=%#v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestTrainerAgentPolicyFailuresUseVersionedErrorMedia(t *testing.T) {
	t.Parallel()
	handler := newTrainerAgentHandler(t, trainerVerifierStub{verify: func(context.Context, string) (string, error) {
		t.Fatal("policy failure reached authentication")
		return "", nil
	}}, trainerServiceStub{maximum: 1 << 20})
	tests := []struct {
		name   string
		method string
		body   []byte
		status int
		code   string
	}{
		{name: "method", method: http.MethodGet, status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
		{name: "body", method: http.MethodGet, body: []byte(`{}`), status: http.StatusBadRequest, code: "request_body_not_allowed"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := trainerAgentHTTPRequest(test.method, traineragentprotocol.HTTPBasePathV1+"/claims", traineragentprotocol.ClaimMediaTypeV1, test.body)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != traineragentprotocol.ErrorMediaTypeV1 ||
				!bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+test.code+`"`)) {
				t.Fatalf("status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestTrainerAgentAuthenticationHappensBeforeBodyRead(t *testing.T) {
	t.Parallel()
	body := &observedRequestBody{Reader: strings.NewReader(strings.Repeat("x", 1<<20))}
	serviceCalled := false
	handler := newTrainerAgentHandler(t, trainerVerifierStub{verify: func(context.Context, string) (string, error) {
		return "", &traineragentserver.Error{Code: traineragentserver.ErrorAuthenticationRejected, Detail: "Authentication was rejected."}
	}}, trainerServiceStub{maximum: 1 << 20, claim: func(context.Context, string, traineragentprotocol.ClaimRequestV1) (*traineragentprotocol.ClaimResponseV1, error) {
		serviceCalled = true
		return nil, nil
	}})
	request := httptest.NewRequest(http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims", body)
	request.RemoteAddr = "192.0.2.15:44200"
	request.Header.Set("Authorization", "Bearer rejected-token_0123456789012345")
	request.Header.Set("Content-Type", traineragentprotocol.ClaimMediaTypeV1)
	request.Header.Set("Accept", traineragentprotocol.ClaimMediaTypeV1)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || body.read || !body.closed || serviceCalled ||
		response.Header().Get("Content-Type") != traineragentprotocol.ErrorMediaTypeV1 {
		t.Fatalf("status=%d read=%v closed=%v service=%v headers=%#v body=%s", response.Code, body.read, body.closed, serviceCalled, response.Header(), response.Body.String())
	}
}

func TestTrainerAgentRejectsBrowserNoncanonicalAndWrongMediaRequests(t *testing.T) {
	t.Parallel()
	serviceCalls := 0
	handler := newTrainerAgentHandler(t, trainerVerifierStub{verify: func(context.Context, string) (string, error) {
		return "rtx-01", nil
	}}, trainerServiceStub{maximum: 1 << 20, claim: func(context.Context, string, traineragentprotocol.ClaimRequestV1) (*traineragentprotocol.ClaimResponseV1, error) {
		serviceCalls++
		return nil, nil
	}})
	canonical := canonicalHTTPTrainerObject(t, traineragentprotocol.ClaimRequestV1{
		Protocol: traineragentprotocol.ClaimRequestProtocolV1, AgentID: "rtx-01", LeaseDurationMilliseconds: 30000,
	})
	tests := []struct {
		name   string
		body   []byte
		media  string
		origin bool
		user   *url.Userinfo
		status int
	}{
		{name: "browser", body: canonical, media: traineragentprotocol.ClaimMediaTypeV1, origin: true, status: http.StatusForbidden},
		{name: "noncanonical", body: append([]byte(" "), canonical...), media: traineragentprotocol.ClaimMediaTypeV1, status: http.StatusBadRequest},
		{name: "wrong media", body: canonical, media: "application/json", status: http.StatusUnsupportedMediaType},
		{name: "URL credentials", body: canonical, media: traineragentprotocol.ClaimMediaTypeV1, user: url.UserPassword("agent", "secret"), status: http.StatusBadRequest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := trainerAgentHTTPRequest(http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims", test.media, test.body)
			if test.origin {
				request.Header.Set("Origin", "https://ascendany.example")
			}
			request.URL.User = test.user
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != traineragentprotocol.ErrorMediaTypeV1 {
				t.Fatalf("status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
	if serviceCalls != 0 {
		t.Fatalf("invalid requests reached service %d times", serviceCalls)
	}
}

func TestTrainerAgentMapsLeaseLossAndOutputRejectionToVersionedErrors(t *testing.T) {
	t.Parallel()
	service := trainerServiceStub{
		maximum: 1 << 20,
		heartbeat: func(context.Context, string, string, traineragentprotocol.HeartbeatRequestV1) (traineragentprotocol.HeartbeatResponseV1, error) {
			return traineragentprotocol.HeartbeatResponseV1{}, &traineragentserver.Error{Code: traineragentserver.ErrorLeaseLost, Detail: "Claim lease is no longer active."}
		},
		publish: func(context.Context, string, string, string, traineragentprotocol.OutputRequestV1) (traineragentprotocol.OutputResponseV1, error) {
			return traineragentprotocol.OutputResponseV1{}, &traineragentserver.Error{Code: traineragentserver.ErrorOutputRejected, Detail: "Training output was rejected."}
		},
	}
	handler := newTrainerAgentHandler(t, trainerVerifierStub{verify: func(context.Context, string) (string, error) {
		return "rtx-01", nil
	}}, service)
	heartbeatBody := canonicalHTTPTrainerObject(t, traineragentprotocol.HeartbeatRequestV1{
		Protocol: traineragentprotocol.HeartbeatRequestProtocolV1, AgentID: "rtx-01", AttemptToken: "22222222-2222-4222-8222-222222222222",
	})
	heartbeat := trainerAgentHTTPRequest(http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims/11111111-1111-4111-8111-111111111111/heartbeats", traineragentprotocol.HeartbeatMediaTypeV1, heartbeatBody)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, heartbeat)
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"lease_lost"`)) {
		t.Fatalf("heartbeat status=%d body=%s", response.Code, response.Body.String())
	}

	outputBody := canonicalHTTPTrainerObject(t, traineragentprotocol.OutputRequestV1{
		Protocol: traineragentprotocol.OutputRequestProtocolV1, AgentID: "rtx-01", AttemptToken: "22222222-2222-4222-8222-222222222222",
		InputManifestSHA256: strings.Repeat("a", 64), OutputBundleSHA256: strings.Repeat("b", 64),
		OutputBundle: json.RawMessage(`{"output":true}`),
	})
	output := trainerAgentHTTPRequest(http.MethodPost, traineragentprotocol.HTTPBasePathV1+"/claims/11111111-1111-4111-8111-111111111111/output", traineragentprotocol.OutputMediaTypeV1, outputBody)
	response = newTestResponseRecorder()
	handler.ServeHTTP(response, output)
	if response.Code != http.StatusUnprocessableEntity || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"output_rejected"`)) {
		t.Fatalf("output status=%d body=%s", response.Code, response.Body.String())
	}
}

func newTrainerAgentHandler(t *testing.T, verifier TrainerAgentBearerVerifier, service TrainerAgentService) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.TrainerAgentTransportEnabled = true
	options.TrainerAgentVerifier = verifier
	options.TrainerAgent = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func trainerAgentHTTPRequest(method, path, mediaType string, body []byte) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.RemoteAddr = "192.0.2.15:44200"
	request.Header.Set("Authorization", "Bearer "+httpTrainerToken)
	request.Header.Set("Content-Type", mediaType)
	request.Header.Set("Accept", mediaType)
	return request
}

func canonicalHTTPTrainerObject(t *testing.T, value any) []byte {
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

type observedRequestBody struct {
	io.Reader
	read   bool
	closed bool
}

func (body *observedRequestBody) Read(buffer []byte) (int, error) {
	body.read = true
	return body.Reader.Read(buffer)
}

func (body *observedRequestBody) Close() error {
	body.closed = true
	return nil
}
