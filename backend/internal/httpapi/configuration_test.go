package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

type configurationServiceStub struct {
	list     func(context.Context, string, *configuration.Kind, *string, int) (configuration.ItemPage, error)
	get      func(context.Context, string, string) (configuration.Item, bool, error)
	versions func(context.Context, string, string, *int64, int) (configuration.VersionPage, bool, error)
	create   func(context.Context, string, configuration.CreateVersionInput) (configuration.CreateVersionResult, error)
}

func (stub configurationServiceStub) List(ctx context.Context, access string, kind *configuration.Kind, afterKey *string, limit int) (configuration.ItemPage, error) {
	if stub.list == nil {
		panic("unexpected configuration list")
	}
	return stub.list(ctx, access, kind, afterKey, limit)
}

func (stub configurationServiceStub) Get(ctx context.Context, access, key string) (configuration.Item, bool, error) {
	if stub.get == nil {
		panic("unexpected configuration read")
	}
	return stub.get(ctx, access, key)
}

func (stub configurationServiceStub) ListVersions(ctx context.Context, access, key string, beforeNumber *int64, limit int) (configuration.VersionPage, bool, error) {
	if stub.versions == nil {
		panic("unexpected configuration version list")
	}
	return stub.versions(ctx, access, key, beforeNumber, limit)
}

func (stub configurationServiceStub) CreateVersion(ctx context.Context, access string, input configuration.CreateVersionInput) (configuration.CreateVersionResult, error) {
	if stub.create == nil {
		panic("unexpected configuration version create")
	}
	return stub.create(ctx, access, input)
}

func TestConfigurationReadRoutesUseCanonicalContracts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)
	listCalls := 0
	getCalls := 0
	versionCalls := 0
	service := configurationServiceStub{
		list: func(_ context.Context, access string, kind *configuration.Kind, afterKey *string, limit int) (configuration.ItemPage, error) {
			listCalls++
			if access != "admin-token" || kind == nil || *kind != configuration.KindPrompt || afterKey == nil || *afterKey != "prompt.default" || limit != 7 {
				t.Fatalf("list arguments access=%q kind=%v after=%v limit=%d", access, kind, afterKey, limit)
			}
			return configuration.ItemPage{Items: []configuration.Item{}}, nil
		},
		get: func(_ context.Context, access, key string) (configuration.Item, bool, error) {
			getCalls++
			if access != "admin-token" || key != "prompt.default" {
				t.Fatalf("get arguments access=%q key=%q", access, key)
			}
			return configuration.Item{ID: "11111111-1111-4111-8111-111111111111", Key: key, Kind: configuration.KindPrompt, CreatedAt: now, UpdatedAt: now}, true, nil
		},
		versions: func(_ context.Context, access, key string, before *int64, limit int) (configuration.VersionPage, bool, error) {
			versionCalls++
			if access != "admin-token" || key != "prompt.default" || before == nil || *before != 8 || limit != 3 {
				t.Fatalf("versions arguments access=%q key=%q before=%v limit=%d", access, key, before, limit)
			}
			return configuration.VersionPage{Key: key, Kind: configuration.KindPrompt, HeadRevision: 7, Items: []configuration.Version{}}, true, nil
		},
	}
	handler := newConfigurationTestHandler(t, service, true)

	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/api/v2/admin/configurations?kind=prompt&afterKey=prompt.default&limit=7", body: `{"items":[],"nextCursor":null}` + "\n"},
		{path: "/api/v2/admin/configurations/prompt.default", body: `{"id":"11111111-1111-4111-8111-111111111111","key":"prompt.default","kind":"prompt","headRevision":0,"activeVersion":null,"createdAt":"2026-07-11T09:00:00Z","updatedAt":"2026-07-11T09:00:00Z"}` + "\n"},
		{path: "/api/v2/admin/configurations/prompt.default/versions?beforeNumber=8&limit=3", body: `{"key":"prompt.default","kind":"prompt","headRevision":7,"items":[],"nextBeforeNumber":null}` + "\n"},
	} {
		request := configurationRequest(http.MethodGet, test.path, "")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != test.body {
			t.Fatalf("path=%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}

	for _, path := range []string{
		"/api/v2/admin/configurations?kind=unknown",
		"/api/v2/admin/configurations?afterKey=Bad",
		"/api/v2/admin/configurations?limit=01",
		"/api/v2/admin/configurations?limit=2&limit=3",
		"/api/v2/admin/configurations/prompt.default?x=1",
		"/api/v2/admin/configurations/Bad",
		"/api/v2/admin/configurations/prompt.default/versions?beforeNumber=1",
		"/api/v2/admin/configurations/prompt.default/versions?limit=101",
	} {
		request := configurationRequest(http.MethodGet, path, "")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if listCalls != 1 || getCalls != 1 || versionCalls != 1 {
		t.Fatalf("invalid contracts reached service list=%d get=%d versions=%d", listCalls, getCalls, versionCalls)
	}
}

func TestConfigurationVersionCreateDistinguishesCreationAndReplay(t *testing.T) {
	t.Parallel()
	created := 0
	service := configurationServiceStub{create: func(_ context.Context, access string, input configuration.CreateVersionInput) (configuration.CreateVersionResult, error) {
		created++
		if access != "admin-token" || input.Key != "prompt.default" || input.Kind != configuration.KindPrompt || input.ExpectedHeadRevision != int64(created-1) || input.SchemaID != "ascendany.prompt.v1" || input.CredentialRef != nil {
			t.Fatalf("create arguments access=%q input=%#v", access, input)
		}
		var document map[string]string
		if json.Unmarshal(input.Document, &document) != nil || document["system"] != "be exact" {
			t.Fatalf("document=%s", input.Document)
		}
		return configuration.CreateVersionResult{Idempotent: created == 2}, nil
	}}
	handler := newConfigurationTestHandler(t, service, true)
	for index, wantStatus := range []int{http.StatusCreated, http.StatusOK} {
		body := `{"key":"prompt.default","kind":"prompt","expectedHeadRevision":` + string(rune('0'+index)) + `,"schemaId":"ascendany.prompt.v1","document":{"system":"be exact"},"credentialRef":null}`
		request := configurationRequest(http.MethodPost, "/api/v2/admin/configurations/versions", body)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("index=%d status=%d body=%s", index, response.Code, response.Body.String())
		}
	}

	for _, body := range []string{
		`{"key":"prompt.default","kind":"prompt","expectedHeadRevision":2,"schemaId":"ascendany.prompt.v1","document":{}}`,
		`{"key":"prompt.default","kind":"prompt","expectedHeadRevision":2,"schemaId":"ascendany.prompt.v1","document":{},"credentialRef":null,"extra":1}`,
		`{"key":"prompt.default","kind":"prompt","expectedHeadRevision":2,"schemaId":"ascendany.prompt.v1","document":{},"document":{},"credentialRef":null}`,
	} {
		request := configurationRequest(http.MethodPost, "/api/v2/admin/configurations/versions", body)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	if created != 2 {
		t.Fatalf("invalid bodies reached service: %d", created)
	}

	disabled := newConfigurationTestHandler(t, service, false)
	request := configurationRequest(http.MethodPost, "/api/v2/admin/configurations/versions", `{"key":"prompt.default","kind":"prompt","expectedHeadRevision":2,"schemaId":"ascendany.prompt.v1","document":{},"credentialRef":null}`)
	response := newTestResponseRecorder()
	disabled.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || created != 2 {
		t.Fatalf("disabled status=%d calls=%d body=%s", response.Code, created, response.Body.String())
	}
}

func TestConfigurationErrorsUseOpaqueHTTPMapping(t *testing.T) {
	t.Parallel()
	secret := "database-password"
	for _, test := range []struct {
		code       configuration.ErrorCode
		wantStatus int
	}{
		{configuration.ErrorPrincipalRejected, http.StatusForbidden},
		{configuration.ErrorNotFound, http.StatusNotFound},
		{configuration.ErrorHeadConflict, http.StatusConflict},
		{configuration.ErrorDocumentConflict, http.StatusConflict},
		{configuration.ErrorDatabase, http.StatusInternalServerError},
	} {
		service := configurationServiceStub{get: func(context.Context, string, string) (configuration.Item, bool, error) {
			return configuration.Item{}, false, &configuration.Error{Code: test.code, Op: "read", Cause: errors.New(secret)}
		}}
		handler := newConfigurationTestHandler(t, service, true)
		request := configurationRequest(http.MethodGet, "/api/v2/admin/configurations/prompt.default", "")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus || strings.Contains(response.Body.String(), secret) {
			t.Fatalf("code=%s status=%d body=%s", test.code, response.Code, response.Body.String())
		}
	}
}

func TestConfigurationSemanticDocumentErrorUses422(t *testing.T) {
	t.Parallel()
	secret := "database-password"
	service := configurationServiceStub{create: func(context.Context, string, configuration.CreateVersionInput) (configuration.CreateVersionResult, error) {
		return configuration.CreateVersionResult{}, &configuration.Error{
			Code:  configuration.ErrorDocumentInvalid,
			Op:    "validate recommendation configuration",
			Cause: errors.New(secret),
		}
	}}
	handler := newConfigurationTestHandler(t, service, true)
	request := configurationRequest(http.MethodPost, "/api/v2/admin/configurations/versions", `{"key":"recommendation.training.default","kind":"training","expectedHeadRevision":0,"schemaId":"ascendany.training.recommendation.v2","document":{},"credentialRef":null}`)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"code":"configuration_document_invalid"`) || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestConfigurationStaticAndDynamicRouteCollisionIsMethodAware(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	getCalls := 0
	createCalls := 0
	service := configurationServiceStub{
		get: func(_ context.Context, _, key string) (configuration.Item, bool, error) {
			getCalls++
			return configuration.Item{
				ID: "11111111-1111-4111-8111-111111111111", Key: key, Kind: configuration.KindPrompt,
				CreatedAt: now, UpdatedAt: now,
			}, true, nil
		},
		create: func(_ context.Context, _ string, input configuration.CreateVersionInput) (configuration.CreateVersionResult, error) {
			createCalls++
			return configuration.CreateVersionResult{Item: configuration.Item{Key: input.Key}}, nil
		},
	}
	handler := newConfigurationTestHandler(t, service, true)

	get := configurationRequest(http.MethodGet, "/api/v2/admin/configurations/versions", "")
	getResponse := newTestResponseRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getCalls != 1 {
		t.Fatalf("GET status=%d calls=%d body=%s", getResponse.Code, getCalls, getResponse.Body.String())
	}

	post := configurationRequest(http.MethodPost, "/api/v2/admin/configurations/versions", `{"key":"versions","kind":"prompt","expectedHeadRevision":0,"schemaId":"ascendany.prompt.v1","document":{},"credentialRef":null}`)
	postResponse := newTestResponseRecorder()
	handler.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusCreated || createCalls != 1 {
		t.Fatalf("POST status=%d calls=%d body=%s", postResponse.Code, createCalls, postResponse.Body.String())
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		preflight := httptest.NewRequest(http.MethodOptions, "/api/v2/admin/configurations/versions", nil)
		preflight.RemoteAddr = "192.0.2.1:44000"
		preflight.Header.Set("Origin", "https://ascendany.example")
		preflight.Header.Set("Access-Control-Request-Method", method)
		preflight.Header.Set("Access-Control-Request-Headers", "Authorization"+map[bool]string{true: ", Content-Type"}[method == http.MethodPost])
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, preflight)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != method {
			t.Fatalf("preflight method=%s status=%d allow=%q body=%s", method, response.Code, response.Header().Get("Access-Control-Allow-Methods"), response.Body.String())
		}
	}
}

func newConfigurationTestHandler(t *testing.T, service ConfigurationService, writes bool) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Configuration = service
	options.Capabilities = testCapabilities(writes)
	if !writes {
		options.Artifacts = nil
		options.Imports = nil
		options.RecommendationQueue = nil
		options.ModelProbe = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func configurationRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer admin-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
