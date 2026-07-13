package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type routeContract struct {
	method      string
	pattern     string
	examplePath string
	handler     http.HandlerFunc
	policy      *routePolicy
}

type routePolicyMatch struct {
	policy routePolicy
}

func (*routePolicyMatch) ServeHTTP(http.ResponseWriter, *http.Request) {
	panic("route policy match handlers must never serve requests")
}

type routeRegistry struct {
	contracts []routeContract
	methodMux *http.ServeMux
	methods   []string
}

func newRouteRegistry(contracts []routeContract) (*routeRegistry, error) {
	registry := &routeRegistry{
		contracts: append([]routeContract(nil), contracts...),
		methodMux: http.NewServeMux(),
	}
	registrations := make(map[string]struct{}, len(contracts))
	methods := make(map[string]struct{})
	for index := range registry.contracts {
		contract := &registry.contracts[index]
		if contract.handler == nil || contract.pattern == "" || contract.examplePath == "" {
			return nil, fmt.Errorf("HTTP route contract %d is incomplete", index)
		}
		if !strings.HasPrefix(contract.pattern, "/") || !strings.HasPrefix(contract.examplePath, "/") {
			return nil, fmt.Errorf("HTTP route contract %q must use absolute paths", contract.pattern)
		}
		if strings.ContainsAny(contract.examplePath, "?#") {
			return nil, fmt.Errorf("HTTP route contract %q has a non-path example", contract.pattern)
		}
		registration := contract.pattern
		if contract.method != "" {
			registration = contract.method + " " + contract.pattern
		}
		if _, exists := registrations[registration]; exists {
			return nil, fmt.Errorf("HTTP route contract %q is duplicated", registration)
		}
		registrations[registration] = struct{}{}

		if contract.policy == nil {
			if strings.HasPrefix(contract.pattern, "/api/v2/") && contract.method != "" {
				return nil, fmt.Errorf("API route contract %q has no security policy", registration)
			}
			continue
		}
		if contract.method == "" || contract.policy.method != contract.method {
			return nil, fmt.Errorf("HTTP route contract %q has an inconsistent policy method", registration)
		}
		if contract.policy.requestHeaders == nil || contract.policy.rateScope == "" {
			return nil, fmt.Errorf("HTTP route contract %q has an incomplete security policy", registration)
		}
		switch contract.method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !contract.policy.requiresWrites {
				return nil, fmt.Errorf("mutation route contract %q does not require write capability", registration)
			}
		}

		match := &routePolicyMatch{policy: *contract.policy}
		registry.methodMux.Handle(registration, match)
		methods[contract.method] = struct{}{}
	}
	registry.methods = make([]string, 0, len(methods))
	for method := range methods {
		registry.methods = append(registry.methods, method)
	}
	slices.Sort(registry.methods)

	for _, contract := range registry.contracts {
		if contract.policy == nil {
			continue
		}
		policy, known := registry.policyForMethod(contract.examplePath, contract.method)
		if !known || policy.method != contract.method || policy.rateScope != contract.policy.rateScope {
			return nil, fmt.Errorf("HTTP route contract %s %s does not match its example %q", contract.method, contract.pattern, contract.examplePath)
		}
	}
	return registry, nil
}

func (registry *routeRegistry) register(mux *http.ServeMux) {
	for _, contract := range registry.contracts {
		pattern := contract.pattern
		if contract.method != "" {
			pattern = contract.method + " " + contract.pattern
		}
		mux.HandleFunc(pattern, contract.handler)
	}
}

func (registry *routeRegistry) methodsForPath(path string) ([]string, bool) {
	methods := make([]string, 0, len(registry.methods))
	for _, method := range registry.methods {
		if _, known := registry.policyForMethod(path, method); known {
			methods = append(methods, method)
		}
	}
	return methods, len(methods) != 0
}

func (registry *routeRegistry) policyForMethod(path, method string) (routePolicy, bool) {
	request := &http.Request{Method: method, URL: &url.URL{Path: path}}
	matched, pattern := registry.methodMux.Handler(request)
	if pattern != "" {
		if match, ok := matched.(*routePolicyMatch); ok && match.policy.method == method {
			return match.policy, true
		}
	}
	return routePolicy{}, false
}

func apiRouteContracts(handler *Handler, authBodyTimeout, uploadBodyTimeout time.Duration) []routeContract {
	noHeaders := func() map[string]string { return map[string]string{} }
	authorization := func() map[string]string {
		return map[string]string{"authorization": "Authorization"}
	}
	authorizationJSON := func() map[string]string {
		return map[string]string{"authorization": "Authorization", "content-type": "Content-Type"}
	}
	authorizationEvents := func() map[string]string {
		return map[string]string{"authorization": "Authorization", "last-event-id": "Last-Event-ID"}
	}
	api := func(method, pattern, examplePath string, routeHandler http.HandlerFunc, policy routePolicy) routeContract {
		policy.method = method
		return routeContract{method: method, pattern: pattern, examplePath: examplePath, handler: routeHandler, policy: &policy}
	}
	plain := func(method, pattern, examplePath string, routeHandler http.HandlerFunc) routeContract {
		return routeContract{method: method, pattern: pattern, examplePath: examplePath, handler: routeHandler}
	}
	write := func(headers map[string]string, rateScope string, bodyTimeout time.Duration) routePolicy {
		return routePolicy{requiresWrites: true, requestHeaders: headers, rateScope: rateScope, bodyTimeout: bodyTimeout}
	}
	read := func(headers map[string]string, rateScope string) routePolicy {
		return routePolicy{requestHeaders: headers, rateScope: rateScope}
	}

	return []routeContract{
		plain(http.MethodGet, "/livez", "/livez", handler.livez),
		plain(http.MethodGet, "/readyz", "/readyz", handler.readyz),
		plain(http.MethodGet, "/version", "/version", handler.buildVersion),

		api(http.MethodGet, "/api/v2/capabilities", "/api/v2/capabilities", handler.getCapabilities, read(noHeaders(), "api.capabilities")),
		api(http.MethodPost, "/api/v2/auth/login", "/api/v2/auth/login", handler.login, routePolicy{
			requiresWrites: true, cookieMutation: true,
			requestHeaders: map[string]string{"content-type": "Content-Type"}, rateScope: "auth.login", bodyTimeout: authBodyTimeout,
		}),
		api(http.MethodPost, "/api/v2/auth/refresh", "/api/v2/auth/refresh", handler.refresh, routePolicy{
			requiresWrites: true, cookieMutation: true,
			requestHeaders: map[string]string{"x-ascendany-csrf": "X-AscendAny-CSRF"}, rateScope: "auth.refresh",
		}),
		api(http.MethodPost, "/api/v2/auth/logout", "/api/v2/auth/logout", handler.logout, routePolicy{
			requiresWrites: true, cookieMutation: true,
			requestHeaders: map[string]string{"authorization": "Authorization", "x-ascendany-csrf": "X-AscendAny-CSRF"}, rateScope: "auth.logout",
		}),
		api(http.MethodGet, "/api/v2/auth/me", "/api/v2/auth/me", handler.me, read(authorization(), "auth.me")),
		api(http.MethodPost, "/api/v2/auth/enrollment-claims/consume", "/api/v2/auth/enrollment-claims/consume", handler.claimEnrollment, routePolicy{
			requiresWrites: true, cookieMutation: true,
			requestHeaders: map[string]string{"content-type": "Content-Type"}, rateScope: "auth.enrollment.claim", bodyTimeout: authBodyTimeout,
		}),

		api(http.MethodPatch, "/api/v2/account/profile", "/api/v2/account/profile", handler.updateAccountProfile, write(authorizationJSON(), "account.profile.update", authBodyTimeout)),
		api(http.MethodGet, "/api/v2/account/sessions", "/api/v2/account/sessions", handler.listAccountSessions, read(authorization(), "account.sessions.list")),
		api(http.MethodDelete, "/api/v2/account/sessions/{sessionId}", "/api/v2/account/sessions/123e4567-e89b-42d3-a456-426614174020", handler.revokeAccountSession, write(authorization(), "account.sessions.revoke", 0)),

		api(http.MethodPost, "/api/v2/admin/enrollment-claims", "/api/v2/admin/enrollment-claims", handler.issueEnrollment, write(authorizationJSON(), "admin.enrollment.issue", authBodyTimeout)),
		api(http.MethodDelete, "/api/v2/admin/enrollment-claims/{grantId}", "/api/v2/admin/enrollment-claims/123e4567-e89b-42d3-a456-426614174021", handler.revokeEnrollment, write(authorization(), "admin.enrollment.revoke", 0)),

		api(http.MethodGet, "/api/v2/students/me/analytics", "/api/v2/students/me/analytics", handler.getSelfStudentAnalytics, read(authorization(), "students.me.analytics")),
		api(http.MethodGet, "/api/v2/students/me/achievements", "/api/v2/students/me/achievements", handler.getSelfAchievements, read(authorization(), "students.me.achievements")),
		api(http.MethodGet, "/api/v2/students/me/recommendation", "/api/v2/students/me/recommendation", handler.getSelfRecommendation, read(authorization(), "students.me.recommendation")),
		api(http.MethodGet, "/api/v2/students/leaderboard", "/api/v2/students/leaderboard", handler.getStudentLeaderboard, read(authorization(), "students.leaderboard")),

		api(http.MethodPost, "/api/v2/students/me/notes", "/api/v2/students/me/notes", handler.createAgentNote, write(authorizationJSON(), "students.me.notes.create", authBodyTimeout)),
		api(http.MethodGet, "/api/v2/students/me/notes", "/api/v2/students/me/notes", handler.listAgentNotes, read(authorization(), "students.me.notes.list")),
		api(http.MethodGet, "/api/v2/students/me/notes/{noteId}", "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022", handler.getAgentNote, read(authorization(), "students.me.notes.get")),
		api(http.MethodPut, "/api/v2/students/me/notes/{noteId}/document", "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022/document", handler.replaceAgentNote, write(authorizationJSON(), "students.me.notes.replace", authBodyTimeout)),
		api(http.MethodPost, "/api/v2/students/me/notes/{noteId}/archive", "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022/archive", handler.archiveAgentNote, write(authorizationJSON(), "students.me.notes.archive", authBodyTimeout)),
		api(http.MethodPost, "/api/v2/students/me/notes/{noteId}/restore", "/api/v2/students/me/notes/123e4567-e89b-42d3-a456-426614174022/restore", handler.restoreAgentNote, write(authorizationJSON(), "students.me.notes.restore", authBodyTimeout)),

		api(http.MethodPost, "/api/v2/students/me/chat/threads", "/api/v2/students/me/chat/threads", handler.createChatThread, write(authorization(), "students.me.chat.threads.create", 0)),
		api(http.MethodGet, "/api/v2/students/me/chat/threads", "/api/v2/students/me/chat/threads", handler.listChatThreads, read(authorization(), "students.me.chat.threads.list")),
		api(http.MethodGet, "/api/v2/students/me/chat/threads/{threadId}/messages", "/api/v2/students/me/chat/threads/123e4567-e89b-42d3-a456-426614174023/messages", handler.listChatMessages, read(authorization(), "students.me.chat.messages.list")),
		api(http.MethodPost, "/api/v2/students/me/chat/threads/{threadId}/runs", "/api/v2/students/me/chat/threads/123e4567-e89b-42d3-a456-426614174023/runs", handler.enqueueAgentRun, write(authorizationJSON(), "students.me.agent.runs.enqueue", authBodyTimeout)),
		api(http.MethodPost, "/api/v2/students/me/auto-analysis", "/api/v2/students/me/auto-analysis", handler.enqueueAutoAnalysis, write(authorizationJSON(), "students.me.auto-analysis.enqueue", authBodyTimeout)),
		api(http.MethodGet, "/api/v2/students/me/agent-runs/{runId}", "/api/v2/students/me/agent-runs/123e4567-e89b-42d3-a456-426614174024", handler.getAgentRun, read(authorization(), "students.me.agent.runs.get")),
		api(http.MethodGet, "/api/v2/students/me/agent-runs/{runId}/events", "/api/v2/students/me/agent-runs/123e4567-e89b-42d3-a456-426614174024/events", handler.streamAgentRunEvents, read(authorizationEvents(), "students.me.agent.runs.events")),

		api(http.MethodGet, "/api/v2/oj/problems", "/api/v2/oj/problems", handler.listOJProblems, read(authorization(), "oj.problems.list")),
		api(http.MethodGet, "/api/v2/oj/problems/{problemId}", "/api/v2/oj/problems/123e4567-e89b-42d3-a456-426614174025", handler.getOJProblem, read(authorization(), "oj.problems.get")),
		api(http.MethodPost, "/api/v2/admin/oj/problems/versions", "/api/v2/admin/oj/problems/versions", handler.createOJProblemVersion, write(authorizationJSON(), "admin.oj.problems.create-version", uploadBodyTimeout)),
		api(http.MethodPost, "/api/v2/oj/submissions", "/api/v2/oj/submissions", handler.createOJSubmission, write(authorizationJSON(), "oj.submissions.create", uploadBodyTimeout)),
		api(http.MethodGet, "/api/v2/oj/submissions/{submissionId}", "/api/v2/oj/submissions/123e4567-e89b-42d3-a456-426614174026", handler.getOJSubmission, read(authorization(), "oj.submissions.get")),
		api(http.MethodGet, "/api/v2/oj/submissions/{submissionId}/events", "/api/v2/oj/submissions/123e4567-e89b-42d3-a456-426614174026/events", handler.streamOJJudgeEvents, read(authorizationEvents(), "oj.submissions.events")),

		api(http.MethodPost, "/api/v2/lsp/sessions", "/api/v2/lsp/sessions", handler.createLSPSession, write(authorization(), "lsp.sessions.create", 0)),
		api(http.MethodDelete, "/api/v2/lsp/sessions/{sessionId}", "/api/v2/lsp/sessions/123e4567-e89b-42d3-a456-426614174027", handler.closeLSPSession, write(authorization(), "lsp.sessions.close", 0)),
		api(http.MethodGet, "/api/v2/lsp/sessions/{sessionId}/websocket", "/api/v2/lsp/sessions/123e4567-e89b-42d3-a456-426614174027/websocket", handler.attachLSPSession, routePolicy{
			requiresWrites: true, requestHeaders: map[string]string{"sec-websocket-protocol": "Sec-WebSocket-Protocol"}, rateScope: "lsp.sessions.attach",
		}),

		api(http.MethodGet, "/api/v2/exams", "/api/v2/exams", handler.listExams, read(authorization(), "exams.list")),
		api(http.MethodGet, "/api/v2/exams/{examId}", "/api/v2/exams/123e4567-e89b-42d3-a456-426614174028", handler.getExam, read(authorization(), "exams.get")),
		api(http.MethodGet, "/api/v2/exams/{examId}/analysis-generation", "/api/v2/exams/123e4567-e89b-42d3-a456-426614174028/analysis-generation", handler.getCurrentExamGeneration, read(authorization(), "exams.analysis-generation.get")),
		api(http.MethodGet, "/api/v2/exams/{examId}/analysis-generations/{generationId}/events", "/api/v2/exams/123e4567-e89b-42d3-a456-426614174028/analysis-generations/42/events", handler.streamExamGenerationEvents, read(authorizationEvents(), "exams.analysis-generation.events")),

		api(http.MethodGet, "/api/v2/admin/accounts", "/api/v2/admin/accounts", handler.listManagedAccounts, read(authorization(), "admin.accounts.list")),
		api(http.MethodPatch, "/api/v2/admin/accounts/{accountId}/state", "/api/v2/admin/accounts/123e4567-e89b-42d3-a456-426614174029/state", handler.setManagedAccountState, write(authorizationJSON(), "admin.accounts.state", authBodyTimeout)),
		api(http.MethodGet, "/api/v2/admin/students", "/api/v2/admin/students", handler.listManagedStudents, read(authorization(), "admin.students.list")),
		api(http.MethodGet, "/api/v2/admin/audit-events", "/api/v2/admin/audit-events", handler.listAuditEvents, read(authorization(), "admin.audit.list")),
		api(http.MethodGet, "/api/v2/admin/configurations", "/api/v2/admin/configurations", handler.listConfigurations, read(authorization(), "admin.configurations.list")),
		api(http.MethodPost, "/api/v2/admin/configurations/versions", "/api/v2/admin/configurations/versions", handler.createConfigurationVersion, write(authorizationJSON(), "admin.configurations.create-version", authBodyTimeout)),
		api(http.MethodGet, "/api/v2/admin/recommendation/review-context", "/api/v2/admin/recommendation/review-context", handler.getRecommendationReviewContext, read(authorization(), "admin.recommendation.review-context.get")),
		api(http.MethodPost, "/api/v2/admin/recommendation/catalog-publication-authorizations", "/api/v2/admin/recommendation/catalog-publication-authorizations", handler.authorizeKnowledgeCatalogPublication, write(authorizationJSON(), "admin.recommendation.catalog-publication-authorizations.create", authBodyTimeout)),
		api(http.MethodPost, "/api/v2/admin/model-connections/{key}/test", "/api/v2/admin/model-connections/chat.primary/test", handler.testModelConnection, write(authorization(), "admin.model-connections.test", 0)),
		api(http.MethodGet, "/api/v2/admin/configurations/{key}", "/api/v2/admin/configurations/feedback.delivery", handler.getConfiguration, read(authorization(), "admin.configurations.get")),
		api(http.MethodGet, "/api/v2/admin/configurations/{key}/versions", "/api/v2/admin/configurations/feedback.delivery/versions", handler.listConfigurationVersions, read(authorization(), "admin.configurations.versions.list")),

		api(http.MethodPost, "/api/v2/feedback", "/api/v2/feedback", handler.submitFeedback, write(authorizationJSON(), "feedback.submit", authBodyTimeout)),
		api(http.MethodGet, "/api/v2/imports", "/api/v2/imports", handler.listImportJobs, read(authorization(), "imports.list")),
		api(http.MethodPost, "/api/v2/imports/pintia", "/api/v2/imports/pintia", handler.createPintiaImport, write(authorizationJSON(), "imports.create", uploadBodyTimeout)),
		api(http.MethodGet, "/api/v2/imports/{jobId}", "/api/v2/imports/123e4567-e89b-42d3-a456-426614174030", handler.getImportJob, read(authorization(), "imports.get")),
		api(http.MethodGet, "/api/v2/imports/{jobId}/events", "/api/v2/imports/123e4567-e89b-42d3-a456-426614174030/events", handler.streamImportEvents, read(authorizationEvents(), "imports.events")),

		plain("", "/api/v2/", "/api/v2/unknown", handler.apiNotFound),
	}
}
