package catalogpublication

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

func TestCanonicalRequestHasExactClosedShape(t *testing.T) {
	t.Parallel()
	request := validPublicationRequest()
	canonical, err := CanonicalRequest(request)
	if err != nil {
		t.Fatalf("CanonicalRequest() error = %v", err)
	}
	parsed, err := ParseRequest(canonical)
	if err != nil || parsed != request {
		t.Fatalf("ParseRequest() = %#v, %v", parsed, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 13 {
		t.Fatalf("request field count = %d, bytes = %s", len(fields), canonical)
	}
}

func TestParseRequestRejectsNoncanonicalUnknownAndIncompleteInputs(t *testing.T) {
	t.Parallel()
	canonical, err := CanonicalRequest(validPublicationRequest())
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.TrimSuffix(canonical, []byte("}"))
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	for name, raw := range map[string][]byte{
		"trailing newline": append(append([]byte(nil), canonical...), '\n'),
		"unknown field":    unknown,
		"duplicate field":  []byte(`{"schema":"ascendany.knowledge_catalog.publication-request.v1","schema":"ascendany.knowledge_catalog.publication-request.v1"}`),
		"non-object":       []byte(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRequest(raw); err == nil {
				t.Fatalf("ParseRequest(%s) accepted %s", raw, name)
			}
		})
	}

	invalid := validPublicationRequest()
	invalid.ExpectedAnalyticsGenerationID = "01"
	if _, err := CanonicalRequest(invalid); err == nil {
		t.Fatal("CanonicalRequest() accepted a noncanonical generation identity")
	}
	invalid = validPublicationRequest()
	invalid.TargetApplicationBuildTime = "2026-07-13T04:00:00+00:00"
	if _, err := CanonicalRequest(invalid); err == nil {
		t.Fatal("CanonicalRequest() accepted a noncanonical build timestamp")
	}
}

func TestReadInputsIgnoresOtherSystemdCredentials(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	requestRaw, err := CanonicalRequest(validPublicationRequest())
	if err != nil {
		t.Fatal(err)
	}
	requestPath := writePublicationInput(t, directory, RequestInputName, requestRaw)
	tokenPath := writePublicationInput(t, directory, AccessTokenInputName, []byte("header.payload.signature"))
	writePublicationInput(t, directory, "catalog_publisher_db_password", []byte("database-password"))
	writePublicationInput(t, directory, "jwt_verification_public_key", []byte(strings.Repeat("v", 32)))

	inputs, err := ReadInputs(requestPath, tokenPath)
	if err != nil {
		t.Fatalf("ReadInputs() error = %v", err)
	}
	if inputs.Request != validPublicationRequest() || inputs.AccessToken != "header.payload.signature" {
		t.Fatalf("ReadInputs() = %#v", inputs)
	}
}

func TestReadInputsRejectsAliasedAndMutableFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	requestRaw, err := CanonicalRequest(validPublicationRequest())
	if err != nil {
		t.Fatal(err)
	}
	requestPath := writePublicationInput(t, directory, RequestInputName, requestRaw)
	tokenPath := writePublicationInput(t, directory, AccessTokenInputName, []byte("header.payload.signature"))
	if _, err := ReadInputs(requestPath, requestPath); err == nil {
		t.Fatal("ReadInputs() accepted one path for both inputs")
	}
	if _, err := ReadInputs(filepath.Base(requestPath), tokenPath); err == nil {
		t.Fatal("ReadInputs() accepted a relative path")
	}

	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInputs(requestPath, tokenPath); err == nil {
		t.Fatal("ReadInputs() accepted a mutable access-token file")
	}
	if err := os.Chmod(tokenPath, InputFileMode); err != nil {
		t.Fatal(err)
	}

	symlinkPath := filepath.Join(directory, "access-token-link")
	if err := os.Symlink(tokenPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInputs(requestPath, symlinkPath); err == nil {
		t.Fatal("ReadInputs() accepted a symlink")
	}

	hardlinkPath := filepath.Join(directory, "access-token-hardlink")
	if err := os.Link(tokenPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInputs(requestPath, tokenPath); err == nil {
		t.Fatal("ReadInputs() accepted a multiply linked file")
	}
}

func validPublicationRequest() Request {
	return Request{
		AuthorizationID: "11111111-1111-4111-8111-111111111111",
		CatalogPublicationIntent: configuration.CatalogPublicationIntent{
			Schema:                             RequestSchema,
			ExpectedConfigurationHeadRevision:  3,
			ExpectedAnalyticsGenerationID:      "17",
			ExpectedAnalyticsHeadRevision:      9,
			ExpectedInputManifestSHA256:        strings.Repeat("a", 64),
			ExpectedCurrentModelHeadRevision:   2,
			ExpectedCurrentModelArtifactSHA256: strings.Repeat("b", 64),
			TargetCatalogSHA256:                strings.Repeat("c", 64),
			TargetModelArtifactSHA256:          strings.Repeat("d", 64),
			TargetApplicationVersion:           "0.2.0",
			TargetApplicationCommit:            strings.Repeat("e", 40),
			TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
		},
	}
}

func writePublicationInput(t *testing.T, directory, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, InputFileMode); err != nil {
		t.Fatal(err)
	}
	return path
}
