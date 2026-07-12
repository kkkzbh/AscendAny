package main

import (
	"errors"
	"testing"
	"time"
)

func TestParseOptionsAcceptsOnlyExplicitPerJobContract(t *testing.T) {
	parsed, err := parseOptions([]string{
		"run",
		"--job-id", "11111111-1111-4111-8111-111111111111",
		"--control-socket", "/run/ascendany-judge/11111111-1111-4111-8111-111111111111.sock",
		"--work-root", "/var/lib/ascendany-judge/jobs/11111111-1111-4111-8111-111111111111",
		"--allowed-client-user", "ascendany",
		"--container-image", "localhost/ascendany-cpp20@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--delegated-cgroup-root", "/sys/fs/cgroup",
	}, func(name string) (uint32, error) {
		if name != "ascendany" {
			t.Fatalf("lookup name = %q", name)
		}
		return 1001, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.allowedUID != 1001 || parsed.acceptTimeout != 30*time.Second || parsed.sessionTimeout != 30*time.Minute {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseOptionsRejectsUnknownFlagsRootPeerAndNonRunCommand(t *testing.T) {
	base := []string{
		"run", "--job-id", "11111111-1111-4111-8111-111111111111",
		"--control-socket", "/run/ascendany-judge/11111111-1111-4111-8111-111111111111.sock",
		"--work-root", "/var/lib/ascendany-judge/jobs/11111111-1111-4111-8111-111111111111",
		"--allowed-client-user", "ascendany",
		"--container-image", "localhost/ascendany-cpp20@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--delegated-cgroup-root", "/sys/fs/cgroup",
	}
	for name, arguments := range map[string][]string{
		"unknown flag": append(append([]string{}, base...), "--fallback", "host"),
		"command":      {"serve"},
		"cgroup root":  append(append([]string{}, base[:len(base)-2]...), "--delegated-cgroup-root", "/tmp/cgroup"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOptions(arguments, func(string) (uint32, error) { return 1001, nil }); err == nil {
				t.Fatal("parseOptions() error = nil")
			}
		})
	}
	if _, err := parseOptions(base, func(string) (uint32, error) { return 0, errors.New("root") }); err == nil {
		t.Fatal("parseOptions(root) error = nil")
	}
}
