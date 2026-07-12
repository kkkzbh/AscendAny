package backup

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestExternalCommandEnvironmentIsClosed(t *testing.T) {
	t.Setenv("LD_PRELOAD", "/tmp/hostile.so")
	t.Setenv("BASH_ENV", "/tmp/hostile-bash-env")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("PGOPTIONS", "-c search_path=hostile")
	t.Setenv("PATH", "/tmp/hostile-path")

	if actual, expected := closedCommandEnvironment(), []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
	}; !slices.Equal(actual, expected) {
		t.Fatalf("closed command environment = %#v, expected %#v", actual, expected)
	}

	if actual, expected := postgresCommandEnvironment("/run/private/backup.pgpass"), []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"PGPASSFILE=/run/private/backup.pgpass",
	}; !slices.Equal(actual, expected) {
		t.Fatalf("PostgreSQL command environment = %#v, expected %#v", actual, expected)
	}

	command := exec.Command("/usr/bin/env")
	command.Env = postgresCommandEnvironment("/run/private/backup.pgpass")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	actualChildEnvironment := strings.Split(strings.TrimSpace(string(output)), "\n")
	if expected := []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"PGPASSFILE=/run/private/backup.pgpass",
	}; !slices.Equal(actualChildEnvironment, expected) {
		t.Fatalf("child command environment = %#v, expected %#v", actualChildEnvironment, expected)
	}
}
