package administration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAdminID   = "11111111-1111-4111-8111-111111111111"
	testSessionID = "22222222-2222-4222-8222-222222222222"
	testJWTID     = "33333333-3333-4333-8333-333333333333"
	testTargetID  = "44444444-4444-4444-8444-444444444444"
)

type repositoryStub struct {
	accounts      AccountPage
	students      StudentPage
	audit         AuditPage
	account       ManagedAccount
	err           error
	accountQuery  AccountQuery
	studentQuery  StudentQuery
	auditQuery    AuditQuery
	stateCommand  AccountStateCommand
	accountCalls  int
	studentCalls  int
	auditCalls    int
	mutationCalls int
}

func (stub *repositoryStub) LoadAccounts(_ context.Context, query AccountQuery) (AccountPage, error) {
	stub.accountCalls++
	stub.accountQuery = query
	return stub.accounts, stub.err
}

func (stub *repositoryStub) LoadStudents(_ context.Context, query StudentQuery) (StudentPage, error) {
	stub.studentCalls++
	stub.studentQuery = query
	return stub.students, stub.err
}

func (stub *repositoryStub) LoadAudit(_ context.Context, query AuditQuery) (AuditPage, error) {
	stub.auditCalls++
	stub.auditQuery = query
	return stub.audit, stub.err
}

func (stub *repositoryStub) SetAccountDisabled(_ context.Context, command AccountStateCommand) (ManagedAccount, error) {
	stub.mutationCalls++
	stub.stateCommand = command
	return stub.account, stub.err
}

func TestStudentCursorRoundTripAndCanonicalRejection(t *testing.T) {
	t.Parallel()
	cursor, err := EncodeStudentCursor("20260001")
	if err != nil {
		t.Fatal(err)
	}
	studentNumber, err := DecodeStudentCursor(cursor)
	if err != nil || studentNumber != "20260001" {
		t.Fatalf("decoded=%q error=%v", studentNumber, err)
	}
	for _, invalid := range []string{"", cursor + "=", "bm90LXRoZS1wcm90b2NvbA", "%%%"} {
		if _, err := DecodeStudentCursor(invalid); err == nil {
			t.Fatalf("DecodeStudentCursor(%q) succeeded", invalid)
		}
	}
}

func TestServiceReturnsValidatedAdministrationPages(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	studentCursor, _ := EncodeStudentCursor("20260001")
	repository := &repositoryStub{
		accounts: AccountPage{Items: []ManagedAccount{validManagedAccount(now)}},
		students: StudentPage{Items: []ManagedStudent{{
			StudentNumber:     "20260001",
			PintiaUserID:      "pintia-user",
			SourceDisplayName: stringPointer("Student"),
			Account: &StudentAccountBinding{
				ID: testTargetID, Username: "student_1", DisplayName: "Student",
			},
			Rating: int64Pointer(1510),
		}}, NextCursor: &studentCursor},
		audit: AuditPage{Items: []AuditEvent{{
			ID: "9", ActorAccountID: stringPointer(testAdminID), ActorSessionID: stringPointer(testSessionID),
			Type: "admin.account_disabled", OccurredAt: now, Payload: []byte(`{"disabled":true}`),
		}}},
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	principal := testPrincipal()
	accounts, err := service.ListAccounts(context.Background(), AccountQuery{Principal: principal, Limit: 20})
	if err != nil || len(accounts.Items) != 1 {
		t.Fatalf("accounts=%#v error=%v", accounts, err)
	}
	students, err := service.ListStudents(context.Background(), StudentQuery{Principal: principal, Limit: 20})
	if err != nil || len(students.Items) != 1 {
		t.Fatalf("students=%#v error=%v", students, err)
	}
	audit, err := service.ListAudit(context.Background(), AuditQuery{Principal: principal, Limit: 20})
	if err != nil || len(audit.Items) != 1 {
		t.Fatalf("audit=%#v error=%v", audit, err)
	}
	if repository.accountCalls != 1 || repository.studentCalls != 1 || repository.auditCalls != 1 {
		t.Fatalf("repository=%#v", repository)
	}
}

func TestServiceRejectsInvalidPrincipalAndPageShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	repository := &repositoryStub{accounts: AccountPage{Items: []ManagedAccount{}}}
	service, _ := NewService(repository)
	student := testPrincipal()
	student.Role = auth.RoleStudent
	if _, err := service.ListAccounts(context.Background(), AccountQuery{Principal: student, Limit: 20}); CodeOf(err) != ErrorPrincipalRejected || repository.accountCalls != 0 {
		t.Fatalf("student list error=%v calls=%d", err, repository.accountCalls)
	}
	if _, err := service.ListAccounts(context.Background(), AccountQuery{Principal: testPrincipal(), Limit: 0}); CodeOf(err) != ErrorInvalidQuery || repository.accountCalls != 0 {
		t.Fatalf("invalid limit error=%v calls=%d", err, repository.accountCalls)
	}
	repository.accounts = AccountPage{Items: []ManagedAccount{validManagedAccount(now), validManagedAccount(now)}}
	if _, err := service.ListAccounts(context.Background(), AccountQuery{Principal: testPrincipal(), Limit: 20}); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("duplicate page error=%v", err)
	}
}

func TestServiceValidatesAccountStateResult(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.FixedZone("+08", 8*60*60))
	disabledAt := now.UTC()
	account := validManagedAccount(now.UTC())
	account.DisabledAt = &disabledAt
	account.ActiveSessionCount = 0
	repository := &repositoryStub{account: account}
	service, _ := NewService(repository)
	result, err := service.SetAccountDisabled(context.Background(), testPrincipal(), testTargetID, true)
	if err != nil || result.DisabledAt == nil || repository.mutationCalls != 1 {
		t.Fatalf("result=%#v command=%#v error=%v", result, repository.stateCommand, err)
	}
	repository.account.ID = testAdminID
	if _, err := service.SetAccountDisabled(context.Background(), testPrincipal(), testTargetID, true); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("wrong target result error=%v", err)
	}
	repository.err = adminError(ErrorTargetNotFound, "test", errors.New("missing"))
	if _, err := service.SetAccountDisabled(context.Background(), testPrincipal(), testTargetID, true); CodeOf(err) != ErrorTargetNotFound {
		t.Fatalf("owned repository error=%v", err)
	}
}

func TestConstructorsRequireDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil repository error=%v", err)
	}
	if _, err := NewPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil pool error=%v", err)
	}
	if _, err := newPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil begin error=%v", err)
	}
}

func testPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{AccountID: testAdminID, SessionID: testSessionID, JWTID: testJWTID, Role: auth.RoleAdmin, AuthRevision: 2}
}

func validManagedAccount(now time.Time) ManagedAccount {
	return ManagedAccount{
		ID: testTargetID, Username: "student_1", DisplayName: "Student", StudentNumber: stringPointer("20260001"),
		Role: auth.RoleStudent, AuthRevision: 1, CreatedAt: now, UpdatedAt: now, ActiveSessionCount: 1,
	}
}

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }
