// Package canonicaljson owns the deterministic JSON object encoding used by
// persisted domain hashes. It rejects ambiguous inputs before hashing them.
package canonicaljson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maximumDepth = 64

// Object validates a UTF-8 JSON object, rejects duplicate keys and excessive
// nesting, normalizes finite decimal numbers, and returns its compact canonical
// representation and lowercase SHA-256 digest.
func Object(raw json.RawMessage, maximumBytes int) (json.RawMessage, string, error) {
	if maximumBytes < 1 {
		return nil, "", errors.New("maximum bytes must be positive")
	}
	if len(raw) == 0 || len(raw) > maximumBytes || !utf8.Valid(raw) {
		return nil, "", fmt.Errorf("document must contain between 1 and %d UTF-8 bytes", maximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, "", fmt.Errorf("decode document: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, "", errors.New("document root must be an object")
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, "", fmt.Errorf("unexpected trailing token %v", token)
		}
		return nil, "", fmt.Errorf("read document trailer: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical document: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return json.RawMessage(encoded), hex.EncodeToString(digest[:]), nil
}

func decodeValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maximumDepth {
		return nil, fmt.Errorf("document nesting exceeds %d levels", maximumDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				if strings.IndexByte(key, 0) >= 0 {
					return nil, errors.New("object key contains NUL")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate object key %q", key)
				}
				value, err := decodeValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, errors.New("object is not terminated")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				value, err := decodeValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, errors.New("array is not terminated")
			}
			return array, nil
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
	case json.Number:
		if len(typed) > 128 {
			return nil, errors.New("number exceeds 128 bytes")
		}
		normalized, err := normalizeNumber(string(typed))
		if err != nil {
			return nil, err
		}
		return json.Number(normalized), nil
	case string:
		if strings.IndexByte(typed, 0) >= 0 {
			return nil, errors.New("string contains NUL")
		}
		return typed, nil
	case bool, nil:
		return typed, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}

func normalizeNumber(raw string) (string, error) {
	if exponentIndex := strings.LastIndexAny(raw, "eE"); exponentIndex >= 0 {
		exponent, err := strconv.ParseInt(raw[exponentIndex+1:], 10, 32)
		if err != nil || exponent < -8192 || exponent > 8192 {
			return "", errors.New("number exponent exceeds 8192 decimal places")
		}
	}
	rational, ok := new(big.Rat).SetString(raw)
	if !ok {
		return "", errors.New("number is invalid")
	}
	denominator := new(big.Int).Set(rational.Denom())
	two := big.NewInt(2)
	five := big.NewInt(5)
	remainder := new(big.Int)
	twos, fives := 0, 0
	for {
		quotient, modulus := new(big.Int).QuoRem(denominator, two, remainder)
		if modulus.Sign() != 0 {
			break
		}
		denominator = quotient
		twos++
	}
	for {
		quotient, modulus := new(big.Int).QuoRem(denominator, five, remainder)
		if modulus.Sign() != 0 {
			break
		}
		denominator = quotient
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", errors.New("number cannot be represented as a finite decimal")
	}
	precision := max(twos, fives)
	if precision > 4096 {
		return "", errors.New("number precision exceeds 4096 decimal places")
	}
	canonical := rational.FloatString(precision)
	if strings.Contains(canonical, ".") {
		canonical = strings.TrimRight(strings.TrimRight(canonical, "0"), ".")
	}
	if canonical == "-0" {
		canonical = "0"
	}
	if len(canonical) > 8192 {
		return "", errors.New("canonical number exceeds 8192 bytes")
	}
	return canonical, nil
}
