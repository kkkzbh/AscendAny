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

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

const testManagedSessionID = "123e4567-e89b-42d3-a456-426614174081"

type stubAccountManagementService struct {
	update func(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error)
	list   func(context.Context, string) ([]auth.ManagedSession, error)
	revoke func(context.Context, string, string) (bool, error)
}

func (service stubAccountManagementService) UpdateProfile(
	ctx context.Context,
	token string,
	input auth.ProfileUpdateInput,
) (auth.Account, error) {
	if service.update == nil {
		panic("unexpected profile update")
	}
	return service.update(ctx, token, input)
}

func (service stubAccountManagementService) ListSessions(
	ctx context.Context,
	token string,
) ([]auth.ManagedSession, error) {
	if service.list == nil {
		panic("unexpected session list")
	}
	return service.list(ctx, token)
}

func (service stubAccountManagementService) RevokeSession(
	ctx context.Context,
	token string,
	targetID string,
) (bool, error) {
	if service.revoke == nil {
		panic("unexpected session revocation")
	}
	return service.revoke(ctx, token, targetID)
}

func TestAccountProfileUpdateUsesAuthenticatedStrictContract(t *testing.T) {
	t.Parallel()
	studentNumber := "20260001"
	service := stubAccountManagementService{update: func(
		_ context.Context,
		token string,
		input auth.ProfileUpdateInput,
	) (auth.Account, error) {
		if token != "account-token" || input.DisplayName != "Updated Student" {
			t.Fatalf("profile input token=%q input=%#v", token, input)
		}
		return auth.Account{
			ID:            "123e4567-e89b-42d3-a456-426614174000",
			Username:      "student_1",
			DisplayName:   input.DisplayName,
			StudentNumber: &studentNumber,
			Role:          auth.RoleStudent,
			AuthRevision:  3,
		}, nil
	}}
	handler := newAccountTestHandler(t, true, service)
	request := authRequest(http.MethodPatch, "/api/v2/account/profile", `{"displayName":"Updated Student"}`)
	request.Header.Set("Authorization", "Bearer account-token")
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != `{"id":"123e4567-e89b-42d3-a456-426614174000","username":"student_1","displayName":"Updated Student","studentNumber":"20260001","role":"student","authRevision":3}`+"\n" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestAccountSessionListReturnsBoundedPublicState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 4, 5, 6, 0, time.UTC)
	service := stubAccountManagementService{list: func(
		_ context.Context,
		token string,
	) ([]auth.ManagedSession, error) {
		if token != "account-token" {
			t.Fatalf("token=%q", token)
		}
		return []auth.ManagedSession{{
			ID:         testManagedSessionID,
			CreatedAt:  now.Add(-time.Hour),
			ExpiresAt:  now.Add(time.Hour),
			LastSeenAt: now,
			Current:    true,
			Active:     true,
		}}, nil
	}}
	handler := newAccountTestHandler(t, true, service)
	request := authRequest(http.MethodGet, "/api/v2/account/sessions", "")
	request.Header.Set("Authorization", "Bearer account-token")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	var payload accountSessionListResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil ||
		len(payload.Items) != 1 || !payload.Items[0].Current || !payload.Items[0].Active ||
		payload.Items[0].ID != testManagedSessionID {
		t.Fatalf("response=%d payload=%#v body=%s", response.Code, payload, response.Body.String())
	}
}

func TestAccountSessionRevocationClearsOnlyCurrentBrowserCookie(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		current       bool
		wantSetCookie bool
	}{
		{name: "current", current: true, wantSetCookie: true},
		{name: "other", current: false, wantSetCookie: false},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := stubAccountManagementService{revoke: func(
				_ context.Context,
				token string,
				targetID string,
			) (bool, error) {
				if token != "account-token" || targetID != testManagedSessionID {
					t.Fatalf("revocation token=%q target=%q", token, targetID)
				}
				return test.current, nil
			}}
			handler := newAccountTestHandler(t, true, service)
			request := authRequest(http.MethodDelete, "/api/v2/account/sessions/"+testManagedSessionID, "")
			request.Header.Set("Authorization", "Bearer account-token")
			response := newTestResponseRecorder()

			handler.ServeHTTP(response, request)

			cookie := response.Header().Get("Set-Cookie")
			if response.Code != http.StatusNoContent || (cookie != "") != test.wantSetCookie {
				t.Fatalf("response=%d cookie=%q body=%s", response.Code, cookie, response.Body.String())
			}
			if test.current && (!strings.Contains(cookie, "Max-Age=0") || !strings.Contains(cookie, "HttpOnly")) {
				t.Fatalf("current-session cookie was not cleared securely: %q", cookie)
			}
		})
	}
}

func TestAccountRoutesRejectInvalidContractsBeforeService(t *testing.T) {
	t.Parallel()
	service := stubAccountManagementService{
		update: func(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error) {
			t.Fatal("invalid profile request reached service")
			return auth.Account{}, nil
		},
		list: func(context.Context, string) ([]auth.ManagedSession, error) {
			t.Fatal("invalid list request reached service")
			return nil, nil
		},
		revoke: func(context.Context, string, string) (bool, error) {
			t.Fatal("invalid revoke request reached service")
			return false, nil
		},
	}
	handler := newAccountTestHandler(t, true, service)
	tests := []struct {
		method      string
		path        string
		body        string
		contentType string
		bearer      bool
		wantStatus  int
	}{
		{method: http.MethodPatch, path: "/api/v2/account/profile", body: `{"displayName":"Name","extra":true}`, contentType: "application/json", bearer: true, wantStatus: http.StatusBadRequest},
		{method: http.MethodPatch, path: "/api/v2/account/profile?extra=1", body: `{"displayName":"Name"}`, contentType: "application/json", bearer: true, wantStatus: http.StatusBadRequest},
		{method: http.MethodPatch, path: "/api/v2/account/profile", body: `{"displayName":"Name"}`, contentType: "application/json", wantStatus: http.StatusUnauthorized},
		{method: http.MethodGet, path: "/api/v2/account/sessions?", bearer: true, wantStatus: http.StatusBadRequest},
		{method: http.MethodGet, path: "/api/v2/account/sessions", body: `{}`, bearer: true, wantStatus: http.StatusBadRequest},
		{method: http.MethodDelete, path: "/api/v2/account/sessions/" + testManagedSessionID + "?extra=1", bearer: true, wantStatus: http.StatusBadRequest},
		{method: http.MethodDelete, path: "/api/v2/account/sessions/" + testManagedSessionID, body: `{}`, bearer: true, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		request := authRequest(test.method, test.path, test.body)
		if test.contentType != "" {
			request.Header.Set("Content-Type", test.contentType)
		}
		if test.bearer {
			request.Header.Set("Authorization", "Bearer account-token")
		}
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Fatalf("%s %s response=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestAccountSessionNotFoundIsPublic404WithoutCauseLeak(t *testing.T) {
	t.Parallel()
	secret := "internal-session-row-42"
	service := stubAccountManagementService{revoke: func(context.Context, string, string) (bool, error) {
		return false, &auth.Error{Code: auth.ErrorSessionNotFound, Message: secret, Cause: errors.New(secret)}
	}}
	handler := newAccountTestHandler(t, true, service)
	request := authRequest(http.MethodDelete, "/api/v2/account/sessions/"+testManagedSessionID, "")
	request.Header.Set("Authorization", "Bearer account-token")
	response := newTestResponseRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), secret) ||
		!strings.Contains(response.Body.String(), `"code":"auth_session_not_found"`) {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAccountMutationsRespectWriteCapability(t *testing.T) {
	t.Parallel()
	service := stubAccountManagementService{
		update: func(context.Context, string, auth.ProfileUpdateInput) (auth.Account, error) {
			t.Fatal("disabled profile update reached service")
			return auth.Account{}, nil
		},
		revoke: func(context.Context, string, string) (bool, error) {
			t.Fatal("disabled session revocation reached service")
			return false, nil
		},
	}
	handler := newAccountTestHandler(t, false, service)
	for _, request := range []*http.Request{
		authRequest(http.MethodPatch, "/api/v2/account/profile", `{"displayName":"Name"}`),
		authRequest(http.MethodDelete, "/api/v2/account/sessions/"+testManagedSessionID, ""),
	} {
		request.Header.Set("Authorization", "Bearer account-token")
		request.Header.Set("Content-Type", "application/json")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestAccountRoutesAcceptExactCORSPreflights(t *testing.T) {
	t.Parallel()
	handler := newAccountTestHandler(t, true, unusedAccountManagementService{})
	for _, test := range []struct {
		path    string
		method  string
		headers string
	}{
		{path: "/api/v2/account/profile", method: http.MethodPatch, headers: "Authorization, Content-Type"},
		{path: "/api/v2/account/sessions", method: http.MethodGet, headers: "Authorization"},
		{path: "/api/v2/account/sessions/" + testManagedSessionID, method: http.MethodDelete, headers: "Authorization"},
	} {
		request := httptest.NewRequest(http.MethodOptions, test.path, nil)
		request.RemoteAddr = "192.0.2.1:54321"
		request.Header.Set("Origin", testWebOrigin)
		request.Header.Set("Access-Control-Request-Method", test.method)
		request.Header.Set("Access-Control-Request-Headers", test.headers)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != test.method {
			t.Fatalf("%s preflight=%d headers=%#v body=%s", test.path, response.Code, response.Header(), response.Body.String())
		}
	}
}

func newAccountTestHandler(t *testing.T, writesEnabled bool, service AccountManagementService) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.AccountManagement = service
	options.Capabilities = testCapabilities(writesEnabled)
	if !writesEnabled {
		options.Artifacts = nil
		options.Imports = nil
		options.ModelProbe = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
