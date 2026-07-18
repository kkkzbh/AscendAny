package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

func TestAgentV1AuthUsesOpaqueRotatingRefreshEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	account := auth.Account{
		ID: "123e4567-e89b-42d3-a456-426614174051", Username: "student_1", DisplayName: "Student One",
		StudentNumber: stringPointer("20260001"), Role: auth.RoleStudent, AuthRevision: 1,
	}
	initialRefresh := testAgentV1RawRefresh("123e4567-e89b-42d3-a456-426614174052", 0x11)
	rotatedRefresh := testAgentV1RawRefresh("123e4567-e89b-42d3-a456-426614174053", 0x22)
	initialCSRF := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	rotatedCSRF := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))
	var refreshInput auth.RefreshInput
	var logoutInput auth.LogoutInput
	service := stubAuthService{
		login: func(_ context.Context, input auth.LoginInput) (auth.AuthResult, error) {
			if input.Username != "student_1" || input.Password != "password-1234" {
				t.Fatalf("login input = %#v", input)
			}
			return auth.AuthResult{
				AccessToken: "access-1", ExpiresAt: now.Add(15 * time.Minute), Account: account,
				RefreshCookieValue: initialRefresh, CSRFToken: initialCSRF,
			}, nil
		},
		refresh: func(_ context.Context, input auth.RefreshInput) (auth.AuthResult, error) {
			refreshInput = input
			return auth.AuthResult{
				AccessToken: "access-2", ExpiresAt: now.Add(20 * time.Minute), Account: account,
				RefreshCookieValue: rotatedRefresh, CSRFToken: rotatedCSRF,
			}, nil
		},
		logout: func(_ context.Context, input auth.LogoutInput) error {
			logoutInput = input
			return nil
		},
		me: func(context.Context, string) (auth.Account, error) { return account, nil },
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	login := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/login", `{"username":"student_1","password":"password-1234","passwordMode":"plain","deviceId":"desktop-a"}`)
	loginResponse := newTestResponseRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var initial agentV1TokensResponse
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.RefreshToken == "" || initial.RefreshToken == initialRefresh ||
		strings.Contains(loginResponse.Body.String(), initialRefresh) || strings.Contains(loginResponse.Body.String(), initialCSRF) ||
		initial.Account.StudentID == nil || *initial.Account.StudentID != "20260001" {
		t.Fatalf("login response exposed or lost credentials: %s", loginResponse.Body.String())
	}

	refreshBody, _ := json.Marshal(agentV1RefreshRequest{RefreshToken: initial.RefreshToken})
	refresh := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/refresh", string(refreshBody))
	refreshResponse := newTestResponseRecorder()
	handler.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var rotated agentV1TokensResponse
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if refreshInput.RefreshToken != initialRefresh || refreshInput.CSRFToken != initialCSRF ||
		rotated.RefreshToken == initial.RefreshToken || !rotated.RefreshTokenExpiresAt.Equal(initial.RefreshTokenExpiresAt) {
		t.Fatalf("refresh input=%#v initial=%#v rotated=%#v", refreshInput, initial, rotated)
	}

	logoutBody, _ := json.Marshal(agentV1LogoutRequest{RefreshToken: &rotated.RefreshToken})
	logout := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/logout", string(logoutBody))
	logout.Header.Set("Authorization", "Bearer access-2")
	logoutResponse := newTestResponseRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK || logoutInput.AccessToken != "access-2" ||
		logoutInput.RefreshToken != rotatedRefresh || logoutInput.CSRFToken != rotatedCSRF {
		t.Fatalf("logout status=%d input=%#v body=%s", logoutResponse.Code, logoutInput, logoutResponse.Body.String())
	}
}

func TestAgentV1ProfileTreatsNullIdentityAsUnchanged(t *testing.T) {
	t.Parallel()

	studentNumber := "20260001"
	current := auth.Account{
		ID: "123e4567-e89b-42d3-a456-426614174054", Username: "student_1", DisplayName: "Student One",
		StudentNumber: &studentNumber, Role: auth.RoleStudent, AuthRevision: 1,
	}
	updates := 0
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{me: func(_ context.Context, token string) (auth.Account, error) {
		if token != "student-access" {
			t.Fatalf("me token = %q", token)
		}
		return current, nil
	}}
	options.AccountManagement = stubAccountManagementService{update: func(
		_ context.Context,
		token string,
		input auth.ProfileUpdateInput,
	) (auth.Account, error) {
		updates++
		if token != "student-access" || input.DisplayName != "Updated Student" {
			t.Fatalf("update token/input = %q/%#v", token, input)
		}
		updated := current
		updated.DisplayName = input.DisplayName
		return updated, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	request := agentV1JSONRequest(http.MethodPut, "/api/v1/auth/profile", `{"displayName":"Updated Student","studentId":null,"ptaNickname":null}`)
	request.Header.Set("Authorization", "Bearer student-access")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || updates != 1 ||
		response.Body.String() != `{"displayName":"Updated Student","studentId":"20260001","ptaNickname":null}`+"\n" {
		t.Fatalf("status=%d updates=%d body=%s", response.Code, updates, response.Body.String())
	}

	rejected := agentV1JSONRequest(http.MethodPut, "/api/v1/auth/profile", `{"displayName":"Updated Student","studentId":"20260002","ptaNickname":null}`)
	rejected.Header.Set("Authorization", "Bearer student-access")
	rejectedResponse := newTestResponseRecorder()
	handler.ServeHTTP(rejectedResponse, rejected)
	if rejectedResponse.Code != http.StatusUnprocessableEntity || updates != 1 ||
		!strings.Contains(rejectedResponse.Body.String(), `"code":"auth_profile_identity_immutable"`) {
		t.Fatalf("rejected status=%d updates=%d body=%s", rejectedResponse.Code, updates, rejectedResponse.Body.String())
	}
}

func TestAgentV1ClosesDisabledSSOAndLocalPasswordCapabilities(t *testing.T) {
	t.Parallel()
	account := auth.Account{
		ID: "123e4567-e89b-42d3-a456-426614174090", Username: "student_1", DisplayName: "Student One",
		Role: auth.RoleStudent, AuthRevision: 1,
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{
		exchangeSSO: func(_ context.Context, input auth.SSOExchangeInput) (auth.AuthResult, error) {
			if input.Token != "12345678901234567890123456789012" {
				t.Fatalf("SSO token = %q", input.Token)
			}
			return auth.AuthResult{}, &auth.Error{Code: auth.ErrorSSODisabled, Message: "SSO is disabled on this server."}
		},
		me: func(_ context.Context, token string) (auth.Account, error) {
			if token != "student-access" {
				t.Fatalf("me token = %q", token)
			}
			return account, nil
		},
		bootstrapLocalPassword: func(_ context.Context, input auth.LocalPasswordBootstrapInput) error {
			if input.AccessToken != "student-access" || input.NewPassword != "passw0rd" {
				t.Fatalf("bootstrap input = %#v", input)
			}
			return &auth.Error{Code: auth.ErrorLocalPasswordEnabled, Message: "Local password login is already enabled for this account."}
		},
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	sso := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/sso/exchange", `{"token":"12345678901234567890123456789012"}`)
	ssoResponse := newTestResponseRecorder()
	handler.ServeHTTP(ssoResponse, sso)
	if ssoResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(ssoResponse.Body.String(), `"code":"AUTH_SSO_DISABLED"`) {
		t.Fatalf("SSO status=%d body=%s", ssoResponse.Code, ssoResponse.Body.String())
	}

	bootstrap := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/local-password/bootstrap", `{"newPassword":"passw0rd"}`)
	bootstrap.Header.Set("Authorization", "Bearer student-access")
	bootstrapResponse := newTestResponseRecorder()
	handler.ServeHTTP(bootstrapResponse, bootstrap)
	if bootstrapResponse.Code != http.StatusConflict ||
		!strings.Contains(bootstrapResponse.Body.String(), `"code":"AUTH_LOCAL_PASSWORD_ALREADY_ENABLED"`) {
		t.Fatalf("bootstrap status=%d body=%s", bootstrapResponse.Code, bootstrapResponse.Body.String())
	}
}

func TestAgentV1ProfileIdentityOnlyRejectsNonemptyChanges(t *testing.T) {
	t.Parallel()

	studentNumber := "20260001"
	current := auth.Account{StudentNumber: &studentNumber}
	empty := ""
	same := studentNumber
	different := "20260002"
	pta := "pta-name"
	for _, test := range []struct {
		name    string
		payload agentV1ProfileRequest
		want    bool
	}{
		{name: "null", payload: agentV1ProfileRequest{}, want: true},
		{name: "empty identity", payload: agentV1ProfileRequest{StudentID: &empty, PTANickname: &empty}, want: true},
		{name: "same student", payload: agentV1ProfileRequest{StudentID: &same}, want: true},
		{name: "different student", payload: agentV1ProfileRequest{StudentID: &different}},
		{name: "nonempty PTA", payload: agentV1ProfileRequest{PTANickname: &pta}},
	} {
		if got := agentV1ProfileIdentityUnchanged(test.payload, current); got != test.want {
			t.Fatalf("%s unchanged = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestAgentV1ErrorsAndGETCORSMatchOriginalClientContract(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(health.Report{Status: health.StatusReady})
	request := agentV1JSONRequest(http.MethodPost, "/api/v1/auth/refresh", `{"refreshToken":"invalid"}`)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Body.String() != `{"error":{"code":"auth_authentication_rejected","message":"Authentication was rejected."}}`+"\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/me", nil)
	preflight.RemoteAddr = "192.0.2.1:44000"
	preflight.Header.Set("Origin", testWebOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type, authorization")
	preflightResponse := newTestResponseRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent ||
		preflightResponse.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" {
		t.Fatalf("preflight status=%d headers=%#v body=%s", preflightResponse.Code, preflightResponse.Header(), preflightResponse.Body.String())
	}
}

func TestAgentV1ProjectsDashboardAchievementsLeaderboardAndRecommendations(t *testing.T) {
	t.Parallel()
	studentNumber := "20260001"
	account := auth.Account{
		ID: "123e4567-e89b-42d3-a456-426614174061", Username: "student_1", DisplayName: "Student One",
		StudentNumber: &studentNumber, Role: auth.RoleStudent, AuthRevision: 1,
	}
	knowledge, accuracy := 80.0, 70.0
	score, solvedMedian := 100.0, 2.0
	eventTime := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	analyticsService := stubStudentAnalyticsService{
		getSelf: func(_ context.Context, token string, limit int) (studentanalytics.Result, error) {
			if token != "student-access" || limit != studentanalytics.MaxHistoryLimit {
				t.Fatalf("dashboard token/limit=%q/%d", token, limit)
			}
			return studentanalytics.Result{State: studentanalytics.StateReady, HeadRevision: 2, Ready: &studentanalytics.ReadyResult{
				ReferenceTime: eventTime, Rating: 1510,
				Current: analytics.MetricValues{Knowledge: &knowledge, Accuracy: &accuracy},
				LatestPeer: &studentanalytics.LatestExamPeer{
					TotalParticipants: 1, Position: 1, Rank: 1, Score: &score, Solved: 2,
					BandMedian: studentanalytics.PeerValues{
						Score: &score, Solved: &solvedMedian,
						Values: analytics.MetricValues{Knowledge: &knowledge, Accuracy: &accuracy},
					},
				},
				ExamHistory: []studentanalytics.ExamHistoryPoint{{
					ExamID: "123e4567-e89b-42d3-a456-426614174062", SnapshotID: "123e4567-e89b-42d3-a456-426614174063",
					Title: "Exam One", EventTime: eventTime, Values: analytics.MetricValues{Knowledge: &knowledge, Accuracy: &accuracy},
				}},
				RatingHistory: []studentanalytics.RatingHistoryPoint{{
					ExamID: "123e4567-e89b-42d3-a456-426614174062", SnapshotID: "123e4567-e89b-42d3-a456-426614174063",
					Title: "Exam One", EventTime: eventTime, Rank: 1, OldRating: 1500, Delta: 10, NewRating: 1510,
				}},
			}}, nil
		},
		getLeaderboard: func(_ context.Context, token string, limit int) (studentanalytics.LeaderboardResult, error) {
			if token != "student-access" || limit != studentanalytics.MaxLeaderboardLimit {
				t.Fatalf("leaderboard token/limit=%q/%d", token, limit)
			}
			return studentanalytics.LeaderboardResult{State: studentanalytics.StateReady, HeadRevision: 2, Population: 1,
				Items: []studentanalytics.LeaderboardItem{{Rank: 1, StudentNumber: studentNumber, DisplayName: stringPointer("Student One"), Rating: 1510, Metrics: analytics.MetricValues{Knowledge: &knowledge}}},
			}, nil
		},
	}
	achievementService := &achievementServiceStub{getSelf: func(context.Context, string) (achievement.Result, error) {
		return validHTTPAchievementResult(), nil
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{me: func(_ context.Context, token string) (auth.Account, error) {
		if token != "student-access" {
			t.Fatalf("me token=%q", token)
		}
		return account, nil
	}}
	options.StudentAnalytics = analyticsService
	options.Achievement = achievementService
	options.RecommendationReader = recommendationReaderStub{read: func(_ context.Context, token string) (current recommendation.CurrentRecommendation, err error) {
		return testFreshRecommendation(recommendation.RecommendationResultReady), nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		path string
		want []string
	}{
		{path: "/api/v1/students/dashboard?studentId=20260001", want: []string{
			`"knowledge":80`, `"studentId":"20260001"`, `"noSubmissionRecords":false`,
			`"milestoneStreak":{"available":true`, `"peerComparison":{"available":true`,
			`"postExamSupport":{"available":true`,
		}},
		{path: "/api/v1/students/achievements?studentId=20260001", want: []string{`"code":"exam_mastery"`, `"studentId":"20260001"`}},
		{path: "/api/v1/students/leaderboard", want: []string{`"grade":"2026"`, `"username":"Student One"`}},
		{path: "/api/v1/recommendations/path/me", want: []string{`"targets":["arrays"]`, `"path":["arrays","graphs"]`}},
		{path: "/api/v1/recommendations/path/me/status", want: []string{
			`"point":"arrays"`, `"mastery":0.45`, `"attempted":3`, `"correct":2`,
			`"lastTriedAt":"2026-07-18T08:30:00Z"`,
		}},
		{path: "/api/v1/recommendations/knowledge/arrays?topK=1", want: []string{
			`"point":"arrays"`, `"attempted":3`, `"correct":2`,
			`"recentSeries":[{"date":"2026-07-18","attempted":4,"correct":2}]`, `"problemId":"arrays"`,
		}},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.RemoteAddr = "192.0.2.1:44000"
		request.Header.Set("Authorization", "Bearer student-access")
		request.Header.Set("Content-Type", "application/json")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
		for _, want := range test.want {
			if !strings.Contains(response.Body.String(), want) {
				t.Fatalf("path=%s missing %s in %s", test.path, want, response.Body.String())
			}
		}
	}
}

func TestAgentV1DashboardReturnsLatestRatingHistoryFirst(t *testing.T) {
	t.Parallel()

	studentNumber := "20260001"
	firstTime := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	latestTime := firstTime.Add(24 * time.Hour)
	firstMetric, latestMetric := 60.0, 75.0
	response, err := mapAgentV1Dashboard(auth.Account{StudentNumber: &studentNumber}, studentanalytics.Result{
		State: studentanalytics.StateReady,
		Ready: &studentanalytics.ReadyResult{
			Rating: 1030,
			ExamHistory: []studentanalytics.ExamHistoryPoint{
				{ExamID: "exam-old", Title: "Old Exam", EventTime: firstTime, Values: analytics.MetricValues{Knowledge: &firstMetric}},
				{ExamID: "exam-latest", Title: "Latest Exam", EventTime: latestTime, Values: analytics.MetricValues{Knowledge: &latestMetric}},
			},
			RatingHistory: []studentanalytics.RatingHistoryPoint{
				{ExamID: "exam-old", Title: "Old Exam", EventTime: firstTime, OldRating: 1000, Delta: 10, NewRating: 1010},
				{ExamID: "exam-latest", Title: "Latest Exam", EventTime: latestTime, OldRating: 1010, Delta: 20, NewRating: 1030},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Rating.History) != 2 || response.Rating.History[0].ExamID != "exam-latest" ||
		response.Rating.History[0].Date != "2026-07-18" || response.Rating.History[1].ExamID != "exam-old" ||
		response.Rating.History[1].Date != "2026-07-17" || response.Rating.LastDelta == nil ||
		*response.Rating.LastDelta != 20 || response.MetricDelta.LatestExamID == nil ||
		*response.MetricDelta.LatestExamID != "exam-latest" || response.MetricDelta.LatestExamDate == nil ||
		*response.MetricDelta.LatestExamDate != "2026-07-18" || response.ProgressExplanation.LatestExamDate == nil ||
		*response.ProgressExplanation.LatestExamDate != "2026-07-18" || !response.MilestoneStreak.Available ||
		response.MilestoneStreak.CurrentPositiveStreak != 2 || response.MilestoneStreak.BestPositiveStreak != 2 ||
		len(response.MilestoneStreak.NewMilestones) != 1 ||
		response.MilestoneStreak.NewMilestones[0].Code != "knowledge_70" ||
		len(response.MilestoneStreak.NextTargets) != 2 || !response.PostExamSupport.Available ||
		response.PostExamSupport.Mode != "steady" || response.PeerComparison.Available {
		t.Fatalf("rating/metric history = %#v/%#v", response.Rating, response.MetricDelta)
	}
}

func TestAgentV1ProgressUsesFrozenNullableAndRoundedDeltaSemantics(t *testing.T) {
	t.Parallel()
	baselineKnowledge, baselineAccuracy := 80.0, 69.5
	baselineQuality, baselineFlexibility := 70.5, 69.5
	currentAccuracy, currentQuality, currentFlexibility := 72.5, 73.5, 66.5
	currentProficiency, baselineProficiency := 60.2, 60.1
	baseline := analytics.MetricValues{
		Knowledge: &baselineKnowledge, Accuracy: &baselineAccuracy, Quality: &baselineQuality,
		Flexibility: &baselineFlexibility, Proficiency: &baselineProficiency,
	}
	current := analytics.MetricValues{
		Knowledge: nil, Accuracy: &currentAccuracy, Quality: &currentQuality,
		Flexibility: &currentFlexibility, Proficiency: &currentProficiency,
	}
	delta := subtractAgentV1Metrics(current, baseline)
	if delta.Knowledge != -80 || delta.Accuracy != 2 || delta.Quality != 4 ||
		delta.Flexibility != -4 || delta.Proficiency != 0 {
		t.Fatalf("rounded metric delta = %#v", delta)
	}

	progress := agentV1ProgressFromLatest(
		studentanalytics.ExamHistoryPoint{
			ExamID: "exam-latest", Title: "Latest Exam",
			EventTime: time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC), Values: current,
		},
		studentanalytics.RatingHistoryPoint{Delta: 0},
		baseline,
		false,
	)
	if len(progress.KeyImprovements) != 1 ||
		progress.KeyImprovements[0] != "质量 +4，说明代码实现质量更稳，边界处理更扎实" ||
		len(progress.KeySetbacks) != 1 ||
		progress.KeySetbacks[0] != "灵活 -4，说明切题策略偏保守，建议优化做题节奏" ||
		progress.Summary != "本场有进步也有波动，建议保留有效策略并优先修正退步项。" {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestAgentV1AchievementsSupportsExplicitStudentNumberReadWithoutAuthorization(t *testing.T) {
	t.Parallel()

	studentNumber := "20260001"
	service := &achievementServiceStub{
		getSelf: func(context.Context, string) (achievement.Result, error) {
			panic("selector read reached authenticated achievement capability")
		},
		getByStudentNumber: func(_ context.Context, selected string) (achievement.Result, error) {
			if selected != studentNumber {
				t.Fatalf("selected student number = %q", selected)
			}
			return validHTTPAchievementResult(), nil
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		panic("anonymous selector read reached auth service")
	}}
	options.Achievement = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/students/achievements?studentId="+studentNumber, nil)
	request.RemoteAddr = "192.0.2.1:44000"
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.studentNumberCalls != 1 || service.calls != 0 ||
		service.selectedStudentNumber != studentNumber ||
		!strings.Contains(response.Body.String(), `"studentId":"20260001"`) ||
		!strings.Contains(response.Body.String(), `"code":"exam_mastery"`) {
		t.Fatalf("status=%d service=%#v body=%s", response.Code, service, response.Body.String())
	}

	malformedAuthorization := httptest.NewRequest(http.MethodGet, "/api/v1/students/achievements?studentId="+studentNumber, nil)
	malformedAuthorization.RemoteAddr = "192.0.2.1:44000"
	malformedAuthorization.Header.Set("Authorization", "Basic invalid")
	malformedResponse := newTestResponseRecorder()
	handler.ServeHTTP(malformedResponse, malformedAuthorization)
	if malformedResponse.Code != http.StatusUnauthorized || service.studentNumberCalls != 1 ||
		!strings.Contains(malformedResponse.Body.String(), `"code":"auth_authentication_rejected"`) {
		t.Fatalf("malformed auth status=%d calls=%d body=%s", malformedResponse.Code, service.studentNumberCalls, malformedResponse.Body.String())
	}
}

func TestAgentV1AchievementsSupportsExactFrozenFrontendSelectorWithoutAuthorization(t *testing.T) {
	t.Parallel()

	studentNumber := "20260001"
	ptaNickname := "Alice"
	service := &achievementServiceStub{
		getSelf: func(context.Context, string) (achievement.Result, error) {
			panic("selector read reached authenticated achievement capability")
		},
		getByStudentIdentity: func(_ context.Context, selectedStudentNumber, selectedPTANickname string) (achievement.Result, error) {
			if selectedStudentNumber != studentNumber || selectedPTANickname != ptaNickname {
				t.Fatalf("selected identity = %q/%q", selectedStudentNumber, selectedPTANickname)
			}
			return validHTTPAchievementResult(), nil
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		panic("anonymous selector read reached auth service")
	}}
	options.Achievement = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/students/achievements?studentId="+studentNumber+"&ptaNickname="+ptaNickname, nil)
	request.RemoteAddr = "192.0.2.1:44000"
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.studentIdentityCalls != 1 || service.studentNumberCalls != 0 ||
		service.calls != 0 || service.selectedStudentNumber != studentNumber || service.selectedPTANickname != ptaNickname ||
		!strings.Contains(response.Body.String(), `"studentId":"20260001"`) ||
		!strings.Contains(response.Body.String(), `"ptaNickname":"Alice"`) {
		t.Fatalf("status=%d service=%#v body=%s", response.Code, service, response.Body.String())
	}
}

func TestAgentV1AchievementsExactSelectorMismatchIsNotFound(t *testing.T) {
	t.Parallel()

	service := &achievementServiceStub{
		getSelf: func(context.Context, string) (achievement.Result, error) {
			panic("selector read reached authenticated achievement capability")
		},
		getByStudentIdentity: func(context.Context, string, string) (achievement.Result, error) {
			return achievement.Result{}, &achievement.Error{
				Code: achievement.ErrorStudentNotFound, Op: "select exact student identity", Cause: errors.New("private database detail"),
			}
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Achievement = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/students/achievements?studentId=20260001&ptaNickname=Wrong", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || service.studentIdentityCalls != 1 ||
		response.Body.String() != `{"error":{"code":"student_not_found","message":"Student was not found."}}`+"\n" {
		t.Fatalf("status=%d service=%#v body=%s", response.Code, service, response.Body.String())
	}
}

func TestAgentV1AchievementsKeepsAuthenticatedSelectorStrict(t *testing.T) {
	t.Parallel()

	studentNumber := "20260002"
	service := &achievementServiceStub{
		getSelf: func(context.Context, string) (achievement.Result, error) {
			panic("rejected selector reached self achievement read")
		},
		getByStudentNumber: func(context.Context, string) (achievement.Result, error) {
			panic("authenticated selector reached public achievement read")
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = stubAuthService{me: func(_ context.Context, token string) (auth.Account, error) {
		if token != "student-access" {
			t.Fatalf("token = %q", token)
		}
		return auth.Account{StudentNumber: &studentNumber, Role: auth.RoleStudent}, nil
	}}
	options.Achievement = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/students/achievements?studentId=20260001", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer student-access")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || service.calls != 0 || service.studentNumberCalls != 0 ||
		!strings.Contains(response.Body.String(), `"code":"student_selector_rejected"`) {
		t.Fatalf("status=%d service=%#v body=%s", response.Code, service, response.Body.String())
	}
}

func TestAgentV1AchievementsMapsMissingStudentSelector(t *testing.T) {
	t.Parallel()

	service := &achievementServiceStub{
		getSelf: func(context.Context, string) (achievement.Result, error) {
			panic("missing student reached self read")
		},
		getByStudentNumber: func(context.Context, string) (achievement.Result, error) {
			return achievement.Result{}, &achievement.Error{
				Code: achievement.ErrorStudentNotFound, Op: "select student", Cause: errors.New("private database detail"),
			}
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Achievement = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/students/achievements?studentId=20269999", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound ||
		response.Body.String() != `{"error":{"code":"student_not_found","message":"Student was not found."}}`+"\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentV1MetaAndFeedbackUseCanonicalGoServices(t *testing.T) {
	t.Parallel()
	importedAt := time.Date(2026, 7, 18, 9, 30, 0, 0, time.UTC)
	var feedbackInput feedback.ApplicationInput
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ImportReader = stubImportReader{list: func(_ context.Context, cursor *string, limit int) (importing.JobPage, error) {
		if cursor != nil || limit != importing.MaxJobPageSize {
			t.Fatalf("cursor/limit=%v/%d", cursor, limit)
		}
		return importing.JobPage{Items: []importing.PublicJob{{Status: importing.JobSucceeded, UpdatedAt: importedAt}}}, nil
	}}
	options.Feedback = feedbackServiceStub{submit: func(_ context.Context, access string, input feedback.ApplicationInput) (feedback.SubmitResult, error) {
		if access != "student-access" {
			t.Fatalf("feedback access=%q", access)
		}
		feedbackInput = input
		return feedback.SubmitResult{}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	meta := httptest.NewRequest(http.MethodGet, "/api/v1/meta/latest_exam_imported_at", nil)
	meta.RemoteAddr = "192.0.2.1:44000"
	meta.Header.Set("Content-Type", "application/json")
	metaResponse := newTestResponseRecorder()
	handler.ServeHTTP(metaResponse, meta)
	if metaResponse.Code != http.StatusOK || !strings.Contains(metaResponse.Body.String(), `"latestExamImportedAt":"2026-07-18T09:30:00Z"`) {
		t.Fatalf("meta status=%d body=%s", metaResponse.Code, metaResponse.Body.String())
	}

	feedbackRequest := agentV1JSONRequest(http.MethodPost, "/api/v1/feedback", `{"title":"UI","content":"Blank","images":[],"platform":"desktop"}`)
	feedbackRequest.Header.Set("Authorization", "Bearer student-access")
	feedbackResponse := newTestResponseRecorder()
	handler.ServeHTTP(feedbackResponse, feedbackRequest)
	if feedbackResponse.Code != http.StatusOK ||
		feedbackResponse.Body.String() != `{"success":true,"message":"反馈已发送，感谢你的反馈。"}`+"\n" ||
		feedbackInput.Title != "UI" || feedbackInput.ClientRequestID == "" {
		t.Fatalf("feedback status=%d input=%#v body=%s", feedbackResponse.Code, feedbackInput, feedbackResponse.Body.String())
	}
}

type agentV1FeedbackPrincipalVerifier struct {
	principal auth.AccessPrincipal
}

func (verifier agentV1FeedbackPrincipalVerifier) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	if token != "student-access" {
		panic("unexpected Agent v1 feedback access token")
	}
	return verifier.principal, nil
}

type agentV1FeedbackSubmissionService func(context.Context, feedback.SubmitInput) (feedback.SubmitResult, error)

func (service agentV1FeedbackSubmissionService) SubmitAuthenticated(ctx context.Context, input feedback.SubmitInput) (feedback.SubmitResult, error) {
	return service(ctx, input)
}

func TestAgentV1FeedbackDecodesHistoricalScreenshotContract(t *testing.T) {
	store, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), feedback.MaxImageBytes)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("frozen Agent screenshot")
	digest := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(digest[:])
	var submitted feedback.SubmitInput
	application, err := feedback.NewApplicationService(
		agentV1FeedbackPrincipalVerifier{principal: validAgentV1FeedbackPrincipal()},
		agentV1FeedbackSubmissionService(func(_ context.Context, input feedback.SubmitInput) (feedback.SubmitResult, error) {
			submitted = input
			return feedback.SubmitResult{}, nil
		}),
		store,
	)
	if err != nil {
		t.Fatal(err)
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Feedback = application
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/VND.ASCENDANY+PNG;base64," + base64.StdEncoding.EncodeToString(content)
	body, err := json.Marshal(map[string]any{
		"title": "UI", "content": "Blank", "platform": "desktop",
		"images": []map[string]string{{"name": "截图 alpha", "dataUrl": dataURL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agentV1JSONRequest(http.MethodPost, "/api/v1/feedback", string(body))
	request.Header.Set("Authorization", "Bearer student-access")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		response.Body.String() != `{"success":true,"message":"反馈已发送，感谢你的反馈。"}`+"\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(submitted.Attachments) != 1 {
		t.Fatalf("attachments=%#v", submitted.Attachments)
	}
	attachment := submitted.Attachments[0]
	if attachment.Sequence != 1 || attachment.Filename != "alpha.vnd.ascendany" ||
		attachment.MediaType != "image/vnd.ascendany+png" || attachment.SHA256 != expectedHash ||
		attachment.SizeBytes != int64(len(content)) || attachment.StorageKey != "sha256/"+expectedHash[:2]+"/"+expectedHash {
		t.Fatalf("attachment=%#v", attachment)
	}
}

func TestAgentV1FeedbackReturnsStableScreenshotErrors(t *testing.T) {
	tests := []struct {
		name        string
		image       map[string]string
		wantCode    string
		wantMessage string
	}{
		{
			name:     "invalid data URL",
			image:    map[string]string{"name": "screen.png", "dataUrl": "DATA:image/png;base64,eA=="},
			wantCode: "FEEDBACK_IMAGE_INVALID", wantMessage: "反馈截图格式不正确。",
		},
		{
			name: "decoded image above eight MiB",
			image: map[string]string{
				"name":    "screen.png",
				"dataUrl": "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, feedback.MaxImageBytes+1)),
			},
			wantCode: "FEEDBACK_IMAGE_TOO_LARGE", wantMessage: "单张反馈截图不能超过 8MB。",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), feedback.MaxImageBytes)
			if err != nil {
				t.Fatal(err)
			}
			application, err := feedback.NewApplicationService(
				agentV1FeedbackPrincipalVerifier{principal: validAgentV1FeedbackPrincipal()},
				agentV1FeedbackSubmissionService(func(context.Context, feedback.SubmitInput) (feedback.SubmitResult, error) {
					t.Fatal("invalid screenshot reached feedback transaction")
					return feedback.SubmitResult{}, nil
				}),
				store,
			)
			if err != nil {
				t.Fatal(err)
			}
			options := testHandlerOptions(health.Report{Status: health.StatusReady})
			options.Feedback = application
			handler, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]any{
				"title": "UI", "content": "Blank", "images": []map[string]string{test.image},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := agentV1JSONRequest(http.MethodPost, "/api/v1/feedback", string(body))
			request.Header.Set("Authorization", "Bearer student-access")
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			wantBody := `{"error":{"code":"` + test.wantCode + `","message":"` + test.wantMessage + `"}}` + "\n"
			if response.Code != http.StatusBadRequest || response.Body.String() != wantBody {
				t.Fatalf("status=%d body=%s want=%s", response.Code, response.Body.String(), wantBody)
			}
		})
	}
}

func TestAgentV1FeedbackRequiresAnExplicitBoundedImageArray(t *testing.T) {
	images := make([]map[string]string, feedback.MaxImages+1)
	for index := range images {
		images[index] = map[string]string{"name": "screen.png", "dataUrl": "data:image/png;base64,eA=="}
	}
	tooMany, err := json.Marshal(map[string]any{"title": "UI", "content": "Blank", "images": images})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
	}{
		{name: "missing", body: `{"title":"UI","content":"Blank"}`, wantCode: "invalid_json", wantMessage: "Request body must contain one strict JSON object."},
		{name: "null", body: `{"title":"UI","content":"Blank","images":null}`, wantCode: "FEEDBACK_IMAGE_INVALID", wantMessage: "反馈截图格式不正确。"},
		{name: "too many", body: string(tooMany), wantCode: "FEEDBACK_TOO_MANY_IMAGES", wantMessage: "最多上传 8 张反馈截图。"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			options := testHandlerOptions(health.Report{Status: health.StatusReady})
			options.Feedback = feedbackServiceStub{submit: func(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error) {
				t.Fatal("invalid image array reached feedback service")
				return feedback.SubmitResult{}, nil
			}}
			handler, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			request := agentV1JSONRequest(http.MethodPost, "/api/v1/feedback", test.body)
			request.Header.Set("Authorization", "Bearer student-access")
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			wantBody := `{"error":{"code":"` + test.wantCode + `","message":"` + test.wantMessage + `"}}` + "\n"
			if response.Code != http.StatusBadRequest || response.Body.String() != wantBody {
				t.Fatalf("status=%d body=%s want=%s", response.Code, response.Body.String(), wantBody)
			}
		})
	}
}

func TestAgentV1FeedbackUsesUploadRequestLifetimeAndFrozenJSONLimit(t *testing.T) {
	authTimeout := 3 * time.Second
	uploadTimeout := 17 * time.Second
	registry, err := newRouteRegistry(apiRouteContracts(&Handler{}, authTimeout, uploadTimeout))
	if err != nil {
		t.Fatal(err)
	}
	policy, known := registry.policyForMethod("/api/v1/feedback", http.MethodPost)
	if !known || policy.bodyTimeout != uploadTimeout {
		t.Fatalf("known=%t policy=%#v", known, policy)
	}
	if maxAgentV1FeedbackJSONBytes != int64(feedback.MaxImages*feedback.MaxImageDataURLBytes+128<<10) {
		t.Fatalf("feedback JSON limit=%d", maxAgentV1FeedbackJSONBytes)
	}
}

func validAgentV1FeedbackPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: "123e4567-e89b-42d3-a456-426614174081",
		SessionID: "123e4567-e89b-42d3-a456-426614174082",
		JWTID:     "123e4567-e89b-42d3-a456-426614174083",
		Role:      auth.RoleStudent, AuthRevision: 1,
	}
}

func TestAgentV1LatestImportScansEveryPageAndUsesMaximumSucceededTimestamp(t *testing.T) {
	t.Parallel()

	pageOneCursor := "123e4567-e89b-42d3-a456-426614174071"
	pageTwoCursor := "123e4567-e89b-42d3-a456-426614174072"
	oldSuccess := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	latestSuccess := time.Date(2026, 7, 18, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	calls := 0
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ImportReader = stubImportReader{list: func(_ context.Context, cursor *string, limit int) (importing.JobPage, error) {
		if limit != importing.MaxJobPageSize {
			t.Fatalf("limit = %d", limit)
		}
		calls++
		switch {
		case cursor == nil:
			return importing.JobPage{
				Items:      []importing.PublicJob{{Status: importing.JobFailed, UpdatedAt: latestSuccess.Add(time.Hour)}},
				NextCursor: &pageOneCursor,
			}, nil
		case *cursor == pageOneCursor:
			return importing.JobPage{
				Items:      []importing.PublicJob{{Status: importing.JobSucceeded, UpdatedAt: oldSuccess}},
				NextCursor: &pageTwoCursor,
			}, nil
		case *cursor == pageTwoCursor:
			return importing.JobPage{Items: []importing.PublicJob{
				{Status: importing.JobSucceeded, UpdatedAt: latestSuccess},
				{Status: importing.JobSucceeded, UpdatedAt: latestSuccess.Add(-time.Minute)},
			}}, nil
		default:
			t.Fatalf("unexpected cursor = %v", cursor)
			return importing.JobPage{}, nil
		}
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/meta/latest_exam_imported_at", nil)
	request.RemoteAddr = "192.0.2.1:44000"
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 3 ||
		response.Body.String() != `{"latestExamImportedAt":"2026-07-18T03:00:00Z"}`+"\n" {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestAgentV1DataEventsSendsSnapshotAndStopsOnCancellation(t *testing.T) {
	t.Parallel()
	importedAt := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ImportReader = stubImportReader{list: func(context.Context, *string, int) (importing.JobPage, error) {
		return importing.JobPage{Items: []importing.PublicJob{{Status: importing.JobSucceeded, UpdatedAt: importedAt}}}, nil
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/meta/data-events/stream", nil).WithContext(ctx)
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Accept", "text/event-stream")
	response := newAgentV1SignalRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(response, request)
	}()
	select {
	case <-response.flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("data event stream did not flush its snapshot")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("data event stream did not stop after cancellation")
	}
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		!strings.Contains(response.Body.String(), `event: snapshot`) ||
		!strings.Contains(response.Body.String(), `"latestExamImportedAt":"2026-07-18T10:00:00Z"`) {
		t.Fatalf("status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

type agentV1SignalRecorder struct {
	*testResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func newAgentV1SignalRecorder() *agentV1SignalRecorder {
	return &agentV1SignalRecorder{testResponseRecorder: newTestResponseRecorder(), flushed: make(chan struct{})}
}

func (recorder *agentV1SignalRecorder) Flush() {
	recorder.testResponseRecorder.Flush()
	recorder.once.Do(func() { close(recorder.flushed) })
}

func agentV1JSONRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testWebOrigin)
	return request
}

func testAgentV1RawRefresh(identifier string, fill byte) string {
	return "v1." + identifier + "." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func stringPointer(value string) *string { return &value }
