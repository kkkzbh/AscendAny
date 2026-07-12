package traineragent

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"golang.org/x/sys/unix"
)

const (
	AcceptanceCandidateSchemaV3          = "ascendany.trainer.acceptance.v3"
	MaximumAcceptanceCandidateBytes      = 64 << 10
	MaximumAcceptanceReleaseVersionBytes = 128
)

var (
	canonicalSemVerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))([.]((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)))*)?([+][0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$`)
	releaseCommitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	orphanSuffixPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type ReleaseIdentity struct {
	Version string
	Commit  string
}

type AcceptanceCandidateConfig struct {
	Path    string
	Release ReleaseIdentity
	AgentID string
	Origin  string
}

type AcceptanceCandidateRecorder interface {
	Record(WorkerOutcome) error
}

type AcceptanceCandidateWriter struct {
	mutex  sync.Mutex
	config AcceptanceCandidateConfig
	sealed bool
}

type AcceptanceCandidate struct {
	Schema                    string `json:"schema"`
	ReleaseVersion            string `json:"releaseVersion"`
	ReleaseCommit             string `json:"releaseCommit"`
	AgentID                   string `json:"agentId"`
	Origin                    string `json:"origin"`
	RunID                     string `json:"runId"`
	AttemptToken              string `json:"attemptToken"`
	RequestSHA256             string `json:"requestSHA256"`
	InputManifestSHA256       string `json:"inputManifestSHA256"`
	OutputBundleSHA256        string `json:"outputBundleSHA256"`
	RuntimeConstructionSHA256 string `json:"runtimeConstructionSha256"`
	RuntimeProvenanceSHA256   string `json:"runtimeProvenanceSha256"`
	RuntimeTreeSHA256         string `json:"runtimeTreeSha256"`
	HostCapabilitySHA256      string `json:"hostCapabilitySha256"`
	RuntimeAttestationSHA256  string `json:"runtimeAttestationSha256"`
	ModelID                   string `json:"modelId"`
	Disposition               string `json:"disposition"`
	ClaimAt                   string `json:"claimAt"`
	HeartbeatAt               string `json:"heartbeatAt"`
	UploadAt                  string `json:"uploadAt"`
}

func NewAcceptanceCandidateWriter(config AcceptanceCandidateConfig) (_ *AcceptanceCandidateWriter, resultErr error) {
	if err := validateAcceptanceCandidateConfig(config); err != nil {
		return nil, err
	}
	directory, base, err := openAcceptanceParent(config.Path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := unix.Close(directory); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close trainer acceptance parent: %w", closeErr)
		}
	}()
	if err := reconcileAcceptanceDirectory(directory, base); err != nil {
		return nil, err
	}
	state, err := inspectAcceptanceEntry(directory, base)
	if err != nil {
		return nil, err
	}
	writer := &AcceptanceCandidateWriter{config: config}
	if !state.exists {
		return writer, nil
	}
	raw, err := readAcceptanceEntry(directory, base, state)
	if err != nil {
		return nil, err
	}
	current, err := inspectAcceptanceEntry(directory, base)
	if err != nil || current != state {
		return nil, errors.New("existing trainer acceptance candidate changed while it was verified")
	}
	candidate, err := VerifyAcceptanceCandidate(raw)
	if err != nil {
		return nil, fmt.Errorf("verify existing trainer acceptance candidate: %w", err)
	}
	if candidate.ReleaseVersion != config.Release.Version || candidate.ReleaseCommit != config.Release.Commit ||
		candidate.AgentID != config.AgentID || candidate.Origin != config.Origin {
		return nil, errors.New("existing trainer acceptance candidate belongs to a different release, agent, or origin")
	}
	writer.sealed = true
	return writer, nil
}

func (writer *AcceptanceCandidateWriter) Record(outcome WorkerOutcome) error {
	if writer == nil {
		return errors.New("trainer acceptance candidate writer is required")
	}
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.sealed {
		return nil
	}
	document, err := writer.document(outcome)
	if err != nil {
		return err
	}
	canonical, err := canonicalAcceptanceCandidate(document)
	if err != nil {
		return err
	}
	if err := writeAcceptanceCandidateOnce(writer.config.Path, canonical); err != nil {
		return err
	}
	writer.sealed = true
	return nil
}

func VerifyAcceptanceCandidateReader(reader io.Reader) (AcceptanceCandidate, error) {
	if reader == nil {
		return AcceptanceCandidate{}, errors.New("trainer acceptance candidate reader is required")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaximumAcceptanceCandidateBytes+1))
	if err != nil {
		return AcceptanceCandidate{}, fmt.Errorf("read trainer acceptance candidate: %w", err)
	}
	if len(raw) > MaximumAcceptanceCandidateBytes {
		return AcceptanceCandidate{}, fmt.Errorf("trainer acceptance candidate exceeds %d bytes", MaximumAcceptanceCandidateBytes)
	}
	return VerifyAcceptanceCandidate(raw)
}

func VerifyAcceptanceCandidate(raw []byte) (AcceptanceCandidate, error) {
	if len(raw) == 0 || len(raw) > MaximumAcceptanceCandidateBytes {
		return AcceptanceCandidate{}, fmt.Errorf("trainer acceptance candidate must contain 1 to %d bytes", MaximumAcceptanceCandidateBytes)
	}
	canonical, _, err := canonicaljson.Object(raw, MaximumAcceptanceCandidateBytes)
	if err != nil || !bytes.Equal(canonical, raw) {
		return AcceptanceCandidate{}, errors.New("trainer acceptance candidate must be one exact canonical JSON object")
	}
	var candidate AcceptanceCandidate
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return AcceptanceCandidate{}, fmt.Errorf("decode trainer acceptance candidate: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AcceptanceCandidate{}, errors.New("trainer acceptance candidate must contain exactly one object")
	}
	if err := validateAcceptanceCandidate(candidate); err != nil {
		return AcceptanceCandidate{}, err
	}
	return candidate, nil
}

func (writer *AcceptanceCandidateWriter) document(outcome WorkerOutcome) (AcceptanceCandidate, error) {
	if outcome.ModelID == nil {
		return AcceptanceCandidate{}, errors.New("trainer acceptance candidate model ID is missing")
	}
	document := AcceptanceCandidate{
		Schema: AcceptanceCandidateSchemaV3, ReleaseVersion: writer.config.Release.Version,
		ReleaseCommit: writer.config.Release.Commit, AgentID: writer.config.AgentID, Origin: writer.config.Origin,
		RunID: outcome.RunID, AttemptToken: outcome.AttemptToken, RequestSHA256: outcome.RequestSHA256,
		InputManifestSHA256: outcome.InputManifestSHA256, OutputBundleSHA256: outcome.OutputBundleSHA256,
		RuntimeConstructionSHA256: outcome.RuntimeConstructionSHA256,
		RuntimeProvenanceSHA256:   outcome.RuntimeProvenanceSHA256,
		RuntimeTreeSHA256:         outcome.RuntimeTreeSHA256,
		HostCapabilitySHA256:      outcome.HostCapabilitySHA256,
		RuntimeAttestationSHA256:  outcome.RuntimeAttestationSHA256,
		ModelID:                   *outcome.ModelID, Disposition: string(outcome.Disposition),
		ClaimAt:     outcome.ClaimedAt.UTC().Format(time.RFC3339Nano),
		HeartbeatAt: outcome.HeartbeatAt.UTC().Format(time.RFC3339Nano),
		UploadAt:    outcome.UploadedAt.UTC().Format(time.RFC3339Nano),
	}
	if err := validateAcceptanceCandidate(document); err != nil {
		return AcceptanceCandidate{}, err
	}
	return document, nil
}

func validateAcceptanceCandidate(candidate AcceptanceCandidate) error {
	if candidate.Schema != AcceptanceCandidateSchemaV3 {
		return errors.New("trainer acceptance candidate schema is unsupported")
	}
	if err := validateReleaseIdentity(ReleaseIdentity{Version: candidate.ReleaseVersion, Commit: candidate.ReleaseCommit}); err != nil {
		return err
	}
	if !agentIDPattern.MatchString(candidate.AgentID) {
		return errors.New("trainer acceptance candidate agent ID is invalid")
	}
	if err := validateHTTPOrigin(candidate.Origin); err != nil {
		return fmt.Errorf("trainer acceptance candidate origin: %w", err)
	}
	if !uuidV4Pattern.MatchString(candidate.RunID) || !uuidV4Pattern.MatchString(candidate.AttemptToken) ||
		!uuidV4Pattern.MatchString(candidate.ModelID) || !sha256Pattern.MatchString(candidate.RequestSHA256) ||
		!sha256Pattern.MatchString(candidate.InputManifestSHA256) || !sha256Pattern.MatchString(candidate.OutputBundleSHA256) ||
		!sha256Pattern.MatchString(candidate.RuntimeConstructionSHA256) ||
		!sha256Pattern.MatchString(candidate.RuntimeProvenanceSHA256) ||
		!sha256Pattern.MatchString(candidate.RuntimeTreeSHA256) ||
		!sha256Pattern.MatchString(candidate.HostCapabilitySHA256) ||
		!sha256Pattern.MatchString(candidate.RuntimeAttestationSHA256) {
		return errors.New("trainer acceptance candidate UUID or SHA-256 provenance is invalid")
	}
	if candidate.Disposition != string(WorkerActivated) && candidate.Disposition != string(WorkerSuperseded) {
		return errors.New("trainer acceptance candidate disposition is invalid")
	}
	claimAt, err := parseAcceptanceTimestamp(candidate.ClaimAt)
	if err != nil {
		return err
	}
	heartbeatAt, err := parseAcceptanceTimestamp(candidate.HeartbeatAt)
	if err != nil {
		return err
	}
	uploadAt, err := parseAcceptanceTimestamp(candidate.UploadAt)
	if err != nil {
		return err
	}
	if claimAt.IsZero() || heartbeatAt.IsZero() || uploadAt.IsZero() ||
		claimAt.After(heartbeatAt) || heartbeatAt.After(uploadAt) {
		return errors.New("trainer acceptance candidate timestamps are missing or unordered at nanosecond precision")
	}
	return nil
}

func parseAcceptanceTimestamp(raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != raw {
		return time.Time{}, errors.New("trainer acceptance candidate timestamps must be canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func validateAcceptanceCandidateConfig(config AcceptanceCandidateConfig) error {
	if err := validateAcceptanceCandidatePath(config.Path); err != nil {
		return err
	}
	if err := validateReleaseIdentity(config.Release); err != nil {
		return err
	}
	if !agentIDPattern.MatchString(config.AgentID) {
		return errors.New("trainer acceptance candidate agent ID is invalid")
	}
	if err := validateHTTPOrigin(config.Origin); err != nil {
		return fmt.Errorf("trainer acceptance candidate origin: %w", err)
	}
	return nil
}

func validateReleaseIdentity(release ReleaseIdentity) error {
	if len(release.Version) == 0 || len(release.Version) > MaximumAcceptanceReleaseVersionBytes ||
		!utf8.ValidString(release.Version) || !canonicalSemVerPattern.MatchString(release.Version) ||
		!releaseCommitPattern.MatchString(release.Commit) {
		return errors.New("trainer acceptance candidate requires a canonical release version of at most 128 ASCII bytes and a 40-character commit")
	}
	return nil
}

func validateAcceptanceCandidatePath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) ||
		filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return errors.New("trainer acceptance candidate path must be a normalized absolute file path below filesystem root")
	}
	return nil
}

func canonicalAcceptanceCandidate(document AcceptanceCandidate) ([]byte, error) {
	if err := validateAcceptanceCandidate(document); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode trainer acceptance candidate: %w", err)
	}
	canonical, _, err := canonicaljson.Object(raw, MaximumAcceptanceCandidateBytes)
	if err != nil {
		return nil, fmt.Errorf("canonicalize trainer acceptance candidate: %w", err)
	}
	return canonical, nil
}

type acceptanceEntryState struct {
	exists bool
	device uint64
	inode  uint64
}

func writeAcceptanceCandidateOnce(path string, content []byte) (resultErr error) {
	directory, base, err := openAcceptanceParent(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := unix.Close(directory); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close trainer acceptance parent: %w", closeErr)
		}
	}()
	if err := reconcileAcceptanceDirectory(directory, base); err != nil {
		return err
	}
	state, err := inspectAcceptanceEntry(directory, base)
	if err != nil {
		return err
	}
	if state.exists {
		return errors.New("trainer acceptance candidate is already sealed")
	}
	temporary, err := acceptanceTemporaryName(base)
	if err != nil {
		return err
	}
	fileDescriptor, err := unix.Openat(
		directory, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600,
	)
	if err != nil {
		return fmt.Errorf("create trainer acceptance temporary file: %w", err)
	}
	temporaryPresent := true
	defer func() {
		if temporaryPresent {
			_ = unix.Unlinkat(directory, temporary, 0)
		}
	}()
	if err := writeAcceptanceTemporary(fileDescriptor, content); err != nil {
		return err
	}
	temporaryState, err := inspectAcceptanceEntry(directory, temporary)
	if err != nil || !temporaryState.exists {
		return errors.New("trainer acceptance temporary file disappeared before publication")
	}
	state, err = inspectAcceptanceEntry(directory, base)
	if err != nil {
		return err
	}
	if state.exists {
		return errors.New("trainer acceptance candidate appeared before write-once publication")
	}
	if err := unix.Renameat2(directory, temporary, directory, base, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("write-once publish trainer acceptance candidate: %w", err)
	}
	temporaryPresent = false
	publishedState, err := inspectAcceptanceEntry(directory, base)
	if err != nil || publishedState != temporaryState {
		return errors.New("published trainer acceptance candidate differs from its prepared inode")
	}
	if err := unix.Fsync(directory); err != nil {
		return fmt.Errorf("sync trainer acceptance parent: %w", err)
	}
	return nil
}

func openAcceptanceParent(path string) (int, string, error) {
	if err := validateAcceptanceCandidatePath(path); err != nil {
		return -1, "", err
	}
	parent := filepath.Dir(path)
	directory, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", fmt.Errorf("open filesystem root for trainer acceptance candidate: %w", err)
	}
	for _, component := range strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(directory, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		closeErr := unix.Close(directory)
		if openErr != nil {
			return -1, "", fmt.Errorf("open trainer acceptance parent without symlinks: %w", openErr)
		}
		if closeErr != nil {
			_ = unix.Close(next)
			return -1, "", fmt.Errorf("close trainer acceptance parent ancestor: %w", closeErr)
		}
		directory = next
	}
	var metadata unix.Stat_t
	if err := unix.Fstat(directory, &metadata); err != nil {
		_ = unix.Close(directory)
		return -1, "", fmt.Errorf("inspect trainer acceptance parent: %w", err)
	}
	if metadata.Mode&unix.S_IFMT != unix.S_IFDIR || metadata.Uid != uint32(os.Geteuid()) || metadata.Mode&0o022 != 0 {
		_ = unix.Close(directory)
		return -1, "", errors.New("trainer acceptance parent must be an existing runtime-user-owned directory without group or other write access")
	}
	return directory, filepath.Base(path), nil
}

func reconcileAcceptanceDirectory(directory int, base string) error {
	names, err := acceptanceDirectoryEntries(directory)
	if err != nil {
		return err
	}
	removed := false
	for _, name := range names {
		if name == base {
			continue
		}
		if !isAcceptanceOrphanName(base, name) {
			return fmt.Errorf("trainer acceptance parent contains foreign entry %q", name)
		}
		state, inspectErr := inspectAcceptanceEntry(directory, name)
		if inspectErr != nil || !state.exists {
			return fmt.Errorf("trainer acceptance orphan %q violates its file contract", name)
		}
		if err := unix.Unlinkat(directory, name, 0); err != nil {
			return fmt.Errorf("remove trainer acceptance orphan %q: %w", name, err)
		}
		removed = true
	}
	if removed {
		if err := unix.Fsync(directory); err != nil {
			return fmt.Errorf("sync trainer acceptance parent after orphan cleanup: %w", err)
		}
	}
	names, err = acceptanceDirectoryEntries(directory)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != base {
			return fmt.Errorf("trainer acceptance parent contains entry %q after reconciliation", name)
		}
	}
	return nil
}

func acceptanceDirectoryEntries(directory int) ([]string, error) {
	duplicate, err := unix.Openat(directory, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open trainer acceptance parent for enumeration: %w", err)
	}
	file := os.NewFile(uintptr(duplicate), "trainer-acceptance-parent")
	if file == nil {
		_ = unix.Close(duplicate)
		return nil, errors.New("create trainer acceptance parent directory handle")
	}
	names, readErr := file.Readdirnames(-1)
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("enumerate trainer acceptance parent: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close trainer acceptance parent enumeration: %w", closeErr)
	}
	sort.Strings(names)
	return names, nil
}

func isAcceptanceOrphanName(base, name string) bool {
	prefix := "." + base + ".tmp-"
	return strings.HasPrefix(name, prefix) && orphanSuffixPattern.MatchString(strings.TrimPrefix(name, prefix))
}

func inspectAcceptanceEntry(directory int, base string) (acceptanceEntryState, error) {
	var metadata unix.Stat_t
	err := unix.Fstatat(directory, base, &metadata, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return acceptanceEntryState{}, nil
	}
	if err != nil {
		return acceptanceEntryState{}, fmt.Errorf("inspect trainer acceptance entry: %w", err)
	}
	if err := validateAcceptanceFileMetadata(metadata); err != nil {
		return acceptanceEntryState{}, err
	}
	return acceptanceEntryState{exists: true, device: uint64(metadata.Dev), inode: metadata.Ino}, nil
}

func validateAcceptanceFileMetadata(metadata unix.Stat_t) error {
	if metadata.Mode&unix.S_IFMT != unix.S_IFREG || metadata.Nlink != 1 || metadata.Uid != uint32(os.Geteuid()) ||
		metadata.Mode&0o7777 != 0o600 {
		return errors.New("trainer acceptance entry must be a runtime-user-owned single-link regular file with mode 0600")
	}
	return nil
}

func readAcceptanceEntry(directory int, base string, expected acceptanceEntryState) ([]byte, error) {
	fileDescriptor, err := unix.Openat(directory, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open existing trainer acceptance candidate: %w", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), base)
	if file == nil {
		_ = unix.Close(fileDescriptor)
		return nil, errors.New("create existing trainer acceptance candidate handle")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, MaximumAcceptanceCandidateBytes+1))
	var metadata unix.Stat_t
	statErr := unix.Fstat(fileDescriptor, &metadata)
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read existing trainer acceptance candidate: %w", readErr)
	}
	if statErr != nil {
		return nil, fmt.Errorf("inspect open trainer acceptance candidate: %w", statErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close existing trainer acceptance candidate: %w", closeErr)
	}
	if err := validateAcceptanceFileMetadata(metadata); err != nil {
		return nil, err
	}
	actual := acceptanceEntryState{exists: true, device: uint64(metadata.Dev), inode: metadata.Ino}
	if actual != expected {
		return nil, errors.New("existing trainer acceptance candidate changed while opening")
	}
	if len(raw) > MaximumAcceptanceCandidateBytes {
		return nil, fmt.Errorf("existing trainer acceptance candidate exceeds %d bytes", MaximumAcceptanceCandidateBytes)
	}
	return raw, nil
}

func acceptanceTemporaryName(base string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate trainer acceptance temporary name: %w", err)
	}
	return "." + base + ".tmp-" + hex.EncodeToString(random), nil
}

func writeAcceptanceTemporary(fileDescriptor int, content []byte) (resultErr error) {
	defer func() {
		if closeErr := unix.Close(fileDescriptor); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close trainer acceptance temporary file: %w", closeErr)
		}
	}()
	if err := unix.Fchmod(fileDescriptor, 0o600); err != nil {
		return fmt.Errorf("set trainer acceptance temporary mode: %w", err)
	}
	var metadata unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &metadata); err != nil {
		return fmt.Errorf("inspect trainer acceptance temporary file: %w", err)
	}
	if err := validateAcceptanceFileMetadata(metadata); err != nil {
		return err
	}
	remaining := content
	for len(remaining) > 0 {
		written, err := unix.Write(fileDescriptor, remaining)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("write trainer acceptance temporary file: %w", err)
		}
		if written <= 0 {
			return errors.New("write trainer acceptance temporary file made no progress")
		}
		remaining = remaining[written:]
	}
	if err := unix.Fsync(fileDescriptor); err != nil {
		return fmt.Errorf("sync trainer acceptance temporary file: %w", err)
	}
	return nil
}
