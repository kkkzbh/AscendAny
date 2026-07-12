package lspwire

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
)

func TestFrameRoundTripAndOptionalContentType(t *testing.T) {
	policy := lsp.DefaultPolicy()
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	var encoded bytes.Buffer
	if err := Write(&encoded, body, policy); err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(&encoded, policy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, body) {
		t.Fatalf("decoded = %s", decoded)
	}
	withType := "Content-Length: " + strconv.Itoa(len(body)) + "\r\nContent-Type: " + contentType + "\r\n\r\n" + string(body)
	typedReader, _ := NewReader(strings.NewReader(withType), policy)
	if _, err := typedReader.Read(); err != nil {
		t.Fatal(err)
	}
}

func TestFrameAttackCorpus(t *testing.T) {
	policy := lsp.DefaultPolicy()
	valid := `{"jsonrpc":"2.0"}`
	attacks := map[string]string{
		"LF header":          "Content-Length: 17\n\n" + valid,
		"duplicate length":   "Content-Length: 17\r\nContent-Length: 17\r\n\r\n" + valid,
		"unknown header":     "Content-Length: 17\r\nX-Test: x\r\n\r\n" + valid,
		"leading zero":       "Content-Length: 017\r\n\r\n" + valid,
		"zero body":          "Content-Length: 0\r\n\r\n",
		"short body":         "Content-Length: 18\r\n\r\n" + valid,
		"duplicate JSON key": "Content-Length: 33\r\n\r\n" + `{"jsonrpc":"2.0","jsonrpc":"2.0"}`,
		"root array":         "Content-Length: 2\r\n\r\n[]",
		"unpaired surrogate": "Content-Length: 26\r\n\r\n" + `{"jsonrpc":"2.0","x":"\uD800"}`,
		"trailing JSON":      "Content-Length: 19\r\n\r\n" + valid + `{}`,
	}
	for name, frame := range attacks {
		t.Run(name, func(t *testing.T) {
			reader, err := NewReader(strings.NewReader(frame), policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Read(); err == nil {
				t.Fatal("attack frame was accepted")
			}
		})
	}
	tooLarge := "Content-Length: " + strings.Repeat("9", 9) + "\r\n\r\n"
	reader, _ := NewReader(strings.NewReader(tooLarge), policy)
	if _, err := reader.Read(); err == nil {
		t.Fatal("oversized body was accepted")
	}
}

func TestReaderAndWriterEnforceMessageCount(t *testing.T) {
	policy := lsp.DefaultPolicy()
	policy.MaximumMessages = 1
	body := []byte(`{"jsonrpc":"2.0"}`)
	var wire bytes.Buffer
	writer, err := NewWriter(&wire, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(body); err == nil {
		t.Fatal("writer exceeded message count")
	}
	reader, _ := NewReader(&wire, policy)
	if _, err := reader.Read(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(); err == nil || errors.Is(err, nil) {
		t.Fatal("reader exceeded message count")
	}
}

func TestControlHelloIsClosedAndVersioned(t *testing.T) {
	policy := lsp.DefaultPolicy()
	identifier := "11111111-1111-4111-8111-111111111111"
	var wire bytes.Buffer
	if err := WriteHello(&wire, identifier, policy); err != nil {
		t.Fatal(err)
	}
	reader, _ := NewReader(&wire, policy)
	hello, err := ReadHello(reader)
	if err != nil {
		t.Fatal(err)
	}
	if hello.Schema != lsp.ControlSchemaV1 || hello.SessionID != identifier {
		t.Fatalf("hello = %#v", hello)
	}
	bad := []byte(`{"schema":"ascendany.lsp.control.v1","sessionId":"11111111-1111-4111-8111-111111111111","extra":true}`)
	wire.Reset()
	if err := Write(&wire, bad, policy); err != nil {
		t.Fatal(err)
	}
	reader, _ = NewReader(&wire, policy)
	if _, err := ReadHello(reader); err == nil {
		t.Fatal("unknown hello field was accepted")
	}
}
