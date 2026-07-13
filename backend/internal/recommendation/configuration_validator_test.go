package recommendation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

type publicationRow func(...any) error

func (row publicationRow) Scan(destinations ...any) error { return row(destinations...) }

type modelAnchorPublicationTx struct {
	headRevision   int64
	artifactSHA256 string
	err            error
	query          string
}

func (*modelAnchorPublicationTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("review query must not run after a model-anchor conflict")
}

func (tx *modelAnchorPublicationTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	tx.query = query
	return publicationRow(func(destinations ...any) error {
		if tx.err != nil {
			return tx.err
		}
		*destinations[0].(*int64) = tx.headRevision
		*destinations[1].(*string) = tx.artifactSHA256
		return nil
	})
}

func TestConfigurationPublicationContractFencesCurrentModelInTransaction(t *testing.T) {
	t.Parallel()
	command := modelAnchorCatalogCommand(t)
	tx := &modelAnchorPublicationTx{headRevision: 4, artifactSHA256: strings.Repeat("c", 64)}
	err := (ConfigurationPublicationContract{}).ValidateVersionWrite(context.Background(), tx, command)
	issue, ok := configuration.PublicationIssueOf(err)
	if configuration.CodeOf(err) != configuration.ErrorReviewConflict || !ok ||
		issue.IssueCode != publicationIssueModelChanged || issue.ExpectedCurrentModelHeadRevision != 3 ||
		issue.CurrentModelHeadRevision != 4 || issue.ExpectedCurrentModelArtifactSHA256 != strings.Repeat("b", 64) ||
		issue.CurrentModelArtifactSHA256 != strings.Repeat("c", 64) || strings.Contains(tx.query, "FOR UPDATE") {
		t.Fatalf("error=%v issue=%#v query=%s", err, issue, tx.query)
	}
}

func TestConfigurationPublicationContractRejectsMissingCurrentModel(t *testing.T) {
	t.Parallel()
	tx := &modelAnchorPublicationTx{err: pgx.ErrNoRows}
	err := (ConfigurationPublicationContract{}).ValidateVersionWrite(context.Background(), tx, modelAnchorCatalogCommand(t))
	issue, ok := configuration.PublicationIssueOf(err)
	if configuration.CodeOf(err) != configuration.ErrorReviewConflict || !ok ||
		issue.IssueCode != publicationIssueModelUnavailable || issue.ExpectedCurrentModelHeadRevision != 3 ||
		issue.ExpectedCurrentModelArtifactSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("error=%v issue=%#v", err, issue)
	}
	if !errors.Is(tx.err, pgx.ErrNoRows) {
		t.Fatal("test transaction error changed")
	}
}

func modelAnchorCatalogCommand(t *testing.T) configuration.CreateVersionCommand {
	t.Helper()
	generationID := "7"
	analyticsHeadRevision := int64(2)
	inputManifestSHA256 := strings.Repeat("a", 64)
	modelHeadRevision := int64(3)
	modelArtifactSHA256 := strings.Repeat("b", 64)
	return configuration.CreateVersionCommand{
		Key: configuration.KnowledgeCatalogKey, Kind: configuration.KindKnowledgeCatalog,
		ExpectedHeadRevision: 1, ExpectedAnalyticsGenerationID: &generationID,
		ExpectedAnalyticsHeadRevision: &analyticsHeadRevision, ExpectedInputManifestSHA256: &inputManifestSHA256,
		ExpectedCurrentModelHeadRevision: &modelHeadRevision, ExpectedCurrentModelArtifactSHA256: &modelArtifactSHA256,
		SchemaID: KnowledgeCatalogSchemaV1, Document: testCatalogDocument(t, []any{}),
	}
}
