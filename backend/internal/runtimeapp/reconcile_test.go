package runtimeapp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

type scanRow func(...any) error

func (row scanRow) Scan(destinations ...any) error {
	return row(destinations...)
}

type referenceDatabase struct {
	references map[string]artifact.Artifact
}

func (database referenceDatabase) QueryRow(_ context.Context, _ string, arguments ...any) pgx.Row {
	hashValue, _ := arguments[0].(string)
	reference, found := database.references[hashValue]
	if !found {
		return scanRow(func(...any) error { return pgx.ErrNoRows })
	}
	return scanRow(func(destinations ...any) error {
		*(destinations[0].(*int64)) = reference.Size
		*(destinations[1].(*string)) = reference.StorageKey
		return nil
	})
}

func TestArtifactReconcilerRetainsDatabaseReferenceAndRemovesOrphan(t *testing.T) {
	t.Parallel()
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	referenced := publishRuntimeArtifact(t, store, "referenced")
	orphan := publishRuntimeArtifact(t, store, "orphan")
	reconciler := &artifactReconciler{
		store: store,
		database: referenceDatabase{references: map[string]artifact.Artifact{
			referenced.Hash: referenced,
		}},
		minAge: 0,
	}

	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if _, err := os.Stat(referenced.Path); err != nil {
		t.Fatalf("referenced artifact missing: %v", err)
	}
	if _, err := os.Stat(orphan.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan artifact still exists: %v", err)
	}
}

func TestArtifactReconcilerFailsClosedOnDatabaseMetadataMismatch(t *testing.T) {
	t.Parallel()
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	published := publishRuntimeArtifact(t, store, "mismatch")
	mismatched := published
	mismatched.Size++
	reconciler := &artifactReconciler{
		store:    store,
		database: referenceDatabase{references: map[string]artifact.Artifact{published.Hash: mismatched}},
		minAge:   0,
	}

	err = reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "metadata differs") {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if _, statErr := os.Stat(published.Path); statErr != nil {
		t.Fatalf("mismatched referenced artifact was removed: %v", statErr)
	}
}

func publishRuntimeArtifact(t *testing.T, store *artifact.Store, contents string) artifact.Artifact {
	t.Helper()
	publication, err := store.Publish(context.Background(), bytes.NewReader([]byte(contents)))
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.Release(); err != nil {
		t.Fatal(err)
	}
	return publication.Artifact
}
