package oj

import (
	"context"
	"path/filepath"
	"testing"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

func TestArtifactOutputPublisherRetainsPublicationUntilRelease(t *testing.T) {
	store, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := NewArtifactOutputPublisher(store)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := publisher.PublishJudgeOutput(context.Background(), []byte("judge output"))
	if err != nil {
		t.Fatal(err)
	}
	if publication.Artifact.MediaType != JudgeOutputMediaType || publication.Artifact.SizeBytes != 12 || publication.Release == nil {
		t.Fatalf("publication=%#v", publication)
	}
	if err := publication.Release(); err != nil {
		t.Fatal(err)
	}
	if err := publication.Release(); err != nil {
		t.Fatalf("second release error=%v", err)
	}
}
