package canonicaljson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObjectCanonicalizesAndHashesEquivalentDocuments(t *testing.T) {
	first, firstHash, err := Object(json.RawMessage(` {"z":2.0,"a":1e0,"nested":{"b":-0}} `), 1024)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := Object(json.RawMessage(`{"nested":{"b":0},"a":1,"z":2}`), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != `{"a":1,"nested":{"b":0},"z":2}` || string(first) != string(second) || firstHash != secondHash {
		t.Fatalf("first=%s second=%s firstHash=%s secondHash=%s", first, second, firstHash, secondHash)
	}
}

func TestObjectRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate": `{"a":1,"a":2}`,
		"root":      `[1]`,
		"trailing":  `{} {}`,
		"NUL key":   `{"\u0000":1}`,
		"NUL value": `{"value":"\u0000"}`,
		"too large": strings.Repeat(" ", 8) + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			limit := 1024
			if name == "too large" {
				limit = 2
			}
			if _, _, err := Object(json.RawMessage(raw), limit); err == nil {
				t.Fatal("Object() error = nil")
			}
		})
	}
}
