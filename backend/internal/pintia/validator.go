package pintia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaResourceURL     = "urn:ascendany:loaded-contract:pintia:snapshot:v2"
	maxSupportedJSONDepth = 128
)

// Limits bounds all attacker-controlled nodes, strings, collections, and code
// bodies before generic JSON materialization. Every value must be positive.
type Limits struct {
	MaxTotalBytes               int64
	MaxTotalNodes               int64
	MaxTotalStringBytes         int64
	MaxJSONDepth                int
	MaxStringBytes              int
	MaxProblems                 int
	MaxParticipants             int
	MaxProblemResultsPerRanking int
	MaxSubmissions              int
	MaxCaseResultsPerSubmission int
	MaxCodeBytes                int
}

func DefaultLimits() Limits {
	return Limits{
		MaxTotalBytes:               64 << 20,
		MaxTotalNodes:               2_000_000,
		MaxTotalStringBytes:         32 << 20,
		MaxJSONDepth:                32,
		MaxStringBytes:              8 << 20,
		MaxProblems:                 1_000,
		MaxParticipants:             20_000,
		MaxProblemResultsPerRanking: 1_000,
		MaxSubmissions:              20_000,
		MaxCaseResultsPerSubmission: 1_000,
		MaxCodeBytes:                1 << 20,
	}
}

func (limits Limits) validate() error {
	if limits.MaxTotalBytes <= 0 || limits.MaxTotalBytes == math.MaxInt64 {
		return validationError(ErrorInvalidLimits, "limits.maxTotalBytes", "must be between 1 and %d", int64(math.MaxInt64-1))
	}
	if limits.MaxTotalNodes <= 0 {
		return validationError(ErrorInvalidLimits, "limits.maxTotalNodes", "must be positive")
	}
	if limits.MaxTotalStringBytes <= 0 || limits.MaxTotalStringBytes > limits.MaxTotalBytes {
		return validationError(ErrorInvalidLimits, "limits.maxTotalStringBytes", "must be positive and not exceed maxTotalBytes")
	}
	if limits.MaxJSONDepth <= 0 || limits.MaxJSONDepth > maxSupportedJSONDepth {
		return validationError(ErrorInvalidLimits, "limits.maxJSONDepth", "must be between 1 and %d", maxSupportedJSONDepth)
	}
	if limits.MaxStringBytes <= 0 || int64(limits.MaxStringBytes) > limits.MaxTotalStringBytes {
		return validationError(ErrorInvalidLimits, "limits.maxStringBytes", "must be positive and not exceed maxTotalStringBytes")
	}
	if limits.MaxProblems <= 0 {
		return validationError(ErrorInvalidLimits, "limits.maxProblems", "must be positive")
	}
	if limits.MaxParticipants <= 0 {
		return validationError(ErrorInvalidLimits, "limits.maxParticipants", "must be positive")
	}
	if limits.MaxProblemResultsPerRanking <= 0 {
		return validationError(ErrorInvalidLimits, "limits.maxProblemResultsPerRanking", "must be positive")
	}
	if limits.MaxSubmissions <= 0 {
		return validationError(ErrorInvalidLimits, "limits.maxSubmissions", "must be positive")
	}
	if limits.MaxCaseResultsPerSubmission <= 0 {
		return validationError(ErrorInvalidLimits, "limits.maxCaseResultsPerSubmission", "must be positive")
	}
	if limits.MaxCodeBytes <= 0 || limits.MaxCodeBytes > limits.MaxStringBytes {
		return validationError(ErrorInvalidLimits, "limits.maxCodeBytes", "must be positive and not exceed maxStringBytes")
	}
	collections := []struct {
		path  string
		value int
	}{
		{"limits.maxProblems", limits.MaxProblems},
		{"limits.maxParticipants", limits.MaxParticipants},
		{"limits.maxProblemResultsPerRanking", limits.MaxProblemResultsPerRanking},
		{"limits.maxSubmissions", limits.MaxSubmissions},
		{"limits.maxCaseResultsPerSubmission", limits.MaxCaseResultsPerSubmission},
	}
	for _, collection := range collections {
		if int64(collection.value) > limits.MaxTotalNodes {
			return validationError(ErrorInvalidLimits, collection.path, "must not exceed maxTotalNodes")
		}
	}
	return nil
}

func (limits Limits) arrayLimits() map[string]int {
	return map[string]int{
		"$.problems":     limits.MaxProblems,
		"$.participants": limits.MaxParticipants,
		"$.participants[].ranking.problemResults": limits.MaxProblemResultsPerRanking,
		"$.submissions":               limits.MaxSubmissions,
		"$.submissions[].caseResults": limits.MaxCaseResultsPerSubmission,
	}
}

// Validator compiles the authoritative schema bytes once and validates many
// snapshots against the exact schema digest.
type Validator struct {
	limits          Limits
	schema          *jsonschema.Schema
	preflightSchema *preflightSchema
	arrayLimits     map[string]int
	schemaDigest    string
}

func NewValidator(schemaJSON []byte, limits Limits) (*Validator, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(schemaJSON)
	digestText := hex.EncodeToString(digest[:])
	if digestText != ExpectedSchemaSHA256 {
		return nil, fmt.Errorf(
			"authoritative Pintia schema digest mismatch: got %s, want %s",
			digestText,
			ExpectedSchemaSHA256,
		)
	}
	document, err := parseStrictJSON(schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("parse authoritative Pintia schema: %w", err)
	}
	preflight, err := compilePreflightSchema(document)
	if err != nil {
		return nil, err
	}
	arrayLimits := limits.arrayLimits()
	if err := validatePreflightArrayCoverage(preflight, arrayLimits); err != nil {
		return nil, err
	}

	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaResourceURL, document); err != nil {
		return nil, fmt.Errorf("load authoritative Pintia schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaResourceURL)
	if err != nil {
		return nil, fmt.Errorf("compile authoritative Pintia schema: %w", err)
	}

	return &Validator{
		limits:          limits,
		schema:          compiled,
		preflightSchema: preflight,
		arrayLimits:     arrayLimits,
		schemaDigest:    digestText,
	}, nil
}

func (v *Validator) SchemaSHA256() string {
	return v.schemaDigest
}

// ValidateReader reads at most MaxTotalBytes+1 bytes so an oversized payload
// fails without consuming unbounded memory.
func (v *Validator) ValidateReader(ctx context.Context, reader io.Reader) (*Snapshot, error) {
	if ctx == nil {
		return nil, errors.New("validation context is required")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	limited := io.LimitReader(&contextReader{ctx: ctx, reader: reader}, v.limits.MaxTotalBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read Pintia snapshot: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	snapshot, err := v.validate(ctx, payload)
	if err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := context.Cause(reader.ctx); err != nil {
		return 0, err
	}
	count, err := reader.reader.Read(buffer)
	if count == 0 {
		if contextErr := context.Cause(reader.ctx); contextErr != nil {
			return 0, contextErr
		}
	}
	return count, err
}

func (v *Validator) Validate(payload []byte) (*Snapshot, error) {
	return v.validate(context.Background(), payload)
}

func (v *Validator) validate(ctx context.Context, payload []byte) (*Snapshot, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if int64(len(payload)) > v.limits.MaxTotalBytes {
		return nil, validationError(
			ErrorPayloadTooLarge,
			"$",
			"payload is %d bytes; maximum is %d",
			len(payload),
			v.limits.MaxTotalBytes,
		)
	}

	if err := validateStreamingPreflight(ctx, payload, v.preflightSchema, v.limits, v.arrayLimits); err != nil {
		return nil, err
	}
	document, err := materializeJSON(ctx, payload)
	if err != nil {
		return nil, err
	}
	if err := v.schema.Validate(document); err != nil {
		return nil, validationError(ErrorSchemaViolation, "$", "%v", err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	var snapshot Snapshot
	decoder := json.NewDecoder(&contextReader{ctx: ctx, reader: bytes.NewReader(payload)})
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, validationError(ErrorSchemaViolation, "$", "decode typed snapshot: %v", err)
	}
	if err := requireDecoderEOF(decoder); err != nil {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, validationError(ErrorMalformedJSON, "$", "%v", err)
	}
	if snapshot.SchemaSHA256 != v.schemaDigest {
		return nil, validationError(
			ErrorSchemaDigestMismatch,
			"$.schemaSha256",
			"got %q, want %q",
			snapshot.SchemaSHA256,
			v.schemaDigest,
		)
	}
	if err := validateSemantics(&snapshot); err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func materializeJSON(ctx context.Context, payload []byte) (any, error) {
	decoder := json.NewDecoder(&contextReader{ctx: ctx, reader: bytes.NewReader(payload)})
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, validationError(ErrorMalformedJSON, "$", "%v", err)
	}
	if err := requireDecoderEOF(decoder); err != nil {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, validationError(ErrorMalformedJSON, "$", "%v", err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return document, nil
}

func parseStrictJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("input is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func readJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maxSupportedJSONDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", maxSupportedJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}

	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, err := readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if closing != json.Delim('}') {
			return nil, fmt.Errorf("object is not closed")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if closing != json.Delim(']') {
			return nil, fmt.Errorf("array is not closed")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delimiter)
	}
}

func requireDecoderEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("multiple JSON values are not allowed")
	}
	return err
}
