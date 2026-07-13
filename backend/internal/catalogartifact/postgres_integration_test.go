package catalogartifact

import (
	"os"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
)

// TestPostgresCatalogArtifactLoadBoundary runs inside the audited PostgreSQL
// rehearsal manifest before any recommendation test consumes the catalog. The
// production loader owns the absolute clean path, regular-file, mode, link-count,
// stable-read, digest, schema, semantic, and model-binding checks.
func TestPostgresCatalogArtifactLoadBoundary(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	catalogPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_PATH")
	catalogSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_SHA256")
	modelPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_PATH")
	modelSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_SHA256")
	if databaseURL == "" || catalogPath == "" || catalogSHA256 == "" || modelPath == "" || modelSHA256 == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL and recommendation model/catalog artifact variables are not configured")
	}

	model, err := modelartifact.Load(modelPath, modelSHA256)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(catalogPath, catalogSHA256, model.Model.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256 != catalogSHA256 || loaded.Artifact.SHA256() != catalogSHA256 ||
		loaded.Mode != RequiredMode || loaded.Size < 1 {
		t.Fatalf("loaded catalog boundary = %#v", loaded)
	}
}
