package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

func TestStudentLeaderboardMapsCanonicalPage(t *testing.T) {
	t.Parallel()
	name := "Student One"
	knowledge := 91.25
	service := stubStudentAnalyticsService{getLeaderboard: func(_ context.Context, token string, limit int) (studentanalytics.LeaderboardResult, error) {
		if token != "student-access-token" || limit != defaultStudentLeaderboardLimit {
			t.Fatalf("token/limit=%q/%d", token, limit)
		}
		return studentanalytics.LeaderboardResult{
			State:        studentanalytics.StateReady,
			HeadRevision: 3,
			Population:   35,
			Items: []studentanalytics.LeaderboardItem{{
				Rank:          1,
				StudentNumber: "20260001",
				DisplayName:   &name,
				Rating:        1512,
				Metrics:       analytics.MetricValues{Knowledge: &knowledge},
			}},
		}, nil
	}}
	handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
	request := studentAnalyticsRequest("/api/v2/students/leaderboard")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	want := `{"state":"ready","headRevision":3,"population":35,"items":[{"rank":1,"studentNumber":"20260001","displayName":"Student One","rating":1512,"metrics":{"knowledge":91.25,"accuracy":null,"quality":null,"flexibility":null,"proficiency":null}}]}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("response=%d %s, want=%s", response.Code, response.Body.String(), want)
	}
}

func TestStudentLeaderboardReturnsExplicitEmptyArray(t *testing.T) {
	t.Parallel()
	service := stubStudentAnalyticsService{getLeaderboard: func(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
		return studentanalytics.LeaderboardResult{State: studentanalytics.StateNotGenerated}, nil
	}}
	handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
	request := studentAnalyticsRequest("/api/v2/students/leaderboard?limit=200")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"state":"not_generated","headRevision":0,"population":0,"items":[]}`+"\n" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestStudentLeaderboardRejectsInvalidQueryBeforeService(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/api/v2/students/leaderboard?",
		"/api/v2/students/leaderboard?limit=0",
		"/api/v2/students/leaderboard?limit=01",
		"/api/v2/students/leaderboard?limit=201",
		"/api/v2/students/leaderboard?limit=1&limit=2",
		"/api/v2/students/leaderboard?other=1",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			calls := 0
			service := stubStudentAnalyticsService{getLeaderboard: func(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
				calls++
				return studentanalytics.LeaderboardResult{}, nil
			}}
			handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, studentAnalyticsRequest(path))
			if response.Code != http.StatusBadRequest || calls != 0 {
				t.Fatalf("response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
			}
		})
	}
}

func TestStudentLeaderboardMapsAuthenticationAndSnapshotFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "token rejected",
			err:        &auth.Error{Code: auth.ErrorAuthentication, Message: "secret"},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "auth_authentication_rejected",
		},
		{
			name: "session changed in snapshot",
			err: &studentanalytics.Error{
				Code:  studentanalytics.ErrorPrincipalRejected,
				Op:    "resolve principal",
				Cause: errors.New("secret"),
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "auth_authentication_rejected",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := stubStudentAnalyticsService{getLeaderboard: func(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
				return studentanalytics.LeaderboardResult{}, test.err
			}}
			handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, studentAnalyticsRequest("/api/v2/students/leaderboard"))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestStudentLeaderboardHasDedicatedRateScopeAndPreflight(t *testing.T) {
	t.Parallel()
	limiter := &captureRateLimiter{}
	service := stubStudentAnalyticsService{getLeaderboard: func(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
		return studentanalytics.LeaderboardResult{State: studentanalytics.StateNotGenerated}, nil
	}}
	handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, limiter)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, studentAnalyticsRequest("/api/v2/students/leaderboard"))
	if response.Code != http.StatusOK || limiter.scope != "students.leaderboard" || limiter.client != "192.0.2.1" {
		t.Fatalf("response=%d scope=%q client=%q", response.Code, limiter.scope, limiter.client)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v2/students/leaderboard", nil)
	preflight.RemoteAddr = "192.0.2.1:54321"
	preflight.Header.Set("Origin", testWebOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
	preflightResponse := newTestResponseRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Headers") != "Authorization" {
		t.Fatalf("preflight=%d %#v %s", preflightResponse.Code, preflightResponse.Header(), preflightResponse.Body.String())
	}
}
