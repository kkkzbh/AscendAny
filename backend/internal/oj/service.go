package oj

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

var (
	canonicalUUIDv4   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	lowercaseSHA256   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	slugPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)
	tagPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9_.+-]{0,63}$`)
	storageKeyPattern = regexp.MustCompile(`^sha256/[0-9a-f]{2}/[0-9a-f]{64}$`)
)

// ValidSlug reports whether value is a canonical OJ problem slug accepted by
// the service contract. HTTP adapters use it to reject malformed cursors before
// invoking the domain service.
func ValidSlug(value string) bool {
	return slugPattern.MatchString(value)
}

// ValidPublicID reports whether value is a canonical lowercase UUIDv4 used by
// public OJ resources.
func ValidPublicID(value string) bool {
	return canonicalUUIDv4.MatchString(value)
}

// ValidPolicy reports whether policy is internally bounded and can be shared
// by the domain, artifact publication, and HTTP upload boundaries.
func ValidPolicy(policy Policy) bool {
	return validPolicy(policy)
}

type Repository interface {
	CreateProblemVersion(context.Context, CreateProblemVersionCommand) (CreateProblemVersionResult, error)
	GetProblem(context.Context, ProblemQuery) (Problem, bool, error)
	ListProblems(context.Context, ProblemListQuery) (ProblemPage, error)
	CreateSubmission(context.Context, CreateSubmissionCommand) (CreateSubmissionResult, error)
	GetSubmission(context.Context, SubmissionQuery) (SubmissionDetail, bool, error)
	ReadJudgeEvents(context.Context, JudgeEventQuery) (JudgeEventBatch, bool, error)
}

type UUIDGenerator func() (string, error)

type Service struct {
	repository Repository
	policy     Policy
	uuid       UUIDGenerator
}

func NewService(repository Repository, policy Policy) (*Service, error) {
	return newService(repository, policy, randomUUIDv4)
}

func newService(repository Repository, policy Policy, uuid UUIDGenerator) (*Service, error) {
	if repository == nil || uuid == nil || !validPolicy(policy) {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ service", errors.New("repository, UUID generator, and bounded policy are required"))
	}
	return &Service{repository: repository, policy: policy, uuid: uuid}, nil
}

func (service *Service) CreateProblemVersion(ctx context.Context, input CreateProblemVersionInput) (CreateProblemVersionResult, error) {
	if err := validateProblemInput(ctx, input, service.policy); err != nil {
		return CreateProblemVersionResult{}, err
	}
	canonicalSpec, specHash, err := canonicaljson.Object(input.ProblemSpec, service.policy.MaximumProblemSpecBytes)
	if err != nil {
		return CreateProblemVersionResult{}, ojError(ErrorInvalidInput, true, "validate OJ problem spec", err)
	}
	input.ProblemSpec = canonicalSpec
	contentHash := problemContentHash(input, specHash)
	publicID, err := service.uuid()
	if err != nil {
		return CreateProblemVersionResult{}, ojError(ErrorInvalidConfiguration, false, "generate OJ problem public ID", err)
	}
	return service.repository.CreateProblemVersion(ctx, CreateProblemVersionCommand{
		CreateProblemVersionInput: input,
		ProblemPublicID:           publicID,
		ProblemSchema:             ProblemSchemaV1,
		ProblemSpecSHA256:         specHash,
		ContentSHA256:             contentHash,
	})
}

func (service *Service) GetProblem(ctx context.Context, query ProblemQuery) (Problem, bool, error) {
	if ctx == nil || validatePrincipal(query.Principal) != nil || !canonicalUUIDv4.MatchString(query.ProblemID) {
		return Problem{}, false, ojError(ErrorInvalidInput, true, "validate OJ problem query", errors.New("canonical principal and problem ID are required"))
	}
	return service.repository.GetProblem(ctx, query)
}

func (service *Service) ListProblems(ctx context.Context, query ProblemListQuery) (ProblemPage, error) {
	if ctx == nil || validatePrincipal(query.Principal) != nil || query.Limit < 1 || query.Limit > MaxPageSize ||
		(query.AfterSlug != nil && !slugPattern.MatchString(*query.AfterSlug)) {
		return ProblemPage{}, ojError(ErrorInvalidInput, true, "validate OJ problem page", errors.New("canonical principal and page are required"))
	}
	if query.IncludeArchived && query.Principal.Role != auth.RoleAdmin {
		return ProblemPage{}, ojError(ErrorPrincipalRejected, true, "authorize archived OJ problem page", errors.New("administrator role is required"))
	}
	return service.repository.ListProblems(ctx, query)
}

func (service *Service) CreateSubmission(ctx context.Context, input CreateSubmissionInput) (CreateSubmissionResult, error) {
	if err := validateSubmissionInput(ctx, input, service.policy); err != nil {
		return CreateSubmissionResult{}, err
	}
	submissionID, err := service.uuid()
	if err != nil {
		return CreateSubmissionResult{}, ojError(ErrorInvalidConfiguration, false, "generate OJ submission public ID", err)
	}
	jobID, err := service.uuid()
	if err != nil {
		return CreateSubmissionResult{}, ojError(ErrorInvalidConfiguration, false, "generate OJ judge job public ID", err)
	}
	return service.repository.CreateSubmission(ctx, CreateSubmissionCommand{
		CreateSubmissionInput: input,
		SubmissionPublicID:    submissionID,
		JudgeJobPublicID:      jobID,
	})
}

func (service *Service) GetSubmission(ctx context.Context, query SubmissionQuery) (SubmissionDetail, bool, error) {
	if ctx == nil || validatePrincipal(query.Principal) != nil || !canonicalUUIDv4.MatchString(query.SubmissionID) {
		return SubmissionDetail{}, false, ojError(ErrorInvalidInput, true, "validate OJ submission query", errors.New("canonical principal and submission ID are required"))
	}
	return service.repository.GetSubmission(ctx, query)
}

func (service *Service) ReadJudgeEvents(ctx context.Context, query JudgeEventQuery) (JudgeEventBatch, bool, error) {
	if ctx == nil || validatePrincipal(query.Principal) != nil || !canonicalUUIDv4.MatchString(query.SubmissionID) ||
		query.AfterSequence < 0 || query.Limit < 1 || query.Limit > 1000 {
		return JudgeEventBatch{}, false, ojError(ErrorInvalidInput, true, "validate OJ judge event query", errors.New("canonical principal, submission ID, and bounded event page are required"))
	}
	return service.repository.ReadJudgeEvents(ctx, query)
}

func validateProblemInput(ctx context.Context, input CreateProblemVersionInput, policy Policy) error {
	if ctx == nil || validatePrincipal(input.Principal) != nil || input.Principal.Role != auth.RoleAdmin ||
		!slugPattern.MatchString(input.Slug) || input.ExpectedHeadRevision < 0 ||
		(input.Lifecycle != LifecycleActive && input.Lifecycle != LifecycleArchived) {
		return ojError(ErrorInvalidInput, true, "validate OJ problem version", errors.New("administrator, canonical slug, head revision, and lifecycle are required"))
	}
	if input.Title != strings.TrimSpace(input.Title) || len(input.Title) < 1 || len(input.Title) > policy.MaximumTitleBytes ||
		len(input.StatementMarkdown) < 1 || len(input.StatementMarkdown) > policy.MaximumStatementBytes ||
		(input.SolutionMarkdown != nil && len(*input.SolutionMarkdown) > policy.MaximumSolutionBytes) ||
		strings.IndexByte(input.Title, 0) >= 0 || strings.IndexByte(input.StatementMarkdown, 0) >= 0 ||
		(input.SolutionMarkdown != nil && strings.IndexByte(*input.SolutionMarkdown, 0) >= 0) ||
		!utf8.ValidString(input.Title) || !utf8.ValidString(input.StatementMarkdown) ||
		(input.SolutionMarkdown != nil && !utf8.ValidString(*input.SolutionMarkdown)) {
		return ojError(ErrorInvalidInput, true, "validate OJ problem version", errors.New("problem text violates byte limits or trimming rules"))
	}
	if input.KnowledgeTags == nil || len(input.KnowledgeTags) > 64 || !slices.IsSorted(input.KnowledgeTags) {
		return ojError(ErrorInvalidInput, true, "validate OJ problem version", errors.New("knowledge tags must be sorted and bounded"))
	}
	for index, tag := range input.KnowledgeTags {
		if !tagPattern.MatchString(tag) || index > 0 && input.KnowledgeTags[index-1] == tag {
			return ojError(ErrorInvalidInput, true, "validate OJ problem version", errors.New("knowledge tags must be canonical and unique"))
		}
	}
	if input.TimeLimitMS < 1 || input.TimeLimitMS > policy.MaximumTimeLimitMS || input.MemoryLimitBytes < 1 ||
		input.MemoryLimitBytes > policy.MaximumMemoryBytes || input.OutputLimitBytes < 1 || input.OutputLimitBytes > policy.MaximumOutputBytes {
		return ojError(ErrorInvalidInput, true, "validate OJ problem version", errors.New("problem resource limits exceed policy"))
	}
	if err := validateArtifact(input.TestBundle, TestBundleMediaType, policy.MaximumTestBundleBytes); err != nil {
		return ojError(ErrorInvalidInput, true, "validate OJ test bundle", err)
	}
	return nil
}

func validateSubmissionInput(ctx context.Context, input CreateSubmissionInput, policy Policy) error {
	if ctx == nil || validatePrincipal(input.Principal) != nil || !canonicalUUIDv4.MatchString(input.ClientRequestID) ||
		!canonicalUUIDv4.MatchString(input.ProblemID) || input.ExpectedProblemHeadRevision < 1 ||
		(input.Mode != SubmissionRun && input.Mode != SubmissionSubmit) || input.LanguageID != LanguageCPP20 {
		return ojError(ErrorInvalidInput, true, "validate OJ submission", errors.New("canonical principal, request, problem head, mode, and language are required"))
	}
	if err := validateArtifact(input.Source, CPP20SourceMediaType, policy.MaximumSourceBytes); err != nil {
		return ojError(ErrorInvalidInput, true, "validate OJ source artifact", err)
	}
	if input.Mode == SubmissionRun {
		if input.Stdin == nil {
			return ojError(ErrorInvalidInput, true, "validate OJ run submission", errors.New("run mode requires stdin"))
		}
		if err := validateArtifact(*input.Stdin, PlainTextMediaType, policy.MaximumStdinBytes); err != nil {
			return ojError(ErrorInvalidInput, true, "validate OJ stdin artifact", err)
		}
	} else if input.Stdin != nil {
		return ojError(ErrorInvalidInput, true, "validate OJ submit submission", errors.New("submit mode forbids stdin"))
	}
	return nil
}

func validateArtifact(value Artifact, expectedMediaType string, maximumBytes int64) error {
	if !lowercaseSHA256.MatchString(value.SHA256) || value.SizeBytes < 1 || value.SizeBytes > maximumBytes ||
		value.MediaType != expectedMediaType || !storageKeyPattern.MatchString(value.StorageKey) ||
		value.StorageKey != "sha256/"+value.SHA256[:2]+"/"+value.SHA256 {
		return errors.New("artifact descriptor violates the content-addressed media contract")
	}
	return nil
}

func validatePrincipal(principal auth.AccessPrincipal) error {
	if !canonicalUUIDv4.MatchString(principal.AccountID) || !canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision < 1 ||
		(principal.Role != auth.RoleAdmin && principal.Role != auth.RoleStudent) {
		return errors.New("canonical active principal is required")
	}
	return nil
}

func validPolicy(policy Policy) bool {
	return policy.MaximumTitleBytes >= 1 && policy.MaximumTitleBytes <= 512 &&
		policy.MaximumStatementBytes >= 1 && policy.MaximumStatementBytes <= 1<<20 &&
		policy.MaximumSolutionBytes >= 1 && policy.MaximumSolutionBytes <= 1<<20 &&
		policy.MaximumProblemSpecBytes >= 1 && policy.MaximumProblemSpecBytes <= 1<<20 &&
		policy.MaximumTestBundleBytes >= 1 && policy.MaximumTestBundleBytes <= 1<<30 &&
		policy.MaximumSourceBytes >= 1 && policy.MaximumSourceBytes <= 16<<20 &&
		policy.MaximumStdinBytes >= 1 && policy.MaximumStdinBytes <= 16<<20 &&
		policy.MaximumTimeLimitMS >= 1 && policy.MaximumTimeLimitMS <= 3_600_000 &&
		policy.MaximumMemoryBytes >= 1 && policy.MaximumMemoryBytes <= 64<<30 &&
		policy.MaximumOutputBytes >= 1 && policy.MaximumOutputBytes <= 1<<30
}

func problemContentHash(input CreateProblemVersionInput, specHash string) string {
	digest := sha256.New()
	writeHashString(digest, "ascendany.oj.problem-content.v1")
	writeHashString(digest, string(input.Lifecycle))
	writeHashString(digest, input.Title)
	writeHashString(digest, input.StatementMarkdown)
	if input.SolutionMarkdown == nil {
		writeHashString(digest, "")
	} else {
		writeHashString(digest, *input.SolutionMarkdown)
	}
	writeHashUint64(digest, uint64(len(input.KnowledgeTags)))
	for _, tag := range input.KnowledgeTags {
		writeHashString(digest, tag)
	}
	writeHashUint64(digest, uint64(input.TimeLimitMS))
	writeHashUint64(digest, uint64(input.MemoryLimitBytes))
	writeHashUint64(digest, uint64(input.OutputLimitBytes))
	writeHashString(digest, specHash)
	writeHashString(digest, input.TestBundle.SHA256)
	return hex.EncodeToString(digest.Sum(nil))
}

func writeHashString(digest hash.Hash, value string) {
	writeHashUint64(digest, uint64(len(value)))
	_, _ = digest.Write([]byte(value))
}

func writeHashUint64(digest hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = digest.Write(buffer[:])
}

func validateJudgeResult(input JudgeResultInput, maximumManifestBytes int) (json.RawMessage, string, error) {
	if !validVerdict(input.Verdict) || math.IsNaN(input.ScoreFraction) || math.IsInf(input.ScoreFraction, 0) ||
		input.ScoreFraction < 0 || input.ScoreFraction > 1 || input.PassedCaseCount < 0 || input.TotalCaseCount < 0 ||
		input.PassedCaseCount > input.TotalCaseCount || input.MaxTimeMS < 0 || input.MaxMemoryBytes < 0 {
		return nil, "", ojError(ErrorInvalidInput, true, "validate OJ judge result", errors.New("judge result metrics are invalid"))
	}
	if input.Output != nil {
		if err := validateArtifact(*input.Output, JudgeOutputMediaType, math.MaxInt64); err != nil {
			return nil, "", ojError(ErrorInvalidInput, true, "validate OJ judge output", err)
		}
	}
	manifest, manifestHash, err := canonicaljson.Object(input.ResultManifest, maximumManifestBytes)
	if err != nil {
		return nil, "", ojError(ErrorInvalidInput, true, "validate OJ judge result manifest", err)
	}
	return manifest, manifestHash, nil
}

func judgeResultHash(input JudgeResultInput, manifestHash string) string {
	digest := sha256.New()
	writeHashString(digest, JudgeResultSchemaV1)
	writeHashString(digest, string(input.Verdict))
	writeHashUint64(digest, math.Float64bits(input.ScoreFraction))
	writeHashUint64(digest, uint64(input.PassedCaseCount))
	writeHashUint64(digest, uint64(input.TotalCaseCount))
	writeHashUint64(digest, uint64(input.MaxTimeMS))
	writeHashUint64(digest, uint64(input.MaxMemoryBytes))
	if input.Output == nil {
		writeHashString(digest, "")
	} else {
		writeHashString(digest, input.Output.SHA256)
	}
	writeHashString(digest, manifestHash)
	return hex.EncodeToString(digest.Sum(nil))
}

func validVerdict(value Verdict) bool {
	switch value {
	case VerdictAccepted, VerdictWrongAnswer, VerdictCompileError, VerdictRuntimeError,
		VerdictTimeLimitExceeded, VerdictMemoryLimitExceeded, VerdictOutputLimitExceeded:
		return true
	default:
		return false
	}
}

func randomUUIDv4() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
