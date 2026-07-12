package recommendation

import (
	"encoding/json"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

func TestConfigurationDocumentValidatorReusesTrainingAndCatalogContracts(t *testing.T) {
	t.Parallel()
	dataset := testTrainingDataset(t)
	validator := ConfigurationDocumentValidator{}
	if err := validator.ValidateRecommendationDocument(
		configuration.KindTraining, trainingConfigurationSchemaV2, dataset.Configuration.Document,
	); err != nil {
		t.Fatalf("training validation error=%v", err)
	}
	if err := validator.ValidateRecommendationDocument(
		configuration.KindKnowledgeCatalog, knowledgeCatalogSchemaV1, dataset.KnowledgeCatalog.Document,
	); err != nil {
		t.Fatalf("catalog validation error=%v", err)
	}
	var invalid map[string]any
	if err := json.Unmarshal(dataset.Configuration.Document, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid["unexpected"] = true
	raw, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateRecommendationDocument(configuration.KindTraining, trainingConfigurationSchemaV2, raw); err == nil {
		t.Fatal("validator accepted an unknown training field")
	}
	if err := validator.ValidateRecommendationDocument(configuration.KindTraining, "ascendany.training.other.v1", dataset.Configuration.Document); err == nil {
		t.Fatal("validator accepted a different training schema")
	}
}
