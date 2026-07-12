package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

const (
	testEnrollmentGrantID = "123e4567-e89b-42d3-a456-426614174020"
	testEnrollmentToken   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type stubEnrollmentService struct {
	issue  func(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error)
	revoke func(context.Context, string, string) error
	claim  func(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error)
}

func (service stubEnrollmentService) IssueEnrollment(ctx context.Context, token string, input auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
	if service.issue == nil {
		panic("unexpected enrollment issue")
	}
	return service.issue(ctx, token, input)
}

func (service stubEnrollmentService) RevokeEnrollment(ctx context.Context, token, grantID string) error {
	if service.revoke == nil {
		panic("unexpected enrollment revoke")
	}
	return service.revoke(ctx, token, grantID)
}

func (service stubEnrollmentService) ClaimEnrollment(ctx context.Context, input auth.EnrollmentClaimInput) (auth.AuthResult, error) {
	if service.claim == nil {
		panic("unexpected enrollment claim")
	}
	return service.claim(ctx, input)
}

func TestEnrollmentIssueReturnsSingleUseCredential(t *testing.T) {
	expiresAt := time.Date(2026, 7, 17, 3, 4, 5, 0, time.UTC)
	issuedAt := time.Date(2026, 7, 11, 3, 4, 5, 0, time.UTC)
	want := auth.IssuedEnrollment{
		Grant: auth.EnrollmentGrant{
			ID:              testEnrollmentGrantID,
			Username:        "student_20",
			DisplayName:     "Student Twenty",
			StudentNumber:   "20260020",
			IssuerAccountID: "123e4567-e89b-42d3-a456-426614174021",
			IssuedAt:        issuedAt,
			ExpiresAt:       expiresAt,
		},
		Token: testEnrollmentToken,
	}
	var received auth.EnrollmentIssueInput
	service := stubEnrollmentService{issue: func(_ context.Context, access string, input auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
		if access != "admin-access" {
			t.Fatalf("access token = %q", access)
		}
		received = input
		return want, nil
	}}
	limiter := &captureRateLimiter{}
	handler := newEnrollmentTestHandler(t, true, service, limiter)
	request := authRequest(http.MethodPost, "/api/v2/admin/enrollment-claims", `{"username":"student_20","displayName":"Student Twenty","studentNumber":"20260020","expiresAt":"2026-07-17T03:04:05Z"}`)
	request.Header.Set("Authorization", "Bearer admin-access")
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if received.Username != "student_20" || received.DisplayName != "Student Twenty" ||
		received.StudentNumber != "20260020" || !received.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("issue input = %#v", received)
	}
	var actual auth.IssuedEnrollment
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Token != want.Token || actual.Grant != want.Grant {
		t.Fatalf("issued enrollment = %#v", actual)
	}
	if strings.Count(response.Body.String(), testEnrollmentToken) != 1 || response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("credential response headers=%#v body=%s", response.Header(), response.Body.String())
	}
	if limiter.scope != "admin.enrollment.issue" || limiter.client != "192.0.2.1" {
		t.Fatalf("rate limit = %q %q", limiter.scope, limiter.client)
	}
}

func TestEnrollmentClaimCreatesBrowserSessionAndHashesRateKey(t *testing.T) {
	result := testEnrollmentAuthResult()
	var received auth.EnrollmentClaimInput
	service := stubEnrollmentService{claim: func(_ context.Context, input auth.EnrollmentClaimInput) (auth.AuthResult, error) {
		received = input
		return result, nil
	}}
	limiter := &captureRateLimiter{}
	handler := newEnrollmentTestHandler(t, true, service, limiter)
	request := authRequest(http.MethodPost, "/api/v2/auth/enrollment-claims/consume", `{"token":"`+testEnrollmentToken+`","password":"long-enough-password"}`)
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || received.Token != testEnrollmentToken || received.Password != "long-enough-password" {
		t.Fatalf("status=%d input=%#v body=%s", response.Code, received, response.Body.String())
	}
	for _, attribute := range []string{
		"__Host-ascendany_refresh=v1.enrollment-refresh",
		"Path=/",
		"HttpOnly",
		"Secure",
		"SameSite=None",
	} {
		if !strings.Contains(response.Header().Get("Set-Cookie"), attribute) {
			t.Fatalf("Set-Cookie %q lacks %q", response.Header().Get("Set-Cookie"), attribute)
		}
	}
	if strings.Contains(strings.ToLower(response.Header().Get("Set-Cookie")), "domain=") ||
		response.Header().Get("Access-Control-Allow-Origin") != testWebOrigin ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("claim browser headers = %#v", response.Header())
	}
	if strings.Contains(response.Body.String(), result.RefreshCookieValue) || strings.Contains(response.Body.String(), testEnrollmentToken) {
		t.Fatalf("credential leaked in claim response: %s", response.Body.String())
	}
	digest := sha256.Sum256([]byte(testEnrollmentToken))
	wantCalls := []rateLimitCall{
		{scope: "auth.enrollment.claim", key: "192.0.2.1"},
		{scope: "auth.enrollment.claim.token", key: hex.EncodeToString(digest[:])},
	}
	if len(limiter.calls) != len(wantCalls) {
		t.Fatalf("rate calls = %#v", limiter.calls)
	}
	for index := range wantCalls {
		if limiter.calls[index] != wantCalls[index] {
			t.Fatalf("rate calls = %#v", limiter.calls)
		}
		if strings.Contains(limiter.calls[index].key, testEnrollmentToken) {
			t.Fatalf("raw enrollment token used as rate key: %#v", limiter.calls)
		}
	}
}

func TestEnrollmentRevokeUsesOpaqueConflictBoundary(t *testing.T) {
	calls := 0
	service := stubEnrollmentService{revoke: func(_ context.Context, accessToken, grantID string) error {
		calls++
		if accessToken != "admin-access" || grantID != testEnrollmentGrantID {
			t.Fatalf("revoke access=%q grant=%q", accessToken, grantID)
		}
		return nil
	}}
	handler := newEnrollmentTestHandler(t, true, service, &captureRateLimiter{})
	request := httptest.NewRequest(http.MethodDelete, "/api/v2/admin/enrollment-claims/"+testEnrollmentGrantID, nil)
	request.Header.Set("Authorization", "Bearer admin-access")
	request.RemoteAddr = "192.0.2.1:54321"
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}

	service.revoke = func(context.Context, string, string) error {
		return &auth.Error{
			Code:    auth.ErrorEnrollmentNotRevocable,
			Message: "grant 42 is missing, expired, used, or revoked",
			Cause:   errors.New("sensitive storage state"),
		}
	}
	handler = newEnrollmentTestHandler(t, true, service, &captureRateLimiter{})
	request = httptest.NewRequest(http.MethodDelete, "/api/v2/admin/enrollment-claims/"+testEnrollmentGrantID, nil)
	request.Header.Set("Authorization", "Bearer admin-access")
	request.RemoteAddr = "192.0.2.1:54321"
	response = newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "42") ||
		strings.Contains(response.Body.String(), "sensitive") {
		t.Fatalf("opaque conflict response=%d body=%s", response.Code, response.Body.String())
	}
	assertAPIErrorCode(t, response, string(auth.ErrorEnrollmentNotRevocable))
}

func TestEnrollmentErrorsDoNotEnumerateCredentialsOrAccounts(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		service    stubEnrollmentService
		body       string
		authorize  bool
		wantStatus int
		wantCode   string
	}{
		{
			name: "claim rejection",
			path: "/api/v2/auth/enrollment-claims/consume",
			service: stubEnrollmentService{claim: func(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
				return auth.AuthResult{}, &auth.Error{Code: auth.ErrorEnrollmentRejected, Message: "token belongs to account 42", Cause: errors.New("secret row")}
			}},
			body:       `{"token":"` + testEnrollmentToken + `","password":"long-enough-password"}`,
			wantStatus: http.StatusUnauthorized,
			wantCode:   string(auth.ErrorEnrollmentRejected),
		},
		{
			name: "issue identity conflict",
			path: "/api/v2/admin/enrollment-claims",
			service: stubEnrollmentService{issue: func(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
				return auth.IssuedEnrollment{}, &auth.Error{Code: auth.ErrorEnrollmentIdentity, Message: "student already has account 42", Cause: errors.New("secret row")}
			}},
			body:       `{"username":"student_20","displayName":"Student Twenty","studentNumber":"20260020","expiresAt":"2026-07-17T03:04:05Z"}`,
			authorize:  true,
			wantStatus: http.StatusConflict,
			wantCode:   string(auth.ErrorEnrollmentIdentity),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newEnrollmentTestHandler(t, true, test.service, &captureRateLimiter{})
			request := authRequest(http.MethodPost, test.path, test.body)
			request.Header.Set("Content-Type", "application/json")
			if test.authorize {
				request.Header.Set("Authorization", "Bearer admin-access")
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || strings.Contains(response.Body.String(), "account 42") ||
				strings.Contains(response.Body.String(), "secret row") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAPIErrorCode(t, response, test.wantCode)
			if response.Header().Get("Set-Cookie") != "" {
				t.Fatalf("rejected enrollment set cookie: %q", response.Header().Get("Set-Cookie"))
			}
		})
	}
}

func TestEnrollmentBoundaryRejectsOriginQueryBodyAndMalformedJSON(t *testing.T) {
	serviceCalls := 0
	service := stubEnrollmentService{
		issue: func(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
			serviceCalls++
			return auth.IssuedEnrollment{}, nil
		},
		revoke: func(context.Context, string, string) error {
			serviceCalls++
			return nil
		},
		claim: func(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
			serviceCalls++
			return auth.AuthResult{}, nil
		},
	}
	handler := newEnrollmentTestHandler(t, true, service, &captureRateLimiter{})
	tests := []struct {
		name       string
		request    *http.Request
		wantStatus int
	}{
		{
			name: "claim missing origin",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/v2/auth/enrollment-claims/consume", strings.NewReader(`{"token":"`+testEnrollmentToken+`","password":"long-enough-password"}`))
				request.Header.Set("Content-Type", "application/json")
				request.RemoteAddr = "192.0.2.1:54321"
				return request
			}(),
			wantStatus: http.StatusForbidden,
		},
		{
			name: "claim wrong origin",
			request: func() *http.Request {
				request := authRequest(http.MethodPost, "/api/v2/auth/enrollment-claims/consume", `{"token":"`+testEnrollmentToken+`","password":"long-enough-password"}`)
				request.Header.Set("Origin", "https://attacker.example")
				request.Header.Set("Content-Type", "application/json")
				return request
			}(),
			wantStatus: http.StatusForbidden,
		},
		{
			name: "issue missing bearer",
			request: func() *http.Request {
				request := authRequest(http.MethodPost, "/api/v2/admin/enrollment-claims", `{}`)
				request.Header.Set("Content-Type", "application/json")
				return request
			}(),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "issue query",
			request: func() *http.Request {
				request := authRequest(http.MethodPost, "/api/v2/admin/enrollment-claims?extra=1", `{}`)
				request.Header.Set("Authorization", "Bearer admin-access")
				request.Header.Set("Content-Type", "application/json")
				return request
			}(),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "claim unknown JSON field",
			request: func() *http.Request {
				request := authRequest(http.MethodPost, "/api/v2/auth/enrollment-claims/consume", `{"token":"`+testEnrollmentToken+`","password":"long-enough-password","accountId":"42"}`)
				request.Header.Set("Content-Type", "application/json")
				return request
			}(),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "revoke body",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodDelete, "/api/v2/admin/enrollment-claims/"+testEnrollmentGrantID, strings.NewReader(`{}`))
				request.Header.Set("Authorization", "Bearer admin-access")
				request.RemoteAddr = "192.0.2.1:54321"
				return request
			}(),
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, test.request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if serviceCalls != 0 {
		t.Fatalf("rejected requests reached service %d times", serviceCalls)
	}
}

func TestEnrollmentCORSPreflightUsesExactRouteContracts(t *testing.T) {
	handler := newEnrollmentTestHandler(t, true, stubEnrollmentService{}, &captureRateLimiter{})
	tests := []struct {
		path        string
		method      string
		headers     string
		wantHeaders string
	}{
		{
			path:        "/api/v2/auth/enrollment-claims/consume",
			method:      http.MethodPost,
			headers:     "content-type",
			wantHeaders: "Content-Type",
		},
		{
			path:        "/api/v2/admin/enrollment-claims",
			method:      http.MethodPost,
			headers:     "authorization, content-type",
			wantHeaders: "Authorization, Content-Type",
		},
		{
			path:        "/api/v2/admin/enrollment-claims/" + testEnrollmentGrantID,
			method:      http.MethodDelete,
			headers:     "authorization",
			wantHeaders: "Authorization",
		},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodOptions, test.path, nil)
		request.Header.Set("Origin", testWebOrigin)
		request.Header.Set("Access-Control-Request-Method", test.method)
		request.Header.Set("Access-Control-Request-Headers", test.headers)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Headers") != test.wantHeaders {
			t.Fatalf("preflight %s = %d %#v %s", test.path, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestEnrollmentWritesDisabledAndMissingDependencyFailClosed(t *testing.T) {
	service := stubEnrollmentService{claim: func(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
		t.Fatal("disabled write reached enrollment service")
		return auth.AuthResult{}, nil
	}}
	handler := newEnrollmentTestHandler(t, false, service, &captureRateLimiter{})
	request := authRequest(http.MethodPost, "/api/v2/auth/enrollment-claims/consume", `{"token":"`+testEnrollmentToken+`","password":"long-enough-password"}`)
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Enrollment = nil
	if _, err := New(options); err == nil {
		t.Fatal("New accepted a nil enrollment dependency")
	}
}

func newEnrollmentTestHandler(t *testing.T, writesEnabled bool, service EnrollmentService, limiter RequestRateLimiter) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Enrollment = service
	options.RateLimiter = limiter
	options.Capabilities = testCapabilities(writesEnabled)
	if !writesEnabled {
		options.Artifacts = nil
		options.Imports = nil
		options.RecommendationQueue = nil
		options.ModelProbe = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func testEnrollmentAuthResult() auth.AuthResult {
	studentNumber := "20260020"
	return auth.AuthResult{
		AccessToken:        "enrollment-access",
		ExpiresAt:          time.Date(2026, 7, 11, 4, 5, 6, 0, time.UTC),
		CSRFToken:          strings.Repeat("C", 43),
		RefreshCookieValue: "v1.enrollment-refresh",
		Account: auth.Account{
			ID:            "123e4567-e89b-42d3-a456-426614174022",
			Username:      "student_20",
			DisplayName:   "Student Twenty",
			StudentNumber: &studentNumber,
			Role:          auth.RoleStudent,
			AuthRevision:  1,
		},
	}
}

func assertAPIErrorCode(t *testing.T, response *testResponseRecorder, code string) {
	t.Helper()
	var payload APIError
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode API error: %v body=%s", err, response.Body.String())
	}
	if payload.Code != code {
		t.Fatalf("API error code=%q want=%q", payload.Code, code)
	}
}
