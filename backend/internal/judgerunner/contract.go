package judgerunner

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

const (
	manifestName         = "manifest.json"
	maximumManifestBytes = 256 << 10
)

var caseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type testBundleManifest struct {
	Cases  []testCaseManifest `json:"cases"`
	Schema string             `json:"schema"`
}

type testCaseManifest struct {
	ID     string `json:"id"`
	Weight int64  `json:"weight"`
}

type testCase struct {
	ID           string
	Weight       int64
	InputPath    string
	ExpectedPath string
}

func extractTestBundle(source io.Reader, casesRoot string, maximumCases int, maximumCaseBytes int64) ([]testCase, error) {
	if source == nil || maximumCases < 1 || maximumCaseBytes < 1 {
		return nil, errors.New("test bundle extraction configuration is invalid")
	}
	if err := os.Mkdir(casesRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create case directory: %w", err)
	}
	reader := tar.NewReader(source)
	header, err := reader.Next()
	if err != nil {
		return nil, fmt.Errorf("read test bundle manifest header: %w", err)
	}
	if err := validateTarHeader(header, manifestName, maximumManifestBytes); err != nil {
		return nil, fmt.Errorf("validate test bundle manifest header: %w", err)
	}
	manifestRaw, err := io.ReadAll(io.LimitReader(reader, maximumManifestBytes+1))
	if err != nil || int64(len(manifestRaw)) != header.Size || len(manifestRaw) > maximumManifestBytes {
		return nil, errors.New("test bundle manifest size is invalid")
	}
	canonical, _, err := canonicaljson.Object(manifestRaw, maximumManifestBytes)
	if err != nil || !bytes.Equal(manifestRaw, canonical) {
		return nil, errors.New("test bundle manifest must be canonical JSON")
	}
	var manifest testBundleManifest
	if err := decodeClosedJSON(canonical, &manifest); err != nil {
		return nil, fmt.Errorf("decode test bundle manifest: %w", err)
	}
	if manifest.Schema != judgecontract.TestBundleSchemaV1 || len(manifest.Cases) < 1 || len(manifest.Cases) > maximumCases {
		return nil, errors.New("test bundle schema or case count is invalid")
	}
	caseIDs := make([]string, 0, len(manifest.Cases))
	weightTotal := int64(0)
	for _, item := range manifest.Cases {
		if !caseIDPattern.MatchString(item.ID) || item.Weight < 1 || item.Weight > 1_000_000 {
			return nil, errors.New("test case identifier or weight is invalid")
		}
		caseIDs = append(caseIDs, item.ID)
		if item.Weight > 1_000_000_000-weightTotal {
			return nil, errors.New("test case weights exceed the hard limit")
		}
		weightTotal += item.Weight
	}
	if !slices.IsSorted(caseIDs) {
		return nil, errors.New("test case identifiers must be sorted")
	}
	for index := 1; index < len(caseIDs); index++ {
		if caseIDs[index] == caseIDs[index-1] {
			return nil, errors.New("test case identifiers must be unique")
		}
	}

	cases := make([]testCase, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		inputName := "cases/" + item.ID + ".in"
		expectedName := "cases/" + item.ID + ".out"
		inputPath := filepath.Join(casesRoot, item.ID+".in")
		expectedPath := filepath.Join(casesRoot, item.ID+".out")
		if err := extractTarFile(reader, inputName, inputPath, maximumCaseBytes); err != nil {
			return nil, err
		}
		if err := extractTarFile(reader, expectedName, expectedPath, maximumCaseBytes); err != nil {
			return nil, err
		}
		cases = append(cases, testCase{ID: item.ID, Weight: item.Weight, InputPath: inputPath, ExpectedPath: expectedPath})
	}
	if trailing, err := reader.Next(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected test bundle member %q", trailing.Name)
		}
		return nil, fmt.Errorf("read test bundle trailer: %w", err)
	}
	return cases, nil
}

func extractTarFile(reader *tar.Reader, expectedName, destination string, maximumBytes int64) (resultErr error) {
	header, err := reader.Next()
	if err != nil {
		return fmt.Errorf("read test bundle member %q: %w", expectedName, err)
	}
	if err := validateTarHeader(header, expectedName, maximumBytes); err != nil {
		return fmt.Errorf("validate test bundle member %q: %w", expectedName, err)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create test bundle member %q: %w", expectedName, err)
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close test bundle member %q: %w", expectedName, closeErr)
		}
	}()
	written, err := io.Copy(file, reader)
	if err != nil || written != header.Size {
		return fmt.Errorf("write test bundle member %q: copied %d of %d bytes: %w", expectedName, written, header.Size, err)
	}
	return nil
}

func validateTarHeader(header *tar.Header, expectedName string, maximumBytes int64) error {
	if header == nil || header.Name != expectedName || header.Typeflag != tar.TypeReg ||
		header.Size < 0 || header.Size > maximumBytes || header.Mode != 0o600 ||
		header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" ||
		header.Linkname != "" || header.Devmajor != 0 || header.Devminor != 0 ||
		len(header.PAXRecords) != 0 || len(header.Xattrs) != 0 || header.Format != tar.FormatUSTAR ||
		!header.ModTime.Equal(time.Unix(0, 0).UTC()) || !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() {
		return errors.New("member violates the deterministic USTAR contract")
	}
	return nil
}

func decodeClosedJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
