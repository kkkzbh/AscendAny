package lsp

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidPublicIDRequiresCanonicalUUIDv4(t *testing.T) {
	valid := "01234567-89ab-4cde-8fab-0123456789ab"
	for _, test := range []struct {
		value string
		valid bool
	}{
		{valid, true},
		{"01234567-89AB-4cde-8fab-0123456789ab", false},
		{"01234567-89ab-3cde-8fab-0123456789ab", false},
		{"01234567-89ab-4cde-7fab-0123456789ab", false},
		{"{" + valid + "}", false},
		{"", false},
	} {
		if got := ValidPublicID(test.value); got != test.valid {
			t.Errorf("ValidPublicID(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}

func TestNewPublicIDSetsCanonicalVersionAndVariant(t *testing.T) {
	identifier, err := NewPublicID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if identifier != "00000000-0000-4000-8000-000000000000" || !ValidPublicID(identifier) {
		t.Fatalf("identifier = %q", identifier)
	}
	if _, err := NewPublicID(bytes.NewReader(make([]byte, 15))); err == nil {
		t.Fatal("short random input was accepted")
	}
}

func TestAttachTicketIsCanonicalHighEntropyBase64URL(t *testing.T) {
	ticket, err := NewAttachTicket(bytes.NewReader(bytes.Repeat([]byte{0xff}, attachTicketBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidAttachTicket(ticket) || len(ticket) != 43 || strings.ContainsAny(ticket, "+/=") {
		t.Fatalf("attach ticket = %q", ticket)
	}
	for _, invalid := range []string{"", ticket + "A", ticket[:42], strings.Repeat("_", 43), strings.Repeat("A", 42) + "!"} {
		if ValidAttachTicket(invalid) {
			t.Errorf("invalid attach ticket accepted: %q", invalid)
		}
	}
}

func TestValidateWorkspaceURIClosesFilesystemBoundary(t *testing.T) {
	valid := []string{
		"file:///workspace",
		"file:///workspace/main.cpp",
		"file:///workspace/include/vector.hpp",
	}
	for _, value := range valid {
		if err := ValidateWorkspaceURI(value); err != nil {
			t.Errorf("ValidateWorkspaceURI(%q): %v", value, err)
		}
	}
	invalid := []string{
		"file:///etc/passwd",
		"file:///workspace/../etc/passwd",
		"file:///workspace/%2e%2e/etc/passwd",
		"file://localhost/workspace/main.cpp",
		"https://example.com/main.cpp",
		"file:///workspace/main.cpp?token=x",
		"file:///workspace/main cpp",
		"file:///workspace//main.cpp",
		"file:///workspace/.hidden",
	}
	for _, value := range invalid {
		if err := ValidateWorkspaceURI(value); err == nil {
			t.Errorf("ValidateWorkspaceURI(%q) succeeded", value)
		}
	}
}

func TestValidateClientMessageRejectsHostAndProcessEscapes(t *testing.T) {
	validInitialize := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"file:///workspace","capabilities":{},"workspaceFolders":[{"uri":"file:///workspace","name":"workspace"}]}}`)
	if err := ValidateClientMessage(validInitialize); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":1,"rootUri":"file:///workspace","capabilities":{}}}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"file:///etc","capabilities":{}}}`),
		[]byte(`{"jsonrpc":"2.0","method":"workspace/executeCommand","params":{"command":"clangd.applyFix","arguments":["/etc/passwd"]}}`),
		[]byte(`{"jsonrpc":"2.0","method":"workspace/didChangeConfiguration","params":{"settings":{"compilationDatabasePath":"/etc"}}}`),
		[]byte(`{"jsonrpc":"1.0","method":"initialized","params":{}}`),
	} {
		if err := ValidateClientMessage(body); err == nil {
			t.Fatalf("unsafe LSP body was accepted: %s", body)
		}
	}
	validDocument := []byte(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///workspace/main.cpp","languageId":"cpp","version":1,"text":"int main(){}"}}}`)
	if err := ValidateClientMessage(validDocument); err != nil {
		t.Fatal(err)
	}
}
