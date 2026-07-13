// Package catalogartifact owns secure loading of the release-bound knowledge
// catalog file. JSON schema and domain semantics remain owned by recommendation.
package catalogartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

var (
	ErrInvalidPath    = errors.New("invalid knowledge catalog path")
	ErrInvalidFile    = errors.New("invalid knowledge catalog file")
	ErrDigestMismatch = errors.New("knowledge catalog digest mismatch")
	ErrModelMismatch  = errors.New("knowledge catalog model mismatch")
)

const RequiredMode os.FileMode = 0o644

type Loaded struct {
	Artifact recommendation.KnowledgeCatalogArtifact
	SHA256   string
	Size     int64
	Mode     os.FileMode
}

// Load opens the final path without following a leaf symlink, verifies the
// immutable release-file shape and independent digest, validates the complete
// catalog contract, and binds it to the inference model manifest.
func Load(path, expectedSHA256 string, manifest inferencemodel.Manifest) (Loaded, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return Loaded{}, fmt.Errorf("%w: an absolute clean path is required", ErrInvalidPath)
	}
	if !lowercaseSHA256(expectedSHA256) {
		return Loaded{}, fmt.Errorf("%w: expected SHA-256 must be 64 lowercase hexadecimal characters", ErrDigestMismatch)
	}

	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: open: %v", ErrInvalidFile, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return Loaded{}, fmt.Errorf("%w: construct file handle", ErrInvalidFile)
	}
	defer file.Close()

	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil {
		return Loaded{}, fmt.Errorf("%w: stat: %v", ErrInvalidFile, err)
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG {
		return Loaded{}, fmt.Errorf("%w: catalog must be a regular file", ErrInvalidFile)
	}
	mode := os.FileMode(before.Mode & 0o777)
	if mode != RequiredMode {
		return Loaded{}, fmt.Errorf("%w: catalog mode is %04o, require %04o", ErrInvalidFile, mode, RequiredMode)
	}
	if before.Nlink != 1 {
		return Loaded{}, fmt.Errorf("%w: catalog must have exactly one hard link", ErrInvalidFile)
	}
	if before.Size < 1 || before.Size > recommendation.MaximumKnowledgeCatalogBytes {
		return Loaded{}, fmt.Errorf(
			"%w: catalog size %d is outside 1..%d",
			ErrInvalidFile,
			before.Size,
			recommendation.MaximumKnowledgeCatalogBytes,
		)
	}

	raw, err := io.ReadAll(io.LimitReader(file, int64(recommendation.MaximumKnowledgeCatalogBytes)+1))
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: read: %v", ErrInvalidFile, err)
	}
	if int64(len(raw)) != before.Size {
		return Loaded{}, fmt.Errorf("%w: catalog size changed while reading", ErrInvalidFile)
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return Loaded{}, fmt.Errorf("%w: restat: %v", ErrInvalidFile, err)
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size ||
		before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return Loaded{}, fmt.Errorf("%w: catalog changed while reading", ErrInvalidFile)
	}

	digest := sha256.Sum256(raw)
	actualSHA256 := hex.EncodeToString(digest[:])
	if actualSHA256 != expectedSHA256 {
		return Loaded{}, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, actualSHA256, expectedSHA256)
	}
	artifact, err := recommendation.ParseKnowledgeCatalogArtifact(raw)
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: %v", ErrInvalidFile, err)
	}
	if artifact.SHA256() != expectedSHA256 {
		return Loaded{}, fmt.Errorf("%w: parsed digest differs from release digest", ErrDigestMismatch)
	}
	if err := artifact.ValidateModelManifest(manifest); err != nil {
		return Loaded{}, fmt.Errorf("%w: %v", ErrModelMismatch, err)
	}
	return Loaded{Artifact: artifact, SHA256: expectedSHA256, Size: before.Size, Mode: mode}, nil
}

func lowercaseSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}
