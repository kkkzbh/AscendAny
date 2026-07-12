package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

type ConfigurationDocumentValidator struct{}

func (ConfigurationDocumentValidator) ValidateRecommendationDocument(
	kind configuration.Kind,
	schemaID string,
	document json.RawMessage,
) error {
	switch kind {
	case configuration.KindTraining:
		if schemaID != trainingConfigurationSchemaV2 {
			return fmt.Errorf("training schema must be %q", trainingConfigurationSchemaV2)
		}
		_, err := parseTrainingConfiguration(document)
		return err
	case configuration.KindKnowledgeCatalog:
		if schemaID != knowledgeCatalogSchemaV1 {
			return fmt.Errorf("knowledge catalog schema must be %q", knowledgeCatalogSchemaV1)
		}
		_, err := parseKnowledgeCatalog(document)
		return err
	default:
		return errors.New("recommendation validator accepts only training and knowledge_catalog kinds")
	}
}
