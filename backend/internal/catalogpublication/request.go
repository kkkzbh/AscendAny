package catalogpublication

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"golang.org/x/sys/unix"
)

const (
	RequestSchema           = configuration.CatalogPublicationRequestSchema
	RequestInputName        = "catalog_publication_request"
	AccessTokenInputName    = "admin_access_token"
	MaximumRequestBytes     = 4096
	MaximumAccessTokenBytes = 8192
	InputFileMode           = os.FileMode(0o400)
)

var compactJWT = regexp.MustCompile(`^[A-Za-z0-9_-]+[.][A-Za-z0-9_-]+[.][A-Za-z0-9_-]+$`)

type Request = configuration.AuthorizedCatalogPublicationRequest

type Inputs struct {
	Request     Request
	AccessToken string
}

func CanonicalRequest(request Request) ([]byte, error) {
	return configuration.CanonicalCatalogPublicationRequest(request)
}

func ParseRequest(raw []byte) (Request, error) {
	return configuration.ParseCatalogPublicationRequest(raw)
}

// ReadInputs reads exactly the two publisher-owned systemd credential paths.
// Other credentials in the shared CREDENTIALS_DIRECTORY are intentionally
// outside this boundary and are never enumerated.
func ReadInputs(requestPath, accessTokenPath string) (Inputs, error) {
	if !validInputPath(requestPath) || !validInputPath(accessTokenPath) || requestPath == accessTokenPath {
		return Inputs{}, errors.New("catalog publication input paths must be distinct, absolute, and normalized")
	}
	requestRaw, err := readExactInput(requestPath, RequestInputName, MaximumRequestBytes)
	if err != nil {
		return Inputs{}, err
	}
	request, err := ParseRequest(requestRaw)
	if err != nil {
		return Inputs{}, fmt.Errorf("catalog publication request rejected: %w", err)
	}
	tokenRaw, err := readExactInput(accessTokenPath, AccessTokenInputName, MaximumAccessTokenBytes)
	if err != nil {
		return Inputs{}, err
	}
	if !compactJWT.Match(tokenRaw) {
		return Inputs{}, errors.New("administrator access token is not one compact JWT")
	}
	return Inputs{Request: request, AccessToken: string(tokenRaw)}, nil
}

func validInputPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Base(path) != "."
}

func readExactInput(path, name string, maximumBytes int64) ([]byte, error) {
	var pathStat unix.Stat_t
	if err := unix.Lstat(path, &pathStat); err != nil || !validInputStat(pathStat, maximumBytes) {
		return nil, fmt.Errorf("%s input must be one owned 0400 regular file", name)
	}
	fileDescriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s input: %w", name, err)
	}
	file := os.NewFile(uintptr(fileDescriptor), name)
	if file == nil {
		_ = unix.Close(fileDescriptor)
		return nil, fmt.Errorf("construct %s input handle", name)
	}
	defer file.Close()
	var openedStat unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &openedStat); err != nil || !sameInputStat(pathStat, openedStat) {
		return nil, fmt.Errorf("%s input changed before reading", name)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || int64(len(raw)) != openedStat.Size {
		return nil, fmt.Errorf("%s input cannot be read exactly", name)
	}
	var afterStat unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &afterStat); err != nil || !sameInputStat(openedStat, afterStat) {
		return nil, fmt.Errorf("%s input changed while reading", name)
	}
	return raw, nil
}

func validInputStat(stat unix.Stat_t, maximumBytes int64) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && os.FileMode(stat.Mode&0o777) == InputFileMode &&
		stat.Nlink == 1 && stat.Uid == uint32(os.Geteuid()) && stat.Size >= 1 && stat.Size <= maximumBytes
}

func sameInputStat(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode &&
		left.Nlink == right.Nlink && left.Uid == right.Uid && left.Gid == right.Gid && left.Size == right.Size &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim
}
