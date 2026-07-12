package examgeneration

import (
	"context"
	"errors"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type applicationVerifierStub struct {
	principal auth.AccessPrincipal
	err       error
	token     string
	calls     int
}

func (stub *applicationVerifierStub) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	stub.calls++
	stub.token = token
	return stub.principal, stub.err
}

type applicationReaderStub struct {
	currentQuery CurrentQuery
	eventQuery   EventQuery
	currentCalls int
	eventCalls   int
}

func (stub *applicationReaderStub) GetCurrent(
	_ context.Context,
	query CurrentQuery,
) (Generation, bool, error) {
	stub.currentCalls++
	stub.currentQuery = query
	return Generation{}, false, nil
}

func (stub *applicationReaderStub) ReadEvents(
	_ context.Context,
	query EventQuery,
) (EventBatch, bool, error) {
	stub.eventCalls++
	stub.eventQuery = query
	return EventBatch{}, false, nil
}

func TestApplicationPassesVerifiedPrincipalAndExactGenerationQueries(t *testing.T) {
	t.Parallel()
	principal := validPrincipal()
	verifier := &applicationVerifierStub{principal: principal}
	reader := &applicationReaderStub{}
	application, err := NewApplicationService(verifier, reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.GetCurrent(context.Background(), "signed-access", testExamID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.ReadEvents(context.Background(), "signed-access", testExamID, "42", 7, 25); err != nil {
		t.Fatal(err)
	}
	if verifier.calls != 2 || verifier.token != "signed-access" || reader.currentCalls != 1 || reader.eventCalls != 1 {
		t.Fatalf("verifier=%#v reader=%#v", verifier, reader)
	}
	if reader.currentQuery != (CurrentQuery{Principal: principal, ExamID: testExamID}) {
		t.Fatalf("current query = %#v", reader.currentQuery)
	}
	if reader.eventQuery != (EventQuery{
		Principal: principal, ExamID: testExamID, GenerationID: "42", AfterSequence: 7, Limit: 25,
	}) {
		t.Fatalf("event query = %#v", reader.eventQuery)
	}
}

func TestApplicationStopsAfterAccessTokenRejection(t *testing.T) {
	t.Parallel()
	want := errors.New("token rejected")
	verifier := &applicationVerifierStub{err: want}
	reader := &applicationReaderStub{}
	application, err := NewApplicationService(verifier, reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.GetCurrent(context.Background(), "bad", testExamID); !errors.Is(err, want) {
		t.Fatalf("GetCurrent() error = %v", err)
	}
	if _, _, err := application.ReadEvents(context.Background(), "bad", testExamID, "42", 0, 25); !errors.Is(err, want) {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	if reader.currentCalls != 0 || reader.eventCalls != 0 {
		t.Fatalf("reader called after rejection: %#v", reader)
	}
}

func TestApplicationConstructorRequiresDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewApplicationService(nil, &applicationReaderStub{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil verifier error = %v", err)
	}
	if _, err := NewApplicationService(&applicationVerifierStub{}, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil reader error = %v", err)
	}
}
