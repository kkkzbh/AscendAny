package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

func TestBrowserBoundaryClientAddressForSSHLoopbackOrigins(t *testing.T) {
	handler := newTrustedLoopbackClientAddressHandler(t, []string{
		"http://127.0.0.1:5173",
		"http://localhost:5173",
		"https://ascendany.example",
	})

	tests := []struct {
		name               string
		origin             string
		forwardedAddresses []string
		wantStatus         int
	}{
		{name: "configured 127 origin", origin: "http://127.0.0.1:5173", wantStatus: http.StatusOK},
		{name: "configured localhost origin", origin: "http://localhost:5173", wantStatus: http.StatusOK},
		{name: "public origin requires forwarding header", origin: "https://ascendany.example", wantStatus: http.StatusBadRequest},
		{name: "public origin with forwarding header", origin: "https://ascendany.example", forwardedAddresses: []string{"203.0.113.7"}, wantStatus: http.StatusOK},
		{name: "duplicate forwarding header", origin: "http://127.0.0.1:5173", forwardedAddresses: []string{"203.0.113.7", "203.0.113.8"}, wantStatus: http.StatusBadRequest},
		{name: "spoofed forwarding header", origin: "http://127.0.0.1:5173", forwardedAddresses: []string{"client.example"}, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/policy", nil)
			request.RemoteAddr = "127.0.0.1:43100"
			request.Header.Set("Origin", test.origin)
			for _, address := range test.forwardedAddresses {
				request.Header.Add("CF-Connecting-IP", address)
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusOK && response.Header().Get("Access-Control-Allow-Origin") != test.origin {
				t.Fatalf("Access-Control-Allow-Origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
			}
		})
	}
}

func TestBrowserBoundaryAllowsPackagedElectronOpaqueOrigin(t *testing.T) {
	handler := newTrustedLoopbackClientAddressHandler(t, []string{
		"http://127.0.0.1:5173",
		"https://ascendany.example",
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/policy", nil)
	request.RemoteAddr = "127.0.0.1:43100"
	request.Header.Set("Origin", opaqueBrowserOrigin)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != opaqueBrowserOrigin ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("GET CORS headers = %#v", response.Header())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	preflight.RemoteAddr = "127.0.0.1:43100"
	preflight.Header.Set("Origin", opaqueBrowserOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type")
	preflightResponse := newTestResponseRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, body = %s", preflightResponse.Code, preflightResponse.Body.String())
	}
	if preflightResponse.Header().Get("Access-Control-Allow-Origin") != opaqueBrowserOrigin ||
		preflightResponse.Header().Get("Access-Control-Allow-Methods") != http.MethodPost ||
		preflightResponse.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf("preflight CORS headers = %#v", preflightResponse.Header())
	}
}

func TestBrowserBoundaryRejectsOpaqueOriginOutsidePackagedElectronCapability(t *testing.T) {
	handler := newTrustedLoopbackClientAddressHandler(t, []string{
		"http://127.0.0.1:5173",
		"https://ascendany.example",
	})

	tests := []struct {
		name               string
		method             string
		path               string
		remoteAddress      string
		requestedMethod    string
		forwardedAddresses []string
	}{
		{
			name:               "cloudflared request",
			method:             http.MethodGet,
			path:               "/api/v1/auth/policy",
			remoteAddress:      "127.0.0.1:43100",
			forwardedAddresses: []string{"203.0.113.7"},
		},
		{
			name:               "cloudflared preflight",
			method:             http.MethodOptions,
			path:               "/api/v1/auth/login",
			remoteAddress:      "127.0.0.1:43100",
			requestedMethod:    http.MethodPost,
			forwardedAddresses: []string{"203.0.113.7"},
		},
		{
			name:               "empty forwarding header",
			method:             http.MethodGet,
			path:               "/api/v1/auth/policy",
			remoteAddress:      "127.0.0.1:43100",
			forwardedAddresses: []string{""},
		},
		{
			name:               "empty forwarding header preflight",
			method:             http.MethodOptions,
			path:               "/api/v1/auth/login",
			remoteAddress:      "127.0.0.1:43100",
			requestedMethod:    http.MethodPost,
			forwardedAddresses: []string{""},
		},
		{
			name:               "duplicate forwarding header",
			method:             http.MethodGet,
			path:               "/api/v1/auth/policy",
			remoteAddress:      "127.0.0.1:43100",
			forwardedAddresses: []string{"203.0.113.7", "203.0.113.8"},
		},
		{
			name:               "duplicate forwarding header preflight",
			method:             http.MethodOptions,
			path:               "/api/v1/auth/login",
			remoteAddress:      "127.0.0.1:43100",
			requestedMethod:    http.MethodPost,
			forwardedAddresses: []string{"203.0.113.7", "203.0.113.8"},
		},
		{
			name:          "non-loopback peer",
			method:        http.MethodGet,
			path:          "/api/v1/auth/policy",
			remoteAddress: "192.0.2.25:43100",
		},
		{
			name:          "v2 route",
			method:        http.MethodGet,
			path:          "/api/v2/capabilities",
			remoteAddress: "127.0.0.1:43100",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.RemoteAddr = test.remoteAddress
			request.Header.Set("Origin", opaqueBrowserOrigin)
			if test.requestedMethod != "" {
				request.Header.Set("Access-Control-Request-Method", test.requestedMethod)
				request.Header.Set("Access-Control-Request-Headers", "Content-Type")
			}
			for _, address := range test.forwardedAddresses {
				request.Header.Add(cloudflareClientAddressHeader, address)
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusForbidden, response.Body.String())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("rejected request received CORS headers: %#v", response.Header())
			}
		})
	}

	publicOnlyHandler := newTrustedLoopbackClientAddressHandler(t, []string{"https://ascendany.example"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/policy", nil)
	request.RemoteAddr = "127.0.0.1:43100"
	request.Header.Set("Origin", opaqueBrowserOrigin)
	response := newTestResponseRecorder()
	publicOnlyHandler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("handler without a loopback HTTP origin returned status = %d, body = %s", response.Code, response.Body.String())
	}
}

func newTrustedLoopbackClientAddressHandler(t *testing.T, allowedOrigins []string) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.AllowedOrigins = allowedOrigins
	options.TrustedProxyCIDRs = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	}
	options.ClientIPHeader = cloudflareClientAddressHeader
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
