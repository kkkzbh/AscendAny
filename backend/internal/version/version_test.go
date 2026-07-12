package version

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestCurrentReportsRuntimeBuildSettings(t *testing.T) {
	previousVersion, previousCommit, previousBuildTime := Version, Commit, BuildTime
	Version = "1.2.3"
	Commit = "0123456789abcdef0123456789abcdef01234567"
	BuildTime = "2026-07-10T00:00:00Z"
	t.Cleanup(func() {
		Version, Commit, BuildTime = previousVersion, previousCommit, previousBuildTime
	})

	wantGOAMD64 := ""
	wantGOExperiment := "none"
	wantGOFIPS140 := "off"
	wantCGOEnabled := false
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "CGO_ENABLED":
				wantCGOEnabled = setting.Value == "1"
			case "GOAMD64":
				wantGOAMD64 = setting.Value
			case "GOEXPERIMENT":
				if setting.Value != "" {
					wantGOExperiment = setting.Value
				}
			case "GOFIPS140":
				wantGOFIPS140 = setting.Value
			}
		}
	}

	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildTime != BuildTime {
		t.Fatalf("release identity = %#v", got)
	}
	if got.GoVersion != runtime.Version() || got.GOOS != runtime.GOOS || got.GOARCH != runtime.GOARCH {
		t.Fatalf("runtime identity = %#v", got)
	}
	if got.GOAMD64 != wantGOAMD64 || got.GOExperiment != wantGOExperiment ||
		got.GOFIPS140 != wantGOFIPS140 || got.CGOEnabled != wantCGOEnabled {
		t.Fatalf(
			"build settings = (%q, %q, %q, %t), want (%q, %q, %q, %t)",
			got.GOAMD64,
			got.GOExperiment,
			got.GOFIPS140,
			got.CGOEnabled,
			wantGOAMD64,
			wantGOExperiment,
			wantGOFIPS140,
			wantCGOEnabled,
		)
	}
}
