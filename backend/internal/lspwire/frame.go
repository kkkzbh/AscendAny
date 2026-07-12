package lspwire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
)

const contentType = "application/vscode-jsonrpc; charset=utf-8"

type Reader struct {
	input  *bufio.Reader
	policy lsp.Policy
	count  int
}

type Writer struct {
	output io.Writer
	policy lsp.Policy
	count  int
}

func NewReader(input io.Reader, policy lsp.Policy) (*Reader, error) {
	if input == nil || !lsp.ValidPolicy(policy) {
		return nil, errors.New("LSP frame reader requires an input and valid policy")
	}
	return &Reader{
		input:  bufio.NewReaderSize(input, policy.MaximumHeaderBytes+1),
		policy: policy,
	}, nil
}

func NewWriter(output io.Writer, policy lsp.Policy) (*Writer, error) {
	if output == nil || !lsp.ValidPolicy(policy) {
		return nil, errors.New("LSP frame writer requires an output and valid policy")
	}
	return &Writer{output: output, policy: policy}, nil
}

func (writer *Writer) Write(body []byte) error {
	if writer.count >= writer.policy.MaximumMessages {
		return errors.New("LSP message count exceeds the session limit")
	}
	if err := Write(writer.output, body, writer.policy); err != nil {
		return err
	}
	writer.count++
	return nil
}

func (reader *Reader) Read() ([]byte, error) {
	if reader.count >= reader.policy.MaximumMessages {
		return nil, errors.New("LSP message count exceeds the session limit")
	}
	length, err := reader.readHeader()
	if err != nil {
		return nil, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader.input, body); err != nil {
		return nil, fmt.Errorf("read LSP body: %w", err)
	}
	if err := ValidateBody(body); err != nil {
		return nil, err
	}
	reader.count++
	return body, nil
}

func (reader *Reader) readHeader() (int, error) {
	headerBytes := 0
	headerCount := 0
	contentLength := -1
	contentTypeSeen := false
	for {
		line, err := reader.input.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				return 0, errors.New("LSP header exceeds the byte limit")
			}
			return 0, fmt.Errorf("read LSP header: %w", err)
		}
		headerBytes += len(line)
		if headerBytes > reader.policy.MaximumHeaderBytes {
			return 0, errors.New("LSP header exceeds the byte limit")
		}
		if !bytes.HasSuffix(line, []byte("\r\n")) {
			return 0, errors.New("LSP header lines require CRLF")
		}
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
		headerCount++
		if headerCount > reader.policy.MaximumHeaderCount {
			return 0, errors.New("LSP header count exceeds the limit")
		}
		field := string(line[:len(line)-2])
		switch {
		case strings.HasPrefix(field, "Content-Length: "):
			if contentLength >= 0 {
				return 0, errors.New("LSP Content-Length is duplicated")
			}
			value := strings.TrimPrefix(field, "Content-Length: ")
			if !canonicalPositiveDecimal(value) {
				return 0, errors.New("LSP Content-Length is not canonical")
			}
			parsed, parseErr := strconv.ParseInt(value, 10, 32)
			if parseErr != nil || parsed > int64(reader.policy.MaximumBodyBytes) {
				return 0, errors.New("LSP body exceeds the byte limit")
			}
			contentLength = int(parsed)
		case field == "Content-Type: "+contentType:
			if contentTypeSeen {
				return 0, errors.New("LSP Content-Type is duplicated")
			}
			contentTypeSeen = true
		default:
			return 0, errors.New("LSP header field is unsupported")
		}
	}
	if contentLength < 1 {
		return 0, errors.New("LSP Content-Length is required")
	}
	return contentLength, nil
}

func Write(writer io.Writer, body []byte, policy lsp.Policy) error {
	if writer == nil || !lsp.ValidPolicy(policy) {
		return errors.New("LSP frame writer requires an output and valid policy")
	}
	if len(body) > policy.MaximumBodyBytes {
		return errors.New("LSP body exceeds the byte limit")
	}
	if err := ValidateBody(body); err != nil {
		return err
	}
	header := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	if err := writeAll(writer, []byte(header)); err != nil {
		return fmt.Errorf("write LSP header: %w", err)
	}
	if err := writeAll(writer, body); err != nil {
		return fmt.Errorf("write LSP body: %w", err)
	}
	return nil
}

func ValidateBody(body []byte) error {
	if len(body) == 0 || !utf8.Valid(body) || validateJSONStringUnicode(body) != nil {
		return errors.New("LSP body must be valid Unicode JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return errors.New("LSP body must contain one JSON object")
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("LSP body root must be a JSON object")
	}
	if err := consumeObject(decoder, 1); err != nil {
		return fmt.Errorf("validate LSP JSON: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return errors.New("LSP body must contain exactly one JSON object")
	}
	return nil
}

func canonicalPositiveDecimal(value string) bool {
	if value == "" || value == "0" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func consumeObject(decoder *json.Decoder, depth int) error {
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
		keys[key] = struct{}{}
		if err := consumeValue(decoder, depth+1); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return errors.New("object is not closed")
	}
	return nil
}

func consumeArray(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds 32 levels")
	}
	for decoder.More() {
		if err := consumeValue(decoder, depth+1); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != ']' {
		return errors.New("array is not closed")
	}
	return nil
}

func consumeValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	switch delimiter {
	case '{':
		return consumeObject(decoder, depth)
	case '[':
		return consumeArray(decoder, depth)
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
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
			codeUnit, valid := parseCodeUnit(body, index+1)
			if !valid {
				continue
			}
			index += 4
			switch {
			case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
				return errors.New("unpaired low surrogate")
			case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
				if index+6 >= len(body) || body[index+1] != '\\' || body[index+2] != 'u' {
					return errors.New("unpaired high surrogate")
				}
				low, lowValid := parseCodeUnit(body, index+3)
				if !lowValid || low < 0xdc00 || low > 0xdfff {
					return errors.New("unpaired high surrogate")
				}
				index += 6
			}
		}
	}
	return nil
}

func parseCodeUnit(body []byte, start int) (uint16, bool) {
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

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written < 1 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}
