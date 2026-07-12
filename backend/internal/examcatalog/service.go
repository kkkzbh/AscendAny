package examcatalog

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var (
	canonicalUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	canonicalDecimal       = regexp.MustCompile(`^(0|[1-9][0-9]*)([.][0-9]+)?$`)
)

func ValidPublicID(value string) bool {
	return canonicalUUIDv4Pattern.MatchString(value)
}

type Repository interface {
	LoadPage(context.Context, ListQuery) (Page, error)
	LoadDetail(context.Context, DetailQuery) (Detail, bool, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, catalogError(ErrorInvalidConfiguration, "construct exam catalog service", errors.New("repository is required"))
	}
	return &Service{repository: repository}, nil
}

func (service *Service) List(ctx context.Context, query ListQuery) (Page, error) {
	if err := validateListQuery(ctx, query); err != nil {
		return Page{}, err
	}
	page, err := service.repository.LoadPage(ctx, query)
	if err != nil {
		return Page{}, err
	}
	if err := validatePage(page, query.Limit); err != nil {
		return Page{}, catalogError(ErrorStoredDataInvalid, "validate exam catalog page", err)
	}
	return page, nil
}

func (service *Service) Get(ctx context.Context, query DetailQuery) (Detail, bool, error) {
	if err := validateDetailQuery(ctx, query); err != nil {
		return Detail{}, false, err
	}
	detail, found, err := service.repository.LoadDetail(ctx, query)
	if err != nil || !found {
		return Detail{}, found, err
	}
	if detail.ID != query.ExamID {
		return Detail{}, false, catalogError(ErrorStoredDataInvalid, "validate exam catalog detail", errors.New("repository returned a different exam"))
	}
	if err := validateSummary(detail.ExamSummary); err != nil {
		return Detail{}, false, catalogError(ErrorStoredDataInvalid, "validate exam catalog detail", err)
	}
	seen := make(map[string]struct{}, len(detail.Problems))
	for _, problem := range detail.Problems {
		if err := validateProblem(problem); err != nil {
			return Detail{}, false, catalogError(ErrorStoredDataInvalid, "validate exam catalog problem", err)
		}
		if _, exists := seen[problem.ID]; exists {
			return Detail{}, false, catalogError(ErrorStoredDataInvalid, "validate exam catalog problem", errors.New("problem ID is duplicated"))
		}
		seen[problem.ID] = struct{}{}
	}
	if int64(len(detail.Problems)) != detail.ProblemCount {
		return Detail{}, false, catalogError(ErrorStoredDataInvalid, "validate exam catalog detail", errors.New("problem rows differ from snapshot count"))
	}
	return detail, true, nil
}

func validateListQuery(ctx context.Context, query ListQuery) error {
	if ctx == nil {
		return catalogError(ErrorInvalidQuery, "validate exam catalog list query", errors.New("context is required"))
	}
	if err := validatePrincipal(query.Principal); err != nil {
		return catalogError(ErrorInvalidQuery, "validate exam catalog list query", err)
	}
	if query.Cursor != nil && !canonicalUUIDv4Pattern.MatchString(*query.Cursor) {
		return catalogError(ErrorInvalidQuery, "validate exam catalog list query", errors.New("cursor must be a canonical UUIDv4"))
	}
	if query.Limit < 1 || query.Limit > MaxPageSize {
		return catalogError(ErrorInvalidQuery, "validate exam catalog list query", errors.New("limit is outside the supported range"))
	}
	return nil
}

func validateDetailQuery(ctx context.Context, query DetailQuery) error {
	if ctx == nil {
		return catalogError(ErrorInvalidQuery, "validate exam catalog detail query", errors.New("context is required"))
	}
	if err := validatePrincipal(query.Principal); err != nil {
		return catalogError(ErrorInvalidQuery, "validate exam catalog detail query", err)
	}
	if !canonicalUUIDv4Pattern.MatchString(query.ExamID) {
		return catalogError(ErrorInvalidQuery, "validate exam catalog detail query", errors.New("exam ID must be a canonical UUIDv4"))
	}
	return nil
}

func validatePrincipal(principal auth.AccessPrincipal) error {
	if !canonicalUUIDv4Pattern.MatchString(principal.AccountID) ||
		!canonicalUUIDv4Pattern.MatchString(principal.SessionID) ||
		!canonicalUUIDv4Pattern.MatchString(principal.JWTID) || principal.AuthRevision < 1 {
		return errors.New("access principal is invalid")
	}
	if principal.Role != auth.RoleStudent && principal.Role != auth.RoleAdmin {
		return errors.New("access principal role is invalid")
	}
	return nil
}

func validatePage(page Page, limit int) error {
	if page.Items == nil || len(page.Items) > limit {
		return errors.New("page items are nil or exceed the requested limit")
	}
	seen := make(map[string]struct{}, len(page.Items))
	var previousUpdatedAt time.Time
	for index, item := range page.Items {
		if err := validateSummary(item); err != nil {
			return err
		}
		if _, exists := seen[item.ID]; exists {
			return errors.New("exam ID is duplicated")
		}
		seen[item.ID] = struct{}{}
		if index > 0 && item.UpdatedAt.After(previousUpdatedAt) {
			return errors.New("exam page order is ascending")
		}
		previousUpdatedAt = item.UpdatedAt
	}
	if page.NextCursor != nil {
		if len(page.Items) == 0 || !canonicalUUIDv4Pattern.MatchString(*page.NextCursor) || *page.NextCursor != page.Items[len(page.Items)-1].ID {
			return errors.New("next cursor must identify the final page item")
		}
	}
	return nil
}

func validateSummary(summary ExamSummary) error {
	if !canonicalUUIDv4Pattern.MatchString(summary.ID) || !canonicalUUIDv4Pattern.MatchString(summary.SnapshotID) ||
		summary.Platform != "pintia" || strings.TrimSpace(summary.ProblemSetID) == "" ||
		strings.TrimSpace(summary.Title) == "" || !strings.HasPrefix(summary.SourceURL, "https://pintia.cn/") ||
		summary.ProblemCount < 1 || summary.ParticipantCount < 0 || summary.RankingCount < 0 || summary.SubmissionCount < 0 ||
		summary.RankingCount > summary.ParticipantCount || summary.SnapshotSequence < 1 || summary.HeadRevision < 1 ||
		strings.TrimSpace(summary.ExporterVersion) == "" || !validUTCTime(summary.ExportedAt) || !validUTCTime(summary.UpdatedAt) {
		return errors.New("exam summary violates the public contract")
	}
	if summary.StartsAt != nil && !validUTCTime(*summary.StartsAt) || summary.EndsAt != nil && !validUTCTime(*summary.EndsAt) {
		return errors.New("exam summary contains an invalid source time")
	}
	if summary.StartsAt != nil && summary.EndsAt != nil && summary.StartsAt.After(*summary.EndsAt) {
		return errors.New("exam end precedes its start")
	}
	if summary.TotalScore != nil && !validDecimal(*summary.TotalScore) {
		return errors.New("exam total score is not canonical")
	}
	return nil
}

func validateProblem(problem Problem) error {
	if strings.TrimSpace(problem.ID) == "" || strings.TrimSpace(problem.ProblemID) == "" || strings.TrimSpace(problem.Title) == "" ||
		problem.SubmissionCount < 0 || problem.SubmittingParticipantCount < 0 || problem.PassedParticipantCount < 0 ||
		problem.SubmittingParticipantCount > problem.SubmissionCount || problem.PassedParticipantCount > problem.SubmittingParticipantCount {
		return errors.New("exam problem violates the public contract")
	}
	if problem.Label != nil && strings.TrimSpace(*problem.Label) == "" {
		return errors.New("exam problem label is empty")
	}
	if problem.MaxScore != nil && !validDecimal(*problem.MaxScore) {
		return errors.New("exam problem maximum score is not canonical")
	}
	if problem.TimeLimitMS != nil && *problem.TimeLimitMS < 0 || problem.MemoryLimitBytes != nil && *problem.MemoryLimitBytes < 0 {
		return errors.New("exam problem resource limit is negative")
	}
	return nil
}

func validDecimal(value string) bool {
	return len(value) <= 128 && canonicalDecimal.MatchString(value)
}

func validUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
