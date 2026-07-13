package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

type achievementServiceStub struct {
	getSelf func(context.Context, string) (achievement.Result, error)
	calls   int
	token   string
}

func (stub *achievementServiceStub) GetSelf(ctx context.Context, token string) (achievement.Result, error) {
	stub.calls++
	stub.token = token
	return stub.getSelf(ctx, token)
}

func TestGetSelfAchievementsReturnsValidatedStudentResult(t *testing.T) {
	t.Parallel()

	want := validHTTPAchievementResult()
	service := &achievementServiceStub{getSelf: func(_ context.Context, token string) (achievement.Result, error) {
		if token != "student-access" {
			t.Fatalf("token = %q", token)
		}
		return want, nil
	}}
	handler := newAchievementTestHandler(t, service, false, allowAllRateLimiter{}, io.Discard)
	request := achievementRequest("/api/v2/students/me/achievements", nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.calls != 1 || service.token != "student-access" {
		t.Fatalf("status=%d calls=%d token=%q body=%s", response.Code, service.calls, service.token, response.Body.String())
	}
	var got achievement.Result
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result = %#v, want %#v", got, want)
	}
}

func TestAchievementReadRemainsAvailableWithWritesDisabled(t *testing.T) {
	t.Parallel()

	service := &achievementServiceStub{getSelf: func(context.Context, string) (achievement.Result, error) {
		return validHTTPAchievementResult(), nil
	}}
	handler := newAchievementTestHandler(t, service, false, allowAllRateLimiter{}, io.Discard)
	request := achievementRequest("/api/v2/students/me/achievements", nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
}

func TestAchievementRequestContractIsRejectedBeforeService(t *testing.T) {
	t.Parallel()

	service := &achievementServiceStub{getSelf: func(context.Context, string) (achievement.Result, error) {
		panic("invalid request reached achievement service")
	}}
	handler := newAchievementTestHandler(t, service, false, allowAllRateLimiter{}, io.Discard)
	tests := []struct {
		name     string
		request  *http.Request
		wantCode string
	}{
		{name: "body", request: achievementRequest("/api/v2/students/me/achievements", strings.NewReader(`{}`)), wantCode: "request_body_not_allowed"},
		{name: "query", request: achievementRequest("/api/v2/students/me/achievements?x=1", nil), wantCode: "invalid_query"},
		{name: "empty query", request: achievementRequest("/api/v2/students/me/achievements?", nil), wantCode: "invalid_query"},
		{name: "missing bearer", request: achievementRequestWithoutBearer("/api/v2/students/me/achievements"), wantCode: "auth_authentication_rejected"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, test.request)
			if response.Code != http.StatusBadRequest && response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
	if service.calls != 0 {
		t.Fatalf("service calls = %d", service.calls)
	}
}

func TestAchievementErrorsAreStrictAndSecretFree(t *testing.T) {
	t.Parallel()

	secret := "postgres://achievement-secret"
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name: "authentication", err: &auth.Error{Code: auth.ErrorAuthentication, Message: secret, Cause: errors.New(secret)},
			wantStatus: http.StatusUnauthorized, wantCode: "auth_authentication_rejected",
		},
		{
			name: "forbidden", err: &achievement.Error{Code: achievement.ErrorForbidden, Op: "read", Cause: errors.New(secret)},
			wantStatus: http.StatusForbidden, wantCode: "auth_forbidden",
		},
		{
			name: "principal rejected", err: &achievement.Error{Code: achievement.ErrorPrincipalRejected, Op: "read", Cause: errors.New(secret)},
			wantStatus: http.StatusUnauthorized, wantCode: "auth_authentication_rejected",
		},
		{
			name: "deadline", err: &achievement.Error{Code: achievement.ErrorCanceled, Op: "read", Cause: context.DeadlineExceeded},
			wantStatus: http.StatusRequestTimeout, wantCode: "request_timeout",
		},
		{
			name: "canceled", err: &achievement.Error{Code: achievement.ErrorCanceled, Op: "read", Cause: context.Canceled},
			wantStatus: http.StatusBadRequest, wantCode: "request_canceled",
		},
		{
			name: "database", err: &achievement.Error{Code: achievement.ErrorDatabase, Op: "read", Cause: errors.New(secret)},
			wantStatus: http.StatusInternalServerError, wantCode: "internal_error",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			service := &achievementServiceStub{getSelf: func(context.Context, string) (achievement.Result, error) {
				return achievement.Result{}, test.err
			}}
			handler := newAchievementTestHandler(t, service, false, allowAllRateLimiter{}, &logs)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, achievementRequest("/api/v2/students/me/achievements", nil))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), secret) || strings.Contains(logs.String(), secret) {
				t.Fatalf("secret leaked: response=%s logs=%s", response.Body.String(), logs.String())
			}
		})
	}
}

func TestAchievementRejectsMalformedServiceResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*achievement.Result)
	}{
		{name: "state", mutate: func(result *achievement.Result) { result.State = "unknown" }},
		{name: "head", mutate: func(result *achievement.Result) { result.AnalyticsHeadRevision = 0 }},
		{name: "summary", mutate: func(result *achievement.Result) { result.Summary.Gold = 0 }},
		{name: "duplicate code", mutate: func(result *achievement.Result) { result.Items[1].Code = result.Items[0].Code }},
		{name: "unknown progress", mutate: func(result *achievement.Result) { result.Items[0].ProgressKey = "unknown" }},
		{name: "wrong tier", mutate: func(result *achievement.Result) { result.Items[0].Tier = 2 }},
		{name: "nonfinite progress", mutate: func(result *achievement.Result) { result.Items[0].Progress = math.NaN() }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := validHTTPAchievementResult()
			test.mutate(&result)
			service := &achievementServiceStub{getSelf: func(context.Context, string) (achievement.Result, error) {
				return result, nil
			}}
			handler := newAchievementTestHandler(t, service, false, allowAllRateLimiter{}, io.Discard)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, achievementRequest("/api/v2/students/me/achievements", nil))
			if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAchievementDependencyIsRequiredForEveryCapabilityMode(t *testing.T) {
	t.Parallel()

	for _, writesEnabled := range []bool{false, true} {
		options := testHandlerOptions(health.Report{Status: health.StatusReady})
		options.Achievement = nil
		if !writesEnabled {
			disableHTTPWrites(&options)
		}
		if _, err := New(options); err == nil {
			t.Fatalf("New accepted missing achievement dependency with writesEnabled=%t", writesEnabled)
		}
	}
}

func TestAchievementCORSAndRatePolicyAreExact(t *testing.T) {
	t.Parallel()

	service := &achievementServiceStub{getSelf: func(context.Context, string) (achievement.Result, error) {
		return validHTTPAchievementResult(), nil
	}}
	limiter := &captureRateLimiter{decision: RateLimitDecision{Allowed: true}}
	handler := newAchievementTestHandler(t, service, false, limiter, io.Discard)

	request := achievementRequest("/api/v2/students/me/achievements", nil)
	request.Header.Set("Origin", testWebOrigin)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || limiter.scope != "students.me.achievements" || limiter.client != "192.0.2.1" {
		t.Fatalf("status=%d scope=%q client=%q body=%s", response.Code, limiter.scope, limiter.client, response.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v2/students/me/achievements", nil)
	preflight.RemoteAddr = "192.0.2.1:44000"
	preflight.Header.Set("Origin", testWebOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
	preflightResponse := newTestResponseRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent ||
		preflightResponse.Header().Get("Access-Control-Allow-Headers") != "Authorization" ||
		preflightResponse.Header().Get("Access-Control-Allow-Origin") != testWebOrigin {
		t.Fatalf("preflight=%d headers=%#v body=%s", preflightResponse.Code, preflightResponse.Header(), preflightResponse.Body.String())
	}
}

func newAchievementTestHandler(
	t *testing.T,
	service AchievementService,
	writesEnabled bool,
	limiter RequestRateLimiter,
	logWriter io.Writer,
) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Achievement = service
	options.RateLimiter = limiter
	options.Logger = slog.New(slog.NewJSONHandler(logWriter, nil))
	if !writesEnabled {
		disableHTTPWrites(&options)
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func disableHTTPWrites(options *Options) {
	options.Artifacts = nil
	options.Imports = nil
	options.ModelProbe = nil
	options.Capabilities = testCapabilities(false)
}

func achievementRequest(path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, body)
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer student-access")
	return request
}

func achievementRequestWithoutBearer(path string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "192.0.2.1:44000"
	return request
}

func validHTTPAchievementResult() achievement.Result {
	return achievement.Result{
		State:                 achievement.StateReady,
		AnalyticsHeadRevision: 7,
		RuleSetVersion:        2,
		RuleHeadRevision:      3,
		Summary:               achievement.Summary{Total: 2, Locked: 1, Gold: 1},
		Items: []achievement.Item{
			{
				Code: "exam_mastery", Title: "Exam mastery", Description: "Complete exams.",
				ProgressKey: achievement.ProgressExamCount, Tier: 3, Progress: 3,
				BronzeTarget: 1, SilverTarget: 2, GoldTarget: 3, SortOrder: 1,
			},
			{
				Code: "ai_dialogue", Title: "AI dialogue", Description: "Discuss solutions with the learning agent.",
				ProgressKey: achievement.ProgressAIDialogueCount, Tier: 0, Progress: 0,
				BronzeTarget: 1, SilverTarget: 5, GoldTarget: 10, SortOrder: 2,
			},
		},
	}
}
