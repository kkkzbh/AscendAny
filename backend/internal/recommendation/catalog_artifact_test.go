package recommendation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func TestParseKnowledgeCatalogArtifactBindsCanonicalDocumentAndModel(t *testing.T) {
	t.Parallel()
	document := testCatalogDocument(t, []any{})
	artifact, err := ParseKnowledgeCatalogArtifact(document)
	if err != nil {
		t.Fatalf("ParseKnowledgeCatalogArtifact() error = %v", err)
	}
	if artifact.SHA256() != sha256Bytes(document) || artifact.TaxonomyID() != "core" ||
		artifact.ProblemAssignmentCount() != 0 ||
		strings.Join(artifact.KnowledgePointIDs(), ",") != "arrays,graphs" ||
		string(artifact.Document()) != string(document) {
		t.Fatalf("artifact = %#v", artifact)
	}
	manifest := inferencemodel.Manifest{
		KnowledgeCatalogSHA256: artifact.SHA256(),
		KnowledgePointIDs:      []string{"arrays", "graphs"},
	}
	if err := artifact.ValidateModelManifest(manifest); err != nil {
		t.Fatalf("ValidateModelManifest() error = %v", err)
	}

	documentCopy := artifact.Document()
	documentCopy[0] = '['
	identityCopy := artifact.KnowledgePointIDs()
	identityCopy[0] = "changed"
	if string(artifact.Document()) != string(document) || artifact.KnowledgePointIDs()[0] != "arrays" {
		t.Fatal("artifact accessors exposed mutable state")
	}
}

func TestParseKnowledgeCatalogArtifactRejectsNoncanonicalBytes(t *testing.T) {
	t.Parallel()
	canonical := testCatalogDocument(t, []any{})
	var value any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	noncanonical, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKnowledgeCatalogArtifact(noncanonical); err == nil ||
		!strings.Contains(err.Error(), "must already be canonical") {
		t.Fatalf("ParseKnowledgeCatalogArtifact() error = %v", err)
	}
}

func TestKnowledgeCatalogArtifactRejectsModelManifestDrift(t *testing.T) {
	t.Parallel()
	artifact, err := ParseKnowledgeCatalogArtifact(testCatalogDocument(t, []any{}))
	if err != nil {
		t.Fatal(err)
	}
	tests := []inferencemodel.Manifest{
		{KnowledgeCatalogSHA256: strings.Repeat("a", 64), KnowledgePointIDs: artifact.KnowledgePointIDs()},
		{KnowledgeCatalogSHA256: artifact.SHA256(), KnowledgePointIDs: []string{"arrays", "trees"}},
	}
	for _, manifest := range tests {
		if err := artifact.ValidateModelManifest(manifest); err == nil {
			t.Fatalf("ValidateModelManifest(%#v) error = nil", manifest)
		}
	}
}
