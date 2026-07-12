package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"
)

const maxAuthJSONBytes int64 = 8 * 1024

type requestContractError struct {
	status  int
	code    string
	message string
}

func (e *requestContractError) Error() string { return e.message }

type jsonObjectContract struct {
	allowed  map[string]struct{}
	required map[string]struct{}
}

func decodeStrictJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	return decodeStrictJSONWithLimit(
		writer,
		request,
		destination,
		maxAuthJSONBytes,
		"Authentication payload exceeds 8192 bytes.",
		"Authentication request body exceeded its duration limit.",
	)
}

func decodeStrictJSONWithLimit(
	writer http.ResponseWriter,
	request *http.Request,
	destination any,
	maximumBytes int64,
	payloadTooLargeMessage string,
	deadlineMessage string,
) error {
	if maximumBytes < 1 || payloadTooLargeMessage == "" || deadlineMessage == "" {
		return errors.New("strict JSON reader configuration is invalid")
	}
	contentTypes := request.Header.Values("Content-Type")
	if len(contentTypes) != 1 || contentTypes[0] != "application/json" {
		return &requestContractError{
			status:  http.StatusUnsupportedMediaType,
			code:    "unsupported_media_type",
			message: "Content-Type must be exactly application/json.",
		}
	}
	if len(request.Header.Values("Content-Encoding")) != 0 {
		return &requestContractError{
			status:  http.StatusUnsupportedMediaType,
			code:    "unsupported_content_encoding",
			message: "Content-Encoding is not supported.",
		}
	}
	readContext := requestBodyReadContext(request)
	limited := http.MaxBytesReader(unwrapResponseWriter(writer), request.Body, maximumBytes)
	body, err := io.ReadAll(limited)
	if contextErr := context.Cause(readContext); contextErr != nil {
		return requestBodyContractError(contextErr, deadlineMessage)
	}
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return &requestContractError{
				status:  http.StatusRequestEntityTooLarge,
				code:    "payload_too_large",
				message: payloadTooLargeMessage,
			}
		}
		return &requestContractError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request JSON could not be read.",
		}
	}
	if err := finishRequestBodyRead(request); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return requestBodyContractError(err, deadlineMessage)
		}
		return err
	}
	return decodeStrictJSONDocument(body, destination)
}

func decodeStrictJSONDocument(body []byte, destination any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return &requestContractError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body must contain one JSON object.",
		}
	}
	if !utf8.Valid(body) || validateJSONStringUnicode(body) != nil {
		return &requestContractError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body must contain valid Unicode JSON.",
		}
	}
	contract, err := jsonContractForDestination(destination)
	if err != nil {
		return err
	}
	if err := validateUniqueJSONObject(body, contract); err != nil {
		return &requestContractError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body must contain one strict JSON object.",
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return &requestContractError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body does not match the JSON contract.",
		}
	}
	if err := requireJSONEOF(decoder); err != nil {
		return &requestContractError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body must contain exactly one JSON object.",
		}
	}
	return nil
}

func requestBodyContractError(err error, deadlineMessage string) *requestContractError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &requestContractError{
			status:  http.StatusRequestTimeout,
			code:    "request_timeout",
			message: deadlineMessage,
		}
	}
	return &requestContractError{
		status:  http.StatusBadRequest,
		code:    "request_canceled",
		message: "Request was canceled.",
	}
}

func jsonContractForDestination(destination any) (*jsonObjectContract, error) {
	typeOf := reflect.TypeOf(destination)
	if typeOf == nil || typeOf.Kind() != reflect.Pointer || typeOf.Elem().Kind() != reflect.Struct {
		return nil, errors.New("strict JSON destination must be a pointer to a struct")
	}
	valueOf := reflect.ValueOf(destination)
	if valueOf.IsNil() {
		return nil, errors.New("strict JSON destination must not be nil")
	}
	contract := &jsonObjectContract{
		allowed:  make(map[string]struct{}, typeOf.Elem().NumField()),
		required: make(map[string]struct{}, typeOf.Elem().NumField()),
	}
	for index := 0; index < typeOf.Elem().NumField(); index++ {
		field := typeOf.Elem().Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag, present := field.Tag.Lookup("json")
		if !present {
			return nil, fmt.Errorf("strict JSON field %s requires an explicit json tag", field.Name)
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			return nil, fmt.Errorf("strict JSON field %s requires an explicit json name", field.Name)
		}
		if _, duplicate := contract.allowed[name]; duplicate {
			return nil, fmt.Errorf("strict JSON contract contains duplicate field %q", name)
		}
		contract.allowed[name] = struct{}{}
		optional := false
		for _, option := range parts[1:] {
			if option == "omitempty" {
				optional = true
			}
		}
		if !optional {
			contract.required[name] = struct{}{}
		}
	}
	return contract, nil
}

func validateUniqueJSONObject(body []byte, contract *jsonObjectContract) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("root value is not an object")
	}
	if err := consumeJSONObject(decoder, 1, contract); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func consumeJSONObject(decoder *json.Decoder, depth int, contract *jsonObjectContract) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds 32 levels")
	}
	keys := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("object key is not a string")
		}
		if _, duplicate := keys[key]; duplicate {
			return fmt.Errorf("duplicate object key %q", key)
		}
		if contract != nil {
			if _, allowed := contract.allowed[key]; !allowed {
				return fmt.Errorf("unknown object key %q", key)
			}
		}
		keys[key] = struct{}{}
		if err := consumeJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("object is not closed")
	}
	if contract != nil {
		for key := range contract.required {
			if _, present := keys[key]; !present {
				return fmt.Errorf("required object key %q is missing", key)
			}
		}
	}
	return nil
}

func consumeJSONArray(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds 32 levels")
	}
	for decoder.More() {
		if err := consumeJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return errors.New("array is not closed")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		return consumeJSONObject(decoder, depth, nil)
	case '[':
		return consumeJSONArray(decoder, depth)
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func validateJSONStringUnicode(body []byte) error {
	inString := false
	for index := 0; index < len(body); index++ {
		switch body[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			index++
			if index >= len(body) || body[index] != 'u' {
				continue
			}
			codeUnit, valid := parseJSONCodeUnit(body, index+1)
			if !valid {
				continue
			}
			index += 4
			switch {
			case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
				return errors.New("JSON string contains an unpaired low surrogate")
			case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
				if index+6 >= len(body) || body[index+1] != '\\' || body[index+2] != 'u' {
					return errors.New("JSON string contains an unpaired high surrogate")
				}
				low, lowValid := parseJSONCodeUnit(body, index+3)
				if !lowValid || low < 0xdc00 || low > 0xdfff {
					return errors.New("JSON string contains an unpaired high surrogate")
				}
				index += 6
			}
		}
	}
	return nil
}

func parseJSONCodeUnit(body []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(body) {
		return 0, false
	}
	var value uint16
	for _, digit := range body[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}
