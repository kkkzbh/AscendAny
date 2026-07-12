package importing

import (
	"strings"
	"testing"
)

func TestAnalyticsManifestCanonicalShapeAndHash(t *testing.T) {
	manifest := AnalyticsManifestV1{
		Protocol:         AnalyticsManifestProtocolV1,
		BaseHeadRevision: 0,
		Target: AnalyticsManifestTargetV1{
			ExamID:           1,
			SnapshotID:       2,
			ExamHeadRevision: 1,
		},
		Snapshots: []AnalyticsManifestEntryV1{
			{ExamID: 1, SnapshotID: 2, DomainHash: strings.Repeat("a", 64)},
		},
	}
	payload, digest, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol":"analytics_input_manifest_v1","baseAnalyticsGenerationId":null,"baseHeadRevision":0,"target":{"examId":1,"snapshotId":2,"examHeadRevision":1},"snapshots":[{"examId":1,"snapshotId":2,"domainHash":"` + strings.Repeat("a", 64) + `"}]}`
	if string(payload) != want {
		t.Fatalf("manifest = %s, want %s", payload, want)
	}
	if !lowercaseSHA256Pattern.MatchString(digest) {
		t.Fatalf("manifest digest = %q", digest)
	}
	parsed, err := ParseAnalyticsManifestV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Snapshots) != 1 || parsed.Snapshots[0].SnapshotID != 2 {
		t.Fatalf("parsed manifest = %#v", parsed)
	}
}

func TestAnalyticsManifestRejectsDriftAndRelationalMismatch(t *testing.T) {
	base := int64(4)
	tests := []AnalyticsManifestV1{
		{
			Protocol:                  AnalyticsManifestProtocolV1,
			BaseAnalyticsGenerationID: &base,
			BaseHeadRevision:          0,
			Target:                    AnalyticsManifestTargetV1{ExamID: 1, SnapshotID: 1, ExamHeadRevision: 1},
			Snapshots:                 []AnalyticsManifestEntryV1{{ExamID: 1, SnapshotID: 1, DomainHash: strings.Repeat("a", 64)}},
		},
		{
			Protocol:         AnalyticsManifestProtocolV1,
			BaseHeadRevision: 0,
			Target:           AnalyticsManifestTargetV1{ExamID: 1, SnapshotID: 2, ExamHeadRevision: 1},
			Snapshots:        []AnalyticsManifestEntryV1{{ExamID: 1, SnapshotID: 3, DomainHash: strings.Repeat("a", 64)}},
		},
		{
			Protocol:         AnalyticsManifestProtocolV1,
			BaseHeadRevision: 0,
			Target:           AnalyticsManifestTargetV1{ExamID: 2, SnapshotID: 2, ExamHeadRevision: 1},
			Snapshots: []AnalyticsManifestEntryV1{
				{ExamID: 2, SnapshotID: 2, DomainHash: strings.Repeat("a", 64)},
				{ExamID: 1, SnapshotID: 1, DomainHash: strings.Repeat("b", 64)},
			},
		},
	}
	for index, manifest := range tests {
		if _, _, err := manifest.CanonicalJSON(); err == nil {
			t.Fatalf("manifest %d error = nil", index)
		}
	}

	_, err := ParseAnalyticsManifestV1([]byte(`{"protocol":"analytics_input_manifest_v1","baseAnalyticsGenerationId":null,"baseHeadRevision":0,"target":{"examId":1,"snapshotId":1,"examHeadRevision":1},"snapshots":[{"examId":1,"snapshotId":1,"domainHash":"` + strings.Repeat("a", 64) + `"}],"extra":true}`))
	assertImportCode(t, err, ErrorManifest)
}
