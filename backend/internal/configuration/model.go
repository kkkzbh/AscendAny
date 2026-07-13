package configuration

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize     = 20
	MaxPageSize         = 100
	KnowledgeCatalogKey = "recommendation.catalog.active"
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
	Principal                     auth.AccessPrincipal
	Key                           string
	Kind                          Kind
	ExpectedHeadRevision          int64
	ExpectedAnalyticsGenerationID *string
	ExpectedAnalyticsHeadRevision *int64
	ExpectedInputManifestSHA256   *string
	SchemaID                      string
	Document                      json.RawMessage
	CredentialRef                 *string
}

type CreateVersionInput struct {
	Key                           string          `json:"key"`
	Kind                          Kind            `json:"kind"`
	ExpectedHeadRevision          int64           `json:"expectedHeadRevision"`
	ExpectedAnalyticsGenerationID *string         `json:"expectedAnalyticsGenerationId,omitempty"`
	ExpectedAnalyticsHeadRevision *int64          `json:"expectedAnalyticsHeadRevision,omitempty"`
	ExpectedInputManifestSHA256   *string         `json:"expectedInputManifestSha256,omitempty"`
	SchemaID                      string          `json:"schemaId"`
	Document                      json.RawMessage `json:"document"`
	CredentialRef                 *string         `json:"credentialRef"`
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
	Item       Item `json:"item"`
	Idempotent bool `json:"idempotent"`
}
