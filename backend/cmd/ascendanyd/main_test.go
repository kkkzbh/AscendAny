package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os/user"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/config"
	configurationdomain "github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/examcatalog"
	"github.com/kkkzbh/AscendAny/backend/internal/examgeneration"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/httpapi"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspexecutor"
	"github.com/kkkzbh/AscendAny/backend/internal/modelprobe"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/runtimeapp"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

type constructionReadiness struct{}

func (constructionReadiness) Check(context.Context) health.Report {
	return health.Report{Status: health.StatusReady}
}

type constructionAuth struct{}

func (constructionAuth) Register(context.Context, auth.RegistrationInput) (auth.AuthResult, error) {
	return auth.AuthResult{}, nil
}

func (constructionAuth) Login(context.Context, auth.LoginInput) (auth.AuthResult, error) {
	panic("unused")
}

func (constructionAuth) ExchangeSSO(context.Context, auth.SSOExchangeInput) (auth.AuthResult, error) {
	panic("unused")
}

func (constructionAuth) Refresh(context.Context, auth.RefreshInput) (auth.AuthResult, error) {
	panic("unused")
}

func (constructionAuth) Logout(context.Context, auth.LogoutInput) error { panic("unused") }
func (constructionAuth) Me(context.Context, string) (auth.Account, error) {
	panic("unused")
}

func (constructionAuth) BootstrapLocalPassword(context.Context, auth.LocalPasswordBootstrapInput) error {
	panic("unused")
}

type constructionEnrollment struct{}

func (constructionEnrollment) IssueEnrollment(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
	panic("unused")
}

func (constructionEnrollment) RevokeEnrollment(context.Context, string, string) error {
	panic("unused")
}

func (constructionEnrollment) ClaimEnrollment(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
	panic("unused")
}

type constructionAccountManagement struct{}

func (constructionAccountManagement) UpdateProfile(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error) {
	panic("unused")
}

func (constructionAccountManagement) ListSessions(context.Context, string) ([]auth.ManagedSession, error) {
	panic("unused")
}

func (constructionAccountManagement) RevokeSession(context.Context, string, string) (bool, error) {
	panic("unused")
}

type constructionRateLimiter struct{}

func (constructionRateLimiter) Allow(string, string) httpapi.RateLimitDecision {
	return httpapi.RateLimitDecision{Allowed: true}
}

type constructionImportReader struct{}

func (constructionImportReader) ListJobs(context.Context, *string, int) (importing.JobPage, error) {
	return importing.JobPage{}, nil
}

func (constructionImportReader) GetJob(context.Context, string) (importing.PublicJob, bool, error) {
	panic("unused")
}

func (constructionImportReader) ReadEvents(context.Context, string, int64, int) (importing.EventBatch, bool, error) {
	panic("unused")
}

type constructionStudentAnalytics struct{}

func (constructionStudentAnalytics) GetSelf(context.Context, string, int) (studentanalytics.Result, error) {
	panic("unused")
}

func (constructionStudentAnalytics) GetLeaderboard(context.Context, string, int) (studentanalytics.LeaderboardResult, error) {
	panic("unused")
}

type constructionAchievement struct{}

func (constructionAchievement) GetSelf(context.Context, string) (achievement.Result, error) {
	panic("unused")
}

func (constructionAchievement) GetByStudentNumber(context.Context, string) (achievement.Result, error) {
	panic("unused")
}

func (constructionAchievement) GetByStudentIdentity(context.Context, string, string) (achievement.Result, error) {
	panic("unused")
}

type constructionExamCatalog struct{}

func (constructionExamCatalog) List(context.Context, string, *string, int) (examcatalog.Page, error) {
	panic("unused")
}

func (constructionExamCatalog) Get(context.Context, string, string) (examcatalog.Detail, bool, error) {
	panic("unused")
}

type constructionExamGeneration struct{}

func (constructionExamGeneration) GetCurrent(context.Context, string, string) (examgeneration.Generation, bool, error) {
	panic("unused")
}

func (constructionExamGeneration) ReadEvents(context.Context, string, string, string, int64, int) (examgeneration.EventBatch, bool, error) {
	panic("unused")
}

type constructionAdministration struct{}

func (constructionAdministration) ListAccounts(context.Context, string, *string, int) (administration.AccountPage, error) {
	panic("unused")
}

func (constructionAdministration) ListStudents(context.Context, string, *string, int) (administration.StudentPage, error) {
	panic("unused")
}

func (constructionAdministration) ListAudit(context.Context, string, *string, int) (administration.AuditPage, error) {
	panic("unused")
}

func (constructionAdministration) SetAccountDisabled(context.Context, string, string, bool) (administration.ManagedAccount, error) {
	panic("unused")
}

type constructionConfiguration struct{}

func (constructionConfiguration) List(context.Context, string, *configurationdomain.Kind, *string, int) (configurationdomain.ItemPage, error) {
	panic("unused")
}

func (constructionConfiguration) Get(context.Context, string, string) (configurationdomain.Item, bool, error) {
	panic("unused")
}

func (constructionConfiguration) ListVersions(context.Context, string, string, *int64, int) (configurationdomain.VersionPage, bool, error) {
	panic("unused")
}

func (constructionConfiguration) CreateVersion(context.Context, string, configurationdomain.CreateVersionInput) (configurationdomain.CreateVersionResult, error) {
	panic("unused")
}

func (constructionConfiguration) AuthorizeKnowledgeCatalogPublication(context.Context, string, configurationdomain.CatalogPublicationAuthorizationInput) (configurationdomain.CatalogPublicationAuthorizationResult, error) {
	panic("unused")
}

type constructionFeedback struct{}

func (constructionFeedback) SubmitAuthenticated(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error) {
	panic("unused")
}

type constructionAgentNotes struct{}

func (constructionAgentNotes) List(context.Context, string, *string, int) (agentnotes.Page, error) {
	panic("unused")
}

func (constructionAgentNotes) Get(context.Context, string, string) (agentnotes.Note, bool, error) {
	panic("unused")
}

func (constructionAgentNotes) Create(context.Context, string, agentnotes.CreateInput) (agentnotes.MutationResult, error) {
	panic("unused")
}

func (constructionAgentNotes) Replace(context.Context, string, string, agentnotes.ReplaceInput) (agentnotes.MutationResult, error) {
	panic("unused")
}

func (constructionAgentNotes) Archive(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
	panic("unused")
}

func (constructionAgentNotes) Restore(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
	panic("unused")
}

type constructionChatAgent struct{}

func (constructionChatAgent) CreateThread(context.Context, string) (chatagent.Thread, error) {
	panic("unused")
}

func (constructionChatAgent) ListThreads(context.Context, string, *string, int) (chatagent.ThreadPage, error) {
	panic("unused")
}

func (constructionChatAgent) ListMessages(context.Context, string, string, int64, int) ([]chatagent.Message, error) {
	panic("unused")
}

func (constructionChatAgent) GetRun(context.Context, string, string) (chatagent.Run, bool, error) {
	panic("unused")
}

func (constructionChatAgent) ReadRunEvents(context.Context, string, string, int64, int) (chatagent.RunEventBatch, error) {
	panic("unused")
}

func (constructionChatAgent) Enqueue(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
	panic("unused")
}

func (constructionChatAgent) EnqueueAutoAnalysis(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
	panic("unused")
}

type constructionOJ struct{}

func (constructionOJ) AuthorizeUpload(context.Context, string, oj.UploadKind) (oj.UploadAuthorization, error) {
	panic("unused")
}

func (constructionOJ) ListProblems(context.Context, string, *string, int, bool) (oj.ProblemPage, error) {
	panic("unused")
}

func (constructionOJ) GetProblem(context.Context, string, string) (oj.Problem, bool, error) {
	panic("unused")
}

func (constructionOJ) CreateProblemVersion(context.Context, oj.UploadAuthorization, oj.ProblemVersionMetadata, io.Reader) (oj.CreateProblemVersionResult, error) {
	panic("unused")
}

func (constructionOJ) CreateSubmission(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error) {
	panic("unused")
}

func (constructionOJ) GetSubmission(context.Context, string, string) (oj.SubmissionDetail, bool, error) {
	panic("unused")
}

func (constructionOJ) ReadJudgeEvents(context.Context, string, string, int64, int) (oj.JudgeEventBatch, bool, error) {
	panic("unused")
}

type constructionRecommendationReader struct{}

func (constructionRecommendationReader) ReadCurrent(context.Context, string) (recommendation.CurrentRecommendation, error) {
	panic("unused")
}

type constructionRecommendationAdminReader struct{}

func (constructionRecommendationAdminReader) ReadReviewContext(context.Context, string) (recommendation.ReviewContext, error) {
	return recommendation.ReviewContext{}, nil
}

func TestValidateCommand(t *testing.T) {
	t.Parallel()

	if err := validateCommand([]string{"serve"}); err != nil {
		t.Fatalf("validateCommand(serve) error = %v", err)
	}
	if err := validateCommand([]string{"activate-model"}); err != nil {
		t.Fatalf("validateCommand(activate-model) error = %v", err)
	}
	if err := validateCommand([]string{"register-model"}); err != nil {
		t.Fatalf("validateCommand(register-model) error = %v", err)
	}
	for _, args := range [][]string{nil, {}, {"--config", "config.toml"}, {"unknown"}, {"serve", "extra"}} {
		if err := validateCommand(args); err == nil {
			t.Fatalf("validateCommand(%q) error = nil", args)
		}
	}
}

func TestResolveSystemUserUID(t *testing.T) {
	t.Parallel()

	uid, err := resolveSystemUserUID("ascendany-lsp", func(name string) (*user.User, error) {
		return &user.User{Username: name, Uid: "991"}, nil
	})
	if err != nil || uid != 991 {
		t.Fatalf("resolveSystemUserUID() = %d, %v", uid, err)
	}
	for name, account := range map[string]*user.User{
		"root-user":    {Username: "root-user", Uid: "0"},
		"wrong-user":   {Username: "different", Uid: "991"},
		"invalid-user": {Username: "invalid-user", Uid: "not-a-number"},
	} {
		name, account := name, account
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := resolveSystemUserUID(name, func(string) (*user.User, error) { return account, nil }); err == nil {
				t.Fatal("resolveSystemUserUID() error = nil")
			}
		})
	}
}

func TestProductionHTTPOptionsConstructHandler(t *testing.T) {
	t.Parallel()

	configuration := config.Config{
		HTTP: config.HTTPConfig{
			AuthBodyTimeout:   15 * time.Second,
			UploadBodyTimeout: 10 * time.Minute,
			SSEMaxDuration:    15 * time.Minute,
			SSEReauthInterval: 15 * time.Second,
			SSEWriteTimeout:   5 * time.Second,
			MaxActiveSSE:      64,
		},
		Auth: config.AuthConfig{
			AllowedOrigins: []string{"https://ascendany.example"},
			AccessTTL:      15 * time.Minute,
			RefreshTTL:     24 * time.Hour,
		},
		Artifact: config.ArtifactConfig{
			MaxBytes: 64 << 20,
		},
		Pintia: config.PintiaConfig{
			MaxProblems:     1_000,
			MaxParticipants: 20_000,
			MaxSubmissions:  200_000,
			MaxCodeBytes:    1 << 20,
		},
		Write: config.WriteConfig{Enabled: false},
	}
	dependencies := httpRuntimeDependencies{
		readiness:                 constructionReadiness{},
		logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
		auth:                      constructionAuth{},
		enrollment:                constructionEnrollment{},
		accountManagement:         constructionAccountManagement{},
		rateLimiter:               constructionRateLimiter{},
		requestIDRandom:           bytes.NewReader([]byte("12345678")),
		importReader:              constructionImportReader{},
		studentAnalytics:          constructionStudentAnalytics{},
		achievement:               constructionAchievement{},
		examCatalog:               constructionExamCatalog{},
		examGeneration:            constructionExamGeneration{},
		administration:            constructionAdministration{},
		configuration:             constructionConfiguration{},
		feedback:                  constructionFeedback{},
		agentNotes:                constructionAgentNotes{},
		chatAgent:                 constructionChatAgent{},
		oj:                        constructionOJ{},
		ojPolicy:                  oj.DefaultPolicy(),
		recommendationReader:      constructionRecommendationReader{},
		recommendationAdminReader: constructionRecommendationAdminReader{},
	}
	dependencies, err := bindWriteHTTPRuntimeDependencies(dependencies, false, writeHTTPRuntime{})
	if err != nil {
		t.Fatalf("bindWriteHTTPRuntimeDependencies(disabled) error = %v", err)
	}
	if dependencies.artifacts != nil || dependencies.imports != nil || dependencies.lsp != nil || dependencies.modelProbe != nil {
		t.Fatal("disabled write dependency binding produced a typed-nil HTTP capability")
	}
	options := buildHTTPHandlerOptions(configuration, dependencies)
	if _, err := httpapi.New(options); err != nil {
		t.Fatalf("httpapi.New(production options) error = %v", err)
	}
	if _, err := bindWriteHTTPRuntimeDependencies(dependencies, false, writeHTTPRuntime{
		lspPolicy: lsp.DefaultPolicy(),
	}); err == nil {
		t.Fatal("disabled write dependency binding accepted an LSP policy")
	}
}

func TestAgentV1EnvelopeKeyDerivationIsStableAndDomainSeparated(t *testing.T) {
	t.Parallel()

	first := deriveAgentV1EnvelopeKey([]byte("signing-key-a"))
	again := deriveAgentV1EnvelopeKey([]byte("signing-key-a"))
	second := deriveAgentV1EnvelopeKey([]byte("signing-key-b"))
	if len(first) != 32 || !bytes.Equal(first, again) || bytes.Equal(first, second) ||
		bytes.Equal(first, []byte("signing-key-a")) {
		t.Fatalf("derived keys are not stable, separated SHA-256 values")
	}
}

func TestEnabledWriteHTTPDependenciesRequireAndBindCompleteCapability(t *testing.T) {
	t.Parallel()

	if _, err := bindWriteHTTPRuntimeDependencies(httpRuntimeDependencies{}, true, writeHTTPRuntime{}); err == nil {
		t.Fatal("enabled write dependency binding accepted an empty runtime")
	}
	dependencies, err := bindWriteHTTPRuntimeDependencies(httpRuntimeDependencies{}, true, writeHTTPRuntime{
		components: &runtimeapp.Components{
			Artifacts: &artifact.Store{},
			Imports:   &importing.Service{},
		},
		lspManager: &lspexecutor.Manager{},
		lspPolicy:  lsp.DefaultPolicy(),
		modelProbe: &modelprobe.Service{},
	})
	if err != nil {
		t.Fatalf("bindWriteHTTPRuntimeDependencies(enabled) error = %v", err)
	}
	if dependencies.artifacts == nil || dependencies.imports == nil || dependencies.lsp == nil || dependencies.modelProbe == nil {
		t.Fatal("enabled write dependency binding omitted a capability")
	}
}
