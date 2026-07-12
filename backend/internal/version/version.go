package version

import (
	"runtime"
	"runtime/debug"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Info struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	BuildTime    string `json:"buildTime"`
	GoVersion    string `json:"goVersion"`
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	GOAMD64      string `json:"goamd64"`
	GOExperiment string `json:"goExperiment"`
	GOFIPS140    string `json:"gofips140"`
	CGOEnabled   bool   `json:"cgoEnabled"`
}

func Current() Info {
	info := Info{
		Version:      Version,
		Commit:       Commit,
		BuildTime:    BuildTime,
		GoVersion:    runtime.Version(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		GOExperiment: "none",
		GOFIPS140:    "off",
	}

	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
		info.Version = buildInfo.Main.Version
	}
	for _, setting := range buildInfo.Settings {
		switch setting.Key {
		case "CGO_ENABLED":
			info.CGOEnabled = setting.Value == "1"
		case "GOAMD64":
			info.GOAMD64 = setting.Value
		case "GOEXPERIMENT":
			if setting.Value != "" {
				info.GOExperiment = setting.Value
			}
		case "GOFIPS140":
			info.GOFIPS140 = setting.Value
		case "vcs.revision":
			if info.Commit == "unknown" {
				info.Commit = setting.Value
			}
		case "vcs.time":
			if info.BuildTime == "unknown" {
				info.BuildTime = setting.Value
			}
		}
	}
	return info
}
