package principalguard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID = "123e4567-e89b-42d3-a456-426614174000"
	testSessionID = "123e4567-e89b-42d3-a456-426614174001"
	testJWTID     = "123e4567-e89b-42d3-a456-426614174002"
)

type rowFunc func(...any) error

func (row rowFunc) Scan(destinations ...any) error { return row(destinations...) }

type rowOwner struct {
	row       pgx.Row
	query     string
	arguments []any
}

func (owner *rowOwner) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	owner.query = query
	owner.arguments = arguments
	return owner.row
}

func TestResolveForUpdateLocksOnlyPrincipalRows(t *testing.T) {
	t.Parallel()
	owner := &rowOwner{row: validAdminRow()}
	principal := auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleAdmin, AuthRevision: 3,
	}
	if _, err := ResolveForUpdate(context.Background(), owner, principal, Roles(auth.RoleAdmin)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(owner.query, "FOR UPDATE OF account, session") {
		t.Fatalf("locking query=%q", owner.query)
	}
	owner = &rowOwner{row: validAdminRow()}
	if _, err := Resolve(context.Background(), owner, principal, Roles(auth.RoleAdmin)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(owner.query, "FOR UPDATE") {
		t.Fatalf("read query unexpectedly locks rows: %q", owner.query)
	}
}

func validAdminRow() pgx.Row {
	return rowFunc(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testAccountID
		*(destinations[2].(*string)) = string(auth.RoleAdmin)
		*(destinations[3].(*int64)) = 3
		*(destinations[4].(**int64)) = nil
		*(destinations[5].(**string)) = nil
		*(destinations[6].(*int64)) = 33
		*(destinations[7].(*string)) = testSessionID
		*(destinations[8].(*int64)) = 3
		*(destinations[9].(**int64)) = nil
		*(destinations[10].(**string)) = nil
		return nil
	})
}

func TestResolveBindsExactActiveStudentPrincipal(t *testing.T) {
	t.Parallel()
	studentNumber := "20260001"
	owner := &rowOwner{row: rowFunc(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testAccountID
		*(destinations[2].(*string)) = string(auth.RoleStudent)
		*(destinations[3].(*int64)) = 3
		actorID := int64(22)
		*(destinations[4].(**int64)) = &actorID
		*(destinations[5].(**string)) = &studentNumber
		*(destinations[6].(*int64)) = 33
		*(destinations[7].(*string)) = testSessionID
		*(destinations[8].(*int64)) = 3
		*(destinations[9].(**int64)) = &actorID
		*(destinations[10].(**string)) = &studentNumber
		return nil
	})}
	principal := auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 3,
	}
	resolved, err := Resolve(context.Background(), owner, principal, Roles(auth.RoleStudent, auth.RoleAdmin))
	if err != nil || resolved.ActorID == nil || *resolved.ActorID != 22 || resolved.StudentNumber == nil || *resolved.StudentNumber != studentNumber {
		t.Fatalf("resolved=%#v error=%v", resolved, err)
	}
	if len(owner.arguments) != 4 || owner.arguments[0] != testAccountID || owner.arguments[1] != int64(3) ||
		owner.arguments[2] != string(auth.RoleStudent) || owner.arguments[3] != testSessionID {
		t.Fatalf("arguments=%#v", owner.arguments)
	}
}

func TestResolveRejectsRoleAndChangedSessionWithoutFallback(t *testing.T) {
	t.Parallel()
	principal := auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 3,
	}
	owner := &rowOwner{row: rowFunc(func(...any) error { return pgx.ErrNoRows })}
	if _, err := Resolve(context.Background(), owner, principal, Roles(auth.RoleAdmin)); CodeOf(err) != ErrorRejected || owner.arguments != nil {
		t.Fatalf("role rejection error=%v args=%#v", err, owner.arguments)
	}
	if _, err := Resolve(context.Background(), owner, principal, Roles(auth.RoleStudent)); CodeOf(err) != ErrorRejected || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("session rejection error=%v code=%q", err, CodeOf(err))
	}
}

func TestResolveRejectsInconsistentStudentBinding(t *testing.T) {
	t.Parallel()
	studentNumber := "20260001"
	owner := &rowOwner{row: rowFunc(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testAccountID
		*(destinations[2].(*string)) = string(auth.RoleStudent)
		*(destinations[3].(*int64)) = 3
		actorID := int64(22)
		otherActorID := int64(23)
		*(destinations[4].(**int64)) = &actorID
		*(destinations[5].(**string)) = &studentNumber
		*(destinations[6].(*int64)) = 33
		*(destinations[7].(*string)) = testSessionID
		*(destinations[8].(*int64)) = 3
		*(destinations[9].(**int64)) = &otherActorID
		*(destinations[10].(**string)) = &studentNumber
		return nil
	})}
	principal := auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 3,
	}
	if _, err := Resolve(context.Background(), owner, principal, Roles(auth.RoleStudent)); CodeOf(err) != ErrorStoredData {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
}
