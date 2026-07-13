package configuration

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize                 = 20
	MaxPageSize                     = 100
	KnowledgeCatalogKey             = "recommendation.catalog.active"
	KnowledgeCatalogSchemaID        = "ascendany.knowledge_catalog.recommendation.v1"
	CatalogPublicationRequestSchema = "ascendany.knowledge_catalog.publication-request.v1"
)

type Kind string

const (
	KindPrompt           Kind = "prompt"
	KindModelConnection  Kind = "model_connection"
	KindKnowledgeCatalog Kind = "knowledge_catalog"
	KindFeedbackPolicy   Kind = "feedback_policy"
	KindFeedbackDelivery Kind = "feedback_delivery"
)

type ListQuery struct {
	Principal auth.AccessPrincipal
	Kind      *Kind
	AfterKey  *string
	Limit     int
}

type ItemQuery struct {
	Principal auth.AccessPrincipal
	Key       string
}

type VersionsQuery struct {
	Principal    auth.AccessPrincipal
	Key          string
	BeforeNumber *int64
	Limit        int
}

type CreateVersionCommand struct {
	Principal                          auth.AccessPrincipal
	PublicationAuthorizationID         string
	PublicationAccessTokenSHA256       string
	PublicationAuthorizationRequest    json.RawMessage
	Key                                string
	Kind                               Kind
	ExpectedHeadRevision               int64
	ExpectedAnalyticsGenerationID      *string
	ExpectedAnalyticsHeadRevision      *int64
	ExpectedInputManifestSHA256        *string
	ExpectedCurrentModelHeadRevision   *int64
	ExpectedCurrentModelArtifactSHA256 *string
	TargetCatalogSHA256                string
	TargetModelID                      string
	TargetModelArtifactSHA256          string
	TargetApplicationVersion           string
	TargetApplicationCommit            string
	TargetApplicationBuildTime         string
	SchemaID                           string
	Document                           json.RawMessage
	CredentialRef                      *string
}

type CreateVersionInput struct {
	Key                  string          `json:"key"`
	Kind                 Kind            `json:"kind"`
	ExpectedHeadRevision int64           `json:"expectedHeadRevision"`
	SchemaID             string          `json:"schemaId"`
	Document             json.RawMessage `json:"document"`
	CredentialRef        *string         `json:"credentialRef"`
}

// KnowledgeCatalogPublicationInput is accepted only by the stopped-runtime
// catalog publication application boundary.
type KnowledgeCatalogPublicationInput struct {
	PublicationAuthorizationID         string
	PublicationAuthorizationRequest    json.RawMessage
	Key                                string
	Kind                               Kind
	ExpectedHeadRevision               int64
	ExpectedAnalyticsGenerationID      string
	ExpectedAnalyticsHeadRevision      int64
	ExpectedInputManifestSHA256        string
	ExpectedCurrentModelHeadRevision   int64
	ExpectedCurrentModelArtifactSHA256 string
	TargetCatalogSHA256                string
	TargetModelID                      string
	TargetModelArtifactSHA256          string
	TargetApplicationVersion           string
	TargetApplicationCommit            string
	TargetApplicationBuildTime         string
	SchemaID                           string
	Document                           json.RawMessage
	CredentialRef                      *string
}

// CatalogPublicationIntent is the exact release intent authorized while the
// online runtime still owns the current analytics, model, and configuration
// heads. The stopped-runtime publisher receives the same fields plus the
// immutable authorization identity.
type CatalogPublicationIntent struct {
	Schema                             string `json:"schema"`
	ExpectedConfigurationHeadRevision  int64  `json:"expectedConfigurationHeadRevision"`
	ExpectedAnalyticsGenerationID      string `json:"expectedAnalyticsGenerationId"`
	ExpectedAnalyticsHeadRevision      int64  `json:"expectedAnalyticsHeadRevision"`
	ExpectedInputManifestSHA256        string `json:"expectedInputManifestSha256"`
	ExpectedCurrentModelHeadRevision   int64  `json:"expectedCurrentModelHeadRevision"`
	ExpectedCurrentModelArtifactSHA256 string `json:"expectedCurrentModelArtifactSha256"`
	TargetCatalogSHA256                string `json:"targetCatalogSha256"`
	TargetModelArtifactSHA256          string `json:"targetModelArtifactSha256"`
	TargetApplicationVersion           string `json:"targetApplicationVersion"`
	TargetApplicationCommit            string `json:"targetApplicationCommit"`
	TargetApplicationBuildTime         string `json:"targetApplicationBuildTime"`
}

type CatalogPublicationAuthorizationInput struct {
	PublicationIntent CatalogPublicationIntent `json:"publicationIntent"`
	Document          json.RawMessage          `json:"document"`
}

type CreateCatalogPublicationAuthorizationCommand struct {
	Principal         auth.AccessPrincipal
	AccessTokenSHA256 string
	PublicationIntent CatalogPublicationIntent
	Document          json.RawMessage
}

type CatalogPublicationAuthorizationRecord struct {
	AuthorizationID    string
	ExpiresAt          time.Time
	PublicationRequest AuthorizedCatalogPublicationRequest
}

type AuthorizedCatalogPublicationRequest struct {
	AuthorizationID string `json:"authorizationId"`
	CatalogPublicationIntent
}

type CatalogPublicationAuthorizationResult struct {
	AuthorizationID    string                              `json:"authorizationId"`
	ExpiresAt          time.Time                           `json:"expiresAt"`
	PublicationRequest AuthorizedCatalogPublicationRequest `json:"publicationRequest"`
}

type ItemPage struct {
	Items      []Item  `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

type VersionPage struct {
	Key              string    `json:"key"`
	Kind             Kind      `json:"kind"`
	HeadRevision     int64     `json:"headRevision"`
	Items            []Version `json:"items"`
	NextBeforeNumber *int64    `json:"nextBeforeNumber"`
}

type Item struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	Kind          Kind      `json:"kind"`
	HeadRevision  int64     `json:"headRevision"`
	ActiveVersion *Version  `json:"activeVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Version struct {
	ID                 string          `json:"id"`
	Number             int64           `json:"number"`
	SchemaID           string          `json:"schemaId"`
	Document           json.RawMessage `json:"document"`
	DocumentSHA256     string          `json:"documentSha256"`
	CredentialRef      *string         `json:"credentialRef"`
	CreatedByAccountID string          `json:"createdByAccountId"`
	CreatedBySessionID string          `json:"createdBySessionId"`
	CreatedAt          time.Time       `json:"createdAt"`
}

type CreateVersionResult struct {
	Item                        Item                         `json:"item"`
	Idempotent                  bool                         `json:"idempotent"`
	AuditEventID                int64                        `json:"-"`
	KnowledgeCatalogPublication *KnowledgeCatalogPublication `json:"-"`
}

// KnowledgeCatalogPublication is the immutable database-owned recovery record
// for one release catalog publication.
type KnowledgeCatalogPublication struct {
	AuthorizationID                   string    `json:"authorizationId"`
	KnowledgeCatalogPublicationID     string    `json:"knowledgeCatalogPublicationId"`
	TargetModelReleaseID              string    `json:"targetModelReleaseId"`
	CatalogSHA256                     string    `json:"catalogSha256"`
	TargetModelArtifactSHA256         string    `json:"targetModelArtifactSha256"`
	TargetModelID                     string    `json:"targetModelId"`
	TargetApplicationVersion          string    `json:"targetApplicationVersion"`
	TargetApplicationCommit           string    `json:"targetApplicationCommit"`
	TargetApplicationBuildTime        string    `json:"targetApplicationBuildTime"`
	ConfigurationID                   string    `json:"configurationId"`
	ExpectedConfigurationHeadRevision int64     `json:"expectedConfigurationHeadRevision"`
	ConfigurationHeadRevision         int64     `json:"configurationHeadRevision"`
	ConfigurationMutated              bool      `json:"configurationMutated"`
	ConfigurationVersionID            string    `json:"configurationVersionId"`
	ConfigurationVersionNumber        int64     `json:"configurationVersionNumber"`
	AnalyticsGenerationID             string    `json:"analyticsGenerationId"`
	AnalyticsHeadRevision             int64     `json:"analyticsHeadRevision"`
	InputManifestSHA256               string    `json:"inputManifestSha256"`
	CurrentModelHeadRevision          int64     `json:"currentModelHeadRevision"`
	CurrentModelArtifactSHA256        string    `json:"currentModelArtifactSha256"`
	PublishedByAccountID              string    `json:"publishedByAccountId"`
	PublishedBySessionID              string    `json:"publishedBySessionId"`
	PublishedAt                       time.Time `json:"publishedAt"`
	AuditEventID                      int64     `json:"auditEventId"`
}
