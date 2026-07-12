package backup

import "time"

const (
	DatabaseDumpFilename    = "database.dump"
	ArtifactArchiveFilename = "artifacts.tar.zst"
	ManifestFilename        = "manifest.json"
	ManifestDigestFilename  = "manifest.sha256"
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

type MigrationDescriptor struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
	SHA256  string `json:"sha256"`
}

type DatabaseSnapshotDescriptor struct {
	DatabaseName string                `json:"databaseName"`
	File         FileDescriptor        `json:"file"`
	Migrations   []MigrationDescriptor `json:"migrations"`
}

type Manifest struct {
	Schema    string                     `json:"schema"`
	BackupID  string                     `json:"backupId"`
	CreatedAt time.Time                  `json:"createdAt"`
	Database  DatabaseSnapshotDescriptor `json:"database"`
	Artifacts ArtifactSnapshotDescriptor `json:"artifacts"`
}

type CreateResult struct {
	BackupID       string
	BundlePath     string
	ManifestSHA256 string
	ArtifactCount  int
}

type VerifyResult struct {
	BackupID       string
	BundlePath     string
	ManifestSHA256 string
	ArtifactCount  int
}

type RestoreResult struct {
	BackupID       string
	ManifestSHA256 string
	ArtifactCount  int
	DatabaseName   string
	ArtifactRoot   string
}

type databaseSnapshot struct {
	ID         string
	Artifacts  []ArtifactDescriptor
	Migrations []MigrationDescriptor
}
