package analytics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const (
	ManifestProtocolV1 = "analytics_input_manifest_v1"
	maxManifestBytes   = 16 * 1024 * 1024
)

var lowercaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Manifest struct {
	Protocol                  string             `json:"protocol"`
	BaseAnalyticsGenerationID *int64             `json:"baseAnalyticsGenerationId"`
	BaseHeadRevision          int64              `json:"baseHeadRevision"`
	Target                    ManifestTarget     `json:"target"`
	Snapshots                 []ManifestSnapshot `json:"snapshots"`
}

type ManifestTarget struct {
	ExamID           int64 `json:"examId"`
	SnapshotID       int64 `json:"snapshotId"`
	ExamHeadRevision int64 `json:"examHeadRevision"`
}

type ManifestSnapshot struct {
	ExamID     int64  `json:"examId"`
	SnapshotID int64  `json:"snapshotId"`
	DomainHash string `json:"domainHash"`
}

type ParsedManifest struct {
	Value     Manifest
	Canonical []byte
	SHA256    string
}

type rawManifest struct {
	Protocol                  *string                `json:"protocol"`
	BaseAnalyticsGenerationID json.RawMessage        `json:"baseAnalyticsGenerationId"`
	BaseHeadRevision          *int64                 `json:"baseHeadRevision"`
	Target                    *rawManifestTarget     `json:"target"`
	Snapshots                 *[]rawManifestSnapshot `json:"snapshots"`
}

type rawManifestTarget struct {
	ExamID           *int64 `json:"examId"`
	SnapshotID       *int64 `json:"snapshotId"`
	ExamHeadRevision *int64 `json:"examHeadRevision"`
}

type rawManifestSnapshot struct {
	ExamID     *int64  `json:"examId"`
	SnapshotID *int64  `json:"snapshotId"`
	DomainHash *string `json:"domainHash"`
}

func ParseManifest(data []byte) (ParsedManifest, error) {
	if len(data) > maxManifestBytes {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "parse manifest", fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes))
	}
	var raw rawManifest
	if err := decodeClosedJSON(data, &raw); err != nil {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "parse manifest", err)
	}
	if raw.Protocol == nil || raw.BaseAnalyticsGenerationID == nil || raw.BaseHeadRevision == nil || raw.Target == nil || raw.Snapshots == nil {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "parse manifest", errors.New("every top-level manifest field is required"))
	}
	if raw.Target.ExamID == nil || raw.Target.SnapshotID == nil || raw.Target.ExamHeadRevision == nil {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "parse manifest", errors.New("every target field is required"))
	}

	snapshots := make([]ManifestSnapshot, 0, len(*raw.Snapshots))
	for index, item := range *raw.Snapshots {
		if item.ExamID == nil || item.SnapshotID == nil || item.DomainHash == nil {
			return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "parse manifest", fmt.Errorf("snapshots[%d] requires examId, snapshotId, and domainHash", index))
		}
		snapshots = append(snapshots, ManifestSnapshot{
			ExamID:     *item.ExamID,
			SnapshotID: *item.SnapshotID,
			DomainHash: *item.DomainHash,
		})
	}
	var baseGenerationID *int64
	if !bytes.Equal(bytes.TrimSpace(raw.BaseAnalyticsGenerationID), []byte("null")) {
		var value int64
		if err := json.Unmarshal(raw.BaseAnalyticsGenerationID, &value); err != nil {
			return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "parse manifest", errors.New("baseAnalyticsGenerationId must be null or an int64"))
		}
		baseGenerationID = &value
	}
	manifest := Manifest{
		Protocol:                  *raw.Protocol,
		BaseAnalyticsGenerationID: baseGenerationID,
		BaseHeadRevision:          *raw.BaseHeadRevision,
		Target: ManifestTarget{
			ExamID:           *raw.Target.ExamID,
			SnapshotID:       *raw.Target.SnapshotID,
			ExamHeadRevision: *raw.Target.ExamHeadRevision,
		},
		Snapshots: snapshots,
	}
	return CanonicalManifest(manifest)
}

func CanonicalManifest(manifest Manifest) (ParsedManifest, error) {
	if err := validateManifest(manifest); err != nil {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "canonicalize manifest", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "canonicalize manifest", err)
	}
	digest := sha256.Sum256(canonical)
	return ParsedManifest{
		Value:     manifest,
		Canonical: canonical,
		SHA256:    hex.EncodeToString(digest[:]),
	}, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Protocol != ManifestProtocolV1 {
		return fmt.Errorf("protocol must be %q", ManifestProtocolV1)
	}
	if manifest.BaseHeadRevision < 0 {
		return errors.New("baseHeadRevision must be non-negative")
	}
	if manifest.BaseAnalyticsGenerationID == nil {
		if manifest.BaseHeadRevision != 0 {
			return errors.New("a null base generation requires baseHeadRevision zero")
		}
	} else if *manifest.BaseAnalyticsGenerationID <= 0 || manifest.BaseHeadRevision <= 0 {
		return errors.New("a base generation requires positive generation ID and head revision")
	}
	if manifest.Target.ExamID <= 0 || manifest.Target.SnapshotID <= 0 || manifest.Target.ExamHeadRevision <= 0 {
		return errors.New("target IDs and examHeadRevision must be positive")
	}
	if len(manifest.Snapshots) == 0 {
		return errors.New("snapshots must not be empty")
	}
	targetFound := false
	var previousExamID int64
	seenSnapshotIDs := make(map[int64]struct{}, len(manifest.Snapshots))
	for index, snapshot := range manifest.Snapshots {
		if snapshot.ExamID <= 0 || snapshot.SnapshotID <= 0 {
			return fmt.Errorf("snapshots[%d] IDs must be positive", index)
		}
		if index > 0 && snapshot.ExamID <= previousExamID {
			return errors.New("snapshots must be strictly ordered by examId")
		}
		previousExamID = snapshot.ExamID
		if _, exists := seenSnapshotIDs[snapshot.SnapshotID]; exists {
			return fmt.Errorf("snapshotId %d is duplicated", snapshot.SnapshotID)
		}
		seenSnapshotIDs[snapshot.SnapshotID] = struct{}{}
		if !lowercaseSHA256Pattern.MatchString(snapshot.DomainHash) {
			return fmt.Errorf("snapshots[%d].domainHash must be lowercase SHA-256", index)
		}
		if snapshot.ExamID == manifest.Target.ExamID {
			if snapshot.SnapshotID != manifest.Target.SnapshotID {
				return errors.New("target exam snapshot differs from the snapshot manifest")
			}
			targetFound = true
		}
	}
	if !targetFound {
		return errors.New("target exam is absent from snapshots")
	}
	return nil
}
