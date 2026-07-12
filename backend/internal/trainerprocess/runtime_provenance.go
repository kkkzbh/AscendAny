package trainerprocess

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

var runtimeReleaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

const (
	runtimeProvenanceMarkerName = ".ascendany-runtime-provenance.json"
	runtimeProvenanceSchemaV3   = "ascendany.trainer-runtime.provenance.v3"
	runtimeAttestationSchemaV1  = "ascendany.trainer-runtime.attestation.v1"
	hostCapabilitySchemaV2      = "ascendany.trainer-host-capabilities.v2"
	productionPythonVersion     = "3.14.6"
	productionTorchVersion      = "2.13.0+cu130"
	productionCUDAVersion       = "13.0"
	productionUVVersion         = "uv 0.9.26"
	productionUVURL             = "https://github.com/astral-sh/uv/releases/download/0.9.26/uv-x86_64-unknown-linux-gnu.tar.gz"
	productionUVArchiveSHA256   = "30ccbf0a66dc8727a02b0e245c583ee970bdafecf3a443c1686e1b30ec4939e8"
	productionUVBinarySHA256    = "0650696de7f403348e9dd617e1f65dc32147c106c40129138017efd8f0f01cc8"
	maximumRuntimeMarkerBytes   = 1 << 20
)

type runtimeAttestationIdentity struct {
	HostCapabilitySHA256      string `json:"hostCapabilitySha256"`
	RuntimeAttestationSHA256  string `json:"runtimeAttestationSha256"`
	RuntimeConstructionSHA256 string `json:"runtimeConstructionSha256"`
	RuntimeProvenanceSHA256   string `json:"runtimeProvenanceSha256"`
	RuntimeTreeSHA256         string `json:"runtimeTreeSha256"`
}

type runtimeProvenanceMarkerWire struct {
	ConstructionDigest string                        `json:"constructionDigest"`
	ConstructionInputs runtimeConstructionInputsWire `json:"constructionInputs"`
	HostCapabilities   json.RawMessage               `json:"hostCapabilities"`
	PythonTree         runtimePythonTreeWire         `json:"pythonTree"`
	Runtime            runtimeVersionWire            `json:"runtime"`
	Schema             string                        `json:"schema"`
	SourceRelease      runtimeSourceReleaseWire      `json:"sourceRelease"`
}

type runtimeConstructionInputsWire struct {
	Closure                runtimeCapturedInputWire `json:"closure"`
	HostCapabilityIdentity runtimeCapturedInputWire `json:"hostCapabilityIdentity"`
	Installer              runtimeCapturedInputWire `json:"installer"`
	PythonSource           runtimeCapturedInputWire `json:"pythonSource"`
	Requirements           runtimeCapturedInputWire `json:"requirements"`
	TreeIdentity           runtimeCapturedInputWire `json:"treeIdentity"`
	Wheels                 runtimeCapturedInputWire `json:"wheels"`
}

type runtimeCapturedInputWire struct {
	CapturedPath string `json:"capturedPath"`
	ReleasePath  string `json:"releasePath"`
	SHA256       string `json:"sha256"`
}

type runtimePythonTreeWire struct {
	Algorithm   string `json:"algorithm"`
	Directories int64  `json:"directories"`
	Files       int64  `json:"files"`
	SHA256      string `json:"sha256"`
	Symlinks    int64  `json:"symlinks"`
}

type runtimeVersionWire struct {
	CUDAVersion   string         `json:"cudaVersion"`
	PythonVersion string         `json:"pythonVersion"`
	TorchVersion  string         `json:"torchVersion"`
	UV            uvArtifactWire `json:"uv"`
}

type uvArtifactWire struct {
	ArchiveSHA256 string `json:"archiveSha256"`
	BinarySHA256  string `json:"binarySha256"`
	CapturedPath  string `json:"capturedPath"`
	URL           string `json:"url"`
	Version       string `json:"version"`
}

type runtimeSourceReleaseWire struct {
	Commit         string `json:"commit"`
	ManifestPath   string `json:"manifestPath"`
	ManifestSHA256 string `json:"manifestSha256"`
	Version        string `json:"version"`
}

type runtimeConstructionDocument struct {
	ClosureSHA256                string         `json:"closureSha256"`
	HostCapabilityIdentitySHA256 string         `json:"hostCapabilityIdentitySha256"`
	HostCapabilitySHA256         string         `json:"hostCapabilitySha256"`
	InstallerSHA256              string         `json:"installerSha256"`
	PythonSourceSHA256           string         `json:"pythonSourceSha256"`
	ReleaseManifestSHA256        string         `json:"releaseManifestSha256"`
	RequirementsSHA256           string         `json:"requirementsSha256"`
	TreeIdentitySHA256           string         `json:"treeIdentitySha256"`
	WheelsSHA256                 string         `json:"wheelsSha256"`
	UV                           uvConstruction `json:"uv"`
}

type uvConstruction struct {
	ArchiveSHA256 string `json:"archiveSha256"`
	BinarySHA256  string `json:"binarySha256"`
	URL           string `json:"url"`
	Version       string `json:"version"`
}

type hostCapabilityWire struct {
	Driver              json.RawMessage   `json:"driver"`
	MappedHostFiles     []json.RawMessage `json:"mappedHostFiles"`
	SandboxMountTargets []string          `json:"sandboxMountTargets"`
	Schema              string            `json:"schema"`
}

type runtimeAttestationDocument struct {
	ClosureSHA256             string   `json:"closureSha256"`
	CUDAVersion               string   `json:"cudaVersion"`
	DevicePaths               []string `json:"devicePaths"`
	HostCapabilitySHA256      string   `json:"hostCapabilitySha256"`
	MountTargets              []string `json:"mountTargets"`
	PythonVersion             string   `json:"pythonVersion"`
	RuntimeConstructionSHA256 string   `json:"runtimeConstructionSha256"`
	RuntimeProvenanceSHA256   string   `json:"runtimeProvenanceSha256"`
	RuntimeTreeSHA256         string   `json:"runtimeTreeSha256"`
	Schema                    string   `json:"schema"`
	TorchVersion              string   `json:"torchVersion"`
}

func loadRuntimeAttestationIdentity(config SubprocessTrainerConfig) (runtimeAttestationIdentity, string, error) {
	operation := "load isolated trainer runtime provenance"
	selectorInfo, err := os.Lstat(config.RuntimeRoot)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, fmt.Errorf("stat runtime selector: %w", err))
	}
	selectorStat, ok := selectorInfo.Sys().(*syscall.Stat_t)
	if !ok || selectorInfo.Mode()&os.ModeSymlink == 0 || selectorStat.Uid != 0 || selectorStat.Gid != 0 {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime selector must be one root-owned symbolic link"))
	}
	selectorTarget, err := os.Readlink(config.RuntimeRoot)
	if err != nil || selectorTarget == "" || filepath.IsAbs(selectorTarget) || filepath.Clean(selectorTarget) != selectorTarget || filepath.Base(selectorTarget) != selectorTarget {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime selector target must be one normalized relative construction name"))
	}
	resolvedRoot, err := filepath.EvalSymlinks(config.RuntimeRoot)
	if err != nil || !filepath.IsAbs(resolvedRoot) || filepath.Clean(resolvedRoot) != resolvedRoot || filepath.Dir(resolvedRoot) != filepath.Dir(config.RuntimeRoot) {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime selector cannot be resolved to one immutable root"))
	}
	rootInfo, err := os.Lstat(resolvedRoot)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, fmt.Errorf("stat immutable runtime root: %w", err))
	}
	rootStat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o755 || rootStat.Uid != 0 || rootStat.Gid != 0 {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("immutable runtime root must be one root-owned mode-0755 directory"))
	}
	markerPath := filepath.Join(resolvedRoot, runtimeProvenanceMarkerName)
	before, err := os.Lstat(markerPath)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, fmt.Errorf("stat runtime provenance marker: %w", err))
	}
	statValue, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || before.Mode().Perm() != 0o644 || statValue.Uid != 0 || statValue.Gid != 0 || statValue.Nlink != 1 || before.Size() <= 0 || before.Size() > maximumRuntimeMarkerBytes {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime provenance marker has unsafe metadata"))
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, fmt.Errorf("read runtime provenance marker: %w", err))
	}
	after, err := os.Lstat(markerPath)
	if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime provenance marker changed while it was read"))
	}
	canonical, markerSHA256, err := canonicaljson.Object(raw, maximumRuntimeMarkerBytes)
	if err != nil || !bytes.Equal(canonical, raw) {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime provenance marker must be one exact canonical JSON object"))
	}
	var marker runtimeProvenanceMarkerWire
	if err := decodeRuntimeProvenanceStrict(raw, &marker); err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
	}
	expectedConstructionName := "torch-2.13.0-cu130-" + marker.ConstructionDigest
	if marker.Schema != runtimeProvenanceSchemaV3 || !lowercaseSHA256Pattern.MatchString(marker.ConstructionDigest) ||
		selectorTarget != expectedConstructionName || filepath.Base(resolvedRoot) != expectedConstructionName ||
		marker.Runtime.CUDAVersion != productionCUDAVersion || marker.Runtime.PythonVersion != productionPythonVersion ||
		marker.Runtime.TorchVersion != productionTorchVersion || marker.Runtime.UV != (uvArtifactWire{
		ArchiveSHA256: productionUVArchiveSHA256,
		BinarySHA256:  productionUVBinarySHA256,
		CapturedPath:  ".ascendany-construction-inputs/uv",
		URL:           productionUVURL,
		Version:       productionUVVersion,
	}) || marker.PythonTree.Algorithm != "ascendany.portable-python-tree.v1" ||
		marker.PythonTree.Directories <= 0 || marker.PythonTree.Files <= 0 || marker.PythonTree.Symlinks <= 0 ||
		!lowercaseSHA256Pattern.MatchString(marker.PythonTree.SHA256) ||
		!runtimeReleaseCommitPattern.MatchString(marker.SourceRelease.Commit) || marker.SourceRelease.ManifestPath != ".ascendany-construction-inputs/release-manifest.json" ||
		!lowercaseSHA256Pattern.MatchString(marker.SourceRelease.ManifestSHA256) || !validRuntimeReleaseVersion(marker.SourceRelease.Version) {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime provenance marker differs from the production contract"))
	}
	expectedInputs := []struct {
		input        runtimeCapturedInputWire
		releasePath  string
		capturedPath string
		mode         os.FileMode
	}{
		{marker.ConstructionInputs.Closure, "trainers/recommendation/runtime-closure-cu130.json", ".ascendany-construction-inputs/runtime-closure-cu130.json", 0o644},
		{marker.ConstructionInputs.HostCapabilityIdentity, "scripts/trainer-host-capability-identity.sh", ".ascendany-construction-inputs/trainer-host-capability-identity.sh", 0o755},
		{marker.ConstructionInputs.Installer, "scripts/install-trainer-runtime.sh", ".ascendany-construction-inputs/install-trainer-runtime.sh", 0o755},
		{marker.ConstructionInputs.PythonSource, "trainers/recommendation/runtime-python-cu130.json", ".ascendany-construction-inputs/runtime-python-cu130.json", 0o644},
		{marker.ConstructionInputs.Requirements, "trainers/recommendation/runtime-requirements-cu130.lock", ".ascendany-construction-inputs/runtime-requirements-cu130.lock", 0o644},
		{marker.ConstructionInputs.TreeIdentity, "scripts/trainer-runtime-tree-identity.sh", ".ascendany-construction-inputs/trainer-runtime-tree-identity.sh", 0o755},
		{marker.ConstructionInputs.Wheels, "trainers/recommendation/runtime-wheels-cu130.json", ".ascendany-construction-inputs/runtime-wheels-cu130.json", 0o644},
	}
	for _, expected := range expectedInputs {
		if expected.input.ReleasePath != expected.releasePath || expected.input.CapturedPath != expected.capturedPath || !lowercaseSHA256Pattern.MatchString(expected.input.SHA256) {
			return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime captured input identity is invalid"))
		}
		if err := verifyRuntimeCapturedFile(resolvedRoot, expected.capturedPath, expected.input.SHA256, expected.mode); err != nil {
			return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
		}
	}
	if err := verifyRuntimeCapturedFile(resolvedRoot, marker.SourceRelease.ManifestPath, marker.SourceRelease.ManifestSHA256, 0o644); err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
	}
	if err := verifyRuntimeCapturedFile(resolvedRoot, marker.Runtime.UV.CapturedPath, marker.Runtime.UV.BinarySHA256, 0o755); err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
	}
	hostCanonical, hostSHA256, err := canonicaljson.Object(marker.HostCapabilities, maximumRuntimeMarkerBytes)
	if err != nil || !bytes.Equal(hostCanonical, marker.HostCapabilities) {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("host capability identity is noncanonical"))
	}
	var host hostCapabilityWire
	if err := decodeRuntimeProvenanceStrict(marker.HostCapabilities, &host); err != nil || host.Schema != hostCapabilitySchemaV2 ||
		len(host.Driver) == 0 || len(host.MappedHostFiles) == 0 || len(host.SandboxMountTargets) == 0 ||
		!sort.StringsAreSorted(host.SandboxMountTargets) || len(slices.Compact(slices.Clone(host.SandboxMountTargets))) != len(host.SandboxMountTargets) {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("host capability identity differs from the exact sandbox contract"))
	}
	construction := runtimeConstructionDocument{
		ClosureSHA256:                marker.ConstructionInputs.Closure.SHA256,
		HostCapabilityIdentitySHA256: marker.ConstructionInputs.HostCapabilityIdentity.SHA256,
		HostCapabilitySHA256:         hostSHA256,
		InstallerSHA256:              marker.ConstructionInputs.Installer.SHA256,
		PythonSourceSHA256:           marker.ConstructionInputs.PythonSource.SHA256,
		ReleaseManifestSHA256:        marker.SourceRelease.ManifestSHA256,
		RequirementsSHA256:           marker.ConstructionInputs.Requirements.SHA256,
		TreeIdentitySHA256:           marker.ConstructionInputs.TreeIdentity.SHA256,
		WheelsSHA256:                 marker.ConstructionInputs.Wheels.SHA256,
		UV: uvConstruction{
			ArchiveSHA256: marker.Runtime.UV.ArchiveSHA256,
			BinarySHA256:  marker.Runtime.UV.BinarySHA256,
			URL:           marker.Runtime.UV.URL,
			Version:       marker.Runtime.UV.Version,
		},
	}
	constructionRaw, err := json.Marshal(construction)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
	}
	_, constructionSHA256, err := canonicaljson.Object(constructionRaw, maximumRuntimeMarkerBytes)
	if err != nil || constructionSHA256 != marker.ConstructionDigest {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime construction digest does not bind its exact inputs"))
	}
	mountTargets := append(slices.Clone(host.SandboxMountTargets), sandboxOutputDirectory, sandboxTrainerPackageRoot)
	sort.Strings(mountTargets)
	attestation := runtimeAttestationDocument{
		ClosureSHA256:             marker.ConstructionInputs.Closure.SHA256,
		CUDAVersion:               productionCUDAVersion,
		DevicePaths:               slices.Clone(config.NVIDIADevicePaths),
		HostCapabilitySHA256:      hostSHA256,
		MountTargets:              mountTargets,
		PythonVersion:             productionPythonVersion,
		RuntimeConstructionSHA256: marker.ConstructionDigest,
		RuntimeProvenanceSHA256:   markerSHA256,
		RuntimeTreeSHA256:         marker.PythonTree.SHA256,
		Schema:                    runtimeAttestationSchemaV1,
		TorchVersion:              productionTorchVersion,
	}
	encoded, err := json.Marshal(attestation)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
	}
	_, attestationSHA256, err := canonicaljson.Object(encoded, maximumRuntimeMarkerBytes)
	if err != nil {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, err)
	}
	selectorAfter, readlinkErr := os.Readlink(config.RuntimeRoot)
	resolvedAfter, resolveErr := filepath.EvalSymlinks(config.RuntimeRoot)
	if readlinkErr != nil || resolveErr != nil || selectorAfter != selectorTarget || resolvedAfter != resolvedRoot {
		return runtimeAttestationIdentity{}, "", configurationFailure(operation, errors.New("runtime selector changed while provenance was loaded"))
	}
	return runtimeAttestationIdentity{
		RuntimeConstructionSHA256: marker.ConstructionDigest,
		RuntimeProvenanceSHA256:   markerSHA256,
		RuntimeTreeSHA256:         marker.PythonTree.SHA256,
		HostCapabilitySHA256:      hostSHA256,
		RuntimeAttestationSHA256:  attestationSHA256,
	}, resolvedRoot, nil
}

func validRuntimeReleaseVersion(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func verifyRuntimeCapturedFile(root, relativePath, expectedSHA256 string, expectedMode os.FileMode) error {
	path := filepath.Join(root, relativePath)
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat runtime captured file %q: %w", relativePath, err)
	}
	metadata, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || before.Mode().Perm() != expectedMode || metadata.Uid != 0 || metadata.Gid != 0 || metadata.Nlink != 1 || before.Size() <= 0 || before.Size() > 256<<20 {
		return fmt.Errorf("runtime captured file %q has unsafe metadata", relativePath)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read runtime captured file %q: %w", relativePath, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		return fmt.Errorf("runtime captured file %q changed while it was read", relativePath)
	}
	digest := sha256.Sum256(raw)
	if fmt.Sprintf("%x", digest) != expectedSHA256 {
		return fmt.Errorf("runtime captured file %q differs from its provenance digest", relativePath)
	}
	return nil
}

func decodeRuntimeProvenanceStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode runtime provenance: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("runtime provenance contains trailing JSON")
	}
	return nil
}
