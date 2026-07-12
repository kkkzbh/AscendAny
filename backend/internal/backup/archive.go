package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

func createArtifactArchive(
	ctx context.Context,
	zstdPath string,
	artifactRoot string,
	archivePath string,
	artifacts []ArtifactDescriptor,
) (FileDescriptor, int64, error) {
	if err := validateArtifactList(artifacts); err != nil {
		return FileDescriptor{}, 0, err
	}
	if err := validateExistingDirectory(artifactRoot, 0o750); err != nil {
		return FileDescriptor{}, 0, fmt.Errorf("artifact root rejected: %w", err)
	}
	root, err := os.OpenRoot(artifactRoot)
	if err != nil {
		return FileDescriptor{}, 0, errors.New("open artifact root")
	}
	defer root.Close()
	for _, directory := range []string{"sha256"} {
		info, statErr := root.Lstat(directory)
		if statErr != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
			return FileDescriptor{}, 0, errors.New("published artifact namespace must be a real 0750 directory")
		}
	}

	archive, err := createPrivateFile(archivePath)
	if err != nil {
		return FileDescriptor{}, 0, errors.New("create artifact archive")
	}
	archiveHash := sha256.New()
	command := exec.CommandContext(ctx, zstdPath, "--compress", "--stdout", "--quiet", "--threads=0")
	stdin, err := command.StdinPipe()
	if err != nil {
		archive.Close()
		return FileDescriptor{}, 0, errors.New("open zstd input")
	}
	command.Stdout = io.MultiWriter(archive, archiveHash)
	var stderr limitedBuffer
	command.Stderr = &stderr
	command.Env = closedCommandEnvironment()
	if err := command.Start(); err != nil {
		archive.Close()
		return FileDescriptor{}, 0, errors.New("start zstd compressor")
	}

	tarWriter := tar.NewWriter(stdin)
	totalBytes := int64(0)
	writeError := writeArtifactTar(ctx, tarWriter, root, artifacts, &totalBytes)
	closeTarError := tarWriter.Close()
	closeInputError := stdin.Close()
	waitError := command.Wait()
	if writeError != nil || closeTarError != nil || closeInputError != nil || waitError != nil {
		_ = archive.Close()
		if writeError != nil {
			return FileDescriptor{}, 0, writeError
		}
		if closeTarError != nil {
			return FileDescriptor{}, 0, fmt.Errorf("finalize artifact tar: %w", closeTarError)
		}
		if closeInputError != nil {
			return FileDescriptor{}, 0, fmt.Errorf("close zstd input: %w", closeInputError)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return FileDescriptor{}, 0, fmt.Errorf("zstd compression failed: %w", waitError)
		}
		return FileDescriptor{}, 0, fmt.Errorf("zstd compression failed: %w: %s", waitError, detail)
	}
	if err := syncAndClose(archive); err != nil {
		return FileDescriptor{}, 0, errors.New("sync artifact archive")
	}
	info, err := validateRegularFile(archivePath, 0o600)
	if err != nil {
		return FileDescriptor{}, 0, fmt.Errorf("artifact archive rejected: %w", err)
	}
	if info.Size() <= 0 {
		return FileDescriptor{}, 0, errors.New("artifact archive is empty")
	}
	return FileDescriptor{
		Filename:  ArtifactArchiveFilename,
		Format:    "tar+zstd",
		SHA256:    hex.EncodeToString(archiveHash.Sum(nil)),
		SizeBytes: info.Size(),
	}, totalBytes, nil
}

func writeArtifactTar(
	ctx context.Context,
	writer *tar.Writer,
	root *os.Root,
	artifacts []ArtifactDescriptor,
	totalBytes *int64,
) error {
	checkedPrefixes := make(map[string]struct{})
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix := path.Dir(artifact.StorageKey)
		if _, checked := checkedPrefixes[prefix]; !checked {
			info, err := root.Lstat(prefix)
			if err != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
				return fmt.Errorf("artifact prefix %s must be a real 0750 directory", prefix)
			}
			checkedPrefixes[prefix] = struct{}{}
		}
		info, err := root.Lstat(artifact.StorageKey)
		if err != nil {
			return fmt.Errorf("artifact %s is missing", artifact.SHA256)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o640 || info.Size() != artifact.SizeBytes {
			return fmt.Errorf("artifact %s metadata does not match the database", artifact.SHA256)
		}
		file, err := root.Open(artifact.StorageKey)
		if err != nil {
			return fmt.Errorf("open artifact %s: %w", artifact.SHA256, err)
		}
		header := &tar.Header{
			Name:     artifact.StorageKey,
			Mode:     0o640,
			Size:     artifact.SizeBytes,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := writer.WriteHeader(header); err != nil {
			file.Close()
			return fmt.Errorf("write artifact %s archive header: %w", artifact.SHA256, err)
		}
		digest := sha256.New()
		written, copyErr := io.CopyN(writer, io.TeeReader(file, digest), artifact.SizeBytes)
		remaining := make([]byte, 1)
		remainingCount, remainingErr := file.Read(remaining)
		closeErr := file.Close()
		if copyErr != nil || written != artifact.SizeBytes {
			return fmt.Errorf("read artifact %s: %w", artifact.SHA256, copyErr)
		}
		if remainingCount != 0 || (remainingErr != nil && !errors.Is(remainingErr, io.EOF)) {
			return fmt.Errorf("artifact %s changed while it was archived", artifact.SHA256)
		}
		if closeErr != nil {
			return fmt.Errorf("close artifact %s: %w", artifact.SHA256, closeErr)
		}
		if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
			return fmt.Errorf("artifact %s content hash mismatch", artifact.SHA256)
		}
		*totalBytes += written
	}
	return nil
}

func verifyOrExtractArtifactArchive(
	ctx context.Context,
	zstdPath string,
	archivePath string,
	manifest ArtifactSnapshotDescriptor,
	destination *os.Root,
) error {
	if err := validateArtifactList(manifest.Entries); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, zstdPath, "--decompress", "--stdout", "--quiet", archivePath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return errors.New("open zstd decompressor output")
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	command.Env = closedCommandEnvironment()
	if err := command.Start(); err != nil {
		return errors.New("start zstd decompressor")
	}

	counting := &countingReader{reader: stdout}
	tarReader := tar.NewReader(counting)
	readError := readArtifactTar(ctx, tarReader, manifest.Entries, destination)
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
			return fmt.Errorf("zstd decompression failed: %w", waitError)
		}
		return fmt.Errorf("zstd decompression failed: %w: %s", waitError, detail)
	}
	expectedTarBytes, err := exactTarSize(manifest.Entries)
	if err != nil {
		return err
	}
	if counting.count != expectedTarBytes {
		return fmt.Errorf("artifact tar size = %d, expected %d", counting.count, expectedTarBytes)
	}
	return nil
}

func readArtifactTar(ctx context.Context, reader *tar.Reader, expected []ArtifactDescriptor, destination *os.Root) error {
	createdPrefixes := make(map[string]struct{})
	if destination != nil {
		if err := destination.Mkdir("sha256", 0o750); err != nil {
			return fmt.Errorf("create restored sha256 namespace: %w", err)
		}
		if err := destination.Chmod("sha256", 0o750); err != nil {
			return fmt.Errorf("set restored sha256 namespace mode: %w", err)
		}
	}
	for index, artifact := range expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err != nil {
			return fmt.Errorf("read artifact tar entry %d: %w", index, err)
		}
		if header.Name != artifact.StorageKey || header.Typeflag != tar.TypeReg || header.Mode != 0o640 || header.Size != artifact.SizeBytes {
			return fmt.Errorf("artifact tar entry %d does not match manifest", index)
		}
		var target io.Writer = io.Discard
		var file *os.File
		if destination != nil {
			prefix := path.Dir(artifact.StorageKey)
			if _, created := createdPrefixes[prefix]; !created {
				if err := destination.Mkdir(prefix, 0o750); err != nil {
					return fmt.Errorf("create restored artifact prefix: %w", err)
				}
				if err := destination.Chmod(prefix, 0o750); err != nil {
					return fmt.Errorf("set restored artifact prefix mode: %w", err)
				}
				createdPrefixes[prefix] = struct{}{}
			}
			file, err = destination.OpenFile(artifact.StorageKey, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
			if err != nil {
				return fmt.Errorf("create restored artifact %s: %w", artifact.SHA256, err)
			}
			if err := file.Chmod(0o640); err != nil {
				_ = file.Close()
				return fmt.Errorf("set restored artifact %s mode: %w", artifact.SHA256, err)
			}
			target = file
		}
		digest := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(target, digest), reader, artifact.SizeBytes)
		if copyErr != nil || written != artifact.SizeBytes {
			if file != nil {
				_ = file.Close()
			}
			return fmt.Errorf("read artifact tar content %s: %w", artifact.SHA256, copyErr)
		}
		if file != nil {
			if err := syncAndClose(file); err != nil {
				return fmt.Errorf("sync restored artifact %s: %w", artifact.SHA256, err)
			}
		}
		if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
			return fmt.Errorf("artifact tar content %s hash mismatch", artifact.SHA256)
		}
	}
	header, err := reader.Next()
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read artifact tar terminator: %w", err)
	}
	if header != nil {
		return errors.New("artifact tar contains an entry absent from the manifest")
	}
	return nil
}

func verifyRootArtifact(root *os.Root, artifact ArtifactDescriptor) error {
	info, err := root.Lstat(artifact.StorageKey)
	if err != nil {
		return errors.New("artifact file is missing")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o640 || info.Size() != artifact.SizeBytes {
		return errors.New("artifact file metadata mismatch")
	}
	file, err := root.Open(artifact.StorageKey)
	if err != nil {
		return errors.New("open artifact file")
	}
	defer file.Close()
	digest := sha256.New()
	written, err := io.Copy(digest, file)
	if err != nil || written != artifact.SizeBytes {
		return errors.New("read artifact file")
	}
	if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return errors.New("artifact file hash mismatch")
	}
	return nil
}

func validateArtifactList(artifacts []ArtifactDescriptor) error {
	previous := ""
	for index, artifact := range artifacts {
		if err := validateArtifactDescriptor(artifact); err != nil {
			return fmt.Errorf("artifact entry %d: %w", index, err)
		}
		if index > 0 && artifact.SHA256 <= previous {
			return errors.New("artifact entries must be unique and sorted by SHA-256")
		}
		previous = artifact.SHA256
	}
	return nil
}

func validateArtifactDescriptor(artifact ArtifactDescriptor) error {
	if !sha256Pattern.MatchString(artifact.SHA256) {
		return errors.New("SHA-256 is invalid")
	}
	if artifact.SizeBytes <= 0 {
		return errors.New("size must be positive")
	}
	expectedKey := "sha256/" + artifact.SHA256[:2] + "/" + artifact.SHA256
	if artifact.StorageKey != expectedKey {
		return errors.New("storage key is not canonical")
	}
	return nil
}

func validateMigrations(values []MigrationDescriptor) error {
	if len(values) == 0 {
		return errors.New("migration history is empty")
	}
	for index, value := range values {
		if value.Version != int64(index+1) {
			return errors.New("migration history must be a contiguous prefix starting at version 1")
		}
		if !migrationNamePattern.MatchString(value.Name) || !sha256Pattern.MatchString(value.SHA256) {
			return fmt.Errorf("migration entry %d is invalid", index)
		}
	}
	return nil
}

func exactTarSize(artifacts []ArtifactDescriptor) (int64, error) {
	total := int64(1024)
	for _, artifact := range artifacts {
		padded := artifact.SizeBytes
		if remainder := padded % 512; remainder != 0 {
			padded += 512 - remainder
		}
		if padded < artifact.SizeBytes || total > int64(^uint64(0)>>1)-512-padded {
			return 0, errors.New("artifact tar size overflow")
		}
		total += 512 + padded
	}
	return total, nil
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (reader *countingReader) Read(value []byte) (int, error) {
	count, err := reader.reader.Read(value)
	reader.count += int64(count)
	return count, err
}

func sortedArtifactCopy(values []ArtifactDescriptor) []ArtifactDescriptor {
	result := append([]ArtifactDescriptor(nil), values...)
	sort.Slice(result, func(left, right int) bool { return result[left].SHA256 < result[right].SHA256 })
	return result
}
