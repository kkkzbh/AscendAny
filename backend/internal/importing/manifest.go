package importing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type AnalyticsManifestV1 struct {
	Protocol                  string                     `json:"protocol"`
	BaseAnalyticsGenerationID *int64                     `json:"baseAnalyticsGenerationId"`
	BaseHeadRevision          int64                      `json:"baseHeadRevision"`
	Target                    AnalyticsManifestTargetV1  `json:"target"`
	Snapshots                 []AnalyticsManifestEntryV1 `json:"snapshots"`
}

type AnalyticsManifestTargetV1 struct {
	ExamID           int64 `json:"examId"`
	SnapshotID       int64 `json:"snapshotId"`
	ExamHeadRevision int64 `json:"examHeadRevision"`
}

type AnalyticsManifestEntryV1 struct {
	ExamID     int64  `json:"examId"`
	SnapshotID int64  `json:"snapshotId"`
	DomainHash string `json:"domainHash"`
}

func (manifest AnalyticsManifestV1) CanonicalJSON() ([]byte, string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", importError(ErrorManifest, false, "encode analytics manifest", err)
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func ParseAnalyticsManifestV1(payload []byte) (AnalyticsManifestV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var manifest AnalyticsManifestV1
	if err := decoder.Decode(&manifest); err != nil {
		return AnalyticsManifestV1{}, importError(ErrorManifest, true, "decode analytics manifest", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("manifest contains multiple JSON values")
		}
		return AnalyticsManifestV1{}, importError(ErrorManifest, true, "decode analytics manifest", err)
	}
	if err := manifest.Validate(); err != nil {
		return AnalyticsManifestV1{}, err
	}
	return manifest, nil
}

func (manifest AnalyticsManifestV1) Validate() error {
	if manifest.Protocol != AnalyticsManifestProtocolV1 {
		return importError(ErrorManifest, true, "validate analytics manifest", fmt.Errorf("protocol must be %q", AnalyticsManifestProtocolV1))
	}
	if manifest.BaseAnalyticsGenerationID == nil {
		if manifest.BaseHeadRevision != 0 {
			return importError(ErrorManifest, true, "validate analytics manifest", errors.New("nil base generation requires revision zero"))
		}
	} else if *manifest.BaseAnalyticsGenerationID <= 0 || manifest.BaseHeadRevision <= 0 {
		return importError(ErrorManifest, true, "validate analytics manifest", errors.New("base generation and revision must be positive together"))
	}
	if manifest.Target.ExamID <= 0 || manifest.Target.SnapshotID <= 0 || manifest.Target.ExamHeadRevision <= 0 {
		return importError(ErrorManifest, true, "validate analytics manifest", errors.New("target IDs and revision must be positive"))
	}
	if len(manifest.Snapshots) == 0 {
		return importError(ErrorManifest, true, "validate analytics manifest", errors.New("snapshot list must not be empty"))
	}
	targetFound := false
	previousExamID := int64(0)
	for index, snapshot := range manifest.Snapshots {
		if snapshot.ExamID <= previousExamID || snapshot.SnapshotID <= 0 {
			return importError(
				ErrorManifest,
				true,
				"validate analytics manifest",
				fmt.Errorf("snapshot %d IDs must be positive and sorted by strictly increasing examId", index),
			)
		}
		if !lowercaseSHA256Pattern.MatchString(snapshot.DomainHash) {
			return importError(ErrorManifest, true, "validate analytics manifest", fmt.Errorf("snapshot %d domainHash is invalid", index))
		}
		if snapshot.ExamID == manifest.Target.ExamID {
			if snapshot.SnapshotID != manifest.Target.SnapshotID {
				return importError(ErrorManifest, true, "validate analytics manifest", errors.New("target exam snapshot differs from snapshot list"))
			}
			targetFound = true
		}
		previousExamID = snapshot.ExamID
	}
	if !targetFound {
		return importError(ErrorManifest, true, "validate analytics manifest", errors.New("target exam is absent from snapshot list"))
	}
	return nil
}
