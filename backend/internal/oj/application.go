package oj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
	Authenticate(context.Context, string) (auth.AuthenticatedAccount, error)
}

type Core interface {
	CreateProblemVersion(context.Context, CreateProblemVersionInput) (CreateProblemVersionResult, error)
	GetProblem(context.Context, ProblemQuery) (Problem, bool, error)
	ListProblems(context.Context, ProblemListQuery) (ProblemPage, error)
	CreateSubmission(context.Context, CreateSubmissionInput) (CreateSubmissionResult, error)
	GetSubmission(context.Context, SubmissionQuery) (SubmissionDetail, bool, error)
	ReadJudgeEvents(context.Context, JudgeEventQuery) (JudgeEventBatch, bool, error)
}

type PublicationStore interface {
	Publish(context.Context, io.Reader) (*artifactstore.Publication, error)
}

type ProblemVersionMetadata struct {
	Slug                 string          `json:"slug"`
	ExpectedHeadRevision int64           `json:"expectedHeadRevision"`
	Lifecycle            Lifecycle       `json:"lifecycle"`
	Title                string          `json:"title"`
	StatementMarkdown    string          `json:"statementMarkdown"`
	SolutionMarkdown     *string         `json:"solutionMarkdown"`
	KnowledgeTags        []string        `json:"knowledgeTags"`
	TimeLimitMS          int             `json:"timeLimitMs"`
	MemoryLimitBytes     int64           `json:"memoryLimitBytes"`
	OutputLimitBytes     int64           `json:"outputLimitBytes"`
	ProblemSpec          json.RawMessage `json:"problemSpec"`
}

type SubmissionMetadata struct {
	ClientRequestID             string         `json:"clientRequestId"`
	ProblemID                   string         `json:"problemId"`
	ExpectedProblemHeadRevision int64          `json:"expectedProblemHeadRevision"`
	Mode                        SubmissionMode `json:"mode"`
	LanguageID                  string         `json:"languageId"`
}

type UploadKind string

const (
	UploadProblemVersion UploadKind = "problem_version"
	UploadSubmission     UploadKind = "submission"
)

type UploadAuthorization struct {
	principal auth.AccessPrincipal
	kind      UploadKind
}

func (authorization UploadAuthorization) principalFor(kind UploadKind) (auth.AccessPrincipal, error) {
	if authorization.kind != kind || validatePrincipal(authorization.principal) != nil {
		return auth.AccessPrincipal{}, ojError(ErrorPrincipalRejected, true, "validate OJ upload authorization", errors.New("upload authorization is invalid"))
	}
	return authorization.principal, nil
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	core     Core
	store    PublicationStore
	policy   Policy
}

func (application *ApplicationService) AuthorizeUpload(ctx context.Context, token string, kind UploadKind) (UploadAuthorization, error) {
	authenticated, err := application.verifier.Authenticate(ctx, token)
	if err != nil {
		return UploadAuthorization{}, err
	}
	principal := authenticated.Principal
	switch kind {
	case UploadProblemVersion:
		if principal.Role != auth.RoleAdmin {
			return UploadAuthorization{}, ojError(ErrorPrincipalRejected, true, "authorize OJ problem upload", errors.New("administrator role is required"))
		}
	case UploadSubmission:
		if principal.Role != auth.RoleAdmin && principal.Role != auth.RoleStudent {
			return UploadAuthorization{}, ojError(ErrorPrincipalRejected, true, "authorize OJ submission upload", errors.New("student or administrator role is required"))
		}
	default:
		return UploadAuthorization{}, ojError(ErrorInvalidInput, true, "authorize OJ upload", errors.New("upload kind is invalid"))
	}
	return UploadAuthorization{principal: principal, kind: kind}, nil
}

func NewApplicationService(
	verifier AccessPrincipalVerifier,
	core Core,
	store PublicationStore,
	policy Policy,
) (*ApplicationService, error) {
	if verifier == nil || core == nil || store == nil || !validPolicy(policy) {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ application service", errors.New("principal verifier, OJ core, artifact store, and bounded policy are required"))
	}
	return &ApplicationService{verifier: verifier, core: core, store: store, policy: policy}, nil
}

func NewReadOnlyApplicationService(
	verifier AccessPrincipalVerifier,
	core Core,
	policy Policy,
) (*ApplicationService, error) {
	if verifier == nil || core == nil || !validPolicy(policy) {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct read-only OJ application service", errors.New("principal verifier, OJ core, and bounded policy are required"))
	}
	return &ApplicationService{verifier: verifier, core: core, policy: policy}, nil
}

func (application *ApplicationService) ListProblems(
	ctx context.Context,
	token string,
	afterSlug *string,
	limit int,
	includeArchived bool,
) (ProblemPage, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return ProblemPage{}, err
	}
	return application.core.ListProblems(ctx, ProblemListQuery{
		Principal: principal, AfterSlug: afterSlug, Limit: limit, IncludeArchived: includeArchived,
	})
}

func (application *ApplicationService) GetProblem(ctx context.Context, token, problemID string) (Problem, bool, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return Problem{}, false, err
	}
	return application.core.GetProblem(ctx, ProblemQuery{Principal: principal, ProblemID: problemID})
}

func (application *ApplicationService) GetSubmission(ctx context.Context, token, submissionID string) (SubmissionDetail, bool, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return SubmissionDetail{}, false, err
	}
	return application.core.GetSubmission(ctx, SubmissionQuery{Principal: principal, SubmissionID: submissionID})
}

func (application *ApplicationService) ReadJudgeEvents(
	ctx context.Context,
	token, submissionID string,
	afterSequence int64,
	limit int,
) (JudgeEventBatch, bool, error) {
	principal, err := application.verifier.VerifyAccessToken(token)
	if err != nil {
		return JudgeEventBatch{}, false, err
	}
	return application.core.ReadJudgeEvents(ctx, JudgeEventQuery{
		Principal: principal, SubmissionID: submissionID, AfterSequence: afterSequence, Limit: limit,
	})
}

func (application *ApplicationService) CreateProblemVersion(
	ctx context.Context,
	authorization UploadAuthorization,
	metadata ProblemVersionMetadata,
	testBundle io.Reader,
) (result CreateProblemVersionResult, resultErr error) {
	principal, err := authorization.principalFor(UploadProblemVersion)
	if err != nil {
		return CreateProblemVersionResult{}, err
	}
	if testBundle == nil {
		return CreateProblemVersionResult{}, ojError(ErrorInvalidInput, true, "create OJ problem version", errors.New("test bundle is required"))
	}
	if application.store == nil {
		return CreateProblemVersionResult{}, ojError(ErrorInvalidConfiguration, true, "create OJ problem version", errors.New("OJ writes are not configured"))
	}
	preflightHash := strings.Repeat("0", 64)
	preflight := problemInputFromMetadata(principal, metadata, Artifact{
		SHA256: preflightHash, SizeBytes: 1, MediaType: TestBundleMediaType,
		StorageKey: "sha256/00/" + preflightHash,
	})
	if err := validateProblemInput(ctx, preflight, application.policy); err != nil {
		return CreateProblemVersionResult{}, err
	}
	if _, _, err := canonicaljson.Object(metadata.ProblemSpec, application.policy.MaximumProblemSpecBytes); err != nil {
		return CreateProblemVersionResult{}, ojError(ErrorInvalidInput, true, "validate OJ problem spec", err)
	}
	publication, err := application.store.Publish(ctx, &hardLimitReader{source: testBundle, remaining: application.policy.MaximumTestBundleBytes})
	if err != nil {
		return CreateProblemVersionResult{}, mapArtifactPublishError("publish OJ test bundle", err)
	}
	if publication == nil {
		return CreateProblemVersionResult{}, ojError(ErrorStoredDataInvalid, true, "publish OJ test bundle", errors.New("artifact store returned no publication"))
	}
	defer releasePublications(&resultErr, publication)
	result, resultErr = application.core.CreateProblemVersion(ctx,
		problemInputFromMetadata(principal, metadata, publicationArtifact(publication, TestBundleMediaType)))
	return result, resultErr
}

func (application *ApplicationService) CreateSubmission(
	ctx context.Context,
	authorization UploadAuthorization,
	metadata SubmissionMetadata,
	sourceReader io.Reader,
	stdinReader io.Reader,
) (result CreateSubmissionResult, resultErr error) {
	principal, err := authorization.principalFor(UploadSubmission)
	if err != nil {
		return CreateSubmissionResult{}, err
	}
	if sourceReader == nil {
		return CreateSubmissionResult{}, ojError(ErrorInvalidInput, true, "create OJ submission", errors.New("source is required"))
	}
	if application.store == nil {
		return CreateSubmissionResult{}, ojError(ErrorInvalidConfiguration, true, "create OJ submission", errors.New("OJ writes are not configured"))
	}
	dummySourceHash := strings.Repeat("0", 64)
	dummySource := Artifact{SHA256: dummySourceHash, SizeBytes: 1, MediaType: CPP20SourceMediaType,
		StorageKey: "sha256/00/" + dummySourceHash}
	var dummyStdin *Artifact
	if stdinReader != nil {
		value := Artifact{SHA256: strings.Repeat("1", 64), SizeBytes: 1, MediaType: PlainTextMediaType,
			StorageKey: "sha256/11/" + strings.Repeat("1", 64)}
		dummyStdin = &value
	}
	if err := validateSubmissionInput(ctx, CreateSubmissionInput{
		Principal: principal, ClientRequestID: metadata.ClientRequestID, ProblemID: metadata.ProblemID,
		ExpectedProblemHeadRevision: metadata.ExpectedProblemHeadRevision, Mode: metadata.Mode,
		LanguageID: metadata.LanguageID, Source: dummySource, Stdin: dummyStdin,
	}, application.policy); err != nil {
		return CreateSubmissionResult{}, err
	}
	source, sourceHash, err := readBoundedArtifact(sourceReader, application.policy.MaximumSourceBytes)
	if err != nil {
		return CreateSubmissionResult{}, err
	}
	if !utf8.Valid(source) {
		return CreateSubmissionResult{}, ojError(ErrorInvalidInput, true, "validate OJ source", errors.New("source must be UTF-8"))
	}
	var stdin []byte
	var stdinHash string
	if stdinReader != nil {
		stdin, stdinHash, err = readBoundedArtifact(stdinReader, application.policy.MaximumStdinBytes)
		if err != nil {
			return CreateSubmissionResult{}, err
		}
		if !utf8.Valid(stdin) {
			return CreateSubmissionResult{}, ojError(ErrorInvalidInput, true, "validate OJ stdin", errors.New("stdin must be UTF-8"))
		}
		if stdinHash == sourceHash {
			return CreateSubmissionResult{}, ojError(ErrorArtifactConflict, true, "create OJ submission", errors.New("source and stdin cannot share one digest across different media contracts"))
		}
	}
	type pendingPublication struct {
		hash      string
		operation string
		data      []byte
		target    **artifactstore.Publication
	}
	var sourcePublication *artifactstore.Publication
	var stdinPublication *artifactstore.Publication
	pending := []pendingPublication{{
		hash: sourceHash, operation: "publish OJ source", data: source, target: &sourcePublication,
	}}
	if stdinReader != nil {
		pending = append(pending, pendingPublication{
			hash: stdinHash, operation: "publish OJ stdin", data: stdin, target: &stdinPublication,
		})
	}
	sort.Slice(pending, func(left, right int) bool { return pending[left].hash < pending[right].hash })
	publications := make([]*artifactstore.Publication, 0, len(pending))
	defer func() { releasePublications(&resultErr, publications...) }()
	for _, item := range pending {
		publication, publishErr := application.store.Publish(ctx, bytes.NewReader(item.data))
		if publishErr != nil {
			return CreateSubmissionResult{}, mapArtifactPublishError(item.operation, publishErr)
		}
		if publication == nil {
			return CreateSubmissionResult{}, ojError(ErrorStoredDataInvalid, true, item.operation, errors.New("artifact store returned no publication"))
		}
		*item.target = publication
		publications = append(publications, publication)
	}
	var stdinArtifact *Artifact
	if stdinReader != nil {
		value := publicationArtifact(stdinPublication, PlainTextMediaType)
		stdinArtifact = &value
	}
	result, resultErr = application.core.CreateSubmission(ctx, CreateSubmissionInput{
		Principal: principal, ClientRequestID: metadata.ClientRequestID, ProblemID: metadata.ProblemID,
		ExpectedProblemHeadRevision: metadata.ExpectedProblemHeadRevision,
		Mode:                        metadata.Mode, LanguageID: metadata.LanguageID,
		Source: publicationArtifact(sourcePublication, CPP20SourceMediaType), Stdin: stdinArtifact,
	})
	return result, resultErr
}

func problemInputFromMetadata(principal auth.AccessPrincipal, metadata ProblemVersionMetadata, testBundle Artifact) CreateProblemVersionInput {
	return CreateProblemVersionInput{
		Principal: principal, Slug: metadata.Slug, ExpectedHeadRevision: metadata.ExpectedHeadRevision,
		Lifecycle: metadata.Lifecycle, Title: metadata.Title, StatementMarkdown: metadata.StatementMarkdown,
		SolutionMarkdown: metadata.SolutionMarkdown, KnowledgeTags: metadata.KnowledgeTags,
		TimeLimitMS: metadata.TimeLimitMS, MemoryLimitBytes: metadata.MemoryLimitBytes,
		OutputLimitBytes: metadata.OutputLimitBytes, ProblemSpec: metadata.ProblemSpec, TestBundle: testBundle,
	}
}

func publicationArtifact(publication *artifactstore.Publication, mediaType string) Artifact {
	return Artifact{
		SHA256: publication.Artifact.Hash, SizeBytes: publication.Artifact.Size,
		MediaType: mediaType, StorageKey: publication.Artifact.StorageKey,
	}
}

func releasePublications(resultErr *error, publications ...*artifactstore.Publication) {
	for index := len(publications) - 1; index >= 0; index-- {
		if publications[index] == nil {
			continue
		}
		if err := publications[index].Release(); err != nil {
			wrapped := ojError(ErrorArtifactFailure, false, "release OJ artifact publication", err)
			if *resultErr == nil {
				*resultErr = wrapped
			} else {
				*resultErr = errors.Join(*resultErr, wrapped)
			}
		}
	}
}

var errArtifactTooLarge = errors.New("OJ artifact exceeds its hard byte limit")

type hardLimitReader struct {
	source     io.Reader
	remaining  int64
	checked    bool
	emptyReads int
}

func (reader *hardLimitReader) Read(buffer []byte) (int, error) {
	if reader.remaining > 0 {
		if int64(len(buffer)) > reader.remaining {
			buffer = buffer[:reader.remaining]
		}
		count, err := reader.source.Read(buffer)
		if count == 0 && err == nil {
			reader.emptyReads++
			if reader.emptyReads >= 100 {
				return 0, io.ErrNoProgress
			}
		} else {
			reader.emptyReads = 0
		}
		reader.remaining -= int64(count)
		return count, err
	}
	if reader.checked {
		return 0, io.EOF
	}
	var probe [1]byte
	count, err := reader.source.Read(probe[:])
	if count > 0 {
		reader.checked = true
		return 0, errArtifactTooLarge
	}
	if err != nil {
		reader.checked = true
		return 0, err
	}
	reader.emptyReads++
	if reader.emptyReads >= 100 {
		reader.checked = true
		return 0, io.ErrNoProgress
	}
	return 0, err
}

func readBoundedArtifact(source io.Reader, maximum int64) ([]byte, string, error) {
	if source == nil || maximum < 1 {
		return nil, "", ojError(ErrorInvalidInput, true, "read OJ artifact", errors.New("reader and positive byte limit are required"))
	}
	data, err := io.ReadAll(&hardLimitReader{source: source, remaining: maximum})
	if errors.Is(err, errArtifactTooLarge) {
		return nil, "", ojError(ErrorPayloadTooLarge, true, "read OJ artifact", err)
	}
	if err != nil {
		return nil, "", ojError(ErrorArtifactFailure, false, "read OJ artifact", err)
	}
	if len(data) == 0 {
		return nil, "", ojError(ErrorInvalidInput, true, "read OJ artifact", errors.New("artifact is empty"))
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}

func mapArtifactPublishError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ojError(ErrorCanceled, false, operation, err)
	}
	if errors.Is(err, errArtifactTooLarge) {
		return ojError(ErrorPayloadTooLarge, true, operation, err)
	}
	if code, owned := artifactstore.CodeOf(err); owned {
		switch code {
		case artifactstore.ErrorPayloadTooLarge:
			return ojError(ErrorPayloadTooLarge, true, operation, err)
		case artifactstore.ErrorEmptyArtifact, artifactstore.ErrorInvalidArgument:
			return ojError(ErrorInvalidInput, true, operation, err)
		case artifactstore.ErrorCanceled:
			return ojError(ErrorCanceled, false, operation, err)
		}
	}
	return ojError(ErrorArtifactFailure, false, operation, err)
}
