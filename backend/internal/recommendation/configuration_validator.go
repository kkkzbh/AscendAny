package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

const (
	publicationIssueAnalyticsUnavailable = "recommendation_review_unavailable"
	publicationIssueAnalyticsChanged     = "recommendation_review_changed"
	publicationIssueCatalogCoverage      = "knowledge_catalog_coverage_mismatch"
	publicationIssueModelUnavailable     = "recommendation_model_unavailable"
	publicationIssueModelChanged         = "recommendation_model_changed"
)

// ConfigurationPublicationContract owns both the pure knowledge-catalog
// document contract and its transactional analytics review precondition.
// The stopped-runtime publisher reuses this exact boundary inside the same
// transaction that owns the model, analytics, configuration, and provenance
// locks.
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
		command.ExpectedInputManifestSHA256 == nil || command.ExpectedCurrentModelHeadRevision == nil ||
		command.ExpectedCurrentModelArtifactSHA256 == nil {
		return configurationPublicationError(configuration.ErrorInvalidQuery, "validate recommendation review expectation", errors.New("complete analytics review provenance is required"))
	}
	var currentModelHeadRevision int64
	var currentModelArtifactSHA256 string
	if err := tx.QueryRow(ctx, `
SELECT head.head_revision,
       release.artifact_sha256
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS release
  ON release.recommendation_model_release_id = head.current_release_id
JOIN ascendany.recommendation_model_activation_events AS activation
  ON activation.head_revision = head.head_revision
 AND activation.recommendation_model_release_id = head.current_release_id
 AND activation.artifact_sha256 = release.artifact_sha256
WHERE head.singleton`).Scan(&currentModelHeadRevision, &currentModelArtifactSHA256); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return configurationPublicationError(configuration.ErrorReviewConflict, "read current recommendation model", &configuration.PublicationIssue{
				IssueCode:                          publicationIssueModelUnavailable,
				ExpectedCurrentModelHeadRevision:   *command.ExpectedCurrentModelHeadRevision,
				ExpectedCurrentModelArtifactSHA256: *command.ExpectedCurrentModelArtifactSHA256,
			})
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return configurationPublicationError(configuration.ErrorCanceled, "read current recommendation model", err)
		}
		return configurationPublicationError(configuration.ErrorDatabase, "read current recommendation model", err)
	}
	if currentModelHeadRevision != *command.ExpectedCurrentModelHeadRevision ||
		currentModelArtifactSHA256 != *command.ExpectedCurrentModelArtifactSHA256 {
		return configurationPublicationError(configuration.ErrorReviewConflict, "compare current recommendation model", &configuration.PublicationIssue{
			IssueCode:                          publicationIssueModelChanged,
			ExpectedCurrentModelHeadRevision:   *command.ExpectedCurrentModelHeadRevision,
			CurrentModelHeadRevision:           currentModelHeadRevision,
			ExpectedCurrentModelArtifactSHA256: *command.ExpectedCurrentModelArtifactSHA256,
			CurrentModelArtifactSHA256:         currentModelArtifactSHA256,
		})
	}
	review, err := loadReviewContext(ctx, tx, false)
	if err != nil {
		switch CodeOf(err) {
		case ErrorAnalyticsUnavailable:
			return configurationPublicationError(configuration.ErrorReviewConflict, "read recommendation review context", &configuration.PublicationIssue{
				IssueCode:                     publicationIssueAnalyticsUnavailable,
				ExpectedAnalyticsGenerationID: *command.ExpectedAnalyticsGenerationID,
				ExpectedAnalyticsHeadRevision: *command.ExpectedAnalyticsHeadRevision,
				ExpectedInputManifestSHA256:   *command.ExpectedInputManifestSHA256,
			})
		case ErrorCanceled:
			return configurationPublicationError(configuration.ErrorCanceled, "read recommendation review context", err)
		case ErrorDatabase:
			return configurationPublicationError(configuration.ErrorDatabase, "read recommendation review context", err)
		default:
			return configurationPublicationError(configuration.ErrorStoredDataInvalid, "read recommendation review context", err)
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
