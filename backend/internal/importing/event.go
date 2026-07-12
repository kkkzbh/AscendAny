package importing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

func canonicalEventPayload(payload any) (string, error) {
	if payload == nil {
		return "", importError(ErrorStateConflict, false, "encode import event", errors.New("event payload is required"))
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", importError(ErrorStateConflict, false, "encode import event", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return "", importError(ErrorStateConflict, false, "encode import event", fmt.Errorf("event payload must be a JSON object"))
	}
	return string(data), nil
}
