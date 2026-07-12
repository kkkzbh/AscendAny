package configuration

import (
	"encoding/json"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const maxDocumentBytes = 256 << 10

func canonicalDocument(raw json.RawMessage) (json.RawMessage, string, error) {
	return canonicaljson.Object(raw, maxDocumentBytes)
}
