package chatagent

import (
	"context"
)

const (
	frontendProviderOpenAICompatible = "openai_compatible"
	frontendRequestModeChat          = "chat_completions"
)

type frontendProviderMetadata struct {
	Provider    string
	Model       string
	RequestMode string
}

func loadFrontendProviderMetadata(
	ctx context.Context,
	tx postgresTx,
	modelConfigurationVersionID int64,
) (frontendProviderMetadata, bool, error) {
	var snapshot ConfigurationSnapshot
	var document string
	err := tx.QueryRow(ctx, `
SELECT version.configuration_version_id,
       item.configuration_key,
       version.schema_id,
       version.document::text,
       version.document_sha256,
       version.credential_ref
FROM ascendany.configuration_versions AS version
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = version.configuration_item_id
 AND item.configuration_kind = version.configuration_kind
WHERE version.configuration_version_id = $1
  AND version.configuration_kind = 'model_connection'`, modelConfigurationVersionID).Scan(
		&snapshot.VersionDatabaseID,
		&snapshot.Key,
		&snapshot.SchemaID,
		&document,
		&snapshot.DocumentSHA256,
		&snapshot.CredentialRef,
	)
	if err != nil {
		return frontendProviderMetadata{}, false, databaseFailure("load Agent frontend provider metadata", err)
	}
	if err := loadConfigurationDocument(&snapshot, document, "model_connection"); err != nil {
		return frontendProviderMetadata{}, false, err
	}
	if snapshot.SchemaID != OpenAICompatibleModelSchema {
		return frontendProviderMetadata{}, false, nil
	}
	model, failure := parseModelConnection(snapshot)
	if failure != nil {
		return frontendProviderMetadata{}, false, failure
	}
	return frontendProviderMetadata{
		Provider:    frontendProviderOpenAICompatible,
		Model:       model.Model,
		RequestMode: frontendRequestModeChat,
	}, true, nil
}

func addFrontendProviderMetadata(payload map[string]any, metadata frontendProviderMetadata) {
	payload["provider"] = metadata.Provider
	payload["model"] = metadata.Model
	payload["requestMode"] = metadata.RequestMode
}
