package main

import (
	"testing"
)

func TestParseOptionsRequiresClosedCanonicalSessionContract(t *testing.T) {
	arguments := []string{
		"serve",
		"--session-id", "11111111-1111-4111-8111-111111111111",
		"--control-socket", "/run/ascendany-lsp-control/control.sock",
		"--workspace", "/var/lib/ascendany-lsp/sessions/11111111-1111-4111-8111-111111111111",
	}
	parsed, err := parseOptions(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.sessionID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("options = %#v", parsed)
	}
	for name, mutate := range map[string]func([]string) []string{
		"wrong command":  func(value []string) []string { value[0] = "run"; return value },
		"uppercase UUID": func(value []string) []string { value[2] = "11111111-1111-4111-8111-11111111111A"; return value },
		"extra argument": func(value []string) []string { return append(value, "extra") },
		"unknown flag":   func(value []string) []string { return append(value, "--database-url", "postgres://secret") },
		"legacy identity flag": func(value []string) []string {
			return append(value, "--allowed-server-user", "ascendany")
		},
		"clangd override": func(value []string) []string {
			return append(value, "--clangd-binary", "/usr/bin/true")
		},
	} {
		t.Run(name, func(t *testing.T) {
			copyOfArguments := append([]string(nil), arguments...)
			if _, err := parseOptions(mutate(copyOfArguments)); err == nil {
				t.Fatal("invalid command contract was accepted")
			}
		})
	}
}
