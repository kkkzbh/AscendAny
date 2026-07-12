package examcatalog

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

type catalogStub struct {
	page        Page
	detail      Detail
	found       bool
	listQuery   ListQuery
	detailQuery DetailQuery
	listCalls   int
	getCalls    int
}

func (stub *catalogStub) List(_ context.Context, query ListQuery) (Page, error) {
	stub.listCalls++
	stub.listQuery = query
	return stub.page, nil
}

func (stub *catalogStub) Get(_ context.Context, query DetailQuery) (Detail, bool, error) {
	stub.getCalls++
	stub.detailQuery = query
	return stub.detail, stub.found, nil
}

func TestApplicationPassesExactSignedPrincipal(t *testing.T) {
	t.Parallel()
	principal := testPrincipal()
	verifier := &verifierStub{principal: principal}
	catalog := &catalogStub{page: Page{Items: []ExamSummary{}}, found: true}
	service, err := NewApplicationService(verifier, catalog)
	if err != nil {
		t.Fatal(err)
	}
	cursor := testExamID
	if _, err := service.List(context.Background(), "access", &cursor, 25); err != nil {
		t.Fatal(err)
	}
	if catalog.listQuery.Principal != principal || catalog.listQuery.Cursor == nil || *catalog.listQuery.Cursor != cursor || catalog.listQuery.Limit != 25 {
		t.Fatalf("list query=%#v", catalog.listQuery)
	}
	if _, _, err := service.Get(context.Background(), "access", testExamID); err != nil {
		t.Fatal(err)
	}
	if catalog.detailQuery.Principal != principal || catalog.detailQuery.ExamID != testExamID || verifier.calls != 2 || verifier.token != "access" {
		t.Fatalf("verifier=%#v detail query=%#v", verifier, catalog.detailQuery)
	}
}

func TestApplicationStopsAfterTokenRejection(t *testing.T) {
	t.Parallel()
	want := errors.New("token rejected")
	verifier := &verifierStub{err: want}
	catalog := &catalogStub{}
	service, err := NewApplicationService(verifier, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), "bad", nil, 20); !errors.Is(err, want) {
		t.Fatalf("List() error=%v", err)
	}
	if _, _, err := service.Get(context.Background(), "bad", testExamID); !errors.Is(err, want) {
		t.Fatalf("Get() error=%v", err)
	}
	if catalog.listCalls != 0 || catalog.getCalls != 0 {
		t.Fatalf("catalog called after rejection: %#v", catalog)
	}
}

func TestApplicationConstructorRequiresOwners(t *testing.T) {
	t.Parallel()
	if _, err := NewApplicationService(nil, &catalogStub{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil verifier error=%v", err)
	}
	if _, err := NewApplicationService(&verifierStub{}, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil catalog error=%v", err)
	}
}
