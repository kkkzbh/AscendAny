package pintia

import "testing"

func TestRegistrationNicknameIdentityCapabilityIsBoundToReviewedExporterSemantics(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		exporter string
		version  string
		want     bool
	}{
		{name: "first capable release", exporter: ExporterName, version: "2.2.3", want: true},
		{name: "build metadata", exporter: ExporterName, version: "2.2.3+build.1", want: true},
		{name: "later patch prerelease", exporter: ExporterName, version: "2.2.4-rc.1", want: true},
		{name: "later minor prerelease", exporter: ExporterName, version: "2.3.0-alpha.1", want: true},
		{name: "old display-name semantics", exporter: ExporterName, version: "2.2.2"},
		{name: "capability release prerelease", exporter: ExporterName, version: "2.2.3-rc.1"},
		{name: "future incompatible major", exporter: ExporterName, version: "3.0.0"},
		{name: "wrong exporter", exporter: "other-exporter", version: "2.2.3"},
		{name: "leading zero", exporter: ExporterName, version: "2.02.3"},
		{name: "missing patch", exporter: ExporterName, version: "2.2"},
		{name: "invalid prerelease", exporter: ExporterName, version: "2.3.0-01"},
		{name: "empty", exporter: ExporterName},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SupportsRegistrationNicknameIdentity(test.exporter, test.version); got != test.want {
				t.Fatalf("SupportsRegistrationNicknameIdentity(%q, %q) = %t, want %t", test.exporter, test.version, got, test.want)
			}
		})
	}
}
