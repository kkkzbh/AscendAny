package modelprobe

import "time"

type Result struct {
	ConfigurationKey          string    `json:"configurationKey"`
	ConfigurationHeadRevision int64     `json:"configurationHeadRevision"`
	ConfigurationVersion      int64     `json:"configurationVersion"`
	ConfigurationSHA256       string    `json:"configurationSha256"`
	Authority                 string    `json:"authority"`
	Model                     string    `json:"model"`
	CheckedAt                 time.Time `json:"checkedAt"`
	LatencyMilliseconds       int64     `json:"latencyMilliseconds"`
}
