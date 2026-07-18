package publicdelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedPublicAssetsMatchManifest(t *testing.T) {
	handler, err := New(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path        string
		contains    string
		contentType string
	}{
		{path: "/", contains: "AscendAny", contentType: "text/html; charset=utf-8"},
		{path: "/app/", contains: "/app/assets/", contentType: "text/html; charset=utf-8"},
		{path: "/admin/", contains: "/admin/assets/", contentType: "text/html; charset=utf-8"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != test.contentType ||
			!strings.Contains(response.Body.String(), test.contains) {
			t.Fatalf("GET %s returned status=%d type=%q body=%q", test.path, response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
	}
}

func TestTrueTypeFontContentType(t *testing.T) {
	contentType, ok := contentTypeForPath("app/assets/KaTeX_Main-Regular-12345678.ttf")
	if !ok || contentType != "font/ttf" {
		t.Fatalf("TrueType content type = %q, supported=%t", contentType, ok)
	}
}

func TestPublicDeliveryOwnsOnlyStaticRoutes(t *testing.T) {
	handler := fixtureHandler(t, http.NotFoundHandler())
	tests := []struct {
		name     string
		path     string
		accept   string
		status   int
		body     string
		location string
	}{
		{name: "site", path: "/", status: http.StatusOK, body: "site-index"},
		{name: "app", path: "/app/", status: http.StatusOK, body: "app-index"},
		{name: "app fallback", path: "/app/exams/42", accept: "text/html,application/xhtml+xml", status: http.StatusOK, body: "app-index"},
		{name: "app fallback with canonical quality", path: "/app/exams/42", accept: "text/html;q=0.500", status: http.StatusOK, body: "app-index"},
		{name: "app fallback with later positive quality", path: "/app/exams/42", accept: "text/html;q=0,text/html;q=1", status: http.StatusOK, body: "app-index"},
		{name: "admin fallback", path: "/admin/students", accept: "text/html", status: http.StatusOK, body: "admin-index"},
		{name: "fallback requires html", path: "/app/exams/42", accept: "application/json", status: http.StatusNotFound},
		{name: "fallback rejects zero-quality html", path: "/app/exams/42", accept: "text/html;q=0,application/json", status: http.StatusNotFound},
		{name: "fallback rejects malformed html quality", path: "/app/exams/42", accept: "text/html;q=invalid", status: http.StatusNotFound},
		{name: "fallback rejects leading-dot quality", path: "/app/exams/42", accept: "text/html;q=.5", status: http.StatusNotFound},
		{name: "fallback rejects exponent quality", path: "/app/exams/42", accept: "text/html;q=1e-1", status: http.StatusNotFound},
		{name: "fallback rejects overprecise quality", path: "/app/exams/42", accept: "text/html;q=1.0000", status: http.StatusNotFound},
		{name: "missing asset never falls back", path: "/app/assets/missing.js", accept: "text/html", status: http.StatusNotFound},
		{name: "site has no spa fallback", path: "/marketing/deep-link", accept: "text/html", status: http.StatusNotFound},
		{name: "app canonical redirect", path: "/app", status: http.StatusPermanentRedirect, location: "/app/"},
		{name: "admin canonical redirect", path: "/admin", status: http.StatusPermanentRedirect, location: "/admin/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || (test.body != "" && response.Body.String() != test.body) ||
				(test.location != "" && response.Header().Get("Location") != test.location) {
				t.Fatalf("response status=%d location=%q body=%q", response.Code, response.Header().Get("Location"), response.Body.String())
			}
			if strings.Contains(test.name, "fallback") && test.status == http.StatusOK && response.Header().Get("Vary") != "Accept" {
				t.Fatalf("successful SPA fallback Vary = %q", response.Header().Get("Vary"))
			}
		})
	}
}

func TestPublicDeliveryPassesAPIHealthSSEAndWebSocketUnchanged(t *testing.T) {
	var calls []string
	var websocketWriter http.ResponseWriter
	api := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		switch {
		case strings.HasSuffix(request.URL.Path, "/events"):
			if _, ok := writer.(http.Flusher); !ok {
				t.Fatal("SSE response writer lost http.Flusher")
			}
			writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("event: ready\n\n"))
			writer.(http.Flusher).Flush()
		case strings.HasSuffix(request.URL.Path, "/websocket"):
			websocketWriter = writer
			if request.Header.Get("Upgrade") != "websocket" || request.Header.Get("Sec-WebSocket-Protocol") != "ascendany.lsp.v1, ascendany.lsp.ticket.test" {
				t.Fatal("WebSocket handshake headers changed")
			}
			writer.WriteHeader(http.StatusSwitchingProtocols)
		default:
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
		}
	})
	handler := fixtureHandler(t, api)

	for _, path := range []string{"/api/v1/auth/policy", "/api/v2/capabilities", "/livez", "/readyz", "/version"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Security-Policy") != "" {
			t.Fatalf("API pass-through %s returned status=%d static_csp=%q", path, response.Code, response.Header().Get("Content-Security-Policy"))
		}
	}

	sse := httptest.NewRecorder()
	handler.ServeHTTP(sse, httptest.NewRequest(http.MethodGet, "/api/v2/imports/123/events", nil))
	if sse.Code != http.StatusOK || sse.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" || sse.Body.String() != "event: ready\n\n" {
		t.Fatalf("SSE changed: status=%d type=%q body=%q", sse.Code, sse.Header().Get("Content-Type"), sse.Body.String())
	}

	websocketRequest := httptest.NewRequest(http.MethodGet, "/api/v2/lsp/sessions/123/websocket", nil)
	websocketRequest.Header.Set("Connection", "Upgrade")
	websocketRequest.Header.Set("Upgrade", "websocket")
	websocketRequest.Header.Set("Sec-WebSocket-Protocol", "ascendany.lsp.v1, ascendany.lsp.ticket.test")
	websocketResponse := httptest.NewRecorder()
	handler.ServeHTTP(websocketResponse, websocketRequest)
	if websocketResponse.Code != http.StatusSwitchingProtocols || websocketWriter != websocketResponse {
		t.Fatalf("WebSocket status = %d", websocketResponse.Code)
	}
	if len(calls) != 7 {
		t.Fatalf("API calls = %v", calls)
	}
}

func TestPublicDeliveryRejectsAmbiguousAndTraversalPaths(t *testing.T) {
	apiCalled := false
	handler := fixtureHandler(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { apiCalled = true }))
	for _, target := range []string{
		"https://ascendany.example/app/../admin/",
		"https://ascendany.example/app//exams",
		"https://ascendany.example/app/%2e%2e/admin/",
		"https://ascendany.example/app/%2fadmin/",
		"https://ascendany.example/app/%5cadmin/",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d", target, response.Code)
		}
	}
	if apiCalled {
		t.Fatal("static traversal reached the API")
	}
}

func TestPublicDeliverySetsDeterministicCacheContentAndSecurityHeaders(t *testing.T) {
	handler := fixtureHandler(t, http.NotFoundHandler())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app/assets/main-12345678.js", nil))
	if response.Code != http.StatusOK || response.Body.String() != "console.log('app')" {
		t.Fatalf("asset response status=%d body=%q", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{
		"Cache-Control":              "public, max-age=31536000, immutable",
		"Content-Type":               "text/javascript; charset=utf-8",
		"Content-Security-Policy":    staticContentSecurityPolicy,
		"Cross-Origin-Opener-Policy": "same-origin",
		"Permissions-Policy":         "camera=(), geolocation=(), microphone=()",
		"Referrer-Policy":            "no-referrer",
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "font-src 'self' data:") {
		t.Fatalf("Agent app CSP blocks its embedded data font: %q", response.Header().Get("Content-Security-Policy"))
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("immutable asset has no ETag")
	}

	revalidatedRequest := httptest.NewRequest(http.MethodGet, "/app/assets/main-12345678.js", nil)
	revalidatedRequest.Header.Set("If-None-Match", etag)
	revalidated := httptest.NewRecorder()
	handler.ServeHTTP(revalidated, revalidatedRequest)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 {
		t.Fatalf("revalidation status=%d body=%q", revalidated.Code, revalidated.Body.String())
	}

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/app/assets/main-12345678.js", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD status=%d length=%q body=%q", head.Code, head.Header().Get("Content-Length"), head.Body.String())
	}

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/app/", nil))
	if index.Header().Get("Cache-Control") != "no-cache" || index.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
		strings.Contains(index.Header().Get("Content-Security-Policy"), "api.github.com") {
		t.Fatalf("index cache=%q type=%q", index.Header().Get("Cache-Control"), index.Header().Get("Content-Type"))
	}

	site := httptest.NewRecorder()
	handler.ServeHTTP(site, httptest.NewRequest(http.MethodGet, "/", nil))
	if site.Header().Get("Content-Security-Policy") != siteContentSecurityPolicy || !strings.Contains(site.Header().Get("Content-Security-Policy"), "https://api.github.com") {
		t.Fatalf("site CSP = %q", site.Header().Get("Content-Security-Policy"))
	}
}

func TestPublicDeliveryRejectsStaticMutations(t *testing.T) {
	handler := fixtureHandler(t, http.NotFoundHandler())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/app/", strings.NewReader("payload")))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d allow=%q cache=%q", response.Code, response.Header().Get("Allow"), response.Header().Get("Cache-Control"))
	}
}

func TestPublicDeliveryManifestValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, tree fstest.MapFS)
	}{
		{
			name: "unknown manifest field",
			mutate: func(t *testing.T, tree fstest.MapFS) {
				var value map[string]any
				decodeManifest(t, tree, &value)
				value["unexpected"] = true
				encodeManifest(t, tree, value)
			},
		},
		{
			name: "digest mismatch",
			mutate: func(t *testing.T, tree fstest.MapFS) {
				var value manifest
				decodeManifest(t, tree, &value)
				value.Files[0].SHA256 = strings.Repeat("0", 64)
				encodeManifest(t, tree, value)
			},
		},
		{
			name: "unlisted file",
			mutate: func(_ *testing.T, tree fstest.MapFS) {
				tree["assets/site/extra.json"] = &fstest.MapFile{Data: []byte("{}")}
			},
		},
		{
			name: "traversal path",
			mutate: func(t *testing.T, tree fstest.MapFS) {
				var value manifest
				decodeManifest(t, tree, &value)
				value.Files[0].Path = "../outside.js"
				encodeManifest(t, tree, value)
			},
		},
		{
			name: "cache mismatch",
			mutate: func(t *testing.T, tree fstest.MapFS) {
				var value manifest
				decodeManifest(t, tree, &value)
				value.Files[0].Cache = "revalidate"
				encodeManifest(t, tree, value)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tree := fixtureAssets(t)
			test.mutate(t, tree)
			if _, err := newHandler(http.NotFoundHandler(), tree); err == nil {
				t.Fatal("invalid public asset tree was accepted")
			}
		})
	}
}

func fixtureHandler(t *testing.T, api http.Handler) *Handler {
	t.Helper()
	handler, err := newHandler(api, fixtureAssets(t))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func fixtureAssets(t *testing.T) fstest.MapFS {
	t.Helper()
	bodies := map[string][]byte{
		"admin/assets/main-12345678.js": []byte("console.log('admin')"),
		"admin/index.html":              []byte("admin-index"),
		"app/assets/main-12345678.js":   []byte("console.log('app')"),
		"app/index.html":                []byte("app-index"),
		"site/assets/main-12345678.css": []byte("body{}"),
		"site/index.html":               []byte("site-index"),
	}
	paths := make([]string, 0, len(bodies))
	for path := range bodies {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	declared := manifest{
		Schema: manifestSchema,
		Routes: manifestRoutes{Site: "/", StudentWeb: "/app/", ImportConsole: "/admin/"},
		Files:  make([]manifestFile, 0, len(paths)),
	}
	tree := fstest.MapFS{}
	for _, path := range paths {
		body := bodies[path]
		digest := sha256.Sum256(body)
		cache := "revalidate"
		if strings.Contains(path, "/assets/") {
			cache = "immutable"
		}
		declared.Files = append(declared.Files, manifestFile{
			Path: path, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(body)), Cache: cache,
		})
		tree["assets/"+path] = &fstest.MapFile{Data: body, Mode: 0o444}
	}
	encodeManifest(t, tree, declared)
	return tree
}

func decodeManifest(t *testing.T, tree fstest.MapFS, value any) {
	t.Helper()
	if err := json.Unmarshal(tree[manifestPath].Data, value); err != nil {
		t.Fatal(err)
	}
}

func encodeManifest(t *testing.T, tree fstest.MapFS, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	tree[manifestPath] = &fstest.MapFile{Data: body, Mode: fs.FileMode(0o444)}
}
