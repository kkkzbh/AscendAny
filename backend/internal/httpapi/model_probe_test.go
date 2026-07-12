package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/modelprobe"
)

type modelProbeServiceStub struct {
	test func(context.Context, string, string) (modelprobe.Result, error)
}

func (stub modelProbeServiceStub) Test(ctx context.Context, access, key string) (modelprobe.Result, error) {
	return stub.test(ctx, access, key)
}

func TestModelConnectionProbeReturnsSafeProvenance(t *testing.T) {
	t.Parallel()
	checkedAt := time.Date(2026, 7, 11, 6, 7, 8, 0, time.UTC)
	calls := 0
	service := modelProbeServiceStub{test: func(_ context.Context, access, key string) (modelprobe.Result, error) {
		calls++
		if access != "admin-access" || key != "chat.primary" {
			t.Fatalf("probe arguments access=%q key=%q", access, key)
		}
		return modelprobe.Result{
			ConfigurationKey:          key,
			ConfigurationHeadRevision: 5,
			ConfigurationVersion:      5,
			ConfigurationSHA256:       strings.Repeat("a", 64),
			Authority:                 "api.example.com",
			Model:                     "provider-model-v3",
			CheckedAt:                 checkedAt,
			LatencyMilliseconds:       42,
		}, nil
	}}
	handler := newModelProbeTestHandler(t, service)
	request := modelProbeRequest(http.MethodPost, "/api/v2/admin/model-connections/chat.primary/test", "")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)

	want := `{"configurationKey":"chat.primary","configurationHeadRevision":5,"configurationVersion":5,"configurationSha256":"` + strings.Repeat("a", 64) + `","authority":"api.example.com","model":"provider-model-v3","checkedAt":"2026-07-11T06:07:08Z","latencyMilliseconds":42}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != want || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestModelConnectionProbeRejectsAmbiguousRequestBeforeService(t *testing.T) {
	t.Parallel()
	calls := 0
	handler := newModelProbeTestHandler(t, modelProbeServiceStub{test: func(context.Context, string, string) (modelprobe.Result, error) {
		calls++
		return modelprobe.Result{}, nil
	}})
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		auth   bool
		status int
	}{
		{name: "body", method: http.MethodPost, path: "/api/v2/admin/model-connections/chat.primary/test", body: `{}`, auth: true, status: http.StatusBadRequest},
		{name: "query", method: http.MethodPost, path: "/api/v2/admin/model-connections/chat.primary/test?retry=1", auth: true, status: http.StatusBadRequest},
		{name: "invalid key", method: http.MethodPost, path: "/api/v2/admin/model-connections/Bad/test", auth: true, status: http.StatusBadRequest},
		{name: "missing bearer", method: http.MethodPost, path: "/api/v2/admin/model-connections/chat.primary/test", status: http.StatusUnauthorized},
		{name: "wrong method", method: http.MethodGet, path: "/api/v2/admin/model-connections/chat.primary/test", auth: true, status: http.StatusMethodNotAllowed},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := modelProbeRequest(test.method, test.path, test.body)
			if !test.auth {
				request.Header.Del("Authorization")
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached model probe service: %d", calls)
	}
}

func TestModelConnectionProbeSanitizesProviderFailures(t *testing.T) {
	t.Parallel()
	secret := "fixture-provider-credential"
	for _, test := range []struct {
		code       string
		wantStatus int
	}{
		{code: "provider_configuration_invalid", wantStatus: http.StatusUnprocessableEntity},
		{code: "provider_credential_invalid", wantStatus: http.StatusUnprocessableEntity},
		{code: "provider_request_rejected", wantStatus: http.StatusUnprocessableEntity},
		{code: "provider_credential_unavailable", wantStatus: http.StatusServiceUnavailable},
		{code: "provider_temporarily_unavailable", wantStatus: http.StatusServiceUnavailable},
		{code: "provider_timeout", wantStatus: http.StatusGatewayTimeout},
		{code: "provider_protocol_invalid", wantStatus: http.StatusBadGateway},
	} {
		test := test
		t.Run(test.code, func(t *testing.T) {
			handler := newModelProbeTestHandler(t, modelProbeServiceStub{test: func(context.Context, string, string) (modelprobe.Result, error) {
				return modelprobe.Result{}, &modelprobe.Error{
					Code: modelprobe.ErrorProviderRejected,
					Op:   "probe",
					Cause: &chatagent.ProviderFailure{
						Code: test.code, Detail: secret, Cause: errors.New(secret),
					},
				}
			}})
			request := modelProbeRequest(http.MethodPost, "/api/v2/admin/model-connections/chat.primary/test", "")
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || strings.Contains(response.Body.String(), secret) || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestModelConnectionProbePreflightUsesAuthorizationOnly(t *testing.T) {
	t.Parallel()
	handler := newModelProbeTestHandler(t, unusedModelProbeService{})
	request := httptest.NewRequest(http.MethodOptions, "/api/v2/admin/model-connections/chat.primary/test", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Origin", "https://ascendany.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "Authorization")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Headers") != "Authorization" {
		t.Fatalf("status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func newModelProbeTestHandler(t *testing.T, service ModelProbeService) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ModelProbe = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func modelProbeRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Origin", "https://ascendany.example")
	request.Header.Set("Authorization", "Bearer admin-access")
	return request
}
