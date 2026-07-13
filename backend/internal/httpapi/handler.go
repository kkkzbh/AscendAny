package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/browserorigin"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/examcatalog"
	"github.com/kkkzbh/AscendAny/backend/internal/examgeneration"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/modelprobe"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

type ReadinessChecker interface {
	Check(context.Context) health.Report
}

type AuthService interface {
	Login(context.Context, auth.LoginInput) (auth.AuthResult, error)
	Refresh(context.Context, auth.RefreshInput) (auth.AuthResult, error)
	Logout(context.Context, auth.LogoutInput) error
	Me(context.Context, string) (auth.Account, error)
}

type EnrollmentService interface {
	IssueEnrollment(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error)
	RevokeEnrollment(context.Context, string, string) error
	ClaimEnrollment(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error)
}

type ArtifactPublisher interface {
	Publish(context.Context, io.Reader) (*artifact.Publication, error)
}

type ImportQueue interface {
	QueuePublication(context.Context, *artifact.Publication, string) (importing.QueueResult, error)
}

type ImportReader interface {
	ListJobs(context.Context, *string, int) (importing.JobPage, error)
	GetJob(context.Context, string) (importing.PublicJob, bool, error)
	ReadEvents(context.Context, string, int64, int) (importing.EventBatch, bool, error)
}

type StudentAnalyticsService interface {
	GetSelf(context.Context, string, int) (studentanalytics.Result, error)
	GetLeaderboard(context.Context, string, int) (studentanalytics.LeaderboardResult, error)
}

type AccountManagementService interface {
	UpdateProfile(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error)
	ListSessions(context.Context, string) ([]auth.ManagedSession, error)
	RevokeSession(context.Context, string, string) (bool, error)
}

type ExamCatalogService interface {
	List(context.Context, string, *string, int) (examcatalog.Page, error)
	Get(context.Context, string, string) (examcatalog.Detail, bool, error)
}

type ExamGenerationService interface {
	GetCurrent(context.Context, string, string) (examgeneration.Generation, bool, error)
	ReadEvents(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error)
}

type AdministrationService interface {
	ListAccounts(context.Context, string, *string, int) (administration.AccountPage, error)
	ListStudents(context.Context, string, *string, int) (administration.StudentPage, error)
	ListAudit(context.Context, string, *string, int) (administration.AuditPage, error)
	SetAccountDisabled(context.Context, string, string, bool) (administration.ManagedAccount, error)
}

type ConfigurationService interface {
	List(context.Context, string, *configuration.Kind, *string, int) (configuration.ItemPage, error)
	Get(context.Context, string, string) (configuration.Item, bool, error)
	ListVersions(context.Context, string, string, *int64, int) (configuration.VersionPage, bool, error)
	CreateVersion(context.Context, string, configuration.CreateVersionInput) (configuration.CreateVersionResult, error)
}

type ModelProbeService interface {
	Test(context.Context, string, string) (modelprobe.Result, error)
}

type FeedbackService interface {
	SubmitAuthenticated(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error)
}

type AgentNotesService interface {
	List(context.Context, string, *string, int) (agentnotes.Page, error)
	Get(context.Context, string, string) (agentnotes.Note, bool, error)
	Create(context.Context, string, agentnotes.CreateInput) (agentnotes.MutationResult, error)
	Replace(context.Context, string, string, agentnotes.ReplaceInput) (agentnotes.MutationResult, error)
	Archive(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error)
	Restore(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error)
}

type ChatAgentService interface {
	CreateThread(context.Context, string) (chatagent.Thread, error)
	ListThreads(context.Context, string, *string, int) (chatagent.ThreadPage, error)
	ListMessages(context.Context, string, string, int64, int) ([]chatagent.Message, error)
	GetRun(context.Context, string, string) (chatagent.Run, bool, error)
	ReadRunEvents(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error)
	Enqueue(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error)
	EnqueueAutoAnalysis(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error)
}

type OJService interface {
	AuthorizeUpload(context.Context, string, oj.UploadKind) (oj.UploadAuthorization, error)
	ListProblems(context.Context, string, *string, int, bool) (oj.ProblemPage, error)
	GetProblem(context.Context, string, string) (oj.Problem, bool, error)
	CreateProblemVersion(context.Context, oj.UploadAuthorization, oj.ProblemVersionMetadata, io.Reader) (oj.CreateProblemVersionResult, error)
	CreateSubmission(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error)
	GetSubmission(context.Context, string, string) (oj.SubmissionDetail, bool, error)
	ReadJudgeEvents(context.Context, string, string, int64, int) (oj.JudgeEventBatch, bool, error)
}

type Capabilities struct {
	PintiaSnapshotSchema string `json:"pintiaSnapshotSchema"`
	PintiaSchemaSHA256   string `json:"pintiaSchemaSha256"`
	MaxUploadBytes       int64  `json:"maxUploadBytes"`
	MaxProblems          int    `json:"maxProblems"`
	MaxParticipants      int    `json:"maxParticipants"`
	MaxSubmissions       int    `json:"maxSubmissions"`
	MaxCodeBytes         int    `json:"maxCodeBytes"`
	WritesEnabled        bool   `json:"writesEnabled"`
}

type Options struct {
	Readiness                 ReadinessChecker
	Version                   version.Info
	Logger                    *slog.Logger
	Auth                      AuthService
	Enrollment                EnrollmentService
	AccountManagement         AccountManagementService
	AllowedOrigins            []string
	RateLimiter               RequestRateLimiter
	RequestIDRandom           io.Reader
	TrustedProxyCIDRs         []netip.Prefix
	ClientIPHeader            string
	Artifacts                 ArtifactPublisher
	Imports                   ImportQueue
	ImportReader              ImportReader
	StudentAnalytics          StudentAnalyticsService
	Achievement               AchievementService
	ExamCatalog               ExamCatalogService
	ExamGeneration            ExamGenerationService
	Administration            AdministrationService
	Configuration             ConfigurationService
	Feedback                  FeedbackService
	AgentNotes                AgentNotesService
	ChatAgent                 ChatAgentService
	OJ                        OJService
	OJPolicy                  oj.Policy
	RecommendationReader      RecommendationReader
	RecommendationAdminReader RecommendationAdminReader
	ModelProbe                ModelProbeService
	LSP                       LSPService
	LSPPolicy                 lsp.Policy
	Capabilities              Capabilities
	AuthBodyTimeout           time.Duration
	UploadBodyTimeout         time.Duration
	SSEMaxDuration            time.Duration
	SSEReauthInterval         time.Duration
	SSEWriteTimeout           time.Duration
	MaxActiveSSE              int
}

type Handler struct {
	readiness                 ReadinessChecker
	version                   version.Info
	logger                    *slog.Logger
	auth                      AuthService
	enrollment                EnrollmentService
	accountManagement         AccountManagementService
	allowedOrigins            map[string]struct{}
	rateLimiter               RequestRateLimiter
	requestIDs                *requestIDGenerator
	routes                    *routeRegistry
	clientAddress             clientAddressResolver
	artifacts                 ArtifactPublisher
	imports                   ImportQueue
	importReader              ImportReader
	studentAnalytics          StudentAnalyticsService
	achievement               AchievementService
	examCatalog               ExamCatalogService
	examGeneration            ExamGenerationService
	administration            AdministrationService
	configuration             ConfigurationService
	feedback                  FeedbackService
	agentNotes                AgentNotesService
	chatAgent                 ChatAgentService
	oj                        OJService
	ojPolicy                  oj.Policy
	recommendationReader      RecommendationReader
	recommendationAdminReader RecommendationAdminReader
	modelProbe                ModelProbeService
	lspService                LSPService
	lspPolicy                 lsp.Policy
	capabilities              Capabilities
	sseMaxDuration            time.Duration
	sseReauthInterval         time.Duration
	sseWriteTimeout           time.Duration
	sseSlots                  chan struct{}
}

type requestIDContextKey struct{}

type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

func New(options Options) (http.Handler, error) {
	if options.Readiness == nil || options.Logger == nil || options.Auth == nil || options.Enrollment == nil ||
		options.AccountManagement == nil ||
		options.RateLimiter == nil || options.StudentAnalytics == nil || options.Achievement == nil || options.ExamCatalog == nil || options.ExamGeneration == nil || options.Administration == nil ||
		options.Configuration == nil || options.Feedback == nil || options.AgentNotes == nil || options.ChatAgent == nil || options.OJ == nil ||
		options.RecommendationReader == nil || options.RecommendationAdminReader == nil {
		return nil, fmt.Errorf("HTTP API requires readiness, logger, auth, enrollment, account management, rate limiter, student analytics, achievement, exam catalog, exam generation, administration, configuration, feedback, agent notes, chat agent, OJ, recommendation reader, and recommendation admin reader dependencies")
	}
	if !oj.ValidPolicy(options.OJPolicy) {
		return nil, fmt.Errorf("HTTP API requires one valid OJ policy shared with the application service")
	}
	if options.LSP == nil {
		if options.LSPPolicy != (lsp.Policy{}) {
			return nil, fmt.Errorf("absent LSP transport cannot receive an LSP policy")
		}
	} else {
		if !options.Capabilities.WritesEnabled {
			return nil, fmt.Errorf("disabled writes cannot receive an LSP transport")
		}
		if !lsp.ValidPolicy(options.LSPPolicy) {
			return nil, fmt.Errorf("enabled LSP transport requires one valid policy shared with the session manager")
		}
	}
	canonicalOrigins, err := browserorigin.Canonicalize(options.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	allowedOrigins := make(map[string]struct{}, len(canonicalOrigins))
	for _, origin := range canonicalOrigins {
		allowedOrigins[origin] = struct{}{}
	}
	requestIDs, err := newRequestIDGenerator(options.RequestIDRandom)
	if err != nil {
		return nil, err
	}
	clientAddress, err := newClientAddressResolver(options.TrustedProxyCIDRs, options.ClientIPHeader)
	if err != nil {
		return nil, err
	}
	if err := validateCapabilities(options.Capabilities); err != nil {
		return nil, err
	}
	if options.ImportReader == nil {
		return nil, fmt.Errorf("HTTP API requires the import reader dependency")
	}
	if options.AuthBodyTimeout <= 0 || options.UploadBodyTimeout <= 0 || options.SSEMaxDuration <= 0 ||
		options.SSEReauthInterval <= 0 || options.SSEReauthInterval >= options.SSEMaxDuration || options.MaxActiveSSE < 1 {
		return nil, fmt.Errorf("HTTP API request lifetime limits are invalid")
	}
	if options.SSEWriteTimeout <= 0 || options.SSEWriteTimeout > options.SSEReauthInterval {
		return nil, fmt.Errorf("HTTP API SSE write timeout is invalid")
	}
	if options.Capabilities.WritesEnabled {
		if options.Artifacts == nil || options.Imports == nil || options.ModelProbe == nil {
			return nil, fmt.Errorf("enabled writes require artifact, import queue, and model probe dependencies")
		}
	} else if options.Artifacts != nil || options.Imports != nil || options.ModelProbe != nil {
		return nil, fmt.Errorf("disabled writes cannot receive artifact, import queue, or model probe dependencies")
	}
	handler := &Handler{
		readiness:                 options.Readiness,
		version:                   options.Version,
		logger:                    options.Logger,
		auth:                      options.Auth,
		enrollment:                options.Enrollment,
		accountManagement:         options.AccountManagement,
		allowedOrigins:            allowedOrigins,
		rateLimiter:               options.RateLimiter,
		requestIDs:                requestIDs,
		clientAddress:             clientAddress,
		artifacts:                 options.Artifacts,
		imports:                   options.Imports,
		importReader:              options.ImportReader,
		studentAnalytics:          options.StudentAnalytics,
		achievement:               options.Achievement,
		examCatalog:               options.ExamCatalog,
		examGeneration:            options.ExamGeneration,
		administration:            options.Administration,
		configuration:             options.Configuration,
		feedback:                  options.Feedback,
		agentNotes:                options.AgentNotes,
		chatAgent:                 options.ChatAgent,
		oj:                        options.OJ,
		ojPolicy:                  options.OJPolicy,
		recommendationReader:      options.RecommendationReader,
		recommendationAdminReader: options.RecommendationAdminReader,
		modelProbe:                options.ModelProbe,
		lspService:                options.LSP,
		lspPolicy:                 options.LSPPolicy,
		capabilities:              options.Capabilities,
		sseMaxDuration:            options.SSEMaxDuration,
		sseReauthInterval:         options.SSEReauthInterval,
		sseWriteTimeout:           options.SSEWriteTimeout,
		sseSlots:                  make(chan struct{}, options.MaxActiveSSE),
	}

	routes, err := newRouteRegistry(apiRouteContracts(handler, options.AuthBodyTimeout, options.UploadBodyTimeout))
	if err != nil {
		return nil, err
	}
	handler.routes = routes
	mux := http.NewServeMux()
	routes.register(mux)

	return handler.withRequestID(handler.requestLogger(handler.writeCapabilityBoundary(handler.browserBoundary(mux)))), nil
}

func validateCapabilities(value Capabilities) error {
	if value.PintiaSnapshotSchema != pintia.SchemaV2 || value.PintiaSchemaSHA256 != pintia.ExpectedSchemaSHA256 {
		return fmt.Errorf("HTTP API capabilities must use the compiled Pintia v2 contract")
	}
	if value.MaxUploadBytes <= 0 || value.MaxProblems <= 0 || value.MaxParticipants <= 0 ||
		value.MaxSubmissions <= 0 || value.MaxCodeBytes <= 0 || int64(value.MaxCodeBytes) > value.MaxUploadBytes {
		return fmt.Errorf("HTTP API capabilities contain invalid upload limits")
	}
	return nil
}

func (handler *Handler) livez(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "alive"})
}

func (handler *Handler) readyz(writer http.ResponseWriter, request *http.Request) {
	report := handler.readiness.Check(request.Context())
	status := http.StatusOK
	if report.Status != health.StatusReady {
		status = http.StatusServiceUnavailable
	}
	writeJSON(writer, status, report)
}

func (handler *Handler) buildVersion(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, handler.version)
}

func (handler *Handler) getCapabilities(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, handler.capabilities)
}

func (handler *Handler) apiNotFound(writer http.ResponseWriter, request *http.Request) {
	handler.writeAPIError(writer, request, http.StatusNotFound, "route_not_found", "API route does not exist.")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (handler *Handler) writeAPIError(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	message string,
) {
	abortUnreadRequestBody(writer, request)
	writeJSON(writer, status, APIError{
		Code:      code,
		Message:   message,
		RequestID: requestID(request.Context()),
	})
}

func (handler *Handler) writeAPIErrorDetails(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	message string,
	details map[string]any,
) {
	abortUnreadRequestBody(writer, request)
	writeJSON(writer, status, APIError{Code: code, Message: message, RequestID: requestID(request.Context()), Details: details})
}

func (handler *Handler) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		identifier := handler.requestIDs.Next()
		request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, identifier))
		writer.Header().Set("X-Request-ID", identifier)
		next.ServeHTTP(writer, request)
	})
}

func requestID(ctx context.Context) string {
	identifier, _ := ctx.Value(requestIDContextKey{}).(string)
	return identifier
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	written, err := recorder.ResponseWriter.Write(body)
	recorder.bytes += written
	return written, err
}

func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func (handler *Handler) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		handler.logger.InfoContext(request.Context(), "http request completed",
			"request_id", requestID(request.Context()),
			"method", request.Method,
			"route", request.Pattern,
			"status", status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}
