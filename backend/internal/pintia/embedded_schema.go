package pintia

import (
	_ "embed"
)

// embeddedSchemaV2 is an exact copy of the language-neutral root contract.
// Tests compare it byte-for-byte with contracts/pintia so drift fails CI.
//
//go:embed schema/ascendany.pintia.snapshot.v2.schema.json
var embeddedSchemaV2 []byte

// EmbeddedSchemaV2 returns a defensive copy of the schema bytes compiled into
// the backend binary.
func EmbeddedSchemaV2() []byte {
	return append([]byte(nil), embeddedSchemaV2...)
}

// NewEmbeddedValidator constructs the only validator used by online workers.
func NewEmbeddedValidator(limits Limits) (*Validator, error) {
	return NewValidator(embeddedSchemaV2, limits)
}
