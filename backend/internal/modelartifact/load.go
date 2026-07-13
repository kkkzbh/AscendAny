// Package modelartifact owns secure loading of the release-bound inference
// model file. Mathematical and JSON contract validation remains in
// inferencemodel.
package modelartifact

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

var (
	ErrInvalidPath    = errors.New("invalid inference model path")
	ErrInvalidFile    = errors.New("invalid inference model file")
	ErrDigestMismatch = errors.New("inference model digest mismatch")
)

const RequiredMode os.FileMode = 0o644

type Loaded struct {
	Model  *inferencemodel.Model
	SHA256 string
	Size   int64
	Mode   os.FileMode
}

// Load opens the final path without following a leaf symlink, validates its
// immutable release-file shape, parses the complete model, and binds it to the
// configured SHA-256 trust anchor.
func Load(path, expectedSHA256 string) (Loaded, error) {
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
		return Loaded{}, fmt.Errorf("%w: model must be a regular file", ErrInvalidFile)
	}
	mode := os.FileMode(before.Mode & 0o777)
	if mode != RequiredMode {
		return Loaded{}, fmt.Errorf("%w: model mode is %04o, require %04o", ErrInvalidFile, mode, RequiredMode)
	}
	if before.Nlink != 1 {
		return Loaded{}, fmt.Errorf("%w: model must have exactly one hard link", ErrInvalidFile)
	}
	if before.Size < 1 || before.Size > inferencemodel.MaximumArtifactBytes {
		return Loaded{}, fmt.Errorf("%w: model size %d is outside 1..%d", ErrInvalidFile, before.Size, inferencemodel.MaximumArtifactBytes)
	}

	raw, err := io.ReadAll(io.LimitReader(file, int64(inferencemodel.MaximumArtifactBytes)+1))
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: read: %v", ErrInvalidFile, err)
	}
	if int64(len(raw)) != before.Size {
		return Loaded{}, fmt.Errorf("%w: model size changed while reading", ErrInvalidFile)
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return Loaded{}, fmt.Errorf("%w: restat: %v", ErrInvalidFile, err)
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size ||
		before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return Loaded{}, fmt.Errorf("%w: model changed while reading", ErrInvalidFile)
	}

	model, err := inferencemodel.Parse(raw)
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: %v", ErrInvalidFile, err)
	}
	if model.SHA256() != expectedSHA256 {
		return Loaded{}, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, model.SHA256(), expectedSHA256)
	}
	return Loaded{Model: model, SHA256: expectedSHA256, Size: before.Size, Mode: mode}, nil
}

func lowercaseSHA256(value string) bool {
	if len(value) != 64 {
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
