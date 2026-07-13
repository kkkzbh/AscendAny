package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

type writeBoundaryRecorder struct {
	mu    sync.Mutex
	calls map[string]int
}

func newWriteBoundaryRecorder() *writeBoundaryRecorder {
	return &writeBoundaryRecorder{calls: make(map[string]int)}
}

func (recorder *writeBoundaryRecorder) record(service string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.calls[service]++
}

func (recorder *writeBoundaryRecorder) total() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	total := 0
	for _, calls := range recorder.calls {
		total += calls
	}
	return total
}

type writeBoundaryAuthSpy struct {
	unusedAuthService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryAuthSpy) Login(context.Context, auth.LoginInput) (auth.AuthResult, error) {
	spy.recorder.record("auth")
	return auth.AuthResult{}, nil
}

func (spy writeBoundaryAuthSpy) Refresh(context.Context, auth.RefreshInput) (auth.AuthResult, error) {
	spy.recorder.record("auth")
	return auth.AuthResult{}, nil
}

func (spy writeBoundaryAuthSpy) Logout(context.Context, auth.LogoutInput) error {
	spy.recorder.record("auth")
	return nil
}

type writeBoundaryEnrollmentSpy struct {
	unusedEnrollmentService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryEnrollmentSpy) IssueEnrollment(context.Context, string, auth.EnrollmentIssueInput) (auth.IssuedEnrollment, error) {
	spy.recorder.record("enrollment")
	return auth.IssuedEnrollment{}, nil
}

func (spy writeBoundaryEnrollmentSpy) RevokeEnrollment(context.Context, string, string) error {
	spy.recorder.record("enrollment")
	return nil
}

func (spy writeBoundaryEnrollmentSpy) ClaimEnrollment(context.Context, auth.EnrollmentClaimInput) (auth.AuthResult, error) {
	spy.recorder.record("enrollment")
	return auth.AuthResult{}, nil
}

type writeBoundaryAccountSpy struct {
	unusedAccountManagementService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryAccountSpy) UpdateProfile(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error) {
	spy.recorder.record("account")
	return auth.Account{}, nil
}

func (spy writeBoundaryAccountSpy) RevokeSession(context.Context, string, string) (bool, error) {
	spy.recorder.record("account")
	return false, nil
}

type writeBoundaryAdministrationSpy struct {
	unusedAdministrationService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryAdministrationSpy) SetAccountDisabled(context.Context, string, string, bool) (administration.ManagedAccount, error) {
	spy.recorder.record("administration")
	return administration.ManagedAccount{}, nil
}

type writeBoundaryConfigurationSpy struct {
	unusedConfigurationService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryConfigurationSpy) CreateVersion(context.Context, string, configuration.CreateVersionInput) (configuration.CreateVersionResult, error) {
	spy.recorder.record("configuration")
	return configuration.CreateVersionResult{}, nil
}

type writeBoundaryFeedbackSpy struct {
	unusedFeedbackService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryFeedbackSpy) SubmitAuthenticated(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error) {
	spy.recorder.record("feedback")
	return feedback.SubmitResult{}, nil
}

type writeBoundaryNotesSpy struct {
	unusedAgentNotesService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryNotesSpy) Create(context.Context, string, agentnotes.CreateInput) (agentnotes.MutationResult, error) {
	spy.recorder.record("agent-notes")
	return agentnotes.MutationResult{}, nil
}

func (spy writeBoundaryNotesSpy) Replace(context.Context, string, string, agentnotes.ReplaceInput) (agentnotes.MutationResult, error) {
	spy.recorder.record("agent-notes")
	return agentnotes.MutationResult{}, nil
}

func (spy writeBoundaryNotesSpy) Archive(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
	spy.recorder.record("agent-notes")
	return agentnotes.MutationResult{}, nil
}

func (spy writeBoundaryNotesSpy) Restore(context.Context, string, string, agentnotes.StateInput) (agentnotes.MutationResult, error) {
	spy.recorder.record("agent-notes")
	return agentnotes.MutationResult{}, nil
}

type writeBoundaryChatSpy struct {
	unusedChatAgentService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryChatSpy) CreateThread(context.Context, string) (chatagent.Thread, error) {
	spy.recorder.record("chat-agent")
	return chatagent.Thread{}, nil
}

func (spy writeBoundaryChatSpy) Enqueue(context.Context, string, string, chatagent.EnqueueRequest) (chatagent.EnqueueResult, error) {
	spy.recorder.record("chat-agent")
	return chatagent.EnqueueResult{}, nil
}

func (spy writeBoundaryChatSpy) EnqueueAutoAnalysis(context.Context, string, chatagent.AutoAnalysisRequest) (chatagent.EnqueueResult, error) {
	spy.recorder.record("chat-agent")
	return chatagent.EnqueueResult{}, nil
}

type writeBoundaryOJSpy struct {
	unusedOJService
	recorder *writeBoundaryRecorder
}

func (spy writeBoundaryOJSpy) AuthorizeUpload(context.Context, string, oj.UploadKind) (oj.UploadAuthorization, error) {
	spy.recorder.record("oj")
	return oj.UploadAuthorization{}, nil
}

func (spy writeBoundaryOJSpy) CreateProblemVersion(context.Context, oj.UploadAuthorization, oj.ProblemVersionMetadata, io.Reader) (oj.CreateProblemVersionResult, error) {
	spy.recorder.record("oj")
	return oj.CreateProblemVersionResult{}, nil
}

func (spy writeBoundaryOJSpy) CreateSubmission(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error) {
	spy.recorder.record("oj")
	return oj.CreateSubmissionResult{}, nil
}

type writeRouteExample struct {
	method  string
	path    string
	service string
}

var writeRouteExamples = []writeRouteExample{
	{http.MethodPost, "/api/v2/auth/login", "auth"},
	{http.MethodPost, "/api/v2/auth/refresh", "auth"},
	{http.MethodPost, "/api/v2/auth/logout", "auth"},
	{http.MethodPost, "/api/v2/auth/enrollment-claims/consume", "enrollment"},
	{http.MethodPatch, "/api/v2/account/profile", "account"},
	{http.MethodDelete, "/api/v2/account/sessions/123e4567-e89b-42d3-a456-426614174020", "account"},
	{http.MethodPost, "/api/v2/admin/enrollment-claims", "enrollment"},
	{http.MethodDelete, "/api/v2/admin/enrollment-claims/123e4567-e89b-42d3-a456-426614174021", "enrollment"},
	{http.MethodPost, "/api/v2/students/me/notes", "agent-notes"},
	{http.MethodPut, "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022/document", "agent-notes"},
	{http.MethodPost, "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022/archive", "agent-notes"},
	{http.MethodPost, "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022/restore", "agent-notes"},
	{http.MethodPost, "/api/v2/students/me/chat/threads", "chat-agent"},
	{http.MethodPost, "/api/v2/students/me/chat/threads/123e4567-e89b-42d3-a456-426614174023/runs", "chat-agent"},
	{http.MethodPost, "/api/v2/students/me/auto-analysis", "chat-agent"},
	{http.MethodPost, "/api/v2/admin/oj/problems/versions", "oj"},
	{http.MethodPost, "/api/v2/oj/submissions", "oj"},
	{http.MethodPost, "/api/v2/lsp/sessions", "lsp"},
	{http.MethodDelete, "/api/v2/lsp/sessions/123e4567-e89b-42d3-a456-426614174027", "lsp"},
	{http.MethodGet, "/api/v2/lsp/sessions/123e4567-e89b-42d3-a456-426614174027/websocket", "lsp"},
	{http.MethodPatch, "/api/v2/admin/accounts/123e4567-e89b-42d3-a456-426614174029/state", "administration"},
	{http.MethodPost, "/api/v2/admin/configurations/versions", "configuration"},
	{http.MethodPost, "/api/v2/admin/model-connections/chat.primary/test", "model-probe"},
	{http.MethodPost, "/api/v2/feedback", "feedback"},
	{http.MethodPost, "/api/v2/imports/pintia", "import"},
}

func TestWriteDisabledRejectsEveryRegisteredWriteRouteBeforeBusinessHandlers(t *testing.T) {
	recorder := newWriteBoundaryRecorder()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = writeBoundaryAuthSpy{recorder: recorder}
	options.Enrollment = writeBoundaryEnrollmentSpy{recorder: recorder}
	options.AccountManagement = writeBoundaryAccountSpy{recorder: recorder}
	options.Administration = writeBoundaryAdministrationSpy{recorder: recorder}
	options.Configuration = writeBoundaryConfigurationSpy{recorder: recorder}
	options.Feedback = writeBoundaryFeedbackSpy{recorder: recorder}
	options.AgentNotes = writeBoundaryNotesSpy{recorder: recorder}
	options.ChatAgent = writeBoundaryChatSpy{recorder: recorder}
	options.OJ = writeBoundaryOJSpy{recorder: recorder}
	options.Artifacts = nil
	options.Imports = nil
	options.ModelProbe = nil
	options.Capabilities = testCapabilities(false)
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	for _, example := range writeRouteExamples {
		example := example
		t.Run(example.method+" "+example.path+" ("+example.service+")", func(t *testing.T) {
			request := httptest.NewRequest(example.method, example.path, strings.NewReader("invalid payload that must remain unread"))
			request.RemoteAddr = "192.0.2.10:43100"
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
			var apiError APIError
			if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
				t.Fatalf("decode API error: %v; body=%s", err, response.Body.String())
			}
			if apiError.Code != "writes_disabled" {
				t.Fatalf("error code = %q, want writes_disabled", apiError.Code)
			}
			if calls := recorder.total(); calls != 0 {
				t.Fatalf("business service calls = %d, want zero", calls)
			}
		})
	}
}

func TestWriteRouteExamplesExactlyCoverTheAuthoritativeContract(t *testing.T) {
	registry, err := newRouteRegistry(apiRouteContracts(&Handler{}, time.Second, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0)
	for _, contract := range registry.contracts {
		if contract.policy != nil && contract.policy.requiresWrites {
			actual = append(actual, contract.method+" "+contract.examplePath)
		}
	}
	expected := make([]string, 0, len(writeRouteExamples))
	for _, example := range writeRouteExamples {
		expected = append(expected, example.method+" "+example.path)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("write route examples differ from route contract\nactual:\n%s\nexpected:\n%s", strings.Join(actual, "\n"), strings.Join(expected, "\n"))
	}
}

func TestRoutePolicyLookupDoesNotAssignUnknownMethodsAnArbitraryPolicy(t *testing.T) {
	registry, err := newRouteRegistry(apiRouteContracts(&Handler{}, time.Second, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if policy, known := registry.policyForMethod("/api/v2/students/me/notes", http.MethodDelete); known {
		t.Fatalf("DELETE unexpectedly matched policy %#v", policy)
	}
	methods, known := registry.methodsForPath("/api/v2/students/me/notes")
	if !known || strings.Join(methods, ",") != "GET,POST" {
		t.Fatalf("methods = %v, known = %t", methods, known)
	}
	methods, known = registry.methodsForPath("/api/v2/admin/configurations/versions")
	if !known || strings.Join(methods, ",") != "GET,POST" {
		t.Fatalf("overlapping static/dynamic methods = %v, known = %t", methods, known)
	}
}

func TestWriteEnabledRouteStillReachesItsBusinessService(t *testing.T) {
	recorder := newWriteBoundaryRecorder()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = writeBoundaryAuthSpy{recorder: recorder}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(`{"username":"student","password":"password"}`))
	request.RemoteAddr = "192.0.2.10:43100"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://ascendany.example")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if calls := recorder.total(); calls != 1 {
		t.Fatalf("business service calls = %d, want one", calls)
	}
}

func TestWriteDisabledKeepsReadOnlyRoutesAvailable(t *testing.T) {
	options := disabledLSPTestOptions()
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var capabilities Capabilities
	if err := json.Unmarshal(response.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.WritesEnabled {
		t.Fatal("read-only capability response reported writes enabled")
	}
}
