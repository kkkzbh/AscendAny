package backup

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/catalogpublication"
)

const (
	DatabaseDumpFilename          = "database.dump"
	ArtifactArchiveFilename       = "artifacts.tar.zst"
	CatalogReceiptArchiveFilename = "catalog-receipts.tar.zst"
	ManifestFilename              = "manifest.json"
	ManifestDigestFilename        = "manifest.sha256"
)

type FileDescriptor struct {
	Filename  string `json:"filename"`
	Format    string `json:"format"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type ArtifactDescriptor struct {
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	StorageKey string `json:"storageKey"`
}

type ArtifactSnapshotDescriptor struct {
	File       FileDescriptor       `json:"file"`
	Count      int                  `json:"count"`
	TotalBytes int64                `json:"totalBytes"`
	Entries    []ArtifactDescriptor `json:"entries"`
}

type CatalogReceiptDescriptor struct {
	PublicationID string `json:"publicationId"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
	Mode          int64  `json:"mode"`
}

type CatalogReceiptSnapshotDescriptor struct {
	File       FileDescriptor             `json:"file"`
	Count      int                        `json:"count"`
	TotalBytes int64                      `json:"totalBytes"`
	Entries    []CatalogReceiptDescriptor `json:"entries"`
}

type MigrationDescriptor struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
	SHA256  string `json:"sha256"`
}

type DatabaseSnapshotDescriptor struct {
	DatabaseName                   string                        `json:"databaseName"`
	File                           FileDescriptor                `json:"file"`
	Migrations                     []MigrationDescriptor         `json:"migrations"`
	KnowledgeCatalogPublicationIDs []string                      `json:"knowledgeCatalogPublicationIds"`
	KnowledgeCatalogPublications   []catalogpublication.Receipt  `json:"knowledgeCatalogPublications"`
	RecommendationModel            RecommendationModelDescriptor `json:"recommendationModel"`
}

type RecommendationModelDescriptor struct {
	ReleaseID                int64           `json:"releaseId"`
	HeadRevision             int64           `json:"headRevision"`
	ModelID                  string          `json:"modelId"`
	ModelPurpose             string          `json:"modelPurpose"`
	ArtifactSHA256           string          `json:"artifactSha256"`
	ArtifactSizeBytes        int64           `json:"artifactSizeBytes"`
	ArtifactMode             int64           `json:"artifactMode"`
	ModelSchema              string          `json:"modelSchema"`
	Algorithm                string          `json:"algorithm"`
	InferenceContract        string          `json:"inferenceContract"`
	TrainedAt                time.Time       `json:"trainedAt"`
	TrainingProvenanceSHA256 string          `json:"trainingProvenanceSha256"`
	FeatureSchemaSHA256      string          `json:"featureSchemaSha256"`
	KnowledgeCatalogSHA256   string          `json:"knowledgeCatalogSha256"`
	ParameterSHA256          string          `json:"parameterSha256"`
	GoldenVectorsSHA256      string          `json:"goldenVectorsSha256"`
	Manifest                 json.RawMessage `json:"manifest"`
	ManifestSHA256           string          `json:"manifestSha256"`
	ReleaseCreatedAt         time.Time       `json:"releaseCreatedAt"`
	ApplicationVersion       string          `json:"applicationVersion"`
	ApplicationCommit        string          `json:"applicationCommit"`
	ApplicationBuildTime     string          `json:"applicationBuildTime"`
	ActivatedAt              time.Time       `json:"activatedAt"`
	HeadUpdatedAt            time.Time       `json:"headUpdatedAt"`
}

type Manifest struct {
	Schema                     string                           `json:"schema"`
	BackupID                   string                           `json:"backupId"`
	CreatedAt                  time.Time                        `json:"createdAt"`
	Database                   DatabaseSnapshotDescriptor       `json:"database"`
	Artifacts                  ArtifactSnapshotDescriptor       `json:"artifacts"`
	CatalogPublicationReceipts CatalogReceiptSnapshotDescriptor `json:"catalogPublicationReceipts"`
}

type CreateResult struct {
	BackupID            string
	BundlePath          string
	ManifestSHA256      string
	ArtifactCount       int
	CatalogReceiptCount int
}

type VerifyResult struct {
	BackupID            string
	BundlePath          string
	ManifestSHA256      string
	ArtifactCount       int
	CatalogReceiptCount int
}

type RestoreResult struct {
	BackupID            string
	ManifestSHA256      string
	ArtifactCount       int
	CatalogReceiptCount int
	DatabaseName        string
	ArtifactRoot        string
	CatalogReceiptRoot  string
	RecommendationModel RecommendationModelDescriptor
}

type databaseSnapshot struct {
	ID                             string
	Artifacts                      []ArtifactDescriptor
	Migrations                     []MigrationDescriptor
	KnowledgeCatalogPublicationIDs []int64
	KnowledgeCatalogPublications   []catalogpublication.Receipt
	RecommendationModel            RecommendationModelDescriptor
}
