package importing

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
)

func TestUUIDv4UsesRFC4122VersionAndVariantBits(t *testing.T) {
	value, err := uuidV4From(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if value != "00000000-0000-4000-8000-000000000000" {
		t.Fatalf("UUID = %q", value)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(value) {
		t.Fatalf("UUID does not have RFC 4122 v4 form: %q", value)
	}
}

func TestUUIDv4FailsClosedOnEntropyError(t *testing.T) {
	_, err := uuidV4From(errorOnlyReader{})
	assertImportCode(t, err, ErrorUUIDGeneration)
}

type errorOnlyReader struct{}

func (errorOnlyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}
