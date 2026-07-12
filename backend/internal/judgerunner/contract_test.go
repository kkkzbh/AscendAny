package judgerunner

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

func TestExtractTestBundleRequiresExactDeterministicMembers(t *testing.T) {
	bundle := testBundle(t, []bundleCase{{id: "a", weight: 1, input: "1\n", expected: "1\n"}, {id: "b", weight: 2, input: "2\n", expected: "2\n"}}, nil)
	root := t.TempDir()
	cases, err := extractTestBundle(bytes.NewReader(bundle), root+"/cases", 10, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 || cases[1].Weight != 2 {
		t.Fatalf("cases = %#v", cases)
	}
	actual, err := os.ReadFile(cases[0].ExpectedPath)
	if err != nil || string(actual) != "1\n" {
		t.Fatalf("expected output = %q, %v", actual, err)
	}
}

func TestExtractTestBundleRejectsTraversalAndTrailingMembers(t *testing.T) {
	base := []bundleCase{{id: "case", weight: 1, input: "", expected: ""}}
	for name, mutate := range map[string]func(*tar.Header){
		"traversal": func(header *tar.Header) {
			if header.Name == "cases/case.in" {
				header.Name = "../escape"
			}
		},
		"symlink": func(header *tar.Header) {
			if header.Name == "cases/case.in" {
				header.Typeflag = tar.TypeSymlink
				header.Linkname = "/etc/passwd"
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bundle := testBundle(t, base, mutate)
			if _, err := extractTestBundle(bytes.NewReader(bundle), t.TempDir()+"/cases", 10, 1024); err == nil {
				t.Fatal("extractTestBundle() error = nil")
			}
		})
	}
}

type bundleCase struct {
	id       string
	weight   int64
	input    string
	expected string
}

func testBundle(t *testing.T, cases []bundleCase, mutate func(*tar.Header)) []byte {
	t.Helper()
	manifestCases := make([]testCaseManifest, 0, len(cases))
	for _, item := range cases {
		manifestCases = append(manifestCases, testCaseManifest{ID: item.id, Weight: item.weight})
	}
	manifest, err := json.Marshal(testBundleManifest{Cases: manifestCases, Schema: judgecontract.TestBundleSchemaV1})
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	write := func(name string, content []byte) {
		header := &tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR,
		}
		if mutate != nil {
			mutate(header)
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	write(manifestName, manifest)
	for _, item := range cases {
		write("cases/"+item.id+".in", []byte(item.input))
		write("cases/"+item.id+".out", []byte(item.expected))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
