package lspwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
)

type Hello struct {
	Schema    string `json:"schema"`
	SessionID string `json:"sessionId"`
}

func WriteHello(writer io.Writer, sessionID string, policy lsp.Policy) error {
	if !lsp.ValidPublicID(sessionID) {
		return errors.New("canonical LSP session ID is required")
	}
	body, err := json.Marshal(Hello{Schema: lsp.ControlSchemaV1, SessionID: sessionID})
	if err != nil {
		return err
	}
	return Write(writer, body, policy)
}

func ReadHello(reader *Reader) (Hello, error) {
	if reader == nil {
		return Hello{}, errors.New("LSP hello reader is required")
	}
	body, err := reader.Read()
	if err != nil {
		return Hello{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var hello Hello
	if err := decoder.Decode(&hello); err != nil {
		return Hello{}, errors.New("LSP control hello does not match its schema")
	}
	if err := requireEOF(decoder); err != nil || hello.Schema != lsp.ControlSchemaV1 || !lsp.ValidPublicID(hello.SessionID) {
		return Hello{}, errors.New("LSP control hello is invalid")
	}
	return hello, nil
}
