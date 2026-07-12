package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

const testWebOrigin = "https://ascendany.example"

type stubAuthService struct {
	login   func(context.Context, auth.LoginInput) (auth.AuthResult, error)
	refresh func(context.Context, auth.RefreshInput) (auth.AuthResult, error)
	logout  func(context.Context, auth.LogoutInput) error
	me      func(context.Context, string) (auth.Account, error)
}

func (service stubAuthService) Login(ctx context.Context, input auth.LoginInput) (auth.AuthResult, error) {
	if service.login == nil {
		panic("unexpected Login call")
	}
	return service.login(ctx, input)
}

func (service stubAuthService) Refresh(ctx context.Context, input auth.RefreshInput) (auth.AuthResult, error) {
	if service.refresh == nil {
		panic("unexpected Refresh call")
	}
	return service.refresh(ctx, input)
}

func (service stubAuthService) Logout(ctx context.Context, input auth.LogoutInput) error {
	if service.logout == nil {
		panic("unexpected Logout call")
	}
	return service.logout(ctx, input)
}

func (service stubAuthService) Me(ctx context.Context, token string) (auth.Account, error) {
	if service.me == nil {
		panic("unexpected Me call")
	}
	return service.me(ctx, token)
}

type captureRateLimiter struct {
	decision  RateLimitDecision
	decisions map[string]RateLimitDecision
	scope     string
	client    string
	calls     []rateLimitCall
}

type rateLimitCall struct {
	scope string
	key   string
}

func (limiter *captureRateLimiter) Allow(scope, client string) RateLimitDecision {
	limiter.scope = scope
	limiter.client = client
	limiter.calls = append(limiter.calls, rateLimitCall{scope: scope, key: client})
	if decision, present := limiter.decisions[scope]; present {
		return decision
	}
	if !limiter.decision.Allowed && limiter.decision.RetryAfter == 0 {
		return RateLimitDecision{Allowed: true}
	}
	return limiter.decision
}

func TestLoginUsesIndependentClientAndCanonicalUsernameLimits(t *testing.T) {
	service := stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		t.Fatal("username-limited request reached auth service")
		return auth.AuthResult{}, nil
	}}
	limiter := &captureRateLimiter{decisions: map[string]RateLimitDecision{
		"auth.login.username": {Allowed: false, RetryAfter: 3 * time.Second},
	}}
	handler, _, _ := newAuthTestHandler(t, service, limiter)
	request := authRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"student_1","password":"long-enough-password"}`)
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "3" {
		t.Fatalf("response = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	wantCalls := []rateLimitCall{
		{scope: "auth.login", key: "192.0.2.1"},
		{scope: "auth.login.username", key: "student_1"},
	}
	if len(limiter.calls) != len(wantCalls) {
		t.Fatalf("rate limiter calls = %#v", limiter.calls)
	}
	for index := range wantCalls {
		if limiter.calls[index] != wantCalls[index] {
			t.Fatalf("rate limiter calls = %#v", limiter.calls)
		}
	}
}

func TestLoginSetsExactHostCookieAndReturnsNoCredential(t *testing.T) {
	studentNumber := "20260001"
	result := auth.AuthResult{
		AccessToken:        "access-token",
		ExpiresAt:          time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC),
		CSRFToken:          strings.Repeat("a", 43),
		RefreshCookieValue: "v1.refresh-secret",
		Account: auth.Account{
			ID:            "123e4567-e89b-42d3-a456-426614174000",
			Username:      "student_1",
			DisplayName:   "Student",
			StudentNumber: &studentNumber,
			Role:          auth.RoleStudent,
			AuthRevision:  1,
		},
	}
	var received auth.LoginInput
	service := stubAuthService{login: func(_ context.Context, input auth.LoginInput) (auth.AuthResult, error) {
		received = input
		return result, nil
	}}
	handler, _, _ := newAuthTestHandler(t, service, &captureRateLimiter{})
	request := authRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"student_1","password":"long-enough-password"}`)
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if received.Username != "student_1" || received.Password != "long-enough-password" {
		t.Fatalf("login input = %#v", received)
	}
	cookie := response.Header().Get("Set-Cookie")
	for _, attribute := range []string{
		"__Host-ascendany_refresh=v1.refresh-secret",
		"Path=/",
		"HttpOnly",
		"Secure",
		"SameSite=None",
	} {
		if !strings.Contains(cookie, attribute) {
			t.Fatalf("Set-Cookie %q lacks %q", cookie, attribute)
		}
	}
	if strings.Contains(strings.ToLower(cookie), "domain=") {
		t.Fatalf("host cookie unexpectedly has Domain: %q", cookie)
	}
	if strings.Contains(response.Body.String(), "refresh-secret") {
		t.Fatalf("refresh credential leaked into JSON: %s", response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != testWebOrigin ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("CORS headers = %#v", response.Header())
	}
}

func TestBrowserBoundaryEchoesOnlyExplicitAllowedOrigins(t *testing.T) {
	t.Parallel()
	origins := []string{
		"https://ascendany.example",
		"ascendany-app://bundle",
		"https://localhost",
		"capacitor://localhost",
		"http://127.0.0.1:5173",
	}
	calls := 0
	service := stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		calls++
		return auth.AuthResult{}, nil
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = service
	options.AllowedOrigins = origins
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range origins {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v2/auth/login",
			strings.NewReader(`{"username":"student_1","password":"long-enough-password"}`),
		)
		request.RemoteAddr = "192.0.2.1:54321"
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("origin=%q response=%d headers=%#v body=%s", origin, response.Code, response.Header(), response.Body.String())
		}
	}
	rejected := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/auth/login",
		strings.NewReader(`{"username":"student_1","password":"long-enough-password"}`),
	)
	rejected.RemoteAddr = "192.0.2.1:54321"
	rejected.Header.Set("Origin", "https://unlisted.example")
	rejected.Header.Set("Content-Type", "application/json")
	rejectedResponse := newTestResponseRecorder()
	handler.ServeHTTP(rejectedResponse, rejected)
	if rejectedResponse.Code != http.StatusForbidden || rejectedResponse.Header().Get("Access-Control-Allow-Origin") != "" || calls != len(origins) {
		t.Fatalf("rejected=%d calls=%d headers=%#v body=%s", rejectedResponse.Code, calls, rejectedResponse.Header(), rejectedResponse.Body.String())
	}
}

func TestStrictJSONAndMediaTypeBoundary(t *testing.T) {
	service := stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		t.Fatal("invalid request reached auth service")
		return auth.AuthResult{}, nil
	}}
	handler, _, _ := newAuthTestHandler(t, service, &captureRateLimiter{})
	tests := []struct {
		name        string
		body        []byte
		contentType string
		encoding    string
		wantStatus  int
	}{
		{name: "missing content type", body: []byte(`{}`), wantStatus: http.StatusUnsupportedMediaType},
		{name: "content type parameter", body: []byte(`{}`), contentType: "application/json; charset=utf-8", wantStatus: http.StatusUnsupportedMediaType},
		{name: "content encoding", body: []byte(`{}`), contentType: "application/json", encoding: "gzip", wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field", body: []byte(`{"unknown":true}`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "missing required field", body: []byte(`{"username":"student_1"}`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "case alias", body: []byte(`{"username":"student_1","Username":"other","password":"long-enough-password"}`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "duplicate field", body: []byte(`{"username":"a","username":"b","password":"long-enough-password"}`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "invalid utf8", body: append([]byte(`{"username":"student_1","password":"`), append([]byte{0xff}, []byte(`"}`)...)...), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unpaired surrogate", body: []byte(`{"username":"student_1","password":"long-enough-\ud800"}`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "trailing value", body: []byte(`{} {}`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "array root", body: []byte(`[]`), contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "oversized", body: []byte(strings.Repeat("x", int(maxAuthJSONBytes)+1)), contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", bytes.NewReader(test.body))
			request.Header.Set("Origin", testWebOrigin)
			request.RemoteAddr = "192.0.2.1:54321"
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.encoding != "" {
				request.Header.Set("Content-Encoding", test.encoding)
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			assertAPIErrorRequestID(t, response)
		})
	}
}

func TestStrictJSONAcceptsPairedUnicodeSurrogates(t *testing.T) {
	var received auth.LoginInput
	service := stubAuthService{login: func(_ context.Context, input auth.LoginInput) (auth.AuthResult, error) {
		received = input
		return auth.AuthResult{}, nil
	}}
	handler, _, _ := newAuthTestHandler(t, service, &captureRateLimiter{})
	request := authRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"student_1","password":"long-enough-\ud83d\ude00"}`)
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if received.Password != "long-enough-😀" {
		t.Fatalf("decoded password = %q", received.Password)
	}
}

func TestPasswordWorkSaturationMapsToRateLimit(t *testing.T) {
	service := stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		return auth.AuthResult{}, &auth.Error{
			Code:    auth.ErrorPasswordWorkSaturated,
			Message: "must not escape",
		}
	}}
	handler, _, _ := newAuthTestHandler(t, service, &captureRateLimiter{})
	request := authRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"student_1","password":"long-enough-password"}`)
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("response = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	if strings.Contains(response.Body.String(), "must not escape") {
		t.Fatalf("password work detail leaked: %s", response.Body.String())
	}
}

func TestRefreshRotationAndReuseCookieClearing(t *testing.T) {
	csrf := strings.Repeat("c", 43)
	call := 0
	service := stubAuthService{refresh: func(_ context.Context, input auth.RefreshInput) (auth.AuthResult, error) {
		call++
		if input.RefreshToken != "v1.initial-token" || input.CSRFToken != csrf {
			t.Fatalf("refresh input = %#v", input)
		}
		if call == 1 {
			return auth.AuthResult{
				AccessToken:        "rotated-access",
				CSRFToken:          strings.Repeat("d", 43),
				RefreshCookieValue: "v1.rotated-token",
			}, nil
		}
		return auth.AuthResult{}, &auth.Error{Code: auth.ErrorRefreshReuse, Message: "must not escape"}
	}}
	handler, _, _ := newAuthTestHandler(t, service, &captureRateLimiter{})

	first := authRequest(http.MethodPost, "/api/v2/auth/refresh", "")
	first.Header.Set("X-AscendAny-CSRF", csrf)
	first.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "v1.initial-token"})
	firstResponse := newTestResponseRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusOK || !strings.Contains(firstResponse.Header().Get("Set-Cookie"), "v1.rotated-token") {
		t.Fatalf("rotation response = %d %#v %s", firstResponse.Code, firstResponse.Header(), firstResponse.Body.String())
	}

	second := authRequest(http.MethodPost, "/api/v2/auth/refresh", "")
	second.Header.Set("X-AscendAny-CSRF", csrf)
	second.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "v1.initial-token"})
	secondResponse := newTestResponseRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	cleared := secondResponse.Header().Get("Set-Cookie")
	for _, attribute := range []string{"__Host-ascendany_refresh=", "Path=/", "Max-Age=0", "HttpOnly", "Secure", "SameSite=None"} {
		if !strings.Contains(cleared, attribute) {
			t.Fatalf("clear cookie %q lacks %q", cleared, attribute)
		}
	}
	if strings.Contains(secondResponse.Body.String(), "must not escape") {
		t.Fatalf("reuse detail leaked: %s", secondResponse.Body.String())
	}
}

func TestCookieMutationRequiresExactOriginAndCSRF(t *testing.T) {
	calls := 0
	service := stubAuthService{refresh: func(context.Context, auth.RefreshInput) (auth.AuthResult, error) {
		calls++
		return auth.AuthResult{}, nil
	}}
	handler, _, _ := newAuthTestHandler(t, service, &captureRateLimiter{})
	tests := []struct {
		name       string
		origin     string
		csrf       string
		wantStatus int
	}{
		{name: "missing origin", csrf: strings.Repeat("a", 43), wantStatus: http.StatusForbidden},
		{name: "wrong origin", origin: "https://evil.example", csrf: strings.Repeat("a", 43), wantStatus: http.StatusForbidden},
		{name: "missing csrf", origin: testWebOrigin, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v2/auth/refresh", nil)
			request.Header.Set("Origin", test.origin)
			if test.origin == "" {
				request.Header.Del("Origin")
			}
			if test.csrf != "" {
				request.Header.Set("X-AscendAny-CSRF", test.csrf)
			}
			request.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "v1.token"})
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected browser requests reached service %d times", calls)
	}
}

func TestLogoutAndMeEnforceCredentials(t *testing.T) {
	csrf := strings.Repeat("e", 43)
	service := stubAuthService{
		logout: func(_ context.Context, input auth.LogoutInput) error {
			if input.AccessToken != "access-token" || input.RefreshToken != "v1.refresh" || input.CSRFToken != csrf {
				t.Fatalf("logout input = %#v", input)
			}
			return nil
		},
		me: func(_ context.Context, token string) (auth.Account, error) {
			if token != "access-token" {
				t.Fatalf("me token = %q", token)
			}
			return auth.Account{ID: "123e4567-e89b-42d3-a456-426614174000", Role: auth.RoleAdmin}, nil
		},
	}
	limiter := &captureRateLimiter{}
	handler, _, _ := newAuthTestHandler(t, service, limiter)

	logout := authRequest(http.MethodPost, "/api/v2/auth/logout", "")
	logout.Header.Set("Authorization", "Bearer access-token")
	logout.Header.Set("X-AscendAny-CSRF", csrf)
	logout.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "v1.refresh"})
	logoutResponse := newTestResponseRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent || !strings.Contains(logoutResponse.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout response = %d %#v", logoutResponse.Code, logoutResponse.Header())
	}

	me := httptest.NewRequest(http.MethodGet, "/api/v2/auth/me", nil)
	me.Header.Set("Authorization", "Bearer access-token")
	me.Header.Set("X-Forwarded-For", "203.0.113.77")
	me.RemoteAddr = "[::ffff:192.0.2.10]:4444"
	meResponse := newTestResponseRecorder()
	handler.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("me response = %d %s", meResponse.Code, meResponse.Body.String())
	}
	if limiter.scope != "auth.me" || limiter.client != "192.0.2.10" {
		t.Fatalf("limiter key = %q %q", limiter.scope, limiter.client)
	}
}

func TestCORSPreflightIsExact(t *testing.T) {
	handler, _, _ := newAuthTestHandler(t, stubAuthService{}, &captureRateLimiter{})
	valid := httptest.NewRequest(http.MethodOptions, "/api/v2/auth/logout", nil)
	valid.Header.Set("Origin", testWebOrigin)
	valid.Header.Set("Access-Control-Request-Method", http.MethodPost)
	valid.Header.Set("Access-Control-Request-Headers", "authorization, x-ascendany-csrf")
	validResponse := newTestResponseRecorder()
	handler.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusNoContent ||
		validResponse.Header().Get("Access-Control-Allow-Headers") != "Authorization, X-AscendAny-CSRF" {
		t.Fatalf("valid preflight = %d %#v %s", validResponse.Code, validResponse.Header(), validResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodOptions, "/api/v2/auth/logout", nil)
	invalid.Header.Set("Origin", testWebOrigin)
	invalid.Header.Set("Access-Control-Request-Method", http.MethodPost)
	invalid.Header.Set("Access-Control-Request-Headers", "x-unapproved")
	invalidResponse := newTestResponseRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusForbidden {
		t.Fatalf("invalid preflight = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestRateLimitAndInternalErrorsDoNotLeakSecrets(t *testing.T) {
	secret := "super-secret-database-detail"
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{}, &auth.Error{Code: auth.ErrorDatabase, Message: "storage failed", Cause: errors.New(secret)}
	}}
	limiter := &captureRateLimiter{decision: RateLimitDecision{Allowed: false, RetryAfter: 1500 * time.Millisecond}}
	handler, logs, _ := newAuthTestHandler(t, service, limiter)
	limited := httptest.NewRequest(http.MethodGet, "/api/v2/auth/me", nil)
	limited.Header.Set("Authorization", "Bearer token")
	limitedResponse := newTestResponseRecorder()
	handler.ServeHTTP(limitedResponse, limited)
	if limitedResponse.Code != http.StatusTooManyRequests || limitedResponse.Header().Get("Retry-After") != "2" {
		t.Fatalf("limited response = %d %#v", limitedResponse.Code, limitedResponse.Header())
	}

	limiter.decision = RateLimitDecision{Allowed: true}
	internal := httptest.NewRequest(http.MethodGet, "/api/v2/auth/me", nil)
	internal.Header.Set("Authorization", "Bearer token")
	internalResponse := newTestResponseRecorder()
	handler.ServeHTTP(internalResponse, internal)
	if internalResponse.Code != http.StatusInternalServerError {
		t.Fatalf("internal response = %d %s", internalResponse.Code, internalResponse.Body.String())
	}
	if strings.Contains(internalResponse.Body.String(), secret) {
		t.Fatalf("secret leaked in response: %s", internalResponse.Body.String())
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("secret leaked in logs: %s", logs.String())
	}
	assertAPIErrorRequestID(t, internalResponse)
}

func TestTrustedProxyMissingClientHeaderFailsBeforeAuth(t *testing.T) {
	calls := 0
	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		calls++
		return auth.Account{}, nil
	}}
	limiter := &captureRateLimiter{}
	handler, err := New(Options{
		Readiness:                 staticReadiness{report: health.Report{Status: health.StatusReady}},
		Version:                   version.Info{},
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:                      service,
		Enrollment:                unusedEnrollmentService{},
		AccountManagement:         unusedAccountManagementService{},
		AllowedOrigins:            []string{testWebOrigin},
		RateLimiter:               limiter,
		RequestIDRandom:           bytes.NewReader([]byte("abcdefgh")),
		TrustedProxyCIDRs:         []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		ClientIPHeader:            "CF-Connecting-IP",
		Artifacts:                 unusedArtifactPublisher{},
		Imports:                   unusedImportQueue{},
		ImportReader:              unusedImportReader{},
		StudentAnalytics:          unusedStudentAnalyticsService{},
		Achievement:               unusedAchievementService{},
		ExamCatalog:               unusedExamCatalogService{},
		ExamGeneration:            unusedExamGenerationService{},
		Administration:            unusedAdministrationService{},
		Configuration:             unusedConfigurationService{},
		Feedback:                  unusedFeedbackService{},
		AgentNotes:                unusedAgentNotesService{},
		ChatAgent:                 unusedChatAgentService{},
		OJ:                        unusedOJService{},
		OJPolicy:                  oj.DefaultPolicy(),
		RecommendationReader:      unusedRecommendationReader{},
		RecommendationAdminReader: unusedRecommendationAdminReader{},
		RecommendationQueue:       unusedRecommendationQueue{},
		ModelProbe:                unusedModelProbeService{},
		Capabilities:              testCapabilities(true),
		AuthBodyTimeout:           time.Second,
		UploadBodyTimeout:         time.Second,
		SSEMaxDuration:            time.Minute,
		SSEReauthInterval:         10 * time.Second,
		SSEWriteTimeout:           time.Second,
		MaxActiveSSE:              4,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/auth/me", nil)
	request.RemoteAddr = "127.0.0.1:44000"
	request.Header.Set("Authorization", "Bearer access-token")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v2/auth/me", nil)
	request.RemoteAddr = "127.0.0.1:44000"
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("CF-Connecting-IP", "203.0.113.19")
	response = newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 1 || limiter.client != "203.0.113.19" {
		t.Fatalf("response=%d calls=%d limiter=%q body=%s", response.Code, calls, limiter.client, response.Body.String())
	}
}

func newAuthTestHandler(
	t *testing.T,
	service AuthService,
	limiter RequestRateLimiter,
) (http.Handler, *bytes.Buffer, error) {
	t.Helper()
	var logs bytes.Buffer
	handler, err := New(Options{
		Readiness:                 staticReadiness{report: health.Report{Status: health.StatusReady}},
		Version:                   version.Info{},
		Logger:                    slog.New(slog.NewJSONHandler(&logs, nil)),
		Auth:                      service,
		Enrollment:                unusedEnrollmentService{},
		AccountManagement:         unusedAccountManagementService{},
		AllowedOrigins:            []string{testWebOrigin},
		RateLimiter:               limiter,
		RequestIDRandom:           bytes.NewReader([]byte("abcdefgh")),
		Artifacts:                 unusedArtifactPublisher{},
		Imports:                   unusedImportQueue{},
		ImportReader:              unusedImportReader{},
		StudentAnalytics:          unusedStudentAnalyticsService{},
		Achievement:               unusedAchievementService{},
		ExamCatalog:               unusedExamCatalogService{},
		ExamGeneration:            unusedExamGenerationService{},
		Administration:            unusedAdministrationService{},
		Configuration:             unusedConfigurationService{},
		Feedback:                  unusedFeedbackService{},
		AgentNotes:                unusedAgentNotesService{},
		ChatAgent:                 unusedChatAgentService{},
		OJ:                        unusedOJService{},
		OJPolicy:                  oj.DefaultPolicy(),
		RecommendationReader:      unusedRecommendationReader{},
		RecommendationAdminReader: unusedRecommendationAdminReader{},
		RecommendationQueue:       unusedRecommendationQueue{},
		ModelProbe:                unusedModelProbeService{},
		Capabilities:              testCapabilities(true),
		AuthBodyTimeout:           time.Second,
		UploadBodyTimeout:         time.Second,
		SSEMaxDuration:            time.Minute,
		SSEReauthInterval:         10 * time.Second,
		SSEWriteTimeout:           time.Second,
		MaxActiveSSE:              4,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, &logs, nil
}

func authRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Origin", testWebOrigin)
	request.RemoteAddr = "192.0.2.1:54321"
	return request
}

func assertAPIErrorRequestID(t *testing.T, response *testResponseRecorder) {
	t.Helper()
	var payload APIError
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode APIError: %v; body=%s", err, response.Body.String())
	}
	uuidV4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidV4.MatchString(payload.RequestID) || payload.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("request IDs body=%q header=%q", payload.RequestID, response.Header().Get("X-Request-ID"))
	}
}
