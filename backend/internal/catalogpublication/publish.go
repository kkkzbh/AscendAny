package catalogpublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/catalogartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/releaseidentity"
)

const (
	ReceiptSchema       = "ascendany.knowledge_catalog.publication-receipt.v1"
	MaximumReceiptBytes = 4096
)

var (
	canonicalUUIDv4      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	lowercaseSHA256Value = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type ConfigurationPublisher interface {
	PublishKnowledgeCatalogVersion(context.Context, string, configuration.KnowledgeCatalogPublicationInput) (configuration.CreateVersionResult, error)
}

type Receipt struct {
	Schema                            string `json:"schema"`
	AuthorizationID                   string `json:"authorizationId"`
	KnowledgeCatalogPublicationID     string `json:"knowledgeCatalogPublicationId"`
	TargetModelReleaseID              string `json:"targetModelReleaseId"`
	CatalogSHA256                     string `json:"catalogSha256"`
	ModelArtifactSHA256               string `json:"modelArtifactSha256"`
	ModelID                           string `json:"modelId"`
	TargetApplicationVersion          string `json:"targetApplicationVersion"`
	TargetApplicationCommit           string `json:"targetApplicationCommit"`
	TargetApplicationBuildTime        string `json:"targetApplicationBuildTime"`
	ConfigurationKey                  string `json:"configurationKey"`
	ConfigurationID                   string `json:"configurationId"`
	ExpectedConfigurationHeadRevision int64  `json:"expectedConfigurationHeadRevision"`
	ConfigurationHeadRevision         int64  `json:"configurationHeadRevision"`
	ConfigurationVersionID            string `json:"configurationVersionId"`
	ConfigurationVersionNumber        int64  `json:"configurationVersionNumber"`
	AnalyticsGenerationID             string `json:"analyticsGenerationId"`
	AnalyticsHeadRevision             int64  `json:"analyticsHeadRevision"`
	InputManifestSHA256               string `json:"inputManifestSha256"`
	CurrentModelHeadRevision          int64  `json:"currentModelHeadRevision"`
	CurrentModelArtifactSHA256        string `json:"currentModelArtifactSha256"`
	PublishedByAccountID              string `json:"publishedByAccountId"`
	PublishedBySessionID              string `json:"publishedBySessionId"`
	PublishedAt                       string `json:"publishedAt"`
	AuditEventID                      string `json:"auditEventId"`
	ConfigurationMutated              bool   `json:"configurationMutated"`
}

func Publish(
	ctx context.Context,
	publisher ConfigurationPublisher,
	accessToken string,
	request Request,
	catalog catalogartifact.Loaded,
	model modelartifact.Loaded,
	targetApplication modelrelease.ApplicationIdentity,
) (Receipt, error) {
	if ctx == nil || publisher == nil || !compactJWT.MatchString(accessToken) || model.Model == nil {
		return Receipt{}, errors.New("catalog publisher, access token, context, and model are required")
	}
	if !validApplicationIdentity(targetApplication.Version, targetApplication.Commit, targetApplication.BuildTime) {
		return Receipt{}, errors.New("catalog publisher release identity is invalid")
	}
	if catalog.SHA256 == "" || catalog.SHA256 != catalog.Artifact.SHA256() {
		return Receipt{}, errors.New("loaded catalog digest is invalid")
	}
	if model.SHA256 == "" || model.SHA256 != model.Model.SHA256() {
		return Receipt{}, errors.New("loaded model digest is invalid")
	}
	if err := catalog.Artifact.ValidateModelManifest(model.Model.Manifest()); err != nil {
		return Receipt{}, fmt.Errorf("catalog/model binding: %w", err)
	}
	canonicalRequest, requestErr := CanonicalRequest(request)
	if requestErr != nil || request.TargetCatalogSHA256 != catalog.SHA256 ||
		request.TargetModelArtifactSHA256 != model.SHA256 ||
		request.TargetApplicationVersion != targetApplication.Version ||
		request.TargetApplicationCommit != targetApplication.Commit ||
		request.TargetApplicationBuildTime != targetApplication.BuildTime {
		return Receipt{}, errors.New("catalog publication request differs from the loaded release")
	}

	result, err := publisher.PublishKnowledgeCatalogVersion(ctx, accessToken, configuration.KnowledgeCatalogPublicationInput{
		PublicationAuthorizationID:         request.AuthorizationID,
		PublicationAuthorizationRequest:    canonicalRequest,
		Key:                                configuration.KnowledgeCatalogKey,
		Kind:                               configuration.KindKnowledgeCatalog,
		ExpectedHeadRevision:               request.ExpectedConfigurationHeadRevision,
		ExpectedAnalyticsGenerationID:      request.ExpectedAnalyticsGenerationID,
		ExpectedAnalyticsHeadRevision:      request.ExpectedAnalyticsHeadRevision,
		ExpectedInputManifestSHA256:        request.ExpectedInputManifestSHA256,
		ExpectedCurrentModelHeadRevision:   request.ExpectedCurrentModelHeadRevision,
		ExpectedCurrentModelArtifactSHA256: request.ExpectedCurrentModelArtifactSHA256,
		TargetCatalogSHA256:                request.TargetCatalogSHA256,
		TargetModelID:                      model.Model.Manifest().ModelID,
		TargetModelArtifactSHA256:          request.TargetModelArtifactSHA256,
		TargetApplicationVersion:           request.TargetApplicationVersion,
		TargetApplicationCommit:            request.TargetApplicationCommit,
		TargetApplicationBuildTime:         request.TargetApplicationBuildTime,
		SchemaID:                           recommendation.KnowledgeCatalogSchemaV1,
		Document:                           catalog.Artifact.Document(),
		CredentialRef:                      nil,
	})
	if err != nil {
		return Receipt{}, err
	}
	if result.Item.Key != configuration.KnowledgeCatalogKey ||
		result.Item.Kind != configuration.KindKnowledgeCatalog ||
		result.Item.ActiveVersion == nil || result.AuditEventID < 1 {
		return Receipt{}, errors.New("catalog publication result violates the stopped-runtime receipt contract")
	}
	active := result.Item.ActiveVersion
	if active.SchemaID != recommendation.KnowledgeCatalogSchemaV1 || active.DocumentSHA256 != catalog.SHA256 ||
		active.CredentialRef != nil || !bytes.Equal(active.Document, catalog.Artifact.Document()) ||
		active.Number < 1 || active.CreatedAt.IsZero() {
		return Receipt{}, errors.New("active catalog version differs from the release artifact")
	}
	manifest := model.Model.Manifest()
	publication := result.KnowledgeCatalogPublication
	if publication == nil || !canonicalPositiveInt64(publication.KnowledgeCatalogPublicationID) ||
		publication.AuthorizationID != request.AuthorizationID ||
		!canonicalPositiveInt64(publication.TargetModelReleaseID) ||
		publication.CatalogSHA256 != catalog.SHA256 ||
		publication.TargetModelArtifactSHA256 != model.SHA256 || publication.TargetModelID != manifest.ModelID ||
		publication.TargetApplicationVersion != targetApplication.Version ||
		publication.TargetApplicationCommit != targetApplication.Commit ||
		publication.TargetApplicationBuildTime != targetApplication.BuildTime ||
		publication.ConfigurationID != result.Item.ID ||
		publication.ExpectedConfigurationHeadRevision != request.ExpectedConfigurationHeadRevision ||
		publication.ConfigurationHeadRevision != result.Item.HeadRevision ||
		publication.ConfigurationMutated && publication.ConfigurationHeadRevision != publication.ExpectedConfigurationHeadRevision+1 ||
		!publication.ConfigurationMutated && publication.ConfigurationHeadRevision != publication.ExpectedConfigurationHeadRevision ||
		publication.ConfigurationVersionID != active.ID ||
		publication.ConfigurationVersionNumber != active.Number ||
		publication.AnalyticsGenerationID != request.ExpectedAnalyticsGenerationID ||
		publication.AnalyticsHeadRevision != request.ExpectedAnalyticsHeadRevision ||
		publication.InputManifestSHA256 != request.ExpectedInputManifestSHA256 ||
		publication.CurrentModelHeadRevision != request.ExpectedCurrentModelHeadRevision ||
		publication.CurrentModelArtifactSHA256 != request.ExpectedCurrentModelArtifactSHA256 ||
		publication.ConfigurationMutated && publication.PublishedByAccountID != active.CreatedByAccountID ||
		publication.ConfigurationMutated && publication.PublishedBySessionID != active.CreatedBySessionID ||
		publication.ConfigurationMutated && !publication.PublishedAt.Equal(active.CreatedAt) ||
		publication.AuditEventID != result.AuditEventID {
		return Receipt{}, errors.New("database catalog publication provenance differs from the release transaction")
	}
	return Receipt{
		Schema:                            ReceiptSchema,
		AuthorizationID:                   publication.AuthorizationID,
		KnowledgeCatalogPublicationID:     publication.KnowledgeCatalogPublicationID,
		TargetModelReleaseID:              publication.TargetModelReleaseID,
		CatalogSHA256:                     publication.CatalogSHA256,
		ModelArtifactSHA256:               publication.TargetModelArtifactSHA256,
		ModelID:                           publication.TargetModelID,
		TargetApplicationVersion:          publication.TargetApplicationVersion,
		TargetApplicationCommit:           publication.TargetApplicationCommit,
		TargetApplicationBuildTime:        publication.TargetApplicationBuildTime,
		ConfigurationKey:                  result.Item.Key,
		ConfigurationID:                   publication.ConfigurationID,
		ExpectedConfigurationHeadRevision: publication.ExpectedConfigurationHeadRevision,
		ConfigurationHeadRevision:         publication.ConfigurationHeadRevision,
		ConfigurationVersionID:            publication.ConfigurationVersionID,
		ConfigurationVersionNumber:        publication.ConfigurationVersionNumber,
		AnalyticsGenerationID:             publication.AnalyticsGenerationID,
		AnalyticsHeadRevision:             publication.AnalyticsHeadRevision,
		InputManifestSHA256:               publication.InputManifestSHA256,
		CurrentModelHeadRevision:          publication.CurrentModelHeadRevision,
		CurrentModelArtifactSHA256:        publication.CurrentModelArtifactSHA256,
		PublishedByAccountID:              publication.PublishedByAccountID,
		PublishedBySessionID:              publication.PublishedBySessionID,
		PublishedAt:                       publication.PublishedAt.UTC().Format(time.RFC3339Nano),
		AuditEventID:                      strconv.FormatInt(publication.AuditEventID, 10),
		ConfigurationMutated:              publication.ConfigurationMutated,
	}, nil
}

func CanonicalReceipt(receipt Receipt) ([]byte, error) {
	if receipt.Schema != ReceiptSchema || !canonicalUUIDv4.MatchString(receipt.AuthorizationID) ||
		!canonicalPositiveInt64(receipt.KnowledgeCatalogPublicationID) ||
		!canonicalPositiveInt64(receipt.TargetModelReleaseID) ||
		!lowercaseSHA256Value.MatchString(receipt.CatalogSHA256) ||
		!lowercaseSHA256Value.MatchString(receipt.ModelArtifactSHA256) || !canonicalUUIDv4.MatchString(receipt.ModelID) ||
		!validApplicationIdentity(receipt.TargetApplicationVersion, receipt.TargetApplicationCommit, receipt.TargetApplicationBuildTime) ||
		receipt.ConfigurationKey != configuration.KnowledgeCatalogKey ||
		!canonicalUUIDv4.MatchString(receipt.ConfigurationID) || receipt.ExpectedConfigurationHeadRevision < 0 ||
		receipt.ConfigurationMutated && receipt.ConfigurationHeadRevision != receipt.ExpectedConfigurationHeadRevision+1 ||
		!receipt.ConfigurationMutated && receipt.ConfigurationHeadRevision != receipt.ExpectedConfigurationHeadRevision ||
		!canonicalPositiveInt64(receipt.ConfigurationVersionID) || receipt.ConfigurationVersionNumber < 1 ||
		!canonicalPositiveInt64(receipt.AnalyticsGenerationID) || receipt.AnalyticsHeadRevision < 1 ||
		!lowercaseSHA256Value.MatchString(receipt.InputManifestSHA256) || !canonicalUUIDv4.MatchString(receipt.PublishedByAccountID) ||
		receipt.CurrentModelHeadRevision < 1 || !lowercaseSHA256Value.MatchString(receipt.CurrentModelArtifactSHA256) ||
		!canonicalUUIDv4.MatchString(receipt.PublishedBySessionID) || receipt.PublishedAt == "" ||
		!canonicalPositiveInt64(receipt.AuditEventID) {
		return nil, errors.New("publication receipt is incomplete")
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, receipt.PublishedAt)
	if err != nil || publishedAt.Location() != time.UTC || publishedAt.Format(time.RFC3339Nano) != receipt.PublishedAt {
		return nil, errors.New("publication receipt timestamp is invalid")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	canonical, _, err := canonicaljson.Object(raw, MaximumReceiptBytes)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

// ParseReceipt decodes one exact canonical production receipt. The closed
// shape is shared by durable receipt files and backup database descriptors.
func ParseReceipt(raw []byte) (Receipt, error) {
	canonical, _, err := canonicaljson.Object(raw, MaximumReceiptBytes)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Receipt{}, errors.New("publication receipt bytes must be canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("publication receipt shape: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Receipt{}, errors.New("publication receipt contains a trailing value")
	}
	reencoded, err := CanonicalReceipt(receipt)
	if err != nil || !bytes.Equal(reencoded, canonical) {
		return Receipt{}, errors.New("publication receipt violates the production contract")
	}
	return receipt, nil
}

func canonicalPositiveInt64(value string) bool {
	if value == "" || value[0] == '0' || len(value) > 19 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}

func validApplicationIdentity(version, commit, buildTime string) bool {
	return releaseidentity.Validate(version, commit, buildTime) == nil
}
