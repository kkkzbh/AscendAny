package judgecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const maximumProblemSpecBytes = 256 << 10

type Checker string

const (
	CheckerExact  Checker = "exact"
	CheckerTokens Checker = "tokens"
)

type ProblemSpec struct {
	Checker Checker `json:"checker"`
	Schema  string  `json:"schema"`
}

func ParseProblemSpec(raw json.RawMessage) (ProblemSpec, error) {
	canonical, _, err := canonicaljson.Object(raw, maximumProblemSpecBytes)
	if err != nil {
		return ProblemSpec{}, fmt.Errorf("validate problem spec: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		return ProblemSpec{}, errors.New("problem spec must use canonical JSON")
	}
	var spec ProblemSpec
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return ProblemSpec{}, fmt.Errorf("decode problem spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ProblemSpec{}, errors.New("decode problem spec: unexpected trailing JSON value")
		}
		return ProblemSpec{}, fmt.Errorf("decode problem spec: %w", err)
	}
	if spec.Schema != ProblemSpecSchemaV1 || (spec.Checker != CheckerExact && spec.Checker != CheckerTokens) {
		return ProblemSpec{}, errors.New("problem spec schema or checker is unsupported")
	}
	return spec, nil
}

func CompareOutput(checker Checker, actual, expected []byte) bool {
	switch checker {
	case CheckerExact:
		return bytes.Equal(actual, expected)
	case CheckerTokens:
		return slices.Equal(strings.Fields(string(actual)), strings.Fields(string(expected)))
	default:
		return false
	}
}
