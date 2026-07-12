package analytics

import (
	"strings"
	"testing"
)

const validManifestJSON = `{"protocol":"analytics_input_manifest_v1","baseAnalyticsGenerationId":null,"baseHeadRevision":0,"target":{"examId":1,"snapshotId":11,"examHeadRevision":1},"snapshots":[{"examId":1,"snapshotId":11,"domainHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"examId":2,"snapshotId":22,"domainHash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`

func TestParseManifestAcceptsExplicitNullAndProducesCanonicalHash(t *testing.T) {
	t.Parallel()

	parsed, err := ParseManifest([]byte(validManifestJSON))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if parsed.Value.BaseAnalyticsGenerationID != nil || string(parsed.Canonical) != validManifestJSON {
		t.Fatalf("parsed manifest = %#v, canonical = %s", parsed.Value, parsed.Canonical)
	}
	if !lowercaseSHA256Pattern.MatchString(parsed.SHA256) {
		t.Fatalf("manifest SHA-256 = %q", parsed.SHA256)
	}
}

func TestParseManifestDistinguishesMissingBaseFromExplicitNull(t *testing.T) {
	t.Parallel()

	missing := strings.Replace(validManifestJSON, `"baseAnalyticsGenerationId":null,`, "", 1)
	if _, err := ParseManifest([]byte(missing)); err == nil || !strings.Contains(err.Error(), "every top-level") {
		t.Fatalf("missing base error = %v", err)
	}
	if _, err := ParseManifest([]byte(validManifestJSON)); err != nil {
		t.Fatalf("explicit null error = %v", err)
	}
}

func TestParseManifestRejectsUnknownOrderHashAndTargetMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown", data: strings.Replace(validManifestJSON, `"protocol"`, `"unknown":1,"protocol"`, 1), want: "unknown field"},
		{name: "duplicate", data: strings.Replace(validManifestJSON, `"protocol":"analytics_input_manifest_v1",`, `"protocol":"analytics_input_manifest_v1","protocol":"analytics_input_manifest_v1",`, 1), want: "duplicate JSON"},
		{name: "unsorted", data: strings.Replace(validManifestJSON, `{"examId":1,"snapshotId":11`, `{"examId":3,"snapshotId":11`, 1), want: "target exam is absent"},
		{name: "bad hash", data: strings.Replace(validManifestJSON, strings.Repeat("a", 64), "ABC", 1), want: "lowercase SHA-256"},
		{name: "target mismatch", data: strings.Replace(validManifestJSON, `"snapshotId":11,"examHeadRevision"`, `"snapshotId":12,"examHeadRevision"`, 1), want: "differs"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseManifest([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseManifest() error = %v, want %q", err, test.want)
			}
		})
	}
}
