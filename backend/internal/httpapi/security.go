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
)

const (
	refreshCookieName             = "__Host-ascendany_refresh"
	cloudflareClientAddressHeader = "CF-Connecting-IP"
	opaqueBrowserOrigin           = "null"
)

var (
	csrfTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	headerNamePattern = regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`)
)

type routePolicy struct {
	method         string
	requiresWrites bool
	cookieMutation bool
	requestHeaders map[string]string
	rateScope      string
	bodyTimeout    time.Duration
}

type clientAddressResolver struct {
	trustedProxyCIDRs                  []netip.Prefix
	clientIPHeader                     string
	allowedLoopbackHTTPOrigins         map[string]struct{}
	opaqueAgentV1LoopbackOriginEnabled bool
}

func newClientAddressResolver(
	trustedProxyCIDRs []netip.Prefix,
	clientIPHeader string,
	canonicalAllowedOrigins []string,
) (clientAddressResolver, error) {
	allowedLoopbackHTTPOrigins := make(map[string]struct{})
	for _, origin := range canonicalAllowedOrigins {
		if strings.HasPrefix(origin, "http://") {
			allowedLoopbackHTTPOrigins[origin] = struct{}{}
		}
	}
	resolver := clientAddressResolver{
		allowedLoopbackHTTPOrigins:         allowedLoopbackHTTPOrigins,
		opaqueAgentV1LoopbackOriginEnabled: len(allowedLoopbackHTTPOrigins) > 0,
	}
	if len(trustedProxyCIDRs) == 0 {
		if clientIPHeader != "" {
			return clientAddressResolver{}, errInvalidClientAddressConfiguration
		}
		return resolver, nil
	}
	if clientIPHeader != cloudflareClientAddressHeader {
		return clientAddressResolver{}, errInvalidClientAddressConfiguration
	}
	prefixes := make([]netip.Prefix, len(trustedProxyCIDRs))
	for index, prefix := range trustedProxyCIDRs {
		if !prefix.IsValid() || prefix.Addr().Zone() != "" || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return clientAddressResolver{}, errInvalidClientAddressConfiguration
		}
		prefixes[index] = prefix
	}
	resolver.trustedProxyCIDRs = prefixes
	resolver.clientIPHeader = clientIPHeader
	return resolver, nil
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
	if !valid {
		return "", errInvalidClientAddress
	}
	if !present {
		origin, originPresent, originValid := singleHeader(request.Header, "Origin")
		_, originAllowed := resolver.allowedLoopbackHTTPOrigins[origin]
		opaqueOriginAllowed := origin == opaqueBrowserOrigin && resolver.allowsOpaqueAgentV1OriginFrom(request, direct)
		if direct.IsLoopback() && originValid && originPresent && (originAllowed || opaqueOriginAllowed) {
			return direct.String(), nil
		}
		return "", errInvalidClientAddress
	}
	address, err := netip.ParseAddr(forwarded)
	if err != nil || address.Zone() != "" || address.Is4In6() || address.String() != forwarded {
		return "", errInvalidClientAddress
	}
	return address.String(), nil
}

func (resolver clientAddressResolver) allowsOpaqueAgentV1Origin(request *http.Request) bool {
	direct, err := parseDirectClientAddress(request.RemoteAddr)
	return err == nil && resolver.allowsOpaqueAgentV1OriginFrom(request, direct)
}

func (resolver clientAddressResolver) allowsOpaqueAgentV1OriginFrom(request *http.Request, direct netip.Addr) bool {
	if !resolver.opaqueAgentV1LoopbackOriginEnabled || !isAgentV1Path(request.URL.Path) || !direct.IsLoopback() {
		return false
	}
	_, forwardedPresent, forwardedValid := singleHeader(request.Header, cloudflareClientAddressHeader)
	return forwardedValid && !forwardedPresent
}

func (handler *Handler) policyForMethod(path, method string) (routePolicy, bool) {
	return handler.routes.policyForMethod(path, method)
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
		allowedMethods, knownPath := handler.routes.methodsForPath(request.URL.Path)
		if request.Method != http.MethodOptions && !knownRoute && knownPath {
			writer.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			handler.writeAPIError(writer, request, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method is not allowed for this route.")
			return
		}
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
				handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
				return
			}
			request = guardedRequest
			defer lifetime.abortIfActive()
		} else if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
			return
		}
		if request.Method == http.MethodOptions {
			handler.handlePreflight(writer, request, policy, knownRoute || knownPath)
			return
		}
		if knownRoute {
			origin, present, valid := singleHeader(request.Header, "Origin")
			allowed := present && handler.requestOriginAllowed(request, origin)
			if !valid || (present && !allowed) || (policy.cookieMutation && !allowed) {
				handler.writeAPIError(writer, request, http.StatusForbidden, "origin_rejected", "Request Origin was rejected.")
				return
			}
			if present {
				handler.setCORSHeaders(writer.Header(), origin)
			}
			clientAddress, err := handler.clientAddress.Resolve(request)
			if err != nil {
				handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_client_address", "Client address is invalid.")
				return
			}
			if !handler.allowRate(writer, request, policy.rateScope, clientAddress) {
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
				handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "writes_disabled", "Write operations are disabled.")
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *Handler) allowRate(
	writer http.ResponseWriter,
	request *http.Request,
	scope string,
	key string,
) bool {
	decision := handler.rateLimiter.Allow(scope, key)
	if decision.Allowed {
		return true
	}
	retryAfter := int64((decision.RetryAfter + time.Second - 1) / time.Second)
	if retryAfter < 1 {
		retryAfter = 1
	}
	writer.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
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
	if !valid || !present || !handler.requestOriginAllowed(request, origin) {
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

func (handler *Handler) requestOriginAllowed(request *http.Request, origin string) bool {
	if handler.originAllowed(origin) {
		return true
	}
	return origin == opaqueBrowserOrigin && handler.clientAddress.allowsOpaqueAgentV1Origin(request)
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
