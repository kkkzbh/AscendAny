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
	result               Result
	err                  error
	query                SelfQuery
	studentNumberQuery   StudentNumberQuery
	studentIdentityQuery StudentIdentityQuery
	calls                int
}

func (reader *fakeReader) GetSelf(_ context.Context, query SelfQuery) (Result, error) {
	reader.calls++
	reader.query = query
	return reader.result, reader.err
}

func (reader *fakeReader) GetByStudentNumber(_ context.Context, query StudentNumberQuery) (Result, error) {
	reader.calls++
	reader.studentNumberQuery = query
	return reader.result, reader.err
}

func (reader *fakeReader) GetByStudentIdentity(_ context.Context, query StudentIdentityQuery) (Result, error) {
	reader.calls++
	reader.studentIdentityQuery = query
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

func TestApplicationForwardsStudentNumberWithoutAccessVerification(t *testing.T) {
	t.Parallel()

	verifier := &fakeVerifier{err: errors.New("must not verify selector reads")}
	reader := &fakeReader{result: Result{State: StateNoObservations}}
	service, err := NewApplicationService(verifier, reader)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetByStudentNumber(context.Background(), "20260001")
	if err != nil || result.State != StateNoObservations || verifier.token != "" || reader.calls != 1 ||
		reader.studentNumberQuery.StudentNumber != "20260001" {
		t.Fatalf("result/error/verifier/reader = %#v/%v/%#v/%#v", result, err, verifier, reader)
	}
}

func TestApplicationForwardsExactStudentIdentityWithoutAccessVerification(t *testing.T) {
	t.Parallel()

	verifier := &fakeVerifier{err: errors.New("must not verify selector reads")}
	reader := &fakeReader{result: Result{State: StateNoObservations}}
	service, err := NewApplicationService(verifier, reader)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetByStudentIdentity(context.Background(), "20260001", "Alice")
	if err != nil || result.State != StateNoObservations || verifier.token != "" || reader.calls != 1 ||
		reader.studentIdentityQuery != (StudentIdentityQuery{StudentNumber: "20260001", PTANickname: "Alice"}) {
		t.Fatalf("result/error/verifier/reader = %#v/%v/%#v/%#v", result, err, verifier, reader)
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
