package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

const (
	publicationIssueAnalyticsUnavailable = "recommendation_review_unavailable"
	publicationIssueAnalyticsChanged     = "recommendation_review_changed"
	publicationIssueCatalogCoverage      = "knowledge_catalog_coverage_mismatch"
)

// ConfigurationPublicationContract owns both the pure knowledge-catalog
// document contract and its transactional analytics review precondition.
// Callers publishing through configuration.Service reuse this exact boundary
// from HTTP, a local administrative command, or a deployment oneshot.
type ConfigurationPublicationContract struct{}

func (ConfigurationPublicationContract) ValidateRecommendationDocument(kind configuration.Kind, schemaID string, document json.RawMessage) error {
	if kind != configuration.KindKnowledgeCatalog {
		return errors.New("recommendation validator accepts only knowledge_catalog")
	}
	if schemaID != KnowledgeCatalogSchemaV1 {
		return fmt.Errorf("knowledge catalog schema must be %q", KnowledgeCatalogSchemaV1)
	}
	_, _, _, err := parseKnowledgeCatalog(document)
	return err
}

func (ConfigurationPublicationContract) ValidateVersionWrite(
	ctx context.Context,
	tx configuration.VersionWriteTransaction,
	command configuration.CreateVersionCommand,
) error {
	if command.Kind != configuration.KindKnowledgeCatalog {
		return nil
	}
	catalog, _, _, err := parseKnowledgeCatalog(command.Document)
	if err != nil {
		return configurationPublicationError(configuration.ErrorDocumentInvalid, "validate recommendation catalog document", err)
	}
	if command.ExpectedAnalyticsGenerationID == nil || command.ExpectedAnalyticsHeadRevision == nil ||
		command.ExpectedInputManifestSHA256 == nil {
		return configurationPublicationError(configuration.ErrorInvalidQuery, "validate recommendation review expectation", errors.New("complete analytics review provenance is required"))
	}
	review, err := loadReviewContext(ctx, tx, true)
	if err != nil {
		switch CodeOf(err) {
		case ErrorAnalyticsUnavailable:
			return configurationPublicationError(configuration.ErrorReviewConflict, "lock recommendation review context", &configuration.PublicationIssue{
				IssueCode:                     publicationIssueAnalyticsUnavailable,
				ExpectedAnalyticsGenerationID: *command.ExpectedAnalyticsGenerationID,
				ExpectedAnalyticsHeadRevision: *command.ExpectedAnalyticsHeadRevision,
				ExpectedInputManifestSHA256:   *command.ExpectedInputManifestSHA256,
			})
		case ErrorCanceled:
			return configurationPublicationError(configuration.ErrorCanceled, "lock recommendation review context", err)
		case ErrorDatabase:
			return configurationPublicationError(configuration.ErrorDatabase, "lock recommendation review context", err)
		default:
			return configurationPublicationError(configuration.ErrorStoredDataInvalid, "lock recommendation review context", err)
		}
	}
	currentGenerationID := strconv.FormatInt(review.AnalyticsGenerationID, 10)
	if *command.ExpectedAnalyticsGenerationID != currentGenerationID ||
		*command.ExpectedAnalyticsHeadRevision != review.AnalyticsHeadRevision ||
		*command.ExpectedInputManifestSHA256 != review.InputManifestSHA256 {
		return configurationPublicationError(configuration.ErrorReviewConflict, "compare recommendation review provenance", &configuration.PublicationIssue{
			IssueCode:                     publicationIssueAnalyticsChanged,
			ExpectedAnalyticsGenerationID: *command.ExpectedAnalyticsGenerationID,
			CurrentAnalyticsGenerationID:  currentGenerationID,
			ExpectedAnalyticsHeadRevision: *command.ExpectedAnalyticsHeadRevision,
			CurrentAnalyticsHeadRevision:  review.AnalyticsHeadRevision,
			ExpectedInputManifestSHA256:   *command.ExpectedInputManifestSHA256,
			CurrentInputManifestSHA256:    review.InputManifestSHA256,
		})
	}
	missing, dangling := catalogCoverageDifference(catalog, review.Problems)
	if len(missing) > 0 || len(dangling) > 0 {
		problemKeys := append(slices.Clone(missing), dangling...)
		slices.Sort(problemKeys)
		return configurationPublicationError(configuration.ErrorDocumentInvalid, "validate recommendation catalog coverage", &configuration.PublicationIssue{
			IssueCode:           publicationIssueCatalogCoverage,
			ProblemKeys:         problemKeys,
			MissingProblemKeys:  missing,
			DanglingProblemKeys: dangling,
		})
	}
	return nil
}

func catalogCoverageDifference(catalog knowledgeCatalog, problems []ReviewProblemCandidate) ([]string, []string) {
	authoritative := make(map[string]struct{}, len(problems))
	for _, problem := range problems {
		authoritative[problem.ProblemKey] = struct{}{}
	}
	assignments := make(map[string]struct{}, len(catalog.Assignments))
	for _, assignment := range catalog.Assignments {
		assignments[pintiaProblemKey(assignment.ProblemID, assignment.ProblemFactSHA256)] = struct{}{}
	}
	missing := make([]string, 0)
	for problemKey := range authoritative {
		if _, exists := assignments[problemKey]; !exists {
			missing = append(missing, problemKey)
		}
	}
	dangling := make([]string, 0)
	for problemKey := range assignments {
		if _, exists := authoritative[problemKey]; !exists {
			dangling = append(dangling, problemKey)
		}
	}
	slices.Sort(missing)
	slices.Sort(dangling)
	return missing, dangling
}

func configurationPublicationError(code configuration.ErrorCode, operation string, cause error) error {
	return &configuration.Error{Code: code, Op: operation, Cause: cause}
}
