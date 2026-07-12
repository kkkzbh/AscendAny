package administration

import (
	"context"
	"errors"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type verifierStub struct {
	principal auth.AccessPrincipal
	err       error
	token     string
	calls     int
}

func (stub *verifierStub) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	stub.calls++
	stub.token = token
	return stub.principal, stub.err
}

type administrationStub struct {
	accountQuery AccountQuery
	studentQuery StudentQuery
	auditQuery   AuditQuery
	principal    auth.AccessPrincipal
	targetID     string
	disabled     bool
	calls        int
}

func (stub *administrationStub) ListAccounts(_ context.Context, query AccountQuery) (AccountPage, error) {
	stub.calls++
	stub.accountQuery = query
	return AccountPage{}, nil
}

func (stub *administrationStub) ListStudents(_ context.Context, query StudentQuery) (StudentPage, error) {
	stub.calls++
	stub.studentQuery = query
	return StudentPage{}, nil
}

func (stub *administrationStub) ListAudit(_ context.Context, query AuditQuery) (AuditPage, error) {
	stub.calls++
	stub.auditQuery = query
	return AuditPage{}, nil
}

func (stub *administrationStub) SetAccountDisabled(_ context.Context, principal auth.AccessPrincipal, target string, disabled bool) (ManagedAccount, error) {
	stub.calls++
	stub.principal = principal
	stub.targetID = target
	stub.disabled = disabled
	return ManagedAccount{}, nil
}

func TestApplicationPassesOnlyVerifiedPrincipal(t *testing.T) {
	t.Parallel()
	principal := testPrincipal()
	verifier := &verifierStub{principal: principal}
	owner := &administrationStub{}
	service, err := NewApplicationService(verifier, owner)
	if err != nil {
		t.Fatal(err)
	}
	cursor := testTargetID
	_, _ = service.ListAccounts(context.Background(), "admin-token", &cursor, 20)
	_, _ = service.ListStudents(context.Background(), "admin-token", nil, 30)
	_, _ = service.ListAudit(context.Background(), "admin-token", nil, 40)
	_, _ = service.SetAccountDisabled(context.Background(), "admin-token", testTargetID, true)
	if verifier.calls != 4 || verifier.token != "admin-token" || owner.calls != 4 ||
		owner.accountQuery.Principal != principal || owner.studentQuery.Principal != principal || owner.auditQuery.Principal != principal ||
		owner.principal != principal || owner.targetID != testTargetID || !owner.disabled {
		t.Fatalf("verifier=%#v owner=%#v", verifier, owner)
	}
}

func TestApplicationStopsWhenVerificationFails(t *testing.T) {
	t.Parallel()
	want := errors.New("rejected")
	verifier := &verifierStub{err: want}
	owner := &administrationStub{}
	service, _ := NewApplicationService(verifier, owner)
	if _, err := service.ListAccounts(context.Background(), "bad", nil, 20); !errors.Is(err, want) {
		t.Fatalf("ListAccounts() error=%v", err)
	}
	if _, err := service.SetAccountDisabled(context.Background(), "bad", testTargetID, true); !errors.Is(err, want) {
		t.Fatalf("SetAccountDisabled() error=%v", err)
	}
	if owner.calls != 0 {
		t.Fatalf("administration called after rejection: %d", owner.calls)
	}
}

func TestApplicationConstructorRequiresOwners(t *testing.T) {
	t.Parallel()
	if _, err := NewApplicationService(nil, &administrationStub{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil verifier error=%v", err)
	}
	if _, err := NewApplicationService(&verifierStub{}, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil administration error=%v", err)
	}
}
