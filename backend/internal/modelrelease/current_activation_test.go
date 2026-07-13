package modelrelease

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRequireCurrentActivationCatalogUsesClosedStateMachineQuery(t *testing.T) {
	t.Parallel()
	expected := CurrentActivationExpectation{
		ReleaseID: 7, HeadRevision: 2,
		ArtifactSHA256:         strings.Repeat("a", 64),
		KnowledgeCatalogSHA256: strings.Repeat("b", 64),
		Application: ApplicationIdentity{
			Version: "0.2.0", Commit: strings.Repeat("c", 40), BuildTime: "2026-07-13T12:00:00Z",
		},
	}
	queryer := &activationCatalogQueryer{valid: true}
	if err := RequireCurrentActivationCatalog(context.Background(), queryer, expected); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"head.pending_catalog_publication_id IS NULL",
		"pending.current_model_head_revision = head.head_revision",
		"consumed.knowledge_catalog_publication_id",
		"head.head_revision = 1",
		"activation.knowledge_catalog_publication_id IS NULL",
		"head.head_revision > 1",
		"publication.knowledge_catalog_publication_id = activation.knowledge_catalog_publication_id",
		"catalog_item.configuration_key = 'recommendation.catalog.active'",
		"catalog_item.active_version_id = publication.configuration_version_id",
		"catalog_version.document_sha256 = publication.catalog_sha256",
	} {
		if !strings.Contains(queryer.sql, fragment) {
			t.Errorf("activation catalog query is missing %q", fragment)
		}
	}
	wantArguments := []any{
		expected.ReleaseID, expected.HeadRevision, expected.ArtifactSHA256, expected.KnowledgeCatalogSHA256,
		expected.Application.Version, expected.Application.Commit, expected.Application.BuildTime,
	}
	if len(queryer.arguments) != len(wantArguments) {
		t.Fatalf("arguments=%v", queryer.arguments)
	}
	for index := range wantArguments {
		if queryer.arguments[index] != wantArguments[index] {
			t.Fatalf("argument[%d]=%v expected=%v", index, queryer.arguments[index], wantArguments[index])
		}
	}

	queryer.valid = false
	if err := RequireCurrentActivationCatalog(context.Background(), queryer, expected); !errors.Is(err, ErrStoredDataInvalid) {
		t.Fatalf("invalid state error=%v", err)
	}
}

func TestRequireCurrentActivationCatalogRejectsInvalidExpectation(t *testing.T) {
	t.Parallel()
	queryer := &activationCatalogQueryer{valid: true}
	err := RequireCurrentActivationCatalog(context.Background(), queryer, CurrentActivationExpectation{})
	if !errors.Is(err, ErrInvalidConfiguration) || queryer.sql != "" {
		t.Fatalf("error=%v query=%q", err, queryer.sql)
	}
}

type activationCatalogQueryer struct {
	valid     bool
	sql       string
	arguments []any
}

func (queryer *activationCatalogQueryer) QueryRow(_ context.Context, sql string, arguments ...any) pgx.Row {
	queryer.sql = sql
	queryer.arguments = append([]any(nil), arguments...)
	return activationCatalogRow{valid: queryer.valid}
}

type activationCatalogRow struct {
	valid bool
}

func (row activationCatalogRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected activation catalog scan shape")
	}
	*destinations[0].(*bool) = row.valid
	return nil
}
