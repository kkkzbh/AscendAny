package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

func TestAgentV1RegistrationUsesStrictFrontendContractAndLoginEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	studentNumber := "20260001"
	ptaNickname := "Alice"
	rawRefresh := testAgentV1RawRefresh("123e4567-e89b-42d3-a456-426614174082", 0x41)
	csrf := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x52}, 32))
	var received auth.RegistrationInput
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{register: func(_ context.Context, input auth.RegistrationInput) (auth.AuthResult, error) {
		received = input
		return auth.AuthResult{
			AccessToken:        "registration-access",
			ExpiresAt:          now.Add(15 * time.Minute),
			CSRFToken:          csrf,
			RefreshCookieValue: rawRefresh,
			Account: auth.Account{
				ID: "123e4567-e89b-42d3-a456-426614174081", Username: "alice_01", DisplayName: "alice_01",
				StudentNumber: &studentNumber, PTANickname: &ptaNickname, Role: auth.RoleStudent, AuthRevision: 1,
			},
		}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	request := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/register", `{
        "username":"alice_01",
        "password":"long-enough-password",
        "studentId":"20260001",
        "ptaNickname":"Alice",
        "phone":"13800000000",
        "email":"alice@example.test",
        "deviceId":"desktop"
    }`)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if received != (auth.RegistrationInput{
		Username: "alice_01", Password: "long-enough-password",
		StudentNumber: "20260001", PTANickname: "Alice",
	}) {
		t.Fatalf("registration input = %#v", received)
	}
	var payload agentV1TokensResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AccessToken != "registration-access" || payload.RefreshToken == "" ||
		payload.RefreshToken == rawRefresh || payload.Account.StudentID == nil ||
		*payload.Account.StudentID != studentNumber || payload.Account.PTANickname == nil ||
		*payload.Account.PTANickname != ptaNickname ||
		strings.Contains(response.Body.String(), rawRefresh) || strings.Contains(response.Body.String(), csrf) {
		t.Fatalf("registration response = %s", response.Body.String())
	}
}

func TestAgentV1RegistrationRejectsUnknownAndNoncanonicalOptionalFields(t *testing.T) {
	t.Parallel()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{register: func(context.Context, auth.RegistrationInput) (auth.AuthResult, error) {
		t.Fatal("invalid registration reached auth service")
		return auth.AuthResult{}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown field",
			body: `{"username":"alice_01","password":"long-enough-password","studentId":"20260001","ptaNickname":"Alice","extra":true}`,
			code: "invalid_json",
		},
		{
			name: "empty phone",
			body: `{"username":"alice_01","password":"long-enough-password","studentId":"20260001","ptaNickname":"Alice","phone":""}`,
			code: "auth_phone_invalid",
		},
		{
			name: "noncanonical email",
			body: `{"username":"alice_01","password":"long-enough-password","studentId":"20260001","ptaNickname":"Alice","email":" alice@example.test"}`,
			code: "auth_email_invalid",
		},
		{
			name: "empty device",
			body: `{"username":"alice_01","password":"long-enough-password","studentId":"20260001","ptaNickname":"Alice","deviceId":""}`,
			code: "auth_device_id_invalid",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/register", test.body)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAgentV1RegistrationMapsConflictsAndAppliesUsernameRateLimit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		code auth.ErrorCode
	}{
		{name: "username", err: &auth.Error{Code: auth.ErrorRegistrationUsername}, code: auth.ErrorRegistrationUsername},
		{name: "identity", err: &auth.Error{Code: auth.ErrorRegistrationIdentity}, code: auth.ErrorRegistrationIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := testHandlerOptions(health.Report{Status: health.StatusReady})
			options.Auth = stubAuthService{register: func(context.Context, auth.RegistrationInput) (auth.AuthResult, error) {
				return auth.AuthResult{}, test.err
			}}
			handler, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			request := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/register", `{"username":"alice_01","password":"long-enough-password","studentId":"20260001","ptaNickname":"Alice"}`)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict ||
				!strings.Contains(response.Body.String(), `"code":"`+string(test.code)+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	limiter := &captureRateLimiter{decisions: map[string]RateLimitDecision{
		"auth.register.username": {Allowed: false, RetryAfter: 4 * time.Second},
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{register: func(context.Context, auth.RegistrationInput) (auth.AuthResult, error) {
		t.Fatal("username-limited registration reached auth service")
		return auth.AuthResult{}, nil
	}}
	options.RateLimiter = limiter
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/register", `{"username":"alice_01","password":"long-enough-password","studentId":"20260001","ptaNickname":"Alice"}`)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "4" {
		t.Fatalf("rate response=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
	want := []rateLimitCall{
		{scope: "agent.auth.register", key: "192.0.2.1"},
		{scope: "auth.register.username", key: "alice_01"},
	}
	if len(limiter.calls) != len(want) || limiter.calls[0] != want[0] || limiter.calls[1] != want[1] {
		t.Fatalf("rate calls = %#v", limiter.calls)
	}
}

func TestAgentV1RegisteredNicknameIsAnAuthenticatedSelfSelector(t *testing.T) {
	t.Parallel()
	studentNumber := "20260001"
	ptaNickname := "Alice"
	account := auth.Account{
		ID: "123e4567-e89b-42d3-a456-426614174083", Username: "alice_01", DisplayName: "alice_01",
		StudentNumber: &studentNumber, PTANickname: &ptaNickname, Role: auth.RoleStudent, AuthRevision: 1,
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{me: func(_ context.Context, access string) (auth.Account, error) {
		if access != "registration-access" {
			t.Fatalf("access = %q", access)
		}
		return account, nil
	}}
	options.StudentAnalytics = stubStudentAnalyticsService{getSelf: func(
		_ context.Context,
		access string,
		limit int,
	) (studentanalytics.Result, error) {
		if access != "registration-access" || limit != studentanalytics.MaxHistoryLimit {
			t.Fatalf("analytics access/limit = %q/%d", access, limit)
		}
		return studentanalytics.Result{State: studentanalytics.StateNotGenerated}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	request := agentV1JSONRequest(http.MethodGet, "/api/v1/students/dashboard?studentId=20260001&ptaNickname=Alice", "")
	request.Header.Set("Authorization", "Bearer registration-access")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"ptaNickname":"Alice"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	rejected := agentV1JSONRequest(http.MethodGet, "/api/v1/students/dashboard?studentId=20260001&ptaNickname=Bob", "")
	rejected.Header.Set("Authorization", "Bearer registration-access")
	rejectedResponse := newTestResponseRecorder()
	handler.ServeHTTP(rejectedResponse, rejected)
	if rejectedResponse.Code != http.StatusForbidden ||
		!strings.Contains(rejectedResponse.Body.String(), `"code":"student_selector_rejected"`) {
		t.Fatalf("status=%d body=%s", rejectedResponse.Code, rejectedResponse.Body.String())
	}
}
