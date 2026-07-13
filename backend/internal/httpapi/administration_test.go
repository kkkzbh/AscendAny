package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/administration"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

const testManagedAccountID = "77777777-7777-4777-8777-777777777777"

type administrationServiceStub struct {
	accounts func(context.Context, string, *string, int) (administration.AccountPage, error)
	students func(context.Context, string, *string, int) (administration.StudentPage, error)
	audit    func(context.Context, string, *string, int) (administration.AuditPage, error)
	state    func(context.Context, string, string, bool) (administration.ManagedAccount, error)
	calls    []string
	access   string
	cursor   *string
	limit    int
	targetID string
	disabled bool
}

func (stub *administrationServiceStub) ListAccounts(ctx context.Context, access string, cursor *string, limit int) (administration.AccountPage, error) {
	stub.calls = append(stub.calls, "accounts")
	stub.record(access, cursor, limit)
	return stub.accounts(ctx, access, cursor, limit)
}

func (stub *administrationServiceStub) ListStudents(ctx context.Context, access string, cursor *string, limit int) (administration.StudentPage, error) {
	stub.calls = append(stub.calls, "students")
	stub.record(access, cursor, limit)
	return stub.students(ctx, access, cursor, limit)
}

func (stub *administrationServiceStub) ListAudit(ctx context.Context, access string, cursor *string, limit int) (administration.AuditPage, error) {
	stub.calls = append(stub.calls, "audit")
	stub.record(access, cursor, limit)
	return stub.audit(ctx, access, cursor, limit)
}

func (stub *administrationServiceStub) SetAccountDisabled(ctx context.Context, access, targetID string, disabled bool) (administration.ManagedAccount, error) {
	stub.calls = append(stub.calls, "state")
	stub.access = access
	stub.targetID = targetID
	stub.disabled = disabled
	return stub.state(ctx, access, targetID, disabled)
}

func (stub *administrationServiceStub) record(access string, cursor *string, limit int) {
	stub.access = access
	stub.cursor = cursor
	stub.limit = limit
}

func TestAdministrationListRoutesUseDomainSpecificCursors(t *testing.T) {
	t.Parallel()
	studentCursor, err := administration.EncodeStudentCursor("20260001")
	if err != nil {
		t.Fatal(err)
	}
	service := &administrationServiceStub{
		accounts: func(context.Context, string, *string, int) (administration.AccountPage, error) {
			return administration.AccountPage{Items: []administration.ManagedAccount{}}, nil
		},
		students: func(context.Context, string, *string, int) (administration.StudentPage, error) {
			return administration.StudentPage{Items: []administration.ManagedStudent{}}, nil
		},
		audit: func(context.Context, string, *string, int) (administration.AuditPage, error) {
			return administration.AuditPage{Items: []administration.AuditEvent{}}, nil
		},
		state: func(context.Context, string, string, bool) (administration.ManagedAccount, error) {
			panic("unexpected state call")
		},
	}
	handler := newAdministrationTestHandler(t, service, true)
	paths := []string{
		"/api/v2/admin/accounts?cursor=" + testManagedAccountID + "&limit=11",
		"/api/v2/admin/students?cursor=" + studentCursor + "&limit=12",
		"/api/v2/admin/audit-events?cursor=91&limit=13",
	}
	for _, path := range paths {
		request := administrationRequest(http.MethodGet, path, "")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != `{"items":[],"nextCursor":null}`+"\n" {
			t.Fatalf("path=%q status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if strings.Join(service.calls, ",") != "accounts,students,audit" || service.access != "admin-access" || service.limit != 13 || service.cursor == nil || *service.cursor != "91" {
		t.Fatalf("service=%#v", service)
	}

	for _, path := range []string{
		"/api/v2/admin/accounts?cursor=invalid",
		"/api/v2/admin/students?cursor=invalid",
		"/api/v2/admin/audit-events?cursor=01",
		"/api/v2/admin/accounts?limit=101",
	} {
		request := administrationRequest(http.MethodGet, path, "")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid path=%q status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if len(service.calls) != 3 {
		t.Fatalf("invalid cursor reached service: calls=%v", service.calls)
	}
}

func TestManagedAccountStateUsesStrictWriteContract(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	disabledAt := now
	service := &administrationServiceStub{
		accounts: func(context.Context, string, *string, int) (administration.AccountPage, error) { panic("unexpected") },
		students: func(context.Context, string, *string, int) (administration.StudentPage, error) { panic("unexpected") },
		audit:    func(context.Context, string, *string, int) (administration.AuditPage, error) { panic("unexpected") },
		state: func(context.Context, string, string, bool) (administration.ManagedAccount, error) {
			studentNumber := "20260001"
			return administration.ManagedAccount{
				ID: testManagedAccountID, Username: "student_1", DisplayName: "Student", StudentNumber: &studentNumber,
				Role: "student", AuthRevision: 2, DisabledAt: &disabledAt, CreatedAt: now, UpdatedAt: now,
			}, nil
		},
	}
	handler := newAdministrationTestHandler(t, service, true)
	request := administrationRequest(http.MethodPatch, "/api/v2/admin/accounts/"+testManagedAccountID+"/state", `{"disabled":true}`)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.access != "admin-access" || service.targetID != testManagedAccountID || !service.disabled {
		t.Fatalf("status=%d service=%#v body=%s", response.Code, service, response.Body.String())
	}

	for _, body := range []string{`{}`, `{"disabled":true,"extra":1}`, `{"disabled":true,"disabled":false}`} {
		invalid := administrationRequest(http.MethodPatch, "/api/v2/admin/accounts/"+testManagedAccountID+"/state", body)
		invalidResponse := newTestResponseRecorder()
		handler.ServeHTTP(invalidResponse, invalid)
		if invalidResponse.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, invalidResponse.Code, invalidResponse.Body.String())
		}
	}
	if len(service.calls) != 1 {
		t.Fatalf("invalid payload reached service: calls=%v", service.calls)
	}

	disabledHandler := newAdministrationTestHandler(t, service, false)
	disabled := administrationRequest(http.MethodPatch, "/api/v2/admin/accounts/"+testManagedAccountID+"/state", `{"disabled":true}`)
	disabledResponse := newTestResponseRecorder()
	disabledHandler.ServeHTTP(disabledResponse, disabled)
	if disabledResponse.Code != http.StatusServiceUnavailable || len(service.calls) != 1 {
		t.Fatalf("writes disabled status=%d calls=%v body=%s", disabledResponse.Code, service.calls, disabledResponse.Body.String())
	}
}

func TestAdministrationErrorsDoNotLeakStorageDetail(t *testing.T) {
	t.Parallel()
	secret := "postgres-secret"
	service := &administrationServiceStub{
		accounts: func(context.Context, string, *string, int) (administration.AccountPage, error) {
			return administration.AccountPage{}, &administration.Error{Code: administration.ErrorDatabase, Op: "read", Cause: errors.New(secret)}
		},
		students: func(context.Context, string, *string, int) (administration.StudentPage, error) { panic("unexpected") },
		audit:    func(context.Context, string, *string, int) (administration.AuditPage, error) { panic("unexpected") },
		state: func(context.Context, string, string, bool) (administration.ManagedAccount, error) {
			panic("unexpected")
		},
	}
	handler := newAdministrationTestHandler(t, service, true)
	request := administrationRequest(http.MethodGet, "/api/v2/admin/accounts", "")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newAdministrationTestHandler(t *testing.T, service AdministrationService, writes bool) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Administration = service
	options.Capabilities = testCapabilities(writes)
	if !writes {
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

func administrationRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer admin-access")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
