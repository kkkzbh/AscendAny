package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
)

const refreshCookieName = "__Host-ascendany_refresh"

var (
	csrfTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	headerNamePattern = regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`)
)

type routePolicy struct {
	method           string
	requiresWrites   bool
	cookieMutation   bool
	browserForbidden bool
	internalProtocol bool
	requestHeaders   map[string]string
	rateScope        string
	bodyTimeout      time.Duration
}

type clientAddressResolver struct {
	trustedProxyCIDRs []netip.Prefix
	clientIPHeader    string
}

func newClientAddressResolver(
	trustedProxyCIDRs []netip.Prefix,
	clientIPHeader string,
) (clientAddressResolver, error) {
	if len(trustedProxyCIDRs) == 0 {
		if clientIPHeader != "" {
			return clientAddressResolver{}, errInvalidClientAddressConfiguration
		}
		return clientAddressResolver{}, nil
	}
	if clientIPHeader != "CF-Connecting-IP" {
		return clientAddressResolver{}, errInvalidClientAddressConfiguration
	}
	prefixes := make([]netip.Prefix, len(trustedProxyCIDRs))
	for index, prefix := range trustedProxyCIDRs {
		if !prefix.IsValid() || prefix.Addr().Zone() != "" || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return clientAddressResolver{}, errInvalidClientAddressConfiguration
		}
		prefixes[index] = prefix
	}
	return clientAddressResolver{
		trustedProxyCIDRs: prefixes,
		clientIPHeader:    clientIPHeader,
	}, nil
}

func (resolver clientAddressResolver) Resolve(request *http.Request) (string, error) {
	direct, err := parseDirectClientAddress(request.RemoteAddr)
	if err != nil {
		return "", err
	}
	trusted := false
	for _, prefix := range resolver.trustedProxyCIDRs {
		if prefix.Contains(direct) {
			trusted = true
			break
		}
	}
	if !trusted {
		return direct.String(), nil
	}
	forwarded, present, valid := singleHeader(request.Header, resolver.clientIPHeader)
	if !valid || !present {
		return "", errInvalidClientAddress
	}
	address, err := netip.ParseAddr(forwarded)
	if err != nil || address.Zone() != "" || address.Is4In6() || address.String() != forwarded {
		return "", errInvalidClientAddress
	}
	return address.String(), nil
}

func apiRoutePolicies(authBodyTimeout, uploadBodyTimeout time.Duration) map[string]routePolicy {
	return map[string]routePolicy{
		"/api/v2/capabilities": {
			method:         http.MethodGet,
			requestHeaders: map[string]string{},
			rateScope:      "api.capabilities",
		},
		"/api/v2/auth/login": {
			method:         http.MethodPost,
			cookieMutation: true,
			requestHeaders: map[string]string{
				"content-type": "Content-Type",
			},
			rateScope:   "auth.login",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/auth/refresh": {
			method:         http.MethodPost,
			cookieMutation: true,
			requestHeaders: map[string]string{
				"x-ascendany-csrf": "X-AscendAny-CSRF",
			},
			rateScope: "auth.refresh",
		},
		"/api/v2/auth/logout": {
			method:         http.MethodPost,
			cookieMutation: true,
			requestHeaders: map[string]string{
				"authorization":    "Authorization",
				"x-ascendany-csrf": "X-AscendAny-CSRF",
			},
			rateScope: "auth.logout",
		},
		"/api/v2/auth/me": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "auth.me",
		},
		"/api/v2/auth/enrollment-claims/consume": {
			method:         http.MethodPost,
			cookieMutation: true,
			requestHeaders: map[string]string{
				"content-type": "Content-Type",
			},
			rateScope:   "auth.enrollment.claim",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/account/profile": {
			method: http.MethodPatch,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "account.profile.update",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/account/sessions": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "account.sessions.list",
		},
		"/api/v2/admin/enrollment-claims": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "admin.enrollment.issue",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/students/me/analytics": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.analytics",
		},
		"/api/v2/students/me/achievements": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.achievements",
		},
		"/api/v2/students/me/recommendation": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.recommendation",
		},
		"/api/v2/students/leaderboard": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.leaderboard",
		},
		"/api/v2/students/me/notes": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "students.me.notes.create",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/students/me/chat/threads": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.chat.threads.create",
		},
		"/api/v2/students/me/auto-analysis": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "students.me.auto-analysis.enqueue",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/oj/problems": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "oj.problems.list",
		},
		"/api/v2/admin/oj/problems/versions": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "admin.oj.problems.create-version",
			bodyTimeout: uploadBodyTimeout,
		},
		"/api/v2/oj/submissions": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "oj.submissions.create",
			bodyTimeout: uploadBodyTimeout,
		},
		"/api/v2/lsp/sessions": {
			method:         http.MethodPost,
			requiresWrites: true,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "lsp.sessions.create",
		},
		"/api/v2/exams": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "exams.list",
		},
		"/api/v2/admin/accounts": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.accounts.list",
		},
		"/api/v2/admin/students": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.students.list",
		},
		"/api/v2/admin/audit-events": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.audit.list",
		},
		"/api/v2/admin/configurations": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.configurations.list",
		},
		"/api/v2/admin/configurations/versions": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "admin.configurations.create-version",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/admin/recommendation/training-runs": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "admin.recommendation.training-runs.create",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/admin/recommendation/review-context": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.recommendation.review-context.get",
		},
		"/api/v2/feedback": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "feedback.submit",
			bodyTimeout: authBodyTimeout,
		},
		"/api/v2/imports": {
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "imports.list",
		},
		"/api/v2/imports/pintia": {
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "imports.create",
			bodyTimeout: uploadBodyTimeout,
		},
	}
}

func (handler *Handler) policyForPath(path string) (routePolicy, bool) {
	if policy, ok := handler.routePolicies[path]; ok {
		return policy, true
	}
	return handler.dynamicPolicyForPath(path)
}

func (handler *Handler) policyForMethod(path, method string) (routePolicy, bool) {
	exact, exactKnown := handler.routePolicies[path]
	dynamic, dynamicKnown := handler.dynamicPolicyForPath(path)
	if exactKnown && exact.method == method {
		return exact, true
	}
	if dynamicKnown && dynamic.method == method {
		return dynamic, true
	}
	if exactKnown {
		return exact, true
	}
	return dynamic, dynamicKnown
}

func (handler *Handler) dynamicPolicyForPath(path string) (routePolicy, bool) {
	if handler.trainerAgentTransportEnabled {
		if path == traineragentprotocol.HTTPBasePathV1+"/claims" {
			return routePolicy{
				method: http.MethodPost, browserForbidden: true, internalProtocol: true,
				requestHeaders: map[string]string{},
				rateScope:      "internal.recommendation.trainer-agent.claim.ip", bodyTimeout: handler.authBodyTimeout,
			}, true
		}
		const trainerClaimPrefix = traineragentprotocol.HTTPBasePathV1 + "/claims/"
		if strings.HasPrefix(path, trainerClaimPrefix) {
			parts := strings.Split(strings.TrimPrefix(path, trainerClaimPrefix), "/")
			if len(parts) == 2 && parts[0] != "" {
				scope := ""
				timeout := handler.authBodyTimeout
				switch parts[1] {
				case "heartbeats":
					scope = "internal.recommendation.trainer-agent.heartbeat.ip"
				case "output":
					scope = "internal.recommendation.trainer-agent.output.ip"
					timeout = handler.uploadBodyTimeout
				case "failures":
					scope = "internal.recommendation.trainer-agent.failure.ip"
				}
				if scope != "" {
					return routePolicy{
						method: http.MethodPost, browserForbidden: true, internalProtocol: true,
						requestHeaders: map[string]string{}, rateScope: scope, bodyTimeout: timeout,
					}, true
				}
			}
		}
	}
	const lspSessionPrefix = "/api/v2/lsp/sessions/"
	if strings.HasPrefix(path, lspSessionPrefix) {
		remainder := strings.TrimPrefix(path, lspSessionPrefix)
		if remainder != "" && !strings.Contains(remainder, "/") {
			return routePolicy{
				method:         http.MethodDelete,
				requiresWrites: true,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
				},
				rateScope: "lsp.sessions.close",
			}, true
		}
		const websocketSuffix = "/websocket"
		if strings.HasSuffix(remainder, websocketSuffix) {
			sessionID := strings.TrimSuffix(remainder, websocketSuffix)
			if sessionID != "" && !strings.Contains(sessionID, "/") {
				return routePolicy{
					method:         http.MethodGet,
					requiresWrites: true,
					requestHeaders: map[string]string{
						"sec-websocket-protocol": "Sec-WebSocket-Protocol",
					},
					rateScope: "lsp.sessions.attach",
				}, true
			}
		}
		return routePolicy{}, false
	}
	const chatThreadCollection = "/api/v2/students/me/chat/threads"
	if path == chatThreadCollection {
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.chat.threads.list",
		}, true
	}
	const chatThreadPrefix = chatThreadCollection + "/"
	if strings.HasPrefix(path, chatThreadPrefix) {
		parts := strings.Split(strings.TrimPrefix(path, chatThreadPrefix), "/")
		if len(parts) != 2 || parts[0] == "" {
			return routePolicy{}, false
		}
		switch parts[1] {
		case "messages":
			return routePolicy{
				method: http.MethodGet,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
				},
				rateScope: "students.me.chat.messages.list",
			}, true
		case "runs":
			return routePolicy{
				method: http.MethodPost,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
					"content-type":  "Content-Type",
				},
				rateScope:   "students.me.agent.runs.enqueue",
				bodyTimeout: handler.authBodyTimeout,
			}, true
		default:
			return routePolicy{}, false
		}
	}
	const agentRunPrefix = "/api/v2/students/me/agent-runs/"
	if strings.HasPrefix(path, agentRunPrefix) {
		remainder := strings.TrimPrefix(path, agentRunPrefix)
		if remainder == "" {
			return routePolicy{}, false
		}
		const eventsSuffix = "/events"
		if strings.HasSuffix(remainder, eventsSuffix) {
			runID := strings.TrimSuffix(remainder, eventsSuffix)
			if runID == "" || strings.Contains(runID, "/") {
				return routePolicy{}, false
			}
			return routePolicy{
				method: http.MethodGet,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
					"last-event-id": "Last-Event-ID",
				},
				rateScope: "students.me.agent.runs.events",
			}, true
		}
		if strings.Contains(remainder, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.agent.runs.get",
		}, true
	}
	const ojProblemPrefix = "/api/v2/oj/problems/"
	if strings.HasPrefix(path, ojProblemPrefix) {
		problemID := strings.TrimPrefix(path, ojProblemPrefix)
		if problemID == "" || strings.Contains(problemID, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "oj.problems.get",
		}, true
	}
	const ojSubmissionPrefix = "/api/v2/oj/submissions/"
	if strings.HasPrefix(path, ojSubmissionPrefix) {
		remainder := strings.TrimPrefix(path, ojSubmissionPrefix)
		if remainder == "" {
			return routePolicy{}, false
		}
		const eventsSuffix = "/events"
		if strings.HasSuffix(remainder, eventsSuffix) {
			submissionID := strings.TrimSuffix(remainder, eventsSuffix)
			if submissionID == "" || strings.Contains(submissionID, "/") {
				return routePolicy{}, false
			}
			return routePolicy{
				method: http.MethodGet,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
					"last-event-id": "Last-Event-ID",
				},
				rateScope: "oj.submissions.events",
			}, true
		}
		if strings.Contains(remainder, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "oj.submissions.get",
		}, true
	}
	const notesCollection = "/api/v2/students/me/notes"
	if path == notesCollection {
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "students.me.notes.list",
		}, true
	}
	const notesPrefix = notesCollection + "/"
	if strings.HasPrefix(path, notesPrefix) {
		remainder := strings.TrimPrefix(path, notesPrefix)
		parts := strings.Split(remainder, "/")
		if len(parts) == 1 && parts[0] != "" {
			return routePolicy{
				method: http.MethodGet,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
				},
				rateScope: "students.me.notes.get",
			}, true
		}
		if len(parts) != 2 || parts[0] == "" {
			return routePolicy{}, false
		}
		policy := routePolicy{
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			bodyTimeout: handler.authBodyTimeout,
		}
		switch parts[1] {
		case "document":
			policy.method = http.MethodPut
			policy.rateScope = "students.me.notes.replace"
		case "archive":
			policy.method = http.MethodPost
			policy.rateScope = "students.me.notes.archive"
		case "restore":
			policy.method = http.MethodPost
			policy.rateScope = "students.me.notes.restore"
		default:
			return routePolicy{}, false
		}
		return policy, true
	}
	const configurationPrefix = "/api/v2/admin/configurations/"
	const recommendationRunPrefix = "/api/v2/admin/recommendation/training-runs/"
	if strings.HasPrefix(path, recommendationRunPrefix) {
		remainder := strings.TrimPrefix(path, recommendationRunPrefix)
		parts := strings.Split(remainder, "/")
		if len(parts) == 1 && parts[0] != "" {
			return routePolicy{
				method:         http.MethodGet,
				requestHeaders: map[string]string{"authorization": "Authorization"},
				rateScope:      "admin.recommendation.training-runs.get",
			}, true
		}
		if len(parts) == 2 && parts[0] != "" && parts[1] == "events" {
			return routePolicy{
				method:         http.MethodGet,
				requestHeaders: map[string]string{"authorization": "Authorization"},
				rateScope:      "admin.recommendation.training-runs.events.list",
			}, true
		}
		return routePolicy{}, false
	}
	const configurationVersionsSuffix = "/versions"
	if strings.HasPrefix(path, configurationPrefix) {
		remainder := strings.TrimPrefix(path, configurationPrefix)
		if remainder == "" {
			return routePolicy{}, false
		}
		if strings.HasSuffix(remainder, configurationVersionsSuffix) {
			key := strings.TrimSuffix(remainder, configurationVersionsSuffix)
			if key == "" || strings.Contains(key, "/") {
				return routePolicy{}, false
			}
			return routePolicy{
				method: http.MethodGet,
				requestHeaders: map[string]string{
					"authorization": "Authorization",
				},
				rateScope: "admin.configurations.versions.list",
			}, true
		}
		if strings.Contains(remainder, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.configurations.get",
		}, true
	}
	const modelConnectionPrefix = "/api/v2/admin/model-connections/"
	const modelConnectionTestSuffix = "/test"
	if strings.HasPrefix(path, modelConnectionPrefix) {
		remainder := strings.TrimPrefix(path, modelConnectionPrefix)
		if !strings.HasSuffix(remainder, modelConnectionTestSuffix) {
			return routePolicy{}, false
		}
		key := strings.TrimSuffix(remainder, modelConnectionTestSuffix)
		if key == "" || strings.Contains(key, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodPost,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.model-connections.test",
		}, true
	}
	const accountSessionPrefix = "/api/v2/account/sessions/"
	if strings.HasPrefix(path, accountSessionPrefix) {
		sessionID := strings.TrimPrefix(path, accountSessionPrefix)
		if sessionID == "" || strings.Contains(sessionID, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodDelete,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "account.sessions.revoke",
		}, true
	}
	const enrollmentPrefix = "/api/v2/admin/enrollment-claims/"
	if strings.HasPrefix(path, enrollmentPrefix) {
		grantID := strings.TrimPrefix(path, enrollmentPrefix)
		if grantID == "" || strings.Contains(grantID, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodDelete,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "admin.enrollment.revoke",
		}, true
	}
	const examPrefix = "/api/v2/exams/"
	if strings.HasPrefix(path, examPrefix) {
		parts := strings.Split(strings.TrimPrefix(path, examPrefix), "/")
		if len(parts) == 0 || parts[0] == "" {
			return routePolicy{}, false
		}
		policy := routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
		}
		switch {
		case len(parts) == 1:
			policy.rateScope = "exams.get"
		case len(parts) == 2 && parts[1] == "analysis-generation":
			policy.rateScope = "exams.analysis-generation.get"
		case len(parts) == 4 && parts[1] == "analysis-generations" && parts[2] != "" && parts[3] == "events":
			policy.requestHeaders["last-event-id"] = "Last-Event-ID"
			policy.rateScope = "exams.analysis-generation.events"
		default:
			return routePolicy{}, false
		}
		return policy, true
	}
	const managedAccountPrefix = "/api/v2/admin/accounts/"
	const managedAccountStateSuffix = "/state"
	if strings.HasPrefix(path, managedAccountPrefix) && strings.HasSuffix(path, managedAccountStateSuffix) {
		accountID := strings.TrimSuffix(strings.TrimPrefix(path, managedAccountPrefix), managedAccountStateSuffix)
		if accountID == "" || strings.Contains(accountID, "/") {
			return routePolicy{}, false
		}
		return routePolicy{
			method: http.MethodPatch,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
				"content-type":  "Content-Type",
			},
			rateScope:   "admin.accounts.state",
			bodyTimeout: handler.authBodyTimeout,
		}, true
	}
	const prefix = "/api/v2/imports/"
	if !strings.HasPrefix(path, prefix) {
		return routePolicy{}, false
	}
	remainder := strings.TrimPrefix(path, prefix)
	if remainder != "" && !strings.Contains(remainder, "/") {
		return routePolicy{
			method: http.MethodGet,
			requestHeaders: map[string]string{
				"authorization": "Authorization",
			},
			rateScope: "imports.get",
		}, true
	}
	const eventsSuffix = "/events"
	if !strings.HasSuffix(remainder, eventsSuffix) {
		return routePolicy{}, false
	}
	publicID := strings.TrimSuffix(remainder, eventsSuffix)
	if publicID == "" || strings.Contains(publicID, "/") {
		return routePolicy{}, false
	}
	return routePolicy{
		method: http.MethodGet,
		requestHeaders: map[string]string{
			"authorization": "Authorization",
			"last-event-id": "Last-Event-ID",
		},
		rateScope: "imports.events",
	}, true
}

func (handler *Handler) browserBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		policyMethod := request.Method
		if request.Method == http.MethodOptions {
			requestedMethod, present, valid := singleHeader(request.Header, "Access-Control-Request-Method")
			if present && valid {
				policyMethod = requestedMethod
			}
		}
		policy, knownRoute := handler.policyForMethod(request.URL.Path, policyMethod)
		bodyAllowed := knownRoute && request.Method == policy.method && policy.bodyTimeout > 0
		if bodyAllowed {
			guardedRequest, lifetime, err := beginRequestBodyLifetime(writer, request, policy.bodyTimeout)
			if err != nil {
				handler.logger.ErrorContext(request.Context(), "HTTP connection does not support request read deadlines",
					"request_id", requestID(request.Context()),
					"code", "read_deadline_unsupported",
				)
				writer.Header().Set("Connection", "close")
				request.Close = true
				if policy.internalProtocol {
					handler.writeTrainerAgentError(writer, request, http.StatusServiceUnavailable, "service_unavailable", "Request could not be completed.", true)
				} else {
					handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
				}
				return
			}
			request = guardedRequest
			defer lifetime.abortIfActive()
		} else if !requestBodyIsEmpty(request) {
			handler.writeRoutePolicyError(writer, request, policy, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.", false)
			return
		}
		if request.Method == http.MethodOptions {
			if knownRoute && policy.browserForbidden {
				handler.writeTrainerAgentError(writer, request, http.StatusForbidden, "browser_request_rejected", "Browser requests are forbidden.", false)
				return
			}
			handler.handlePreflight(writer, request, policy, knownRoute)
			return
		}
		if knownRoute {
			origin, present, valid := singleHeader(request.Header, "Origin")
			if policy.browserForbidden && present {
				handler.writeTrainerAgentError(writer, request, http.StatusForbidden, "browser_request_rejected", "Browser requests are forbidden.", false)
				return
			}
			allowed := present && handler.originAllowed(origin)
			if !valid || (present && !allowed) || (policy.cookieMutation && !allowed) {
				handler.writeAPIError(writer, request, http.StatusForbidden, "origin_rejected", "Request Origin was rejected.")
				return
			}
			if present {
				handler.setCORSHeaders(writer.Header(), origin)
			}
			if request.Method != policy.method {
				writer.Header().Set("Allow", policy.method)
				handler.writeRoutePolicyError(writer, request, policy, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method is not allowed for this route.", false)
				return
			}
			clientAddress, err := handler.clientAddress.Resolve(request)
			if err != nil {
				handler.writeRoutePolicyError(writer, request, policy, http.StatusBadRequest, "invalid_client_address", "Client address is invalid.", false)
				return
			}
			if !handler.allowRate(writer, request, policy.rateScope, clientAddress, policy.internalProtocol) {
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *Handler) writeCapabilityBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodOptions {
			policy, knownRoute := handler.policyForMethod(request.URL.Path, request.Method)
			if knownRoute && request.Method == policy.method && policy.requiresWrites && !handler.capabilities.WritesEnabled {
				handler.requireWritesEnabled(writer, request)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *Handler) writeRoutePolicyError(
	writer http.ResponseWriter,
	request *http.Request,
	policy routePolicy,
	status int,
	code, detail string,
	retryable bool,
) {
	if policy.internalProtocol {
		handler.writeTrainerAgentError(writer, request, status, code, detail, retryable)
		return
	}
	handler.writeAPIError(writer, request, status, code, detail)
}

func (handler *Handler) allowRate(
	writer http.ResponseWriter,
	request *http.Request,
	scope string,
	key string,
	protocol ...bool,
) bool {
	internalProtocol := len(protocol) == 1 && protocol[0]
	decision := handler.rateLimiter.Allow(scope, key)
	if decision.Allowed {
		return true
	}
	retryAfter := int64((decision.RetryAfter + time.Second - 1) / time.Second)
	if retryAfter < 1 {
		retryAfter = 1
	}
	writer.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	if internalProtocol {
		handler.writeTrainerAgentError(writer, request, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit was exceeded.", true)
		return false
	}
	handler.writeAPIError(writer, request, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit was exceeded.")
	return false
}

func (handler *Handler) handlePreflight(
	writer http.ResponseWriter,
	request *http.Request,
	policy routePolicy,
	knownRoute bool,
) {
	if !knownRoute {
		handler.writeAPIError(writer, request, http.StatusNotFound, "route_not_found", "API route does not exist.")
		return
	}
	origin, present, valid := singleHeader(request.Header, "Origin")
	if !valid || !present || !handler.originAllowed(origin) {
		handler.writeAPIError(writer, request, http.StatusForbidden, "origin_rejected", "Request Origin was rejected.")
		return
	}
	handler.setCORSHeaders(writer.Header(), origin)
	requestedMethod, methodPresent, methodValid := singleHeader(request.Header, "Access-Control-Request-Method")
	if !methodValid || !methodPresent || requestedMethod != policy.method {
		handler.writeAPIError(writer, request, http.StatusForbidden, "preflight_rejected", "CORS preflight method was rejected.")
		return
	}
	requestedHeaders, valid := parseRequestedHeaders(request.Header.Values("Access-Control-Request-Headers"))
	if !valid {
		handler.writeAPIError(writer, request, http.StatusForbidden, "preflight_rejected", "CORS preflight headers were rejected.")
		return
	}
	for _, requested := range requestedHeaders {
		if _, allowed := policy.requestHeaders[requested]; !allowed {
			handler.writeAPIError(writer, request, http.StatusForbidden, "preflight_rejected", "CORS preflight headers were rejected.")
			return
		}
	}
	allowedHeaders := make([]string, 0, len(policy.requestHeaders))
	for _, canonical := range policy.requestHeaders {
		allowedHeaders = append(allowedHeaders, canonical)
	}
	sort.Strings(allowedHeaders)
	writer.Header().Set("Access-Control-Allow-Methods", policy.method)
	if len(allowedHeaders) > 0 {
		writer.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
	}
	writer.Header().Set("Access-Control-Max-Age", "600")
	addVary(writer.Header(), "Access-Control-Request-Method")
	addVary(writer.Header(), "Access-Control-Request-Headers")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) originAllowed(origin string) bool {
	_, allowed := handler.allowedOrigins[origin]
	return allowed
}

func (handler *Handler) setCORSHeaders(header http.Header, origin string) {
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Allow-Credentials", "true")
	header.Set("Access-Control-Expose-Headers", "Retry-After, X-Request-ID")
	addVary(header, "Origin")
}

func singleHeader(header http.Header, name string) (string, bool, bool) {
	values := header.Values(name)
	if len(values) == 0 {
		return "", false, true
	}
	if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] {
		return "", true, false
	}
	return values[0], true, true
}

func parseRequestedHeaders(values []string) ([]string, bool) {
	if len(values) == 0 {
		return nil, true
	}
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			name := strings.TrimSpace(part)
			if name == "" || !headerNamePattern.MatchString(name) {
				return nil, false
			}
			lower := strings.ToLower(name)
			if _, duplicate := seen[lower]; duplicate {
				return nil, false
			}
			seen[lower] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, true
}

func addVary(header http.Header, value string) {
	for _, existingLine := range header.Values("Vary") {
		for _, existing := range strings.Split(existingLine, ",") {
			if strings.EqualFold(strings.TrimSpace(existing), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func directClientAddress(remoteAddress string) (string, error) {
	address, err := parseDirectClientAddress(remoteAddress)
	if err != nil {
		return "", err
	}
	return address.String(), nil
}

func parseDirectClientAddress(remoteAddress string) (netip.Addr, error) {
	host, port, err := net.SplitHostPort(remoteAddress)
	if err != nil || port == "" {
		return netip.Addr{}, errInvalidClientAddress
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return netip.Addr{}, errInvalidClientAddress
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, errInvalidClientAddress
	}
	return address.Unmap(), nil
}

type clientAddressError struct{}

func (clientAddressError) Error() string { return "invalid direct TCP client address" }

var errInvalidClientAddress error = clientAddressError{}

type clientAddressConfigurationError struct{}

func (clientAddressConfigurationError) Error() string {
	return "trusted proxy CIDRs require the exact CF-Connecting-IP client header"
}

var errInvalidClientAddressConfiguration error = clientAddressConfigurationError{}

func bearerToken(request *http.Request) (string, bool) {
	value, present, valid := singleHeader(request.Header, "Authorization")
	if !valid || !present || !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(value, "Bearer ")
	if token == "" || len(token) > 4096 || strings.IndexFunc(token, func(r rune) bool {
		return r <= ' ' || r == 0x7f
	}) >= 0 {
		return "", false
	}
	return token, true
}

func refreshCookie(request *http.Request) (string, bool) {
	var value string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == refreshCookieName {
			count++
			value = cookie.Value
		}
	}
	if count != 1 || len(value) < 1 || len(value) > 128 || strings.IndexFunc(value, func(r rune) bool {
		return r <= ' ' || r == 0x7f
	}) >= 0 {
		return "", false
	}
	return value, true
}

func csrfToken(request *http.Request) (string, bool) {
	value, present, valid := singleHeader(request.Header, "X-AscendAny-CSRF")
	return value, valid && present && csrfTokenPattern.MatchString(value)
}

func requestBodyIsEmpty(request *http.Request) bool {
	return request.ContentLength == 0 && len(request.TransferEncoding) == 0
}
