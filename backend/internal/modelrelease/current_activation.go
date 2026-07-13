package modelrelease

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// QueryRower is the read boundary shared by startup verification and online
// recommendation transactions.
type QueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// CurrentActivationExpectation identifies the exact immutable model activation
// that a runtime process has already verified from its release-owned artifact.
type CurrentActivationExpectation struct {
	ReleaseID              int64
	HeadRevision           int64
	ArtifactSHA256         string
	KnowledgeCatalogSHA256 string
	Application            ApplicationIdentity
}

// RequireCurrentActivationCatalog verifies the complete model-head,
// publication, and active-catalog state machine in one database snapshot. H1
// is valid only before the first catalog publication. Every later head must
// consume one publication, and no publication may remain pending for the
// current head.
func RequireCurrentActivationCatalog(
	ctx context.Context,
	queryer QueryRower,
	expected CurrentActivationExpectation,
) error {
	if queryer == nil || expected.ReleaseID < 1 || expected.HeadRevision < 1 ||
		!validLowercaseSHA256(expected.ArtifactSHA256) ||
		!validLowercaseSHA256(expected.KnowledgeCatalogSHA256) ||
		validateApplicationIdentity(expected.Application) != nil {
		return fmt.Errorf("%w: current activation expectation is invalid", ErrInvalidConfiguration)
	}
	var valid bool
	err := queryer.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.recommendation_model_head AS head
    JOIN ascendany.recommendation_model_releases AS release
      ON release.recommendation_model_release_id = head.current_release_id
    JOIN ascendany.recommendation_model_activation_events AS activation
      ON activation.head_revision = head.head_revision
     AND activation.recommendation_model_release_id = head.current_release_id
     AND activation.artifact_sha256 = release.artifact_sha256
    WHERE head.singleton
      AND head.current_release_id = $1
      AND head.head_revision = $2
      AND head.pending_catalog_publication_id IS NULL
      AND release.artifact_sha256 = $3
      AND release.knowledge_catalog_sha256 = $4
      AND activation.application_version = $5
      AND activation.application_commit = $6
      AND activation.application_build_time = $7
      AND NOT EXISTS (
          SELECT 1
          FROM ascendany.knowledge_catalog_publications AS pending
          WHERE pending.current_model_head_revision = head.head_revision
            AND pending.current_model_artifact_sha256 = release.artifact_sha256
            AND NOT EXISTS (
                SELECT 1
                FROM ascendany.recommendation_model_activation_events AS consumed
                WHERE consumed.knowledge_catalog_publication_id = pending.knowledge_catalog_publication_id
            )
      )
      AND (
          (
              head.head_revision = 1
              AND activation.knowledge_catalog_publication_id IS NULL
              AND NOT EXISTS (
                  SELECT 1
                  FROM ascendany.configuration_items AS initial_catalog
                  WHERE initial_catalog.configuration_key = 'recommendation.catalog.active'
                    AND initial_catalog.configuration_kind = 'knowledge_catalog'
              )
          )
          OR
          (
              head.head_revision > 1
              AND activation.knowledge_catalog_publication_id IS NOT NULL
              AND EXISTS (
                  SELECT 1
                  FROM ascendany.knowledge_catalog_publications AS publication
                  JOIN ascendany.recommendation_model_activation_events AS prior_activation
                    ON prior_activation.head_revision = publication.current_model_head_revision
                   AND prior_activation.artifact_sha256 = publication.current_model_artifact_sha256
                  JOIN ascendany.configuration_items AS catalog_item
                    ON catalog_item.configuration_item_id = publication.configuration_item_id
                   AND catalog_item.configuration_key = 'recommendation.catalog.active'
                   AND catalog_item.configuration_kind = 'knowledge_catalog'
                   AND catalog_item.active_version_id = publication.configuration_version_id
                   AND catalog_item.head_revision = publication.configuration_head_revision
                  JOIN ascendany.configuration_versions AS catalog_version
                    ON catalog_version.configuration_item_id = publication.configuration_item_id
                   AND catalog_version.configuration_version_id = publication.configuration_version_id
                   AND catalog_version.configuration_kind = 'knowledge_catalog'
                   AND catalog_version.document_sha256 = publication.catalog_sha256
                   AND catalog_version.credential_ref IS NULL
                  WHERE publication.knowledge_catalog_publication_id = activation.knowledge_catalog_publication_id
                    AND publication.target_model_release_id = head.current_release_id
                    AND publication.target_model_artifact_sha256 = release.artifact_sha256
                    AND publication.catalog_sha256 = release.knowledge_catalog_sha256
                    AND publication.target_application_version = activation.application_version
                    AND publication.target_application_commit = activation.application_commit
                    AND publication.target_application_build_time = activation.application_build_time
                    AND publication.current_model_head_revision = head.head_revision - 1
              )
          )
      )
)`,
		expected.ReleaseID,
		expected.HeadRevision,
		expected.ArtifactSHA256,
		expected.KnowledgeCatalogSHA256,
		expected.Application.Version,
		expected.Application.Commit,
		expected.Application.BuildTime,
	).Scan(&valid)
	if err != nil {
		return fmt.Errorf("query current activation catalog binding: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: current model activation is not bound to the active catalog publication state", ErrStoredDataInvalid)
	}
	return nil
}

func validLowercaseSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
