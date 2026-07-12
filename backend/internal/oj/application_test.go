package oj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type verifierStub struct {
	principal auth.AccessPrincipal
}

func (verifier verifierStub) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	if token != "access-token" {
		return auth.AccessPrincipal{}, errors.New("invalid token")
	}
	return verifier.principal, nil
}

func (verifier verifierStub) Authenticate(_ context.Context, token string) (auth.AuthenticatedAccount, error) {
	principal, err := verifier.VerifyAccessToken(token)
	if err != nil {
		return auth.AuthenticatedAccount{}, err
	}
	return auth.AuthenticatedAccount{Principal: principal}, nil
}

func TestApplicationPublishesProblemAndSubmissionArtifacts(t *testing.T) {
	store, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), 300<<20)
	if err != nil {
		t.Fatal(err)
	}
	repository := &capturingRepository{}
	ids := []string{testProblemID, "77777777-7777-4777-8777-777777777777", testJobID}
	core, err := newService(repository, DefaultPolicy(), func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplicationService(verifierStub{principal: testPrincipal(auth.RoleAdmin)}, core, store, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	problemAuthorization, err := application.AuthorizeUpload(context.Background(), "access-token", UploadProblemVersion)
	if err != nil {
		t.Fatal(err)
	}
	testBundle := []byte("strict test bundle")
	problem, err := application.CreateProblemVersion(context.Background(), problemAuthorization, ProblemVersionMetadata{
		Slug: "sum-array", ExpectedHeadRevision: 0, Lifecycle: LifecycleActive,
		Title: "Sum array", StatementMarkdown: "Compute the sum.", KnowledgeTags: []string{"array"},
		TimeLimitMS: 1000, MemoryLimitBytes: 128 << 20, OutputLimitBytes: 1 << 20,
		ProblemSpec: []byte(`{"checker":"token"}`),
	}, bytes.NewReader(testBundle))
	if err != nil || problem.Problem.ID != testProblemID || repository.problemCommand.TestBundle.SizeBytes != int64(len(testBundle)) {
		t.Fatalf("problem=%#v command=%#v error=%v", problem, repository.problemCommand, err)
	}
	// The application releases its per-hash lock after the repository returns.
	replayedPublication, err := store.Publish(context.Background(), bytes.NewReader(testBundle))
	if err != nil {
		t.Fatal(err)
	}
	if err := replayedPublication.Release(); err != nil {
		t.Fatal(err)
	}

	source := []byte("int main(){}")
	stdin := []byte("1 2\n")
	submissionAuthorization, err := application.AuthorizeUpload(context.Background(), "access-token", UploadSubmission)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := application.CreateSubmission(context.Background(), submissionAuthorization, SubmissionMetadata{
		ClientRequestID: testRequestID, ProblemID: testProblemID, ExpectedProblemHeadRevision: 1,
		Mode: SubmissionRun, LanguageID: LanguageCPP20,
	}, bytes.NewReader(source), bytes.NewReader(stdin))
	if err != nil || !submission.Created || repository.submissionCommand.Source.SizeBytes != int64(len(source)) ||
		repository.submissionCommand.Stdin == nil || repository.submissionCommand.Stdin.SizeBytes != int64(len(stdin)) {
		t.Fatalf("submission=%#v command=%#v error=%v", submission, repository.submissionCommand, err)
	}
}

func TestApplicationRejectsMetadataAndSizeBeforePublication(t *testing.T) {
	store := &countingPublicationStore{}
	repository := &capturingRepository{}
	core, err := newService(repository, DefaultPolicy(), func() (string, error) { return testProblemID, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplicationService(verifierStub{principal: testPrincipal(auth.RoleAdmin)}, core, store, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := application.AuthorizeUpload(context.Background(), "access-token", UploadProblemVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.CreateProblemVersion(context.Background(), authorization, ProblemVersionMetadata{
		Slug: "INVALID", Lifecycle: LifecycleActive, Title: "x", StatementMarkdown: "x",
		TimeLimitMS: 1, MemoryLimitBytes: 1, OutputLimitBytes: 1, ProblemSpec: []byte(`{}`),
	}, strings.NewReader("bundle"))
	if CodeOf(err) != ErrorInvalidInput || store.calls != 0 {
		t.Fatalf("problem error=%v calls=%d", err, store.calls)
	}
	policy := DefaultPolicy()
	policy.MaximumSourceBytes = 4
	application, err = NewApplicationService(verifierStub{principal: testPrincipal(auth.RoleAdmin)}, core, store, policy)
	if err != nil {
		t.Fatal(err)
	}
	submissionAuthorization, err := application.AuthorizeUpload(context.Background(), "access-token", UploadSubmission)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.CreateSubmission(context.Background(), submissionAuthorization, SubmissionMetadata{
		ClientRequestID: testRequestID, ProblemID: testProblemID, ExpectedProblemHeadRevision: 1,
		Mode: SubmissionSubmit, LanguageID: LanguageCPP20,
	}, strings.NewReader("12345"), nil)
	if CodeOf(err) != ErrorPayloadTooLarge || store.calls != 0 {
		t.Fatalf("submission error=%v calls=%d", err, store.calls)
	}
}

func TestApplicationAuthorizesUploadBeforeBodyOwnership(t *testing.T) {
	core, err := newService(&capturingRepository{}, DefaultPolicy(), func() (string, error) { return testProblemID, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewReadOnlyApplicationService(verifierStub{principal: testPrincipal(auth.RoleStudent)}, core, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AuthorizeUpload(context.Background(), "access-token", UploadProblemVersion); CodeOf(err) != ErrorPrincipalRejected {
		t.Fatalf("student problem authorization error=%v", err)
	}
	if _, err := application.AuthorizeUpload(context.Background(), "access-token", UploadSubmission); err != nil {
		t.Fatalf("student submission authorization error=%v", err)
	}
	if _, err := application.AuthorizeUpload(context.Background(), "access-token", UploadKind("unknown")); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("unknown upload authorization error=%v", err)
	}
}

func TestHardLimitReaderDoesNotTreatZeroProgressAsEOF(t *testing.T) {
	_, _, err := readBoundedArtifact(&zeroProbeReader{}, 4)
	if CodeOf(err) != ErrorPayloadTooLarge {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
}

func TestApplicationRejectsNonUTF8TextArtifactsBeforePublication(t *testing.T) {
	store := &countingPublicationStore{}
	core, err := newService(&capturingRepository{}, DefaultPolicy(), func() (string, error) { return testProblemID, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplicationService(verifierStub{principal: testPrincipal(auth.RoleStudent)}, core, store, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := application.AuthorizeUpload(context.Background(), "access-token", UploadSubmission)
	if err != nil {
		t.Fatal(err)
	}
	metadata := SubmissionMetadata{
		ClientRequestID: testRequestID, ProblemID: testProblemID, ExpectedProblemHeadRevision: 1,
		Mode: SubmissionSubmit, LanguageID: LanguageCPP20,
	}
	if _, err := application.CreateSubmission(context.Background(), authorization, metadata, bytes.NewReader([]byte{0xff}), nil); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("source error=%v", err)
	}
	metadata.Mode = SubmissionRun
	if _, err := application.CreateSubmission(context.Background(), authorization, metadata, strings.NewReader("int main(){}"), bytes.NewReader([]byte{0xff})); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("stdin error=%v", err)
	}
	if store.calls != 0 {
		t.Fatalf("invalid UTF-8 reached artifact store: calls=%d", store.calls)
	}
}

func TestSubmissionPublishesMultipleHashesInGlobalOrder(t *testing.T) {
	inner, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingPublicationStore{inner: inner}
	repository := &capturingRepository{}
	ids := []string{"77777777-7777-4777-8777-777777777777", testJobID}
	core, err := newService(repository, DefaultPolicy(), func() (string, error) {
		identifier := ids[0]
		ids = ids[1:]
		return identifier, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplicationService(verifierStub{principal: testPrincipal(auth.RoleStudent)}, core, store, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := application.AuthorizeUpload(context.Background(), "access-token", UploadSubmission)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("source-content")
	stdin := []byte("stdin-content")
	sourceHash := sha256Hex(source)
	stdinHash := sha256Hex(stdin)
	if sourceHash < stdinHash {
		source, stdin = stdin, source
		sourceHash, stdinHash = stdinHash, sourceHash
	}
	_, err = application.CreateSubmission(context.Background(), authorization, SubmissionMetadata{
		ClientRequestID: testRequestID, ProblemID: testProblemID, ExpectedProblemHeadRevision: 1,
		Mode: SubmissionRun, LanguageID: LanguageCPP20,
	}, bytes.NewReader(source), bytes.NewReader(stdin))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.hashes) != 2 || store.hashes[0] != stdinHash || store.hashes[1] != sourceHash {
		t.Fatalf("publication order=%v want=[%s %s]", store.hashes, stdinHash, sourceHash)
	}
}

type recordingPublicationStore struct {
	inner  *artifactstore.Store
	hashes []string
}

func (store *recordingPublicationStore) Publish(ctx context.Context, source io.Reader) (*artifactstore.Publication, error) {
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	store.hashes = append(store.hashes, sha256Hex(data))
	return store.inner.Publish(ctx, bytes.NewReader(data))
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type zeroProbeReader struct {
	step int
}

func (reader *zeroProbeReader) Read(buffer []byte) (int, error) {
	reader.step++
	switch reader.step {
	case 1:
		return copy(buffer, "1234"), nil
	case 2:
		return 0, nil
	case 3:
		return copy(buffer, "5"), nil
	default:
		return 0, io.EOF
	}
}

type countingPublicationStore struct {
	calls int
}

func (store *countingPublicationStore) Publish(context.Context, io.Reader) (*artifactstore.Publication, error) {
	store.calls++
	return nil, errors.New("unexpected publication")
}
