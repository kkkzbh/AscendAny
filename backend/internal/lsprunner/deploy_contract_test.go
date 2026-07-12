package lsprunner

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLSPSystemdContractHasNoCredentialOrNetworkFallback(t *testing.T) {
	unit := readContractFile(t, "../../../deploy/v2/systemd/ascendany-lsp@.service")
	for _, required := range []string{
		"PrivateNetwork=yes",
		"PrivateTmp=disconnected",
		"PrivatePIDs=yes",
		"RootDirectory=/var/lib/ascendany-lsp-root",
		"MountAPIVFS=yes",
		"RestrictAddressFamilies=AF_UNIX",
		"NoNewPrivileges=yes",
		"CapabilityBoundingSet=",
		"SupplementaryGroups=ascendany-lsp-control",
		"BindReadOnlyPaths=/usr",
		"BindReadOnlyPaths=/opt/ascendany/v2/bin/ascendany-lsp:/opt/ascendany/v2/bin/ascendany-lsp",
		"BindReadOnlyPaths=/run/ascendany-lsp-control/control.sock:/run/ascendany-lsp-control/control.sock",
		"--workspace /tmp/ascendany-lsp-sessions/%i",
		"--control-socket /run/ascendany-lsp-control/control.sock",
		"AssertFileIsExecutable=/usr/bin/clangd",
		"LimitFSIZE=32M",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("LSP unit is missing %q", required)
		}
	}
	if strings.Contains(unit, "ReadWritePaths=/var/lib/ascendany-lsp") || strings.Contains(unit, "StateDirectory=ascendany-lsp") {
		t.Fatal("LSP unit exposes shared writable state across session instances")
	}
	if strings.Contains(unit, "--clangd-binary") {
		t.Fatal("LSP unit exposes an alternate clangd executable path")
	}
	var bindReadOnly []string
	for _, line := range strings.Split(unit, "\n") {
		if value, found := strings.CutPrefix(line, "BindReadOnlyPaths="); found {
			bindReadOnly = append(bindReadOnly, value)
		}
	}
	expectedBinds := []string{
		"/usr",
		"/opt/ascendany/v2/bin/ascendany-lsp:/opt/ascendany/v2/bin/ascendany-lsp",
		"/run/ascendany-lsp-control/control.sock:/run/ascendany-lsp-control/control.sock",
	}
	if !slices.Equal(bindReadOnly, expectedBinds) || strings.Contains(unit, "BindPaths=") {
		t.Fatalf("LSP host bind set is not exact: %v", bindReadOnly)
	}
	for _, forbidden := range []string{"LoadCredential=", "LoadCredentialEncrypted=", "DATABASE", "PASSWORD", "TOKEN", "EnvironmentFile="} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("LSP unit contains forbidden credential/config transport %q", forbidden)
		}
	}
	tmpfiles := readContractFile(t, "../../../deploy/v2/tmpfiles.d/ascendany-v2.conf")
	sysusers := readContractFile(t, "../../../deploy/v2/sysusers.d/ascendany-v2.conf")
	for _, required := range []string{
		`g ascendany-lsp-control`,
		`m ascendany                ascendany-lsp-control`,
		`m ascendany-lsp            ascendany-lsp-control`,
		`u ascendany-lsp            -   "AscendAny sandboxed LSP"      /var/empty`,
	} {
		if !strings.Contains(sysusers, required) {
			t.Errorf("LSP sysusers contract is missing %q", required)
		}
	}
	if strings.Contains(sysusers, "m ascendany-lsp            ascendany-runtime") {
		t.Fatal("LSP identity retains the online runtime capability group")
	}
	for _, required := range []string{
		`d /var/lib/ascendany-lsp-root                0755 root`,
		`L+ /var/lib/ascendany-lsp-root/bin`,
		`usr/bin`,
		`L+ /var/lib/ascendany-lsp-root/lib`,
		`usr/lib`,
		`L+ /var/lib/ascendany-lsp-root/lib64`,
		`usr/lib64`,
		`d /run/ascendany-lsp-control                 2770 ascendany            ascendany-lsp-control`,
	} {
		if !strings.Contains(tmpfiles, required) {
			t.Errorf("LSP tmpfiles contract is missing %q", required)
		}
	}
}

func TestLSPSystemdAuthorizationIsCanonicalAndScoped(t *testing.T) {
	rule := readContractFile(t, "../../../deploy/v2/polkit-1/rules.d/61-ascendany-lsp.rules")
	for _, required := range []string{
		`subject.user !== "ascendany"`,
		`ascendany-lsp@`,
		`[0-9a-f]{8}`,
		`(verb === "start" || verb === "stop")`,
	} {
		if !strings.Contains(rule, required) {
			t.Errorf("LSP polkit rule is missing %q", required)
		}
	}
	if strings.Contains(rule, "Result.YES;") && !strings.Contains(rule, "lspUnit.test(unit)") {
		t.Fatal("LSP polkit rule grants without matching the exact unit")
	}
	runtimeUnit := readContractFile(t, "../../../deploy/v2/systemd/ascendanyd.service")
	if !strings.Contains(runtimeUnit, "SupplementaryGroups=ascendany-runtime ascendany-lsp-control") ||
		!strings.Contains(runtimeUnit, "ReadWritePaths=/var/lib/ascendany /run/ascendany /run/ascendany-lsp-control") {
		t.Fatal("ascendanyd does not hold the dedicated LSP control socket capability")
	}
	if strings.Contains(runtimeUnit, "chgrp ascendany-runtime /run/ascendany") {
		t.Fatal("ascendanyd retains the obsolete shared LSP socket group setup")
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
