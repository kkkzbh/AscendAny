package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

const testStudentAccountID = "11111111-1111-4111-8111-111111111111"

type stubStudentAnalyticsService struct {
	getSelf        func(context.Context, string, int) (studentanalytics.Result, error)
	getLeaderboard func(context.Context, string, int) (studentanalytics.LeaderboardResult, error)
}

func (service stubStudentAnalyticsService) GetSelf(ctx context.Context, accessToken string, limit int) (studentanalytics.Result, error) {
	if service.getSelf == nil {
		panic("unexpected student analytics call")
	}
	return service.getSelf(ctx, accessToken, limit)
}

func (service stubStudentAnalyticsService) GetLeaderboard(ctx context.Context, accessToken string, limit int) (studentanalytics.LeaderboardResult, error) {
	if service.getLeaderboard == nil {
		panic("unexpected student leaderboard call")
	}
	return service.getLeaderboard(ctx, accessToken, limit)
}

func TestSelfStudentAnalyticsUsesAuthenticatedPrincipalAndMapsReadyDTO(t *testing.T) {
	t.Parallel()

	knowledge := 91.25
	accuracy := 82.5
	quality := 73.75
	flexibility := 64.0
	proficiency := 55.5
	eventTime := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	service := stubStudentAnalyticsService{getSelf: func(_ context.Context, accessToken string, limit int) (studentanalytics.Result, error) {
		if accessToken != "student-access-token" || limit != defaultStudentAnalyticsHistoryLimit {
			t.Fatalf("access token/limit = %q/%d", accessToken, limit)
		}
		return studentanalytics.Result{
			State:        studentanalytics.StateReady,
			HeadRevision: 3,
			Ready: &studentanalytics.ReadyResult{
				ReferenceTime: eventTime,
				Rating:        1512,
				Current: analytics.MetricValues{
					Knowledge:   &knowledge,
					Accuracy:    &accuracy,
					Quality:     &quality,
					Flexibility: &flexibility,
					Proficiency: &proficiency,
				},
				ExamHistory: []studentanalytics.ExamHistoryPoint{{
					ExamID:     "22222222-2222-4222-8222-222222222222",
					SnapshotID: "33333333-3333-4333-8333-333333333333",
					Title:      "Exam One",
					EventTime:  eventTime,
					Values: analytics.MetricValues{
						Knowledge: &knowledge,
					},
				}},
				RatingHistory: []studentanalytics.RatingHistoryPoint{{
					ExamID:      "22222222-2222-4222-8222-222222222222",
					SnapshotID:  "33333333-3333-4333-8333-333333333333",
					Title:       "Exam One",
					EventTime:   eventTime,
					Rank:        2,
					OldRating:   1500,
					Delta:       12,
					NewRating:   1512,
					Seed:        0.5,
					Performance: 1530.25,
				}},
			},
		}, nil
	}}
	handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
	request := studentAnalyticsRequest("/api/v2/students/me/analytics")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	wantBody := `{"state":"ready","headRevision":3,"referenceTime":"2026-07-10T01:02:03Z","rating":1512,"current":{"knowledge":91.25,"accuracy":82.5,"quality":73.75,"flexibility":64,"proficiency":55.5},"examHistory":[{"examId":"22222222-2222-4222-8222-222222222222","snapshotId":"33333333-3333-4333-8333-333333333333","title":"Exam One","eventTime":"2026-07-10T01:02:03Z","values":{"knowledge":91.25,"accuracy":null,"quality":null,"flexibility":null,"proficiency":null}}],"ratingHistory":[{"examId":"22222222-2222-4222-8222-222222222222","snapshotId":"33333333-3333-4333-8333-333333333333","title":"Exam One","eventTime":"2026-07-10T01:02:03Z","rank":2,"oldRating":1500,"delta":12,"newRating":1512,"seed":0.5,"performance":1530.25}]}` + "\n"
	if response.Body.String() != wantBody {
		t.Fatalf("body = %s\nwant = %s", response.Body.String(), wantBody)
	}
}

func TestSelfStudentAnalyticsMapsUnavailableStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		path  string
		want  studentanalytics.Result
		body  string
		limit int
	}{
		{
			name:  "not generated default limit",
			path:  "/api/v2/students/me/analytics",
			want:  studentanalytics.Result{State: studentanalytics.StateNotGenerated},
			body:  `{"state":"not_generated","headRevision":0}` + "\n",
			limit: 50,
		},
		{
			name:  "no observations explicit upper limit",
			path:  "/api/v2/students/me/analytics?limit=100",
			want:  studentanalytics.Result{State: studentanalytics.StateNoObservations, HeadRevision: 4},
			body:  `{"state":"no_observations","headRevision":4}` + "\n",
			limit: 100,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := stubStudentAnalyticsService{getSelf: func(_ context.Context, accessToken string, limit int) (studentanalytics.Result, error) {
				if accessToken != "student-access-token" || limit != test.limit {
					t.Fatalf("access token/limit = %q/%d, want token/%d", accessToken, limit, test.limit)
				}
				return test.want, nil
			}}
			handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
			request := studentAnalyticsRequest(test.path)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != test.body {
				t.Fatalf("response = %d %s, want %d %s", response.Code, response.Body.String(), http.StatusOK, test.body)
			}
		})
	}
}

func TestSelfStudentAnalyticsRejectsNonCanonicalQueryContract(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/api/v2/students/me/analytics?",
		"/api/v2/students/me/analytics?limit=",
		"/api/v2/students/me/analytics?limit=0",
		"/api/v2/students/me/analytics?limit=01",
		"/api/v2/students/me/analytics?limit=101",
		"/api/v2/students/me/analytics?limit=+1",
		"/api/v2/students/me/analytics?limit=%31",
		"/api/v2/students/me/analytics?limit=1&limit=2",
		"/api/v2/students/me/analytics?other=1",
	}
	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			serviceCalls := 0
			service := stubStudentAnalyticsService{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
				serviceCalls++
				return studentanalytics.Result{}, nil
			}}
			handler := newStudentAnalyticsTestHandler(t, auth.Account{}, service, allowAllRateLimiter{})
			request := studentAnalyticsRequest(path)
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || serviceCalls != 0 {
				t.Fatalf("response = %d, service calls = %d, body = %s", response.Code, serviceCalls, response.Body.String())
			}
			assertAPIErrorRequestID(t, response)
		})
	}
}

func TestSelfStudentAnalyticsRequiresCurrentStudentSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		authorize  bool
		err        error
		wantStatus int
	}{
		{
			name:       "missing bearer",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "rejected bearer",
			authorize:  true,
			err:        &auth.Error{Code: auth.ErrorAuthentication, Message: "secret"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:      "admin role",
			authorize: true,
			err: &studentanalytics.Error{
				Code:  studentanalytics.ErrorForbidden,
				Op:    "verify student role",
				Cause: errors.New("student role required"),
			},
			wantStatus: http.StatusForbidden,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			studentService := stubStudentAnalyticsService{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
				return studentanalytics.Result{}, test.err
			}}
			handler := newStudentAnalyticsTestHandler(t, auth.Account{}, studentService, allowAllRateLimiter{})
			request := studentAnalyticsRequest("/api/v2/students/me/analytics")
			if !test.authorize {
				request.Header.Del("Authorization")
			}
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestSelfStudentAnalyticsRejectsRevisionRaceAndSanitizesInternalFailure(t *testing.T) {
	t.Parallel()

	secret := "database-secret-must-not-escape"
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name: "principal changed after session validation",
			err: &studentanalytics.Error{
				Code:  studentanalytics.ErrorPrincipalRejected,
				Op:    "test",
				Cause: errors.New(secret),
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "auth_authentication_rejected",
		},
		{
			name: "database failure",
			err: &studentanalytics.Error{
				Code:  studentanalytics.ErrorDatabase,
				Op:    "test",
				Cause: errors.New(secret),
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := stubStudentAnalyticsService{getSelf: func(context.Context, string, int) (studentanalytics.Result, error) {
				return studentanalytics.Result{}, test.err
			}}
			handler := newStudentAnalyticsTestHandler(t, testStudentAccount(), service, allowAllRateLimiter{})
			request := studentAnalyticsRequest("/api/v2/students/me/analytics")
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("internal detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestSelfStudentAnalyticsHasDedicatedRateScopeAndPreflightContract(t *testing.T) {
	t.Parallel()

	limiter := &captureRateLimiter{}
	service := stubStudentAnalyticsService{getSelf: func(_ context.Context, accessToken string, limit int) (studentanalytics.Result, error) {
		if accessToken != "student-access-token" || limit != 1 {
			t.Fatalf("access token/limit = %q/%d, want token/1", accessToken, limit)
		}
		return studentanalytics.Result{State: studentanalytics.StateNotGenerated}, nil
	}}
	handler := newStudentAnalyticsTestHandler(t, testStudentAccount(), service, limiter)
	request := studentAnalyticsRequest("/api/v2/students/me/analytics?limit=1")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || limiter.scope != "students.me.analytics" || limiter.client != "192.0.2.1" {
		t.Fatalf("response = %d, scope = %q, client = %q", response.Code, limiter.scope, limiter.client)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v2/students/me/analytics", nil)
	preflight.RemoteAddr = "192.0.2.1:54321"
	preflight.Header.Set("Origin", testWebOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
	preflightResponse := newTestResponseRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Headers") != "Authorization" {
		t.Fatalf("preflight = %d %#v %s", preflightResponse.Code, preflightResponse.Header(), preflightResponse.Body.String())
	}
}

func TestNewRequiresStudentAnalyticsDependency(t *testing.T) {
	t.Parallel()

	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.StudentAnalytics = nil
	if _, err := New(options); err == nil {
		t.Fatal("New accepted a missing student analytics dependency")
	}
}

func testStudentAccount() auth.Account {
	studentNumber := "20260001"
	return auth.Account{
		ID:            testStudentAccountID,
		Username:      "student_1",
		DisplayName:   "Student One",
		StudentNumber: &studentNumber,
		Role:          auth.RoleStudent,
		AuthRevision:  7,
	}
}

func newStudentAnalyticsTestHandler(
	t *testing.T,
	_ auth.Account,
	studentService StudentAnalyticsService,
	limiter RequestRateLimiter,
) http.Handler {
	t.Helper()
	return newStudentAnalyticsTestHandlerWithAuth(t, unusedAuthService{}, studentService, limiter)
}

func newStudentAnalyticsTestHandlerWithAuth(
	t *testing.T,
	authService AuthService,
	studentService StudentAnalyticsService,
	limiter RequestRateLimiter,
) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	options.Auth = authService
	options.StudentAnalytics = studentService
	options.RateLimiter = limiter
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func studentAnalyticsRequest(path string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "192.0.2.1:54321"
	request.Header.Set("Origin", testWebOrigin)
	request.Header.Set("Authorization", "Bearer student-access-token")
	return request
}
