package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/catalogpublication"
)

const (
	catalogReceiptDirectoryMode = 0o750
	catalogReceiptFileMode      = 0o640
	maximumCatalogReceiptBytes  = catalogpublication.MaximumReceiptBytes
	maximumCatalogReceiptCount  = 1_000_000
)

func encodeKnowledgeCatalogPublicationIDs(values []int64) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strconv.FormatInt(value, 10)
	}
	return result
}

func parseKnowledgeCatalogPublicationIDs(values []string) ([]int64, error) {
	result := make([]int64, len(values))
	for index, value := range values {
		parsed, err := parseKnowledgeCatalogPublicationID(value)
		if err != nil {
			return nil, fmt.Errorf("knowledge catalog publication id at index %d is not canonical", index)
		}
		result[index] = parsed
	}
	if err := validateKnowledgeCatalogPublicationIDs(result); err != nil {
		return nil, err
	}
	return result, nil
}

func parseKnowledgeCatalogPublicationID(value string) (int64, error) {
	if value == "" || value[0] == '0' {
		return 0, errors.New("publication id is not canonical")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("publication id is not canonical")
	}
	return parsed, nil
}

func validateKnowledgeCatalogPublicationIDs(values []int64) error {
	if len(values) == 0 {
		return errors.New("at least one immutable knowledge catalog publication is required")
	}
	if len(values) > maximumCatalogReceiptCount {
		return fmt.Errorf("knowledge catalog publication count exceeds %d", maximumCatalogReceiptCount)
	}
	var previous int64
	for index, value := range values {
		if value <= 0 {
			return fmt.Errorf("knowledge catalog publication id at index %d must be positive", index)
		}
		if index > 0 && value <= previous {
			return errors.New("knowledge catalog publication ids must be unique and strictly increasing")
		}
		previous = value
	}
	return nil
}

func validateKnowledgeCatalogPublications(values []catalogpublication.Receipt) error {
	if len(values) == 0 || len(values) > maximumCatalogReceiptCount {
		return errors.New("knowledge catalog publication descriptors must be nonempty and bounded")
	}
	ids := make([]string, len(values))
	for index, value := range values {
		if _, err := catalogpublication.CanonicalReceipt(value); err != nil {
			return fmt.Errorf("knowledge catalog publication descriptor %d: %w", index, err)
		}
		ids[index] = value.KnowledgeCatalogPublicationID
	}
	_, err := parseKnowledgeCatalogPublicationIDs(ids)
	return err
}

func equalPublicationIDsAndDescriptors(ids []int64, publications []catalogpublication.Receipt) bool {
	if len(ids) != len(publications) {
		return false
	}
	for index, id := range ids {
		if publications[index].KnowledgeCatalogPublicationID != strconv.FormatInt(id, 10) {
			return false
		}
	}
	return true
}

func validateCatalogReceiptDatabaseBinding(
	publications []catalogpublication.Receipt,
	receipts CatalogReceiptSnapshotDescriptor,
) error {
	if err := validateKnowledgeCatalogPublications(publications); err != nil {
		return err
	}
	if len(publications) != len(receipts.Entries) {
		return errors.New("catalog publication database descriptors and receipt entries differ in count")
	}
	for index, publication := range publications {
		canonical, err := catalogpublication.CanonicalReceipt(publication)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(canonical)
		receipt := receipts.Entries[index]
		if receipt.PublicationID != publication.KnowledgeCatalogPublicationID ||
			receipt.SHA256 != hex.EncodeToString(digest[:]) || receipt.SizeBytes != int64(len(canonical)) {
			return fmt.Errorf("catalog publication %s database descriptor differs from its receipt entry", publication.KnowledgeCatalogPublicationID)
		}
	}
	return nil
}

func canonicalCatalogReceiptPath(publicationID int64) string {
	return strconv.FormatInt(publicationID, 10) + ".json"
}

func createCatalogReceiptArchive(
	ctx context.Context,
	zstdPath string,
	receiptRoot string,
	archivePath string,
	publicationIDs []int64,
) (FileDescriptor, []CatalogReceiptDescriptor, int64, error) {
	if err := validateKnowledgeCatalogPublicationIDs(publicationIDs); err != nil {
		return FileDescriptor{}, nil, 0, err
	}
	if err := validateExistingDirectory(receiptRoot, catalogReceiptDirectoryMode); err != nil {
		return FileDescriptor{}, nil, 0, fmt.Errorf("catalog publication receipt root rejected: %w", err)
	}
	root, err := os.OpenRoot(receiptRoot)
	if err != nil {
		return FileDescriptor{}, nil, 0, errors.New("open catalog publication receipt root")
	}
	defer root.Close()
	entries, totalBytes, err := inspectCatalogReceiptRoot(root, publicationIDs)
	if err != nil {
		return FileDescriptor{}, nil, 0, err
	}

	archive, err := createPrivateFile(archivePath)
	if err != nil {
		return FileDescriptor{}, nil, 0, errors.New("create catalog publication receipt archive")
	}
	archiveHash := sha256.New()
	command := exec.CommandContext(ctx, zstdPath, "--compress", "--stdout", "--quiet", "--threads=0")
	stdin, err := command.StdinPipe()
	if err != nil {
		archive.Close()
		return FileDescriptor{}, nil, 0, errors.New("open catalog receipt zstd input")
	}
	command.Stdout = io.MultiWriter(archive, archiveHash)
	var stderr limitedBuffer
	command.Stderr = &stderr
	command.Env = closedCommandEnvironment()
	if err := command.Start(); err != nil {
		archive.Close()
		return FileDescriptor{}, nil, 0, errors.New("start catalog receipt zstd compressor")
	}

	tarWriter := tar.NewWriter(stdin)
	writeError := writeCatalogReceiptTar(ctx, tarWriter, root, entries)
	if writeError == nil {
		var afterEntries []CatalogReceiptDescriptor
		var afterTotalBytes int64
		afterEntries, afterTotalBytes, writeError = inspectCatalogReceiptRoot(root, publicationIDs)
		if writeError == nil &&
			(afterTotalBytes != totalBytes || !equalCatalogReceiptDescriptors(entries, afterEntries)) {
			writeError = errors.New("catalog publication receipt root changed while it was archived")
		}
	}
	closeTarError := tarWriter.Close()
	closeInputError := stdin.Close()
	waitError := command.Wait()
	if writeError != nil || closeTarError != nil || closeInputError != nil || waitError != nil {
		_ = archive.Close()
		if writeError != nil {
			return FileDescriptor{}, nil, 0, writeError
		}
		if closeTarError != nil {
			return FileDescriptor{}, nil, 0, fmt.Errorf("finalize catalog receipt tar: %w", closeTarError)
		}
		if closeInputError != nil {
			return FileDescriptor{}, nil, 0, fmt.Errorf("close catalog receipt zstd input: %w", closeInputError)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return FileDescriptor{}, nil, 0, fmt.Errorf("catalog receipt zstd compression failed: %w", waitError)
		}
		return FileDescriptor{}, nil, 0, fmt.Errorf("catalog receipt zstd compression failed: %w: %s", waitError, detail)
	}
	if err := syncAndClose(archive); err != nil {
		return FileDescriptor{}, nil, 0, errors.New("sync catalog publication receipt archive")
	}
	info, err := validateRegularFile(archivePath, 0o600)
	if err != nil {
		return FileDescriptor{}, nil, 0, fmt.Errorf("catalog publication receipt archive rejected: %w", err)
	}
	if info.Size() <= 0 {
		return FileDescriptor{}, nil, 0, errors.New("catalog publication receipt archive is empty")
	}
	return FileDescriptor{
		Filename:  CatalogReceiptArchiveFilename,
		Format:    "tar+zstd",
		SHA256:    hex.EncodeToString(archiveHash.Sum(nil)),
		SizeBytes: info.Size(),
	}, entries, totalBytes, nil
}

func inspectCatalogReceiptRoot(
	root *os.Root,
	publicationIDs []int64,
) ([]CatalogReceiptDescriptor, int64, error) {
	if root == nil {
		return nil, 0, errors.New("catalog publication receipt root is required")
	}
	if err := validateKnowledgeCatalogPublicationIDs(publicationIDs); err != nil {
		return nil, 0, err
	}
	directoryEntries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, 0, errors.New("list catalog publication receipt root")
	}
	expectedNames := make(map[string]int64, len(publicationIDs))
	for _, publicationID := range publicationIDs {
		expectedNames[canonicalCatalogReceiptPath(publicationID)] = publicationID
	}
	if len(directoryEntries) != len(expectedNames) {
		return nil, 0, fmt.Errorf(
			"catalog publication receipt root contains %d entries; database snapshot contains %d publications",
			len(directoryEntries),
			len(expectedNames),
		)
	}
	for _, directoryEntry := range directoryEntries {
		if _, exists := expectedNames[directoryEntry.Name()]; !exists {
			return nil, 0, fmt.Errorf("unexpected catalog publication receipt entry %q", directoryEntry.Name())
		}
	}

	descriptors := make([]CatalogReceiptDescriptor, 0, len(publicationIDs))
	var totalBytes int64
	for _, publicationID := range publicationIDs {
		path := canonicalCatalogReceiptPath(publicationID)
		contents, info, err := readCatalogReceiptFile(root, path)
		if err != nil {
			return nil, 0, fmt.Errorf("catalog publication receipt %d rejected: %w", publicationID, err)
		}
		descriptor, err := catalogReceiptDescriptor(publicationID, path, contents)
		if err != nil {
			return nil, 0, fmt.Errorf("catalog publication receipt %d rejected: %w", publicationID, err)
		}
		if descriptor.SizeBytes != info.Size() {
			return nil, 0, fmt.Errorf("catalog publication receipt %d changed while it was read", publicationID)
		}
		if totalBytes > int64(^uint64(0)>>1)-descriptor.SizeBytes {
			return nil, 0, errors.New("catalog publication receipt total size overflow")
		}
		totalBytes += descriptor.SizeBytes
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, totalBytes, nil
}

func readCatalogReceiptFile(root *os.Root, path string) ([]byte, fs.FileInfo, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, nil, errors.New("receipt file is missing")
	}
	if err := validateCatalogReceiptFileInfo(info); err != nil {
		return nil, nil, err
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, nil, errors.New("open receipt file")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, nil, errors.New("stat opened receipt file")
	}
	if err := validateCatalogReceiptFileInfo(openedInfo); err != nil {
		return nil, nil, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, nil, errors.New("receipt file changed before it was opened")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumCatalogReceiptBytes+1))
	if err != nil {
		return nil, nil, errors.New("read receipt file")
	}
	if int64(len(contents)) != openedInfo.Size() {
		return nil, nil, errors.New("receipt file changed while it was read")
	}
	return contents, openedInfo, nil
}

func validateCatalogReceiptFileInfo(info fs.FileInfo) error {
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != catalogReceiptFileMode ||
		!ok || metadata.Nlink != 1 {
		return errors.New("receipt must be a single-link regular 0640 file")
	}
	if info.Size() <= 0 || info.Size() > maximumCatalogReceiptBytes {
		return fmt.Errorf("receipt size must be between 1 and %d bytes", maximumCatalogReceiptBytes)
	}
	return nil
}

func catalogReceiptDescriptor(
	publicationID int64,
	path string,
	contents []byte,
) (CatalogReceiptDescriptor, error) {
	if publicationID <= 0 || path != canonicalCatalogReceiptPath(publicationID) {
		return CatalogReceiptDescriptor{}, errors.New("receipt identity or path is not canonical")
	}
	receipt, err := catalogpublication.ParseReceipt(contents)
	if err != nil {
		return CatalogReceiptDescriptor{}, fmt.Errorf("receipt must satisfy the canonical v2 production contract: %w", err)
	}
	if receipt.KnowledgeCatalogPublicationID != strconv.FormatInt(publicationID, 10) {
		return CatalogReceiptDescriptor{}, errors.New("receipt publication identity does not match its path")
	}
	digest := sha256.Sum256(contents)
	return CatalogReceiptDescriptor{
		PublicationID: strconv.FormatInt(publicationID, 10),
		Path:          path,
		SHA256:        hex.EncodeToString(digest[:]),
		SizeBytes:     int64(len(contents)),
		Mode:          catalogReceiptFileMode,
	}, nil
}

func writeCatalogReceiptTar(
	ctx context.Context,
	writer *tar.Writer,
	root *os.Root,
	entries []CatalogReceiptDescriptor,
) error {
	for index, descriptor := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateCatalogReceiptDescriptor(descriptor); err != nil {
			return fmt.Errorf("catalog receipt descriptor %d: %w", index, err)
		}
		publicationID, err := parseKnowledgeCatalogPublicationID(descriptor.PublicationID)
		if err != nil {
			return fmt.Errorf("catalog receipt descriptor %d: %w", index, err)
		}
		contents, _, err := readCatalogReceiptFile(root, descriptor.Path)
		if err != nil {
			return fmt.Errorf("read catalog receipt %s for archive: %w", descriptor.PublicationID, err)
		}
		actual, err := catalogReceiptDescriptor(publicationID, descriptor.Path, contents)
		if err != nil || actual != descriptor {
			return fmt.Errorf("catalog receipt %s changed before archive", descriptor.PublicationID)
		}
		header := &tar.Header{
			Name:     descriptor.Path,
			Mode:     descriptor.Mode,
			Size:     descriptor.SizeBytes,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := writer.WriteHeader(header); err != nil {
			return fmt.Errorf("write catalog receipt %s archive header: %w", descriptor.PublicationID, err)
		}
		written, err := writer.Write(contents)
		if err != nil {
			return fmt.Errorf("write catalog receipt %s archive content: %w", descriptor.PublicationID, err)
		}
		if int64(written) != descriptor.SizeBytes {
			return fmt.Errorf("write catalog receipt %s archive content: short write", descriptor.PublicationID)
		}
	}
	return nil
}

func verifyOrExtractCatalogReceiptArchive(
	ctx context.Context,
	zstdPath string,
	archivePath string,
	manifest CatalogReceiptSnapshotDescriptor,
	publicationIDs []int64,
	destination *os.Root,
) error {
	if err := validateCatalogReceiptSnapshot(manifest, publicationIDs); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, zstdPath, "--decompress", "--stdout", "--quiet", archivePath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return errors.New("open catalog receipt zstd decompressor output")
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	command.Env = closedCommandEnvironment()
	if err := command.Start(); err != nil {
		return errors.New("start catalog receipt zstd decompressor")
	}
	counting := &countingReader{reader: stdout}
	tarReader := tar.NewReader(counting)
	readError := readCatalogReceiptTar(ctx, tarReader, manifest.Entries, destination)
	if readError == nil {
		_, readError = io.Copy(io.Discard, counting)
	}
	if readError != nil {
		_ = stdout.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}
	waitError := command.Wait()
	if readError != nil {
		return readError
	}
	if waitError != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return fmt.Errorf("catalog receipt zstd decompression failed: %w", waitError)
		}
		return fmt.Errorf("catalog receipt zstd decompression failed: %w: %s", waitError, detail)
	}
	expectedTarBytes, err := exactCatalogReceiptTarSize(manifest.Entries)
	if err != nil {
		return err
	}
	if counting.count != expectedTarBytes {
		return fmt.Errorf("catalog receipt tar size = %d, expected %d", counting.count, expectedTarBytes)
	}
	return nil
}

func readCatalogReceiptTar(
	ctx context.Context,
	reader *tar.Reader,
	expected []CatalogReceiptDescriptor,
	destination *os.Root,
) error {
	for index, descriptor := range expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err != nil {
			return fmt.Errorf("read catalog receipt tar entry %d: %w", index, err)
		}
		if header.Name != descriptor.Path || header.Typeflag != tar.TypeReg ||
			header.Mode != descriptor.Mode || header.Size != descriptor.SizeBytes {
			return fmt.Errorf("catalog receipt tar entry %d does not match manifest", index)
		}
		publicationID, err := parseKnowledgeCatalogPublicationID(descriptor.PublicationID)
		if err != nil {
			return fmt.Errorf("catalog receipt tar entry %d has invalid publication id", index)
		}
		contents := make([]byte, descriptor.SizeBytes)
		if _, err := io.ReadFull(reader, contents); err != nil {
			return fmt.Errorf("read catalog receipt tar content %s: %w", descriptor.PublicationID, err)
		}
		actual, err := catalogReceiptDescriptor(publicationID, descriptor.Path, contents)
		if err != nil || actual != descriptor {
			return fmt.Errorf("catalog receipt tar content %s does not match manifest", descriptor.PublicationID)
		}
		if destination != nil {
			file, err := destination.OpenFile(descriptor.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, catalogReceiptFileMode)
			if err != nil {
				return fmt.Errorf("create restored catalog receipt %s: %w", descriptor.PublicationID, err)
			}
			if err := file.Chmod(catalogReceiptFileMode); err != nil {
				_ = file.Close()
				return fmt.Errorf("set restored catalog receipt %s mode: %w", descriptor.PublicationID, err)
			}
			written, err := file.Write(contents)
			if err != nil {
				_ = file.Close()
				return fmt.Errorf("write restored catalog receipt %s: %w", descriptor.PublicationID, err)
			}
			if int64(written) != descriptor.SizeBytes {
				_ = file.Close()
				return fmt.Errorf("write restored catalog receipt %s: short write", descriptor.PublicationID)
			}
			if err := syncAndClose(file); err != nil {
				return fmt.Errorf("sync restored catalog receipt %s: %w", descriptor.PublicationID, err)
			}
		}
	}
	header, err := reader.Next()
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read catalog receipt tar terminator: %w", err)
	}
	if header != nil {
		return errors.New("catalog receipt tar contains an entry absent from the manifest")
	}
	return nil
}

func validateCatalogReceiptSnapshot(
	manifest CatalogReceiptSnapshotDescriptor,
	publicationIDs []int64,
) error {
	if err := validateKnowledgeCatalogPublicationIDs(publicationIDs); err != nil {
		return err
	}
	if err := validateCatalogReceiptDescriptors(manifest.Entries); err != nil {
		return err
	}
	if manifest.Count != len(manifest.Entries) || manifest.Count != len(publicationIDs) {
		return errors.New("catalog receipt count does not match database publications")
	}
	var totalBytes int64
	for index, descriptor := range manifest.Entries {
		if descriptor.PublicationID != strconv.FormatInt(publicationIDs[index], 10) {
			return errors.New("catalog receipt entries do not match database publication ids")
		}
		if totalBytes > int64(^uint64(0)>>1)-descriptor.SizeBytes {
			return errors.New("catalog receipt total size overflow")
		}
		totalBytes += descriptor.SizeBytes
	}
	if totalBytes != manifest.TotalBytes {
		return errors.New("catalog receipt total bytes does not match entries")
	}
	return nil
}

func validateCatalogReceiptDescriptors(values []CatalogReceiptDescriptor) error {
	if len(values) == 0 {
		return errors.New("catalog receipt entries are empty")
	}
	var previous int64
	for index, value := range values {
		if err := validateCatalogReceiptDescriptor(value); err != nil {
			return fmt.Errorf("catalog receipt entry %d: %w", index, err)
		}
		publicationID, _ := parseKnowledgeCatalogPublicationID(value.PublicationID)
		if index > 0 && publicationID <= previous {
			return errors.New("catalog receipt entries must be unique and sorted by publication id")
		}
		previous = publicationID
	}
	return nil
}

func validateCatalogReceiptDescriptor(value CatalogReceiptDescriptor) error {
	publicationID, err := parseKnowledgeCatalogPublicationID(value.PublicationID)
	if err != nil || value.Path != canonicalCatalogReceiptPath(publicationID) {
		return errors.New("publication id or path is invalid")
	}
	if !sha256Pattern.MatchString(value.SHA256) || value.SizeBytes <= 0 ||
		value.SizeBytes > maximumCatalogReceiptBytes || value.Mode != catalogReceiptFileMode {
		return errors.New("digest, size, or mode is invalid")
	}
	return nil
}

func exactCatalogReceiptTarSize(entries []CatalogReceiptDescriptor) (int64, error) {
	total := int64(1024)
	for _, entry := range entries {
		padded := entry.SizeBytes
		if remainder := padded % 512; remainder != 0 {
			padded += 512 - remainder
		}
		if padded < entry.SizeBytes || total > int64(^uint64(0)>>1)-512-padded {
			return 0, errors.New("catalog receipt tar size overflow")
		}
		total += 512 + padded
	}
	return total, nil
}

func verifyCatalogReceiptRoot(
	rootPath string,
	expected []CatalogReceiptDescriptor,
	publicationIDs []int64,
) error {
	if err := validateExistingDirectory(rootPath, catalogReceiptDirectoryMode); err != nil {
		return err
	}
	if err := validateCatalogReceiptSnapshot(CatalogReceiptSnapshotDescriptor{
		Count:      len(expected),
		TotalBytes: catalogReceiptTotalBytes(expected),
		Entries:    expected,
	}, publicationIDs); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return errors.New("open restored catalog publication receipt root")
	}
	defer root.Close()
	actual, _, err := inspectCatalogReceiptRoot(root, publicationIDs)
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return errors.New("restored catalog receipt count differs from manifest")
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return fmt.Errorf("restored catalog receipt %s differs from manifest", expected[index].PublicationID)
		}
	}
	return nil
}

func catalogReceiptTotalBytes(entries []CatalogReceiptDescriptor) int64 {
	var total int64
	for _, entry := range entries {
		total += entry.SizeBytes
	}
	return total
}

func equalCatalogReceiptDescriptors(left, right []CatalogReceiptDescriptor) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedCatalogReceiptDescriptorCopy(values []CatalogReceiptDescriptor) []CatalogReceiptDescriptor {
	result := append([]CatalogReceiptDescriptor(nil), values...)
	sort.Slice(result, func(left, right int) bool {
		leftID, _ := parseKnowledgeCatalogPublicationID(result[left].PublicationID)
		rightID, _ := parseKnowledgeCatalogPublicationID(result[right].PublicationID)
		return leftID < rightID
	})
	return result
}
