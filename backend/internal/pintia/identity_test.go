package pintia

import (
	"encoding/json"
	"testing"
)

func TestIDValidatorMatchesEmbeddedSnapshotContract(t *testing.T) {
	t.Parallel()
	var document struct {
		Definitions map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(embeddedSchemaV2, &document); err != nil {
		t.Fatal(err)
	}
	raw, exists := document.Definitions["pintiaId"]
	if !exists {
		t.Fatal("embedded Pintia schema has no $defs.pintiaId")
	}
	var contract struct {
		Type      string `json:"type"`
		MinLength int    `json:"minLength"`
		MaxLength int    `json:"maxLength"`
		Pattern   string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Type != "string" || contract.MinLength != 1 ||
		contract.MaxLength != maximumIDBytes || contract.Pattern != idPattern.String() {
		t.Fatalf("embedded Pintia ID contract differs: %#v", contract)
	}

	for _, value := range []string{"problem-set-100", "problem:100", "A._:-0"} {
		if !ValidID(value) {
			t.Fatalf("valid Pintia ID %q was rejected", value)
		}
	}
	for _, value := range []string{"", ":problem", "problem/100", " problem", "problem%3A100"} {
		if ValidID(value) {
			t.Fatalf("invalid Pintia ID %q was accepted", value)
		}
	}
}
