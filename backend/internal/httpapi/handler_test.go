package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/examcatalog"
	"github.com/kkkzbh/AscendAny/backend/internal/examgeneration"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/modelprobe"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

type staticReadiness struct {
	report health.Report
}

type testResponseRecorder struct {
	*httptest.ResponseRecorder
}

func newTestResponseRecorder() *testResponseRecorder {
	return &testResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (*testResponseRecorder) SetReadDeadline(time.Time) error {
	return nil
}

func (*testResponseRecorder) SetWriteDeadline(time.Time) error {
	return nil
}

type unusedAuthService struct{}

func (unusedAuthService) Login(context.Context, auth.LoginInput) (auth.AuthResult, error) {
	panic("unexpected auth call")
}

func (unusedAuthService) Refresh(context.Context, auth.RefreshInput) (auth.AuthResult, error) {
	panic("unexpected auth call")
}

func (unusedAuthService) Logout(context.Context, auth.LogoutInput) error {
	panic("unexpected auth call")
}

func (unusedAuthService) Me(context.Context, string) (auth.Account, error) {
	panic("unexpected auth call")
}

type unusedEnrollmentService struct{}

func (unusedEnrollmentService) IssueEnrollment(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
	panic("unexpected enrollment issue")
}

func (unusedEnrollmentService) RevokeEnrollment(context.Context, string, string) error {
	panic("unexpected enrollment revoke")
}

func (unusedEnrollmentService) ClaimEnrollment(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
	panic("unexpected enrollment claim")
}

type unusedAccountManagementService struct{}

func (unusedAccountManagementService) UpdateProfile(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error) {
	panic("unexpected account profile update")
}

func (unusedAccountManagementService) ListSessions(context.Context, string) ([]auth.ManagedSession, error) {
	panic("unexpected account session list")
}

func (unusedAccountManagementService) RevokeSession(context.Context, string, string) (bool, error) {
	panic("unexpected account session revocation")
}

type allowAllRateLimiter struct{}

func (allowAllRateLimiter) Allow(string, string) RateLimitDecision {
	return RateLimitDecision{Allowed: true}
}

type unusedArtifactPublisher struct{}

func (unusedArtifactPublisher) Publish(context.Context, io.Reader) (*artifact.Publication, error) {
	panic("unexpected artifact publish call")
}

type unusedImportQueue struct{}

func (unusedImportQueue) QueuePublication(context.Context, *artifact.Publication, string) (importing.QueueResult, error) {
	panic("unexpected import queue call")
}

type unusedImportReader struct{}

func (unusedImportReader) ListJobs(context.Context, *string, int) (importing.JobPage, error) {
	panic("unexpected import job list")
}

func (unusedImportReader) GetJob(context.Context, string) (importing.PublicJob, bool, error) {
	panic("unexpected import job read")
}

func (unusedImportReader) ReadEvents(context.Context, string, int64, int) (importing.EventBatch, bool, error) {
	panic("unexpected import event read")
}

type unusedStudentAnalyticsService struct{}

func (unusedStudentAnalyticsService) GetSelf(context.Context, string, int) (studentanalytics.Result, error) {
	panic("unexpected student analytics read")
}

type unusedAchievementService struct{}

func (unusedAchievementService) GetSelf(context.Context, string) (achievement.Result, error) {
	panic("unexpected achievement read")
}

type unusedRecommendationReader struct{}

func (unusedRecommendationReader) ReadCurrent(context.Context, string) (recommendation.CurrentRecommendation, error) {
	panic("unexpected recommendation read")
}

type unusedRecommendationAdminReader struct{}

func (unusedRecommendationAdminReader) ReadReviewContext(context.Context, string) (recommendation.ReviewContext, error) {
	panic("unexpected recommendation review context read")
}

type unusedModelProbeService struct{}

func (unusedModelProbeService) Test(context.Context, string, string) (modelprobe.Result, error) {
	panic("unexpected model connection probe")
}

func testModelProbe(writesEnabled bool) ModelProbeService {
	if !writesEnabled {
		return nil
	}
	return unusedModelProbeService{}
}

func (unusedStudentAnalyticsService) GetLeaderboard(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
	panic("unexpected student leaderboard read")
}

type unusedExamCatalogService struct{}

func (unusedExamCatalogService) List(context.Context, string, *string, int) (examcatalog.Page, error) {
	panic("unexpected exam catalog list")
}

func (unusedExamCatalogService) Get(context.Context, string, string) (examcatalog.Detail, bool, error) {
	panic("unexpected exam catalog detail")
}

type unusedExamGenerationService struct{}

func (unusedExamGenerationService) GetCurrent(context.Context, string, string) (examgeneration.Generation, bool, error) {
	panic("unexpected exam generation read")
}

func (unusedExamGenerationService) ReadEvents(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
	panic("unexpected exam generation event read")
}

type unusedAdministrationService struct{}

func (unusedAdministrationService) ListAccounts(context.Context, string, *string, int) (administration.AccountPage, error) {
	panic("unexpected managed account list")
}

func (unusedAdministrationService) ListStudents(context.Context, string, *string, int) (administration.StudentPage, error) {
	panic("unexpected managed student list")
}

func (unusedAdministrationService) ListAudit(context.Context, string, *string, int) (administration.AuditPage, error) {
	panic("unexpected audit list")
}

func (unusedAdministrationService) SetAccountDisabled(context.Context, string, string, bool) (administration.ManagedAccount, error) {
	panic("unexpected managed account mutation")
}

type unusedConfigurationService struct{}

func (unusedConfigurationService) List(context.Context, string, *configuration.Kind, *string, int) (configuration.ItemPage, error) {
	panic("unexpected configuration list")
}

func (unusedConfigurationService) Get(context.Context, string, string) (configuration.Item, bool, error) {
	panic("unexpected configuration read")
}

func (unusedConfigurationService) ListVersions(context.Context, string, string, *int64, int) (configuration.VersionPage, bool, error) {
	panic("unexpected configuration version list")
}

func (unusedConfigurationService) CreateVersion(context.Context, string, configuration.CreateVersionInput) (configuration.CreateVersionResult, error) {
	panic("unexpected configuration version create")
}

func (unusedConfigurationService) AuthorizeKnowledgeCatalogPublication(context.Context, string, configuration.CatalogPublicationAuthorizationInput) (configuration.CatalogPublicationAuthorizationResult, error) {
	panic("unexpected catalog publication authorization")
}

type unusedFeedbackService struct{}

func (unusedFeedbackService) SubmitAuthenticated(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error) {
	panic("unexpected feedback submission")
}

type unusedAgentNotesService struct{}

func (unusedAgentNotesService) List(context.Context, string, *string, int) (agentnotes.Page, error) {
	panic("unexpected agent notes list")
}

func (unusedAgentNotesService) Get(context.Context, string, string) (agentnotes.Note, bool, error) {
	panic("unexpected agent note read")
}

func (unusedAgentNotesService) Create(context.Context, string, agentnotes.CreateInput) (agentnotes.MutationResult, error) {
	panic("unexpected agent note create")
}

func (unusedAgentNotesService) Replace(context.Context, string, string, agentnotes.ReplaceInput) (agentnotes.MutationResult, error) {
	panic("unexpected agent note replace")
}

func (unusedAgentNotesService) Archive(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
	panic("unexpected agent note archive")
}

func (unusedAgentNotesService) Restore(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
	panic("unexpected agent note restore")
}

type unusedChatAgentService struct{}

func (unusedChatAgentService) CreateThread(context.Context, string) (chatagent.Thread, error) {
	panic("unexpected chat thread create")
}

func (unusedChatAgentService) ListThreads(context.Context, string, *string, int) (chatagent.ThreadPage, error) {
	panic("unexpected chat thread list")
}

func (unusedChatAgentService) ListMessages(context.Context, string, string, int64, int) ([]chatagent.Message, error) {
	panic("unexpected chat message list")
}

func (unusedChatAgentService) GetRun(context.Context, string, string) (chatagent.Run, bool, error) {
	panic("unexpected agent run read")
}

func (unusedChatAgentService) ReadRunEvents(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
	panic("unexpected agent run event list")
}

func (unusedChatAgentService) Enqueue(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
	panic("unexpected agent run enqueue")
}

func (unusedChatAgentService) EnqueueAutoAnalysis(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
	panic("unexpected automatic analysis enqueue")
}

type unusedOJService struct{}

func (unusedOJService) AuthorizeUpload(context.Context, string, oj.UploadKind) (oj.UploadAuthorization, error) {
	panic("unexpected OJ upload authorization")
}

func (unusedOJService) ListProblems(context.Context, string, *string, int, bool) (oj.ProblemPage, error) {
	panic("unexpected OJ problem list")
}

func (unusedOJService) GetProblem(context.Context, string, string) (oj.Problem, bool, error) {
	panic("unexpected OJ problem read")
}

func (unusedOJService) CreateProblemVersion(context.Context, oj.UploadAuthorization, oj.ProblemVersionMetadata, io.Reader) (oj.CreateProblemVersionResult, error) {
	panic("unexpected OJ problem version creation")
}

func (unusedOJService) CreateSubmission(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error) {
	panic("unexpected OJ submission creation")
}

func (unusedOJService) GetSubmission(context.Context, string, string) (oj.SubmissionDetail, bool, error) {
	panic("unexpected OJ submission read")
}

func (unusedOJService) ReadJudgeEvents(context.Context, string, string, int64, int) (oj.JudgeEventBatch, bool, error) {
	panic("unexpected OJ judge event read")
}

func testCapabilities(writesEnabled bool) Capabilities {
	return Capabilities{
		PintiaSnapshotSchema: pintia.SchemaV2,
		PintiaSchemaSHA256:   pintia.ExpectedSchemaSHA256,
		MaxUploadBytes:       64 << 20,
		MaxProblems:          1_000,
		MaxParticipants:      20_000,
		MaxSubmissions:       200_000,
		MaxCodeBytes:         1 << 20,
		WritesEnabled:        writesEnabled,
	}
}

func (readiness staticReadiness) Check(context.Context) health.Report {
	return readiness.report
}

func TestHealthAndVersionRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		readiness  health.Report
		wantStatus int
		wantBody   string
	}{
		{
			name:       "liveness",
			path:       "/livez",
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"alive"}` + "\n",
		},
		{
			name: "readiness success",
			path: "/readyz",
			readiness: health.Report{
				Status: health.StatusReady,
				Checks: map[string]health.Check{},
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ready","checks":{}}` + "\n",
		},
		{
			name: "readiness failure",
			path: "/readyz",
			readiness: health.Report{
				Status: health.StatusNotReady,
				Checks: map[string]health.Check{},
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"not_ready","checks":{}}` + "\n",
		},
		{
			name:       "version",
			path:       "/version",
			wantStatus: http.StatusOK,
			wantBody:   `{"version":"1.2.3","commit":"abc123","buildTime":"2026-07-10T00:00:00Z","goVersion":"go1.26","goos":"linux","goarch":"amd64","goamd64":"v1","goExperiment":"none","gofips140":"off","cgoEnabled":false}` + "\n",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := newTestHandler(test.readiness)
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := newTestResponseRecorder()

			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if response.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHealthRoutesRejectUnsupportedMethods(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(health.Report{Status: health.StatusReady})
	request := httptest.NewRequest(http.MethodPost, "/livez", nil)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestNewRejectsInvalidRequestLifetimeLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Options)
	}{
		{name: "auth body timeout", mutate: func(options *Options) { options.AuthBodyTimeout = 0 }},
		{name: "upload body timeout", mutate: func(options *Options) { options.UploadBodyTimeout = 0 }},
		{name: "SSE maximum duration", mutate: func(options *Options) { options.SSEMaxDuration = 0 }},
		{name: "SSE reauthorization interval", mutate: func(options *Options) { options.SSEReauthInterval = 0 }},
		{name: "SSE reauthorization equals maximum", mutate: func(options *Options) {
			options.SSEReauthInterval = options.SSEMaxDuration
		}},
		{name: "SSE reauthorization exceeds maximum", mutate: func(options *Options) {
			options.SSEReauthInterval = options.SSEMaxDuration + time.Second
		}},
		{name: "SSE write timeout", mutate: func(options *Options) { options.SSEWriteTimeout = 0 }},
		{name: "SSE write timeout exceeds reauthorization", mutate: func(options *Options) {
			options.SSEWriteTimeout = options.SSEReauthInterval + time.Second
		}},
		{name: "SSE capacity", mutate: func(options *Options) { options.MaxActiveSSE = 0 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := testHandlerOptions(health.Report{Status: health.StatusReady})
			test.mutate(&options)
			if _, err := New(options); err == nil {
				t.Fatal("New accepted invalid request lifetime limits")
			}
		})
	}
}

func newTestHandler(report health.Report) http.Handler {
	handler, err := New(testHandlerOptions(report))
	if err != nil {
		panic(err)
	}
	return handler
}

func testHandlerOptions(report health.Report) Options {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return Options{
		Readiness: staticReadiness{report: report},
		Version: version.Info{
			Version:      "1.2.3",
			Commit:       "abc123",
			BuildTime:    "2026-07-10T00:00:00Z",
			GoVersion:    "go1.26",
			GOOS:         "linux",
			GOARCH:       "amd64",
			GOAMD64:      "v1",
			GOExperiment: "none",
			GOFIPS140:    "off",
		},
		Logger:                    logger,
		Auth:                      unusedAuthService{},
		Enrollment:                unusedEnrollmentService{},
		AccountManagement:         unusedAccountManagementService{},
		AllowedOrigins:            []string{"https://ascendany.example"},
		RateLimiter:               allowAllRateLimiter{},
		RequestIDRandom:           bytes.NewReader([]byte("12345678")),
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
		ModelProbe:                unusedModelProbeService{},
		Capabilities:              testCapabilities(true),
		AuthBodyTimeout:           time.Second,
		UploadBodyTimeout:         time.Second,
		SSEMaxDuration:            time.Minute,
		SSEReauthInterval:         10 * time.Second,
		SSEWriteTimeout:           time.Second,
		MaxActiveSSE:              4,
	}
}
