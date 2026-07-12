package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var workspaceSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var allowedClientMethods = map[string]struct{}{
	"$/cancelRequest":                        {},
	"callHierarchy/incomingCalls":            {},
	"callHierarchy/outgoingCalls":            {},
	"exit":                                   {},
	"initialize":                             {},
	"initialized":                            {},
	"shutdown":                               {},
	"textDocument/codeAction":                {},
	"textDocument/codeLens":                  {},
	"textDocument/completion":                {},
	"textDocument/declaration":               {},
	"textDocument/definition":                {},
	"textDocument/didChange":                 {},
	"textDocument/didClose":                  {},
	"textDocument/didOpen":                   {},
	"textDocument/didSave":                   {},
	"textDocument/documentHighlight":         {},
	"textDocument/documentLink":              {},
	"textDocument/documentSymbol":            {},
	"textDocument/foldingRange":              {},
	"textDocument/formatting":                {},
	"textDocument/hover":                     {},
	"textDocument/implementation":            {},
	"textDocument/inlayHint":                 {},
	"textDocument/onTypeFormatting":          {},
	"textDocument/prepareCallHierarchy":      {},
	"textDocument/prepareRename":             {},
	"textDocument/prepareTypeHierarchy":      {},
	"textDocument/rangeFormatting":           {},
	"textDocument/references":                {},
	"textDocument/rename":                    {},
	"textDocument/selectionRange":            {},
	"textDocument/semanticTokens/full":       {},
	"textDocument/semanticTokens/full/delta": {},
	"textDocument/semanticTokens/range":      {},
	"textDocument/signatureHelp":             {},
	"textDocument/switchSourceHeader":        {},
	"textDocument/typeDefinition":            {},
	"typeHierarchy/subtypes":                 {},
	"typeHierarchy/supertypes":               {},
	"workspace/didChangeWatchedFiles":        {},
	"workspace/symbol":                       {},
	"workspace/workspaceFolders":             {},
}

func ValidateClientMessage(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var message map[string]any
	if err := decoder.Decode(&message); err != nil || message == nil {
		return errors.New("LSP client message must be one JSON object")
	}
	if message["jsonrpc"] != "2.0" {
		return errors.New("LSP client message requires jsonrpc 2.0")
	}
	methodValue, hasMethod := message["method"]
	if hasMethod {
		method, ok := methodValue.(string)
		if !ok {
			return errors.New("LSP method must be a string")
		}
		if _, allowed := allowedClientMethods[method]; !allowed {
			return fmt.Errorf("LSP method %q is not allowed", method)
		}
		if method == "initialize" {
			if err := validateInitialize(message["params"]); err != nil {
				return err
			}
		}
	}
	if err := validateMessageValues(message, ""); err != nil {
		return err
	}
	return nil
}

func validateInitialize(value any) error {
	params, ok := value.(map[string]any)
	if !ok {
		return errors.New("LSP initialize params must be an object")
	}
	if processID, present := params["processId"]; present && processID != nil {
		return errors.New("LSP initialize processId must be null")
	}
	if rootPath, present := params["rootPath"]; present && rootPath != nil {
		return errors.New("LSP initialize rootPath must be null")
	}
	if options, present := params["initializationOptions"]; present && options != nil {
		return errors.New("LSP initialize options are not accepted")
	}
	rootURI, present := params["rootUri"]
	if !present || rootURI != PublicWorkspaceURI {
		return errors.New("LSP initialize rootUri must be file:///workspace")
	}
	if folders, present := params["workspaceFolders"]; present && folders != nil {
		values, ok := folders.([]any)
		if !ok || len(values) != 1 {
			return errors.New("LSP initialize requires at most one workspace folder")
		}
		folder, ok := values[0].(map[string]any)
		if !ok || folder["uri"] != PublicWorkspaceURI {
			return errors.New("LSP workspace folder must be file:///workspace")
		}
	}
	return nil
}

func validateMessageValues(value any, key string) error {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if err := validateMessageValues(child, childKey); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateMessageValues(child, key); err != nil {
				return err
			}
		}
	case string:
		if isURIKey(key) {
			if err := ValidateWorkspaceURI(typed); err != nil {
				return err
			}
		}
	}
	return nil
}

func isURIKey(key string) bool {
	switch key {
	case "uri", "rootUri", "targetUri", "oldUri", "newUri":
		return true
	default:
		return strings.HasSuffix(key, "Uri")
	}
}

func ValidateWorkspaceURI(value string) error {
	if len(value) < len(PublicWorkspaceURI) || len(value) > 512 || strings.ContainsAny(value, "\\%?#") {
		return errors.New("LSP file URI is outside the bounded workspace")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("LSP file URI is outside the bounded workspace")
	}
	if parsed.Path != "/workspace" && !strings.HasPrefix(parsed.Path, "/workspace/") {
		return errors.New("LSP file URI is outside the bounded workspace")
	}
	if path.Clean(parsed.Path) != parsed.Path || strings.Contains(parsed.Path, "//") {
		return errors.New("LSP file URI is not canonical")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(parsed.Path, "/workspace/"), "/") {
		if parsed.Path == "/workspace" {
			break
		}
		if !workspaceSegment.MatchString(segment) {
			return errors.New("LSP file URI contains an unsupported path segment")
		}
	}
	return nil
}
