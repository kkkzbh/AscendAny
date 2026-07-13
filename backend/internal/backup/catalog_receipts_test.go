package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/catalogpublication"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

func TestCatalogReceiptArchiveRoundTripsExactCanonicalPublicationSet(t *testing.T) {
	t.Parallel()
	zstd := requireZstd(t)
	rootPath := filepath.Join(t.TempDir(), "catalog-receipts")
	if err := os.Mkdir(rootPath, catalogReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rootPath, catalogReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	publicationIDs := []int64{1, 9_007_199_254_740_993}
	for _, publicationID := range publicationIDs {
		writeTestCatalogReceipt(t, rootPath, publicationID)
	}
	archivePath := filepath.Join(t.TempDir(), CatalogReceiptArchiveFilename)
	file, entries, totalBytes, err := createCatalogReceiptArchive(
		context.Background(),
		zstd,
		rootPath,
		archivePath,
		publicationIDs,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := CatalogReceiptSnapshotDescriptor{
		File:       file,
		Count:      len(entries),
		TotalBytes: totalBytes,
		Entries:    entries,
	}
	if err := verifyOrExtractCatalogReceiptArchive(
		context.Background(), zstd, archivePath, manifest, publicationIDs, nil,
	); err != nil {
		t.Fatal(err)
	}
	destinationPath := filepath.Join(t.TempDir(), "restored-catalog-receipts")
	if err := os.Mkdir(destinationPath, catalogReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destinationPath, catalogReceiptDirectoryMode); err != nil {
		t.Fatal(err)
	}
	destination, err := os.OpenRoot(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyOrExtractCatalogReceiptArchive(
		context.Background(), zstd, archivePath, manifest, publicationIDs, destination,
	); err != nil {
		destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyCatalogReceiptRoot(destinationPath, entries, publicationIDs); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CatalogReceiptSnapshotDescriptor){
		"checksum": func(value *CatalogReceiptSnapshotDescriptor) {
			value.Entries[0].SHA256 = strings.Repeat("f", 64)
		},
		"mode": func(value *CatalogReceiptSnapshotDescriptor) {
			value.Entries[0].Mode = 0o600
		},
		"size": func(value *CatalogReceiptSnapshotDescriptor) {
			value.Entries[0].SizeBytes++
			value.TotalBytes++
		},
		"entry set": func(value *CatalogReceiptSnapshotDescriptor) {
			value.Entries = value.Entries[:1]
			value.Count = 1
			value.TotalBytes = value.Entries[0].SizeBytes
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			mutated := manifest
			mutated.Entries = append([]CatalogReceiptDescriptor(nil), manifest.Entries...)
			mutate(&mutated)
			if err := verifyOrExtractCatalogReceiptArchive(
				context.Background(), zstd, archivePath, mutated, publicationIDs, nil,
			); err == nil {
				t.Fatal("mutated catalog receipt archive manifest was accepted")
			}
		})
	}
}

func TestCatalogReceiptRootRejectsExactSetAndImmutableFileDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "1.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "contains 0 entries",
		},
		{
			name: "extra",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				writeTestCatalogReceipt(t, root, 2)
			},
			want: "contains 2 entries",
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "target.json")
				if err := os.WriteFile(target, testCatalogReceiptBytes(t, 1), catalogReceiptFileMode); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(root, "1.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "1.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "single-link regular 0640",
		},
		{
			name: "hardlink",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Link(filepath.Join(root, "1.json"), filepath.Join(t.TempDir(), "linked.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "single-link regular 0640",
		},
		{
			name: "noncanonical json",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(
					filepath.Join(root, "1.json"),
					append(testCatalogReceiptBytes(t, 1), '\n'),
					catalogReceiptFileMode,
				); err != nil {
					t.Fatal(err)
				}
			},
			want: "canonical JSON",
		},
		{
			name: "identity mismatch",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "1.json"), testCatalogReceiptBytes(t, 2), catalogReceiptFileMode); err != nil {
					t.Fatal(err)
				}
			},
			want: "identity does not match",
		},
		{
			name: "missing target model release id",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				contents := mutateTestCatalogReceipt(t, 1, func(value map[string]any) {
					delete(value, "targetModelReleaseId")
				})
				if err := os.WriteFile(filepath.Join(root, "1.json"), contents, catalogReceiptFileMode); err != nil {
					t.Fatal(err)
				}
			},
			want: "production contract",
		},
		{
			name: "invalid target model release id",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				contents := mutateTestCatalogReceipt(t, 1, func(value map[string]any) {
					value["targetModelReleaseId"] = "01"
				})
				if err := os.WriteFile(filepath.Join(root, "1.json"), contents, catalogReceiptFileMode); err != nil {
					t.Fatal(err)
				}
			},
			want: "production contract",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rootPath := filepath.Join(t.TempDir(), "catalog-receipts")
			if err := os.Mkdir(rootPath, catalogReceiptDirectoryMode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(rootPath, catalogReceiptDirectoryMode); err != nil {
				t.Fatal(err)
			}
			writeTestCatalogReceipt(t, rootPath, 1)
			test.mutate(t, rootPath)
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = inspectCatalogReceiptRoot(root, []int64{1})
			closeErr := root.Close()
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inspectCatalogReceiptRoot() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestKnowledgeCatalogPublicationSetMustBePresentAndCanonical(t *testing.T) {
	t.Parallel()
	for _, values := range [][]int64{nil, {}, {0}, {1, 1}, {2, 1}} {
		if err := validateKnowledgeCatalogPublicationIDs(values); err == nil {
			t.Fatalf("validateKnowledgeCatalogPublicationIDs(%v) error = nil", values)
		}
	}
	if err := validateKnowledgeCatalogPublicationIDs([]int64{1, 2, 10}); err != nil {
		t.Fatal(err)
	}
	encoded := encodeKnowledgeCatalogPublicationIDs([]int64{1, 9_223_372_036_854_775_807})
	if len(encoded) != 2 || encoded[1] != "9223372036854775807" {
		t.Fatalf("encoded publication ids = %v", encoded)
	}
	parsed, err := parseKnowledgeCatalogPublicationIDs(encoded)
	if err != nil || !equalInt64s(parsed, []int64{1, 9_223_372_036_854_775_807}) {
		t.Fatalf("parsed publication ids = %v, error = %v", parsed, err)
	}
	for _, invalid := range [][]string{{"01"}, {"1.0"}, {"9223372036854775808"}} {
		if _, err := parseKnowledgeCatalogPublicationIDs(invalid); err == nil {
			t.Fatalf("parseKnowledgeCatalogPublicationIDs(%v) error = nil", invalid)
		}
	}
}

func TestCatalogReceiptManifestBindsExactDatabasePublicationBytes(t *testing.T) {
	t.Parallel()
	publications := []catalogpublication.Receipt{
		testCatalogPublication(t, 1),
		testCatalogPublication(t, 9_007_199_254_740_993),
	}
	entries := make([]CatalogReceiptDescriptor, len(publications))
	for index, publication := range publications {
		publicationID, err := parseKnowledgeCatalogPublicationID(publication.KnowledgeCatalogPublicationID)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := catalogpublication.CanonicalReceipt(publication)
		if err != nil {
			t.Fatal(err)
		}
		entries[index], err = catalogReceiptDescriptor(
			publicationID,
			canonicalCatalogReceiptPath(publicationID),
			canonical,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := CatalogReceiptSnapshotDescriptor{Count: len(entries), Entries: entries}
	if err := validateCatalogReceiptDatabaseBinding(publications, snapshot); err != nil {
		t.Fatal(err)
	}
	mutated := snapshot
	mutated.Entries = append([]CatalogReceiptDescriptor(nil), snapshot.Entries...)
	mutated.Entries[0].SHA256 = strings.Repeat("f", 64)
	if err := validateCatalogReceiptDatabaseBinding(publications, mutated); err == nil {
		t.Fatal("receipt digest drift from database publication was accepted")
	}
	wrongRelease := publications[0]
	wrongRelease.TargetModelReleaseID = "8"
	wrongReleaseBytes, err := catalogpublication.CanonicalReceipt(wrongRelease)
	if err != nil {
		t.Fatal(err)
	}
	wrongReleaseEntry, err := catalogReceiptDescriptor(1, canonicalCatalogReceiptPath(1), wrongReleaseBytes)
	if err != nil {
		t.Fatal(err)
	}
	mutated = snapshot
	mutated.Entries = append([]CatalogReceiptDescriptor(nil), snapshot.Entries...)
	mutated.Entries[0] = wrongReleaseEntry
	if err := validateCatalogReceiptDatabaseBinding(publications, mutated); err == nil {
		t.Fatal("receipt target model release drift from database publication was accepted")
	}
}

func writeTestCatalogReceipt(t *testing.T, root string, publicationID int64) {
	t.Helper()
	path := filepath.Join(root, canonicalCatalogReceiptPath(publicationID))
	if err := os.WriteFile(path, testCatalogReceiptBytes(t, publicationID), catalogReceiptFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, catalogReceiptFileMode); err != nil {
		t.Fatal(err)
	}
}

func testCatalogReceiptBytes(t *testing.T, publicationID int64) []byte {
	t.Helper()
	contents, err := catalogpublication.CanonicalReceipt(catalogpublication.Receipt{
		Schema:                            catalogpublication.ReceiptSchema,
		AuthorizationID:                   fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", publicationID%1_000_000_000_000),
		KnowledgeCatalogPublicationID:     strconv.FormatInt(publicationID, 10),
		TargetModelReleaseID:              "7",
		CatalogSHA256:                     strings.Repeat("a", 64),
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
	return contents
}

func mutateTestCatalogReceipt(
	t *testing.T,
	publicationID int64,
	mutate func(map[string]any),
) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(testCatalogReceiptBytes(t, publicationID), &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func testCatalogPublication(t *testing.T, publicationID int64) catalogpublication.Receipt {
	t.Helper()
	publication, err := catalogpublication.ParseReceipt(testCatalogReceiptBytes(t, publicationID))
	if err != nil {
		t.Fatal(err)
	}
	return publication
}
