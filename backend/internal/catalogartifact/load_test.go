package catalogartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func TestLoadBindsCatalogFileDigestAndModel(t *testing.T) {
	t.Parallel()
	catalogBytes, model := testCatalogAndModel(t)
	path, digest := writeCatalog(t, catalogBytes, RequiredMode)

	loaded, err := Load(path, digest, model.Manifest())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.SHA256 != digest || loaded.Size != int64(len(catalogBytes)) || loaded.Mode != RequiredMode ||
		loaded.Artifact.SHA256() != digest || loaded.Artifact.TaxonomyID() != "e2e_test_only" ||
		strings.Join(loaded.Artifact.KnowledgePointIDs(), ",") != "arrays,graphs" ||
		loaded.Artifact.ProblemAssignmentCount() != 2 {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestLoadRejectsCatalogBoundaryDrift(t *testing.T) {
	t.Parallel()
	catalogBytes, model := testCatalogAndModel(t)
	_, digest := writeCatalog(t, catalogBytes, RequiredMode)

	t.Run("relative path", func(t *testing.T) {
		if _, err := Load("catalog.json", digest, model.Manifest()); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("independent digest", func(t *testing.T) {
		path, _ := writeCatalog(t, catalogBytes, RequiredMode)
		if _, err := Load(path, strings.Repeat("f", 64), model.Manifest()); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("replacement after independent review", func(t *testing.T) {
		path, reviewedDigest := writeCatalog(t, catalogBytes, RequiredMode)
		replacement := []byte(strings.Replace(string(catalogBytes), "E2E array knowledge", "E2E Array knowledge", 1))
		if string(replacement) == string(catalogBytes) {
			t.Fatal("catalog replacement fixture did not change the reviewed bytes")
		}
		if err := os.WriteFile(path, replacement, RequiredMode); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, reviewedDigest, model.Manifest()); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("unsafe mode", func(t *testing.T) {
		path, _ := writeCatalog(t, catalogBytes, 0o600)
		if _, err := Load(path, digest, model.Manifest()); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("leaf symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.json")
		if err := os.WriteFile(target, catalogBytes, RequiredMode); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "catalog.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(link, digest, model.Manifest()); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("hard link", func(t *testing.T) {
		path, _ := writeCatalog(t, catalogBytes, RequiredMode)
		if err := os.Link(path, path+".second"); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, digest, model.Manifest()); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("noncanonical bytes", func(t *testing.T) {
		pretty := append([]byte(" \n"), catalogBytes...)
		path, prettyDigest := writeCatalog(t, pretty, RequiredMode)
		if _, err := Load(path, prettyDigest, model.Manifest()); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("Load() error = %v", err)
		}
	})
}

func TestLoadRejectsModelManifestDigestAndIdentityDrift(t *testing.T) {
	t.Parallel()
	catalogBytes, model := testCatalogAndModel(t)
	path, digest := writeCatalog(t, catalogBytes, RequiredMode)

	manifest := model.Manifest()
	manifest.KnowledgeCatalogSHA256 = strings.Repeat("a", 64)
	if _, err := Load(path, digest, manifest); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("Load(digest drift) error = %v", err)
	}
	manifest = model.Manifest()
	manifest.KnowledgePointIDs = []string{"arrays", "trees"}
	if _, err := Load(path, digest, manifest); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("Load(identity drift) error = %v", err)
	}
}

func writeCatalog(t *testing.T, raw []byte, mode os.FileMode) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "knowledge-catalog.json")
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return path, hex.EncodeToString(digest[:])
}

func testCatalogAndModel(t *testing.T) ([]byte, *inferencemodel.Model) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate catalog artifact test source")
	}
	fixtureDirectory := filepath.Clean(filepath.Join(
		filepath.Dir(source), "..", "..", "..", "contracts", "recommendation", "fixtures",
	))
	catalogBytes, err := os.ReadFile(filepath.Join(fixtureDirectory, "e2e-test-only.knowledge-catalog.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	modelBytes, err := os.ReadFile(filepath.Join(fixtureDirectory, "e2e-test-only.inference-model.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	model, err := inferencemodel.Parse(modelBytes)
	if err != nil {
		t.Fatal(err)
	}
	return catalogBytes, model
}
