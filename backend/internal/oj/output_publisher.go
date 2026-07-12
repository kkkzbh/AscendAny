package oj

import (
	"bytes"
	"context"
	"errors"
	"io"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

type ArtifactStore interface {
	Publish(context.Context, io.Reader) (*artifactstore.Publication, error)
}

type ArtifactOutputPublisher struct {
	store ArtifactStore
}

func NewArtifactOutputPublisher(store ArtifactStore) (*ArtifactOutputPublisher, error) {
	if store == nil {
		return nil, ojError(ErrorInvalidConfiguration, true, "construct OJ output publisher", errors.New("artifact store is required"))
	}
	return &ArtifactOutputPublisher{store: store}, nil
}

func (publisher *ArtifactOutputPublisher) PublishJudgeOutput(ctx context.Context, output []byte) (*PublishedOutput, error) {
	if ctx == nil || len(output) == 0 {
		return nil, ojError(ErrorInvalidInput, true, "publish OJ judge output", errors.New("context and non-empty output are required"))
	}
	publication, err := publisher.store.Publish(ctx, bytes.NewReader(output))
	if err != nil {
		return nil, ojError(ErrorArtifactFailure, false, "publish OJ judge output", err)
	}
	if publication == nil {
		return nil, ojError(ErrorStoredDataInvalid, true, "publish OJ judge output", errors.New("artifact store returned no publication"))
	}
	return &PublishedOutput{
		Artifact: Artifact{
			SHA256: publication.Artifact.Hash, SizeBytes: publication.Artifact.Size,
			MediaType: JudgeOutputMediaType, StorageKey: publication.Artifact.StorageKey,
		},
		Release: publication.Release,
	}, nil
}
