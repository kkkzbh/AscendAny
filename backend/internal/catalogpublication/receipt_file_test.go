package catalogpublication

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"golang.org/x/sys/unix"
)

func TestWriteReceiptSetsDeterministicModeUnderRestrictiveUmask(t *testing.T) {
	directory := newReceiptDirectory(t)
	previous := unix.Umask(0o077)
	t.Cleanup(func() { unix.Umask(previous) })

	const publicationID = "3"
	canonical := testReceiptBytes(t, publicationID, strings.Repeat("a", 64))
	if err := WriteReceipt(directory, publicationID, canonical); err != nil {
		t.Fatalf("WriteReceipt() error = %v", err)
	}
	info, err := os.Lstat(filepath.Join(directory, publicationID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	metadata := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != ReceiptFileMode || metadata.Uid != uint32(os.Geteuid()) ||
		metadata.Gid != uint32(os.Getegid()) || metadata.Nlink != 1 {
		t.Fatalf("receipt mode=%04o uid=%d gid=%d nlink=%d", info.Mode().Perm(), metadata.Uid, metadata.Gid, metadata.Nlink)
	}
}

func TestWriteReceiptPublishesAppendOnlyBackupReadableBytes(t *testing.T) {
	t.Parallel()
	directory := newReceiptDirectory(t)
	const firstPublicationID = "1"
	const secondPublicationID = "2"
	first := testReceiptBytes(t, firstPublicationID, strings.Repeat("a", 64))
	if err := WriteReceipt(directory, firstPublicationID, first); err != nil {
		t.Fatalf("WriteReceipt(first) error = %v", err)
	}
	if err := WriteReceipt(directory, firstPublicationID, first); err != nil {
		t.Fatalf("WriteReceipt(idempotent) error = %v", err)
	}
	different := testReceiptBytes(t, firstPublicationID, strings.Repeat("f", 64))
	if err := WriteReceipt(directory, firstPublicationID, different); err == nil || !strings.Contains(err.Error(), "different bytes") {
		t.Fatalf("WriteReceipt(different) error = %v", err)
	}
	second := testReceiptBytes(t, secondPublicationID, strings.Repeat("b", 64))
	if err := WriteReceipt(directory, secondPublicationID, second); err != nil {
		t.Fatalf("WriteReceipt(second publication) error = %v", err)
	}
	for filename, expected := range map[string][]byte{firstPublicationID + ".json": first, secondPublicationID + ".json": second} {
		path := filepath.Join(directory, filename)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != string(expected) || !info.Mode().IsRegular() || info.Mode().Perm() != ReceiptFileMode {
			t.Fatalf("%s contents=%s mode=%o", filename, contents, info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != firstPublicationID+".json" || entries[1].Name() != secondPublicationID+".json" {
		t.Fatalf("directory entries = %#v", entries)
	}
}

func TestWriteReceiptSerializesConcurrentNoReplacePublication(t *testing.T) {
	t.Parallel()
	directory := newReceiptDirectory(t)
	first := testReceiptBytes(t, "1", strings.Repeat("a", 64))
	second := testReceiptBytes(t, "1", strings.Repeat("f", 64))
	inputs := [][]byte{first, second, first, second, first, second, first, second}
	errorsByAttempt := make([]error, len(inputs))
	var wait sync.WaitGroup
	for index, input := range inputs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByAttempt[index] = WriteReceipt(directory, "1", input)
		}()
	}
	wait.Wait()

	published, err := os.ReadFile(filepath.Join(directory, "1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(published, first) && !bytes.Equal(published, second) {
		t.Fatalf("published bytes differ from every complete candidate: %s", published)
	}
	for index, err := range errorsByAttempt {
		if bytes.Equal(inputs[index], published) {
			if err != nil {
				t.Fatalf("exact concurrent attempt %d error = %v", index, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "different bytes") {
			t.Fatalf("conflicting concurrent attempt %d error = %v", index, err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "1.json" {
		t.Fatalf("concurrent publication left unexpected entries: %#v", entries)
	}
}

func TestWriteReceiptRejectsInvalidContractWithoutNamespaceResidue(t *testing.T) {
	t.Parallel()
	directory := newReceiptDirectory(t)
	valid := testReceiptBytes(t, "1", strings.Repeat("a", 64))

	for name, testCase := range map[string]struct {
		publicationID string
		raw           []byte
	}{
		"non-object":      {publicationID: "1", raw: []byte("not-json")},
		"partial receipt": {publicationID: "1", raw: []byte(`{"schema":"ascendany.knowledge_catalog.publication-receipt.v1"}`)},
		"identity mismatch": {
			publicationID: "2",
			raw:           valid,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := WriteReceipt(directory, testCase.publicationID, testCase.raw); err == nil {
				t.Fatal("WriteReceipt() error = nil")
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid write left directory entries: %#v", entries)
			}
		})
	}
}

func TestWriteReceiptRejectsInvalidFilesystemBoundary(t *testing.T) {
	t.Parallel()
	valid := testReceiptBytes(t, "1", strings.Repeat("a", 64))
	if err := WriteReceipt("relative", "1", valid); err == nil {
		t.Fatal("relative path was accepted")
	}
	directory := newReceiptDirectory(t)
	if err := WriteReceipt(directory, "01", valid); err == nil {
		t.Fatal("noncanonical publication identity was accepted")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(directory, "1", valid); err == nil {
		t.Fatal("incorrect receipt directory mode was accepted")
	}
	if err := os.Chmod(directory, ReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(directory, "1", valid); err == nil || !strings.Contains(err.Error(), "single-level") {
		t.Fatalf("directory with extra link WriteReceipt() error = %v", err)
	}
}

func TestWriteReceiptRejectsExistingFileMetadataViolations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, string){
		"mode": func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"link count": func(t *testing.T, path string) {
			if err := os.Link(path, path+".alias"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			directory := newReceiptDirectory(t)
			canonical := testReceiptBytes(t, "1", strings.Repeat("a", 64))
			path := filepath.Join(directory, "1.json")
			if err := WriteReceipt(directory, "1", canonical); err != nil {
				t.Fatal(err)
			}
			mutate(t, path)
			if err := WriteReceipt(directory, "1", canonical); err == nil || !strings.Contains(err.Error(), "immutable owner, group, mode, type, or link-count") {
				t.Fatalf("WriteReceipt() error = %v", err)
			}
		})
	}
}

func TestWriteReceiptRejectsExistingSymlink(t *testing.T) {
	t.Parallel()
	directory := newReceiptDirectory(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "1.json")); err != nil {
		t.Fatal(err)
	}
	canonical := testReceiptBytes(t, "1", strings.Repeat("a", 64))
	if err := WriteReceipt(directory, "1", canonical); err == nil || !strings.Contains(err.Error(), "open existing publication receipt") {
		t.Fatalf("WriteReceipt() error = %v", err)
	}
	contents, err := os.ReadFile(outside)
	if err != nil || string(contents) != "outside" {
		t.Fatalf("symlink target contents=%q error=%v", contents, err)
	}
}

func TestReceiptStatValidatorsBindOwnerGroupModeTypeAndLinks(t *testing.T) {
	t.Parallel()
	uid := uint32(os.Geteuid())
	gid := uint32(os.Getegid())
	directory := unix.Stat_t{Mode: unix.S_IFDIR | uint32(ReceiptDirectoryMode), Nlink: 2, Uid: uid, Gid: gid}
	if err := validateReceiptDirectoryStat(directory, uid, gid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*unix.Stat_t){
		"owner":      func(stat *unix.Stat_t) { stat.Uid++ },
		"group":      func(stat *unix.Stat_t) { stat.Gid++ },
		"mode":       func(stat *unix.Stat_t) { stat.Mode = unix.S_IFDIR | 0o700 },
		"type":       func(stat *unix.Stat_t) { stat.Mode = unix.S_IFREG | uint32(ReceiptDirectoryMode) },
		"link count": func(stat *unix.Stat_t) { stat.Nlink++ },
	} {
		t.Run("directory "+name, func(t *testing.T) {
			stat := directory
			mutate(&stat)
			if err := validateReceiptDirectoryStat(stat, uid, gid); err == nil {
				t.Fatal("validateReceiptDirectoryStat() error = nil")
			}
		})
	}

	file := unix.Stat_t{Mode: unix.S_IFREG | uint32(ReceiptFileMode), Nlink: 1, Uid: uid, Gid: gid}
	if err := validateReceiptFileStat(file, uid, gid, 1, "receipt"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*unix.Stat_t){
		"owner":      func(stat *unix.Stat_t) { stat.Uid++ },
		"group":      func(stat *unix.Stat_t) { stat.Gid++ },
		"mode":       func(stat *unix.Stat_t) { stat.Mode = unix.S_IFREG | 0o600 },
		"type":       func(stat *unix.Stat_t) { stat.Mode = unix.S_IFDIR | uint32(ReceiptFileMode) },
		"link count": func(stat *unix.Stat_t) { stat.Nlink++ },
	} {
		t.Run("file "+name, func(t *testing.T) {
			stat := file
			mutate(&stat)
			if err := validateReceiptFileStat(stat, uid, gid, 1, "receipt"); err == nil {
				t.Fatal("validateReceiptFileStat() error = nil")
			}
		})
	}
}

func newReceiptDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "receipts")
	if err := os.Mkdir(directory, ReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, ReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	return directory
}

func testReceiptBytes(t *testing.T, publicationID, catalogSHA256 string) []byte {
	t.Helper()
	canonical, err := CanonicalReceipt(Receipt{
		Schema:                            ReceiptSchema,
		AuthorizationID:                   "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		KnowledgeCatalogPublicationID:     publicationID,
		TargetModelReleaseID:              "23",
		CatalogSHA256:                     catalogSHA256,
		ModelArtifactSHA256:               strings.Repeat("b", 64),
		ModelID:                           "11111111-1111-4111-8111-111111111111",
		TargetApplicationVersion:          "0.2.0",
		TargetApplicationCommit:           strings.Repeat("c", 40),
		TargetApplicationBuildTime:        "2026-07-13T04:00:00Z",
		ConfigurationKey:                  configuration.KnowledgeCatalogKey,
		ConfigurationID:                   "22222222-2222-4222-8222-222222222222",
		ExpectedConfigurationHeadRevision: 3,
		ConfigurationHeadRevision:         4,
		ConfigurationVersionID:            "4",
		ConfigurationVersionNumber:        4,
		AnalyticsGenerationID:             "17",
		AnalyticsHeadRevision:             9,
		InputManifestSHA256:               strings.Repeat("d", 64),
		CurrentModelHeadRevision:          2,
		CurrentModelArtifactSHA256:        strings.Repeat("e", 64),
		PublishedByAccountID:              "33333333-3333-4333-8333-333333333333",
		PublishedBySessionID:              "44444444-4444-4444-8444-444444444444",
		PublishedAt:                       time.Date(2026, 7, 13, 4, 0, 0, 123_000_000, time.UTC).Format(time.RFC3339Nano),
		AuditEventID:                      "9223372036854775807",
		ConfigurationMutated:              true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
