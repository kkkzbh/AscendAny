package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommandRequiresSecretFileAndNoPasswordArgument(t *testing.T) {
	parsed, err := parseCommand([]string{
		"create",
		"--username", "admin_1",
		"--display-name", "Administrator",
	})
	if err != nil || parsed.username != "admin_1" || parsed.displayName != "Administrator" {
		t.Fatalf("parsed = %#v, err = %v", parsed, err)
	}
	for _, args := range [][]string{
		nil,
		{"create", "--username", "admin_1"},
		{"create", "--username", "admin_1", "--display-name", "Administrator", "--password", "secret"},
		{"create", "--username", "admin_1", "--display-name", "Administrator", "--password-file", "/run/credentials/admin_password"},
		{"serve"},
	} {
		if _, err := parseCommand(args); err == nil {
			t.Fatalf("parseCommand(%q) unexpectedly succeeded", args)
		}
	}
}

func TestReadPasswordFileRequiresSystemdCredentialPathAndExactBytes(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "admin_password")
	password := "long-enough-password"
	if err := os.WriteFile(path, []byte(password), 0o440); err != nil {
		t.Fatal(err)
	}
	read, err := readAdminPasswordCredential(directory)
	if err != nil || read != password {
		t.Fatalf("read = %q, err = %v", read, err)
	}
	for _, invalidDirectory := range []string{"", "relative", directory + "/."} {
		if _, err := readAdminPasswordCredential(invalidDirectory); err == nil {
			t.Fatalf("invalid CREDENTIALS_DIRECTORY %q was accepted", invalidDirectory)
		}
	}
}

func TestReadPasswordFileRejectsSymlinkAndOversize(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("long-enough-password"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "admin_password")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readAdminPasswordCredential(directory); err == nil {
		t.Fatal("symlink password file was accepted")
	}
	oversizeDirectory := t.TempDir()
	oversize := filepath.Join(oversizeDirectory, "admin_password")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", 129)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAdminPasswordCredential(oversizeDirectory); err == nil {
		t.Fatal("oversized password file was accepted")
	}
}
