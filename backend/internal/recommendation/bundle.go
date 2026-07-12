package recommendation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const (
	maxManifestBytes      = 1 << 20
	maxConfigurationBytes = 4 << 20
	maxMetricsBytes       = 4 << 20
	maxModelManifestBytes = 4 << 20
	maxStudentResultBytes = 4 << 20
)

var (
	lowercaseSHA256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	configurationKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	schemaIDPattern         = regexp.MustCompile(`^ascendany[.][a-z][a-z0-9_.-]{0,126}[.]v[1-9][0-9]*$`)
	canonicalIDPattern      = regexp.MustCompile(`^[1-9][0-9]*$`)
)

func canonicalNumber(value string) (json.RawMessage, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return nil, errors.New("number is empty or padded")
	}
	object, _, err := canonicaljson.Object(json.RawMessage(`{"value":`+value+`}`), 16<<10)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(object, &decoded); err != nil || len(decoded.Value) == 0 {
		return nil, errors.New("canonical number could not be recovered")
	}
	return decoded.Value, nil
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func requireCanonicalObject(raw json.RawMessage, maximumBytes int, label string) (json.RawMessage, string, error) {
	canonical, digest, err := canonicaljson.Object(raw, maximumBytes)
	if err != nil {
		return nil, "", domainError(ErrorInvalidBundle, true, "canonicalize "+label, err)
	}
	if !bytes.Equal(raw, canonical) {
		return nil, "", domainError(ErrorInvalidBundle, true, "canonicalize "+label, errors.New(label+" bytes must already be canonical"))
	}
	return canonical, digest, nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON has a trailing value")
		}
		return err
	}
	return nil
}

func parseCanonicalID(value string) (int64, error) {
	if !canonicalIDPattern.MatchString(value) {
		return 0, errors.New("ID must be a canonical positive decimal")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("ID exceeds the signed 64-bit range")
	}
	return parsed, nil
}
