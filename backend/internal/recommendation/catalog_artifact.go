package recommendation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

// KnowledgeCatalogArtifact is a canonical, schema-validated catalog document.
// Its slices and document are returned only through copying accessors.
type KnowledgeCatalogArtifact struct {
	document               json.RawMessage
	sha256                 string
	taxonomyID             string
	knowledgePointIDs      []string
	problemAssignmentCount int
}

// ParseKnowledgeCatalogArtifact validates the canonical file bytes and the
// complete knowledge-catalog schema and semantic contract.
func ParseKnowledgeCatalogArtifact(raw []byte) (KnowledgeCatalogArtifact, error) {
	catalog, canonical, digest, err := parseKnowledgeCatalog(json.RawMessage(raw))
	if err != nil {
		return KnowledgeCatalogArtifact{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return KnowledgeCatalogArtifact{}, errors.New("knowledge catalog bytes must already be canonical JSON")
	}
	knowledgePointIDs := make([]string, len(catalog.Points))
	for index, point := range catalog.Points {
		knowledgePointIDs[index] = point.ID
	}
	if err := inferencemodel.ValidateKnowledgePointIDs(knowledgePointIDs); err != nil {
		return KnowledgeCatalogArtifact{}, fmt.Errorf("knowledge catalog identity domain: %w", err)
	}
	return KnowledgeCatalogArtifact{
		document:               append(json.RawMessage(nil), canonical...),
		sha256:                 digest,
		taxonomyID:             catalog.TaxonomyID,
		knowledgePointIDs:      knowledgePointIDs,
		problemAssignmentCount: len(catalog.Assignments),
	}, nil
}

// ValidateModelManifest binds the catalog bytes and ordered knowledge identity
// domain to one parsed inference-model manifest.
func (artifact KnowledgeCatalogArtifact) ValidateModelManifest(manifest inferencemodel.Manifest) error {
	if artifact.sha256 == "" || artifact.sha256 != manifest.KnowledgeCatalogSHA256 {
		return errors.New("knowledge catalog SHA-256 differs from the inference model manifest")
	}
	if !slices.Equal(artifact.knowledgePointIDs, manifest.KnowledgePointIDs) {
		return errors.New("knowledge catalog identities differ from the inference model manifest")
	}
	return nil
}

func (artifact KnowledgeCatalogArtifact) Document() json.RawMessage {
	return append(json.RawMessage(nil), artifact.document...)
}

func (artifact KnowledgeCatalogArtifact) SHA256() string { return artifact.sha256 }

func (artifact KnowledgeCatalogArtifact) TaxonomyID() string { return artifact.taxonomyID }

func (artifact KnowledgeCatalogArtifact) KnowledgePointIDs() []string {
	return slices.Clone(artifact.knowledgePointIDs)
}

func (artifact KnowledgeCatalogArtifact) ProblemAssignmentCount() int {
	return artifact.problemAssignmentCount
}
