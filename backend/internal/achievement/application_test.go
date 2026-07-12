package achievement

import (
	"context"
	"errors"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type fakeVerifier struct {
	principal auth.AccessPrincipal
	err       error
	token     string
}

func (verifier *fakeVerifier) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	verifier.token = token
	return verifier.principal, verifier.err
}

type fakeReader struct {
	result Result
	err    error
	query  SelfQuery
	calls  int
}

func (reader *fakeReader) GetSelf(_ context.Context, query SelfQuery) (Result, error) {
	reader.calls++
	reader.query = query
	return reader.result, reader.err
}

func TestApplicationVerifiesAndForwardsImmutablePrincipal(t *testing.T) {
	t.Parallel()

	principal := testQuery().Principal
	verifier := &fakeVerifier{principal: principal}
	reader := &fakeReader{result: Result{State: StateNotGenerated}}
	service, err := NewApplicationService(verifier, reader)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetSelf(context.Background(), "access-token")
	if err != nil || result.State != StateNotGenerated || verifier.token != "access-token" || reader.calls != 1 || reader.query.Principal != principal {
		t.Fatalf("result/error/verifier/reader = %#v/%v/%#v/%#v", result, err, verifier, reader)
	}
}

func TestApplicationDoesNotCallReaderAfterVerificationFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("invalid access token")
	reader := &fakeReader{}
	service, err := NewApplicationService(&fakeVerifier{err: want}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, got := service.GetSelf(context.Background(), "bad"); !errors.Is(got, want) || reader.calls != 0 {
		t.Fatalf("GetSelf() error/calls = %v/%d", got, reader.calls)
	}
}

func TestAchievementConstructorsRejectNilDependencies(t *testing.T) {
	t.Parallel()

	if _, err := NewService(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewService(nil) error = %v", err)
	}
	if _, err := NewPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewPostgresRepository(nil) error = %v", err)
	}
	if _, err := newPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("newPostgresRepository(nil) error = %v", err)
	}
	if _, err := NewApplicationService(nil, &fakeReader{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewApplicationService(nil, reader) error = %v", err)
	}
	if _, err := NewApplicationService(&fakeVerifier{}, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewApplicationService(verifier, nil) error = %v", err)
	}
}
