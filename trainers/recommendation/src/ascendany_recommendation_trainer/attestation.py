"""Fail-closed provenance attestation for the isolated production runtime."""

from __future__ import annotations

import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import re
import socket
import stat
import sys
import sysconfig
from typing import NoReturn
import urllib.parse

import torch

from .contract import TrainerRuntimeError, canonical_json


ATTESTATION_SCHEMA = "ascendany.trainer-runtime.attestation.v1"
MARKER_SCHEMA = "ascendany.trainer-runtime.provenance.v3"
HOST_CAPABILITY_SCHEMA = "ascendany.trainer-host-capabilities.v2"
TREE_ALGORITHM = "ascendany.portable-python-tree.v1"
RUNTIME_MARKER = ".ascendany-runtime-provenance.json"
CONSTRUCTION_INPUTS = ".ascendany-construction-inputs"
EXPECTED_RUNTIME_ROOT = "/opt/ascendany-trainer-runtime/current"
EXPECTED_PYTHON_VERSION = "3.14.6"
EXPECTED_TORCH_VERSION = "2.13.0+cu130"
EXPECTED_CUDA_VERSION = "13.0"
EXPECTED_UV_VERSION = "uv 0.9.26"
EXPECTED_UV_URL = "https://github.com/astral-sh/uv/releases/download/0.9.26/uv-x86_64-unknown-linux-gnu.tar.gz"
EXPECTED_UV_ARCHIVE_SHA256 = "30ccbf0a66dc8727a02b0e245c583ee970bdafecf3a443c1686e1b30ec4939e8"
EXPECTED_UV_BINARY_SHA256 = "0650696de7f403348e9dd617e1f65dc32147c106c40129138017efd8f0f01cc8"
EXPECTED_RELEASE_MANIFEST_FILE_COUNT = 77
EXPECTED_DEVICE_PATHS = (
    "/dev/nvidia-uvm",
    "/dev/nvidia0",
    "/dev/nvidiactl",
)
SENSITIVE_PATHS = (
    "/etc/ascendany",
    "/opt/ascendany/Release",
    "/run/credentials",
    "/usr",
    "/var/lib/ascendany",
)
RUNTIME_ENVIRONMENT_KEYS = frozenset(
    {
        "ASCENDANY_TRAINER_EXPECTED_HOST_CAPABILITY_SHA256",
        "ASCENDANY_TRAINER_EXPECTED_RUNTIME_CONSTRUCTION_SHA256",
        "ASCENDANY_TRAINER_EXPECTED_RUNTIME_PROVENANCE_SHA256",
        "ASCENDANY_TRAINER_EXPECTED_RUNTIME_TREE_SHA256",
        "ASCENDANY_TRAINER_RUNTIME_ROOT",
    }
)
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
MOUNT_ESCAPE_PATTERN = re.compile(r"\\([0-7]{3})")


def _fail(message: str) -> NoReturn:
    raise TrainerRuntimeError(message)


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _read_stable_file(path: Path) -> tuple[bytes, os.stat_result]:
    try:
        before = path.stat(follow_symlinks=False)
        if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1:
            _fail(f"attested file is not one regular file: {path}")
        value = path.read_bytes()
        after = path.stat(follow_symlinks=False)
    except OSError as error:
        raise TrainerRuntimeError(f"attested file cannot be read: {path}") from error
    if _stable_identity(before) != _stable_identity(after):
        _fail(f"attested file changed while it was read: {path}")
    return value, before


def _stable_identity(value: os.stat_result) -> tuple[int, ...]:
    return (
        value.st_dev,
        value.st_ino,
        value.st_mode,
        value.st_nlink,
        value.st_uid,
        value.st_gid,
        value.st_size,
        value.st_mtime_ns,
        value.st_ctime_ns,
    )


def _decode_canonical_object(raw: bytes, label: str) -> dict[str, object]:
    try:
        value = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise TrainerRuntimeError(f"{label} is not canonical JSON") from error
    if not isinstance(value, dict) or canonical_json(value) != raw:
        _fail(f"{label} is not one exact canonical JSON object")
    return value


def _require_exact_keys(value: dict[str, object], keys: set[str], label: str) -> None:
    if set(value) != keys:
        _fail(f"{label} field set differs from its contract")


def _require_sha256(value: object, label: str) -> str:
    if not isinstance(value, str) or SHA256_PATTERN.fullmatch(value) is None:
        _fail(f"{label} is not a lowercase SHA-256")
    return value


def _validate_release_manifest_identity(
    release_manifest: dict[str, object], source: dict[str, object]
) -> None:
    _require_exact_keys(
        release_manifest,
        {"build", "commit", "files", "schema", "sourceDateEpoch", "version"},
        "captured release manifest",
    )
    files = release_manifest.get("files")
    if (
        release_manifest.get("schema") != "ascendany.release.v2"
        or release_manifest.get("commit") != source.get("commit")
        or release_manifest.get("version") != source.get("version")
        or not isinstance(files, list)
        or len(files) != EXPECTED_RELEASE_MANIFEST_FILE_COUNT
    ):
        _fail("captured release manifest identity drifted")


def _mount_targets() -> tuple[str, ...]:
    try:
        raw = Path("/proc/self/mountinfo").read_text(encoding="utf-8")
    except OSError as error:
        raise TrainerRuntimeError("mount namespace cannot be inspected") from error
    targets: list[str] = []
    for line in raw.splitlines():
        fields = line.split()
        if len(fields) < 10 or "-" not in fields:
            _fail("mount namespace contains malformed mountinfo")
        target = MOUNT_ESCAPE_PATTERN.sub(
            lambda match: chr(int(match.group(1), 8)), fields[4]
        )
        if not target.startswith("/") or "\x00" in target:
            _fail("mount namespace contains a noncanonical target")
        targets.append(os.path.normpath(target))
    ordered = tuple(sorted(targets, key=os.fsencode))
    if len(ordered) != len(set(ordered)):
        _fail("mount namespace repeats a target")
    return ordered


def _tree_entries(root: Path) -> tuple[Path, ...]:
    entries: list[Path] = []

    def visit(directory: Path) -> None:
        try:
            children = list(os.scandir(directory))
        except OSError as error:
            raise TrainerRuntimeError("portable Python tree cannot be enumerated") from error
        for child in children:
            path = Path(child.path)
            entries.append(path)
            if child.is_dir(follow_symlinks=False):
                visit(path)

    visit(root)
    return tuple(sorted(entries, key=lambda path: os.fsencode(str(path))))


def _mode(value: int) -> str:
    return format(stat.S_IMODE(value), "o")


def _portable_python_tree_identity(root: Path) -> dict[str, object]:
    try:
        root = root.resolve(strict=True)
        root_before = root.stat()
    except OSError as error:
        raise TrainerRuntimeError("portable Python tree root is unavailable") from error
    if (
        not stat.S_ISDIR(root_before.st_mode)
        or root_before.st_uid != 0
        or root_before.st_gid != 0
        or stat.S_IMODE(root_before.st_mode) & 0o022
    ):
        _fail("portable Python tree root has unsafe metadata")
    root_device = root_before.st_dev
    for target in _mount_targets():
        if target != str(root) and target.startswith(str(root) + os.sep):
            _fail("portable Python tree contains a descendant mount")

    entries_before = _tree_entries(root)
    records = bytearray(TREE_ALGORITHM.encode("ascii") + b"\x00")
    records.extend(b"D\x00.\x00" + _mode(root_before.st_mode).encode("ascii") + b"\x00")
    directories = 1
    files = 0
    symlinks = 0
    for path in entries_before:
        relative = str(path.relative_to(root))
        if not relative or "\n" in relative or "\r" in relative:
            _fail("portable Python tree contains a noncanonical path")
        try:
            before = path.lstat()
        except OSError as error:
            raise TrainerRuntimeError("portable Python tree entry cannot be read") from error
        if before.st_dev != root_device or before.st_uid != 0 or before.st_gid != 0:
            _fail(f"portable Python tree entry crosses its filesystem: {relative}")
        encoded_relative = relative.encode("utf-8")
        if stat.S_ISLNK(before.st_mode):
            try:
                target = os.readlink(path)
                resolved = path.resolve(strict=True)
                after = path.lstat()
            except OSError as error:
                raise TrainerRuntimeError("portable Python symbolic link is invalid") from error
            if (
                stat.S_IMODE(before.st_mode) != 0o777
                or before.st_nlink != 1
                or not target
                or os.path.isabs(target)
                or "\n" in target
                or "\r" in target
                or len(os.fsencode(target)) != before.st_size
                or not resolved.is_relative_to(root)
                or _stable_identity(before) != _stable_identity(after)
            ):
                _fail(f"portable Python symbolic link violates its contract: {relative}")
            records.extend(
                b"L\x00"
                + encoded_relative
                + b"\x00"
                + _mode(before.st_mode).encode("ascii")
                + b"\x00"
                + os.fsencode(target)
                + b"\x00"
            )
            symlinks += 1
        elif stat.S_ISDIR(before.st_mode):
            after = path.stat()
            if (
                before.st_nlink < 1
                or stat.S_IMODE(before.st_mode) & 0o022
                or _stable_identity(before) != _stable_identity(after)
            ):
                _fail(f"portable Python directory violates its contract: {relative}")
            records.extend(
                b"D\x00"
                + encoded_relative
                + b"\x00"
                + _mode(before.st_mode).encode("ascii")
                + b"\x00"
            )
            directories += 1
        elif stat.S_ISREG(before.st_mode):
            if before.st_nlink != 1 or stat.S_IMODE(before.st_mode) & 0o022:
                _fail(f"portable Python file violates its contract: {relative}")
            contents, after = _read_stable_file(path)
            if _stable_identity(before) != _stable_identity(after):
                _fail(f"portable Python file changed during attestation: {relative}")
            records.extend(
                b"F\x00"
                + encoded_relative
                + b"\x00"
                + _mode(before.st_mode).encode("ascii")
                + b"\x00"
                + str(before.st_size).encode("ascii")
                + b"\x00"
                + _sha256_bytes(contents).encode("ascii")
                + b"\x00"
            )
            files += 1
        else:
            _fail(f"portable Python tree contains a special node: {relative}")
    if entries_before != _tree_entries(root) or _stable_identity(root_before) != _stable_identity(root.stat()):
        _fail("portable Python tree changed during attestation")
    if directories <= 0 or files <= 0 or symlinks <= 0:
        _fail("portable Python tree identity is empty")
    return {
        "algorithm": TREE_ALGORITHM,
        "directories": directories,
        "files": files,
        "sha256": _sha256_bytes(bytes(records)),
        "symlinks": symlinks,
    }


def _validate_source_package(runtime_root: Path, release_manifest: dict[str, object]) -> None:
    package = Path("/trainer/recommendation/ascendany_recommendation_trainer")
    expected_names = {
        "__init__.py",
        "__main__.py",
        "attestation.py",
        "cli.py",
        "contract.py",
        "model.py",
        "train.py",
    }
    try:
        entries = tuple(sorted(package.iterdir(), key=lambda path: os.fsencode(path.name)))
    except OSError as error:
        raise TrainerRuntimeError("trainer source package cannot be enumerated") from error
    if {path.name for path in entries} != expected_names:
        _fail("trainer source package differs from its closed file set")
    files = release_manifest.get("files")
    if not isinstance(files, list):
        _fail("captured release manifest has no file set")
    by_path: dict[str, dict[str, object]] = {}
    for item in files:
        if (
            not isinstance(item, dict)
            or set(item) != {"mode", "path", "sha256", "size"}
            or not isinstance(item.get("path"), str)
            or item["path"] in by_path
        ):
            _fail("captured release manifest file entry is invalid")
        by_path[item["path"]] = item
    for path in entries:
        relative = f"trainers/recommendation/ascendany_recommendation_trainer/{path.name}"
        expected = by_path.get(relative)
        if expected is None or set(expected) != {"mode", "path", "sha256", "size"}:
            _fail(f"captured release manifest does not bind trainer source {path.name}")
        contents, metadata = _read_stable_file(path)
        if (
            expected["mode"] != "0644"
            or expected["size"] != metadata.st_size
            or expected["sha256"] != _sha256_bytes(contents)
            or metadata.st_uid != 0
            or metadata.st_gid != 0
            or stat.S_IMODE(metadata.st_mode) != 0o644
        ):
            _fail(f"trainer source differs from its release manifest: {path.name}")
    if runtime_root != Path(EXPECTED_RUNTIME_ROOT):
        _fail("trainer source attestation received an unexpected runtime root")


def _installed_closure() -> bytes:
    distributions: list[dict[str, str]] = []
    for distribution in importlib.metadata.distributions():
        name = distribution.metadata.get("Name")
        if not name:
            _fail("installed distribution has no name")
        canonical_name = re.sub(r"[-_.]+", "-", name).lower()
        distributions.append({"name": canonical_name, "version": distribution.version})
    distributions.sort(key=lambda item: item["name"])
    if len(distributions) != 30 or len({item["name"] for item in distributions}) != 30:
        _fail("installed distribution closure is not exact")
    return canonical_json(
        {
            "distributions": distributions,
            "schema": "ascendany.trainer-runtime.closure.v1",
        }
    )


def _validate_wheel_contract(
    lock_raw: bytes, closure_raw: bytes, wheels_raw: bytes
) -> None:
    try:
        lock_lines = lock_raw.decode("utf-8").splitlines()
    except UnicodeDecodeError as error:
        raise TrainerRuntimeError("captured runtime lock is not UTF-8") from error
    packages: dict[str, dict[str, object]] = {}
    current: str | None = None
    for raw in lock_lines:
        line = raw.strip()
        if (
            not line
            or line.startswith("#")
            or line.startswith("--index-url")
            or line.startswith("--extra-index-url")
        ):
            continue
        requirement = re.fullmatch(
            r"([A-Za-z0-9][A-Za-z0-9_.-]*)==([^ ;\\]+)\s*\\?", line
        )
        if requirement is not None:
            name = re.sub(r"[-_.]+", "-", requirement.group(1)).lower()
            if name in packages:
                _fail("captured runtime lock repeats a distribution")
            packages[name] = {"hashes": set(), "version": requirement.group(2)}
            current = name
            continue
        digest = re.fullmatch(r"--hash=sha256:([0-9a-f]{64})\s*\\?", line)
        if digest is None or current is None:
            _fail("captured runtime lock contains a detached or invalid hash")
        hashes = packages[current]["hashes"]
        assert isinstance(hashes, set)
        hashes.add(digest.group(1))
    if len(packages) != 30 or any(not value["hashes"] for value in packages.values()):
        _fail("captured runtime lock package or hash set is incomplete")

    closure = _decode_canonical_object(closure_raw.removesuffix(b"\n"), "captured runtime closure")
    expected_closure = {
        "distributions": [
            {"name": name, "version": packages[name]["version"]}
            for name in sorted(packages, key=os.fsencode)
        ],
        "schema": "ascendany.trainer-runtime.closure.v1",
    }
    if closure != expected_closure or canonical_json(closure) + b"\n" != closure_raw:
        _fail("captured runtime closure differs from the hashed lock")

    wheels = _decode_canonical_object(wheels_raw.removesuffix(b"\n"), "captured wheel manifest")
    _require_exact_keys(wheels, {"schema", "wheels"}, "captured wheel manifest")
    entries = wheels.get("wheels")
    if wheels.get("schema") != "ascendany.trainer-runtime.wheels.v1" or not isinstance(entries, list) or len(entries) != 30:
        _fail("captured wheel manifest identity drifted")
    seen_files: set[str] = set()
    seen_urls: set[str] = set()
    previous_name = ""
    allowed_hosts = {
        "download.pytorch.org",
        "download-r2.pytorch.org",
        "files.pythonhosted.org",
        "pypi.nvidia.com",
    }
    for entry in entries:
        if not isinstance(entry, dict) or set(entry) != {"filename", "name", "sha256", "url"}:
            _fail("captured wheel manifest entry shape drifted")
        name = entry.get("name")
        filename = entry.get("filename")
        digest = entry.get("sha256")
        url = entry.get("url")
        if not all(isinstance(value, str) for value in (name, filename, digest, url)):
            _fail("captured wheel manifest entry type drifted")
        assert isinstance(name, str)
        assert isinstance(filename, str)
        assert isinstance(digest, str)
        assert isinstance(url, str)
        if previous_name >= name or name not in packages:
            _fail("captured wheel manifest names are duplicated or unsorted")
        previous_name = name
        if re.fullmatch(
            r"[0-9A-Za-z_.+]+-[0-9A-Za-z.+]+-[0-9A-Za-z_.+-]+\.whl", filename
        ) is None:
            _fail("captured wheel filename is noncanonical")
        filename_parts = filename.split("-", 2)
        hashes = packages[name]["hashes"]
        assert isinstance(hashes, set)
        if (
            re.sub(r"[-_.]+", "-", filename_parts[0]).lower() != name
            or filename_parts[1] != packages[name]["version"]
            or digest not in hashes
        ):
            _fail("captured wheel filename, version, or hash differs from the lock")
        parsed = urllib.parse.urlsplit(url)
        try:
            port = parsed.port
        except ValueError as error:
            raise TrainerRuntimeError("captured wheel URL port is invalid") from error
        if (
            parsed.scheme != "https"
            or parsed.hostname not in allowed_hosts
            or port is not None
            or parsed.username is not None
            or parsed.password is not None
            or parsed.query
            or parsed.fragment
            or urllib.parse.unquote(parsed.path.rsplit("/", 1)[-1]) != filename
            or filename in seen_files
            or url in seen_urls
        ):
            _fail("captured wheel URL differs from the closed origin contract")
        seen_files.add(filename)
        seen_urls.add(url)


def _mapped_host_files(runtime_root: Path) -> list[dict[str, object]]:
    paths: set[Path] = set()
    try:
        mappings = Path("/proc/self/maps").read_text(encoding="utf-8").splitlines()
    except OSError as error:
        raise TrainerRuntimeError("mapped host files cannot be inspected") from error
    for raw in mappings:
        parts = raw.split(maxsplit=5)
        if len(parts) != 6 or not parts[5].startswith("/") or parts[5] == "/dev/zero (deleted)":
            continue
        if parts[5].endswith(" (deleted)"):
            _fail("mapped host regular file was deleted")
        try:
            path = Path(parts[5]).resolve(strict=True)
            mode = path.stat().st_mode
        except OSError as error:
            raise TrainerRuntimeError("mapped host regular file cannot be resolved") from error
        if path.is_relative_to(runtime_root):
            continue
        if stat.S_ISREG(mode):
            paths.add(path)
    items: list[dict[str, object]] = []
    for path in sorted(paths, key=lambda value: os.fsencode(str(value))):
        contents, metadata = _read_stable_file(path)
        if (
            metadata.st_uid != 0
            or metadata.st_gid != 0
            or metadata.st_nlink < 1
            or metadata.st_mode & 0o022
        ):
            _fail("mapped host file is writable outside root")
        items.append(
            {
                "resolvedPath": str(path),
                "sha256": _sha256_bytes(contents),
                "size": metadata.st_size,
            }
        )
    return items


def _validate_marker(runtime_root: Path, environment: dict[str, str]) -> tuple[dict[str, object], str]:
    marker_raw, marker_metadata = _read_stable_file(runtime_root / RUNTIME_MARKER)
    if marker_metadata.st_uid != 0 or marker_metadata.st_gid != 0 or stat.S_IMODE(marker_metadata.st_mode) != 0o644:
        _fail("runtime provenance marker has unsafe metadata")
    marker_sha256 = _sha256_bytes(marker_raw)
    marker = _decode_canonical_object(marker_raw, "runtime provenance marker")
    _require_exact_keys(
        marker,
        {
            "constructionDigest",
            "constructionInputs",
            "hostCapabilities",
            "pythonTree",
            "runtime",
            "schema",
            "sourceRelease",
        },
        "runtime provenance marker",
    )
    if marker.get("schema") != MARKER_SCHEMA:
        _fail("runtime provenance marker schema is unsupported")
    expected_marker = _require_sha256(
        environment.get("ASCENDANY_TRAINER_EXPECTED_RUNTIME_PROVENANCE_SHA256"),
        "expected runtime provenance",
    )
    if marker_sha256 != expected_marker:
        _fail("runtime provenance marker differs from the supervisor identity")
    construction = _require_sha256(marker.get("constructionDigest"), "runtime construction digest")
    if construction != environment.get("ASCENDANY_TRAINER_EXPECTED_RUNTIME_CONSTRUCTION_SHA256"):
        _fail("runtime construction digest differs from the supervisor identity")

    source = marker.get("sourceRelease")
    construction_inputs = marker.get("constructionInputs")
    runtime = marker.get("runtime")
    python_tree = marker.get("pythonTree")
    host = marker.get("hostCapabilities")
    if not all(isinstance(value, dict) for value in (source, construction_inputs, runtime, python_tree, host)):
        _fail("runtime provenance marker contains an invalid object")
    assert isinstance(source, dict)
    assert isinstance(construction_inputs, dict)
    assert isinstance(runtime, dict)
    assert isinstance(python_tree, dict)
    assert isinstance(host, dict)
    _require_exact_keys(
        source,
        {"commit", "manifestPath", "manifestSha256", "version"},
        "runtime source release",
    )
    if (
        not isinstance(source.get("commit"), str)
        or re.fullmatch(r"[0-9a-f]{40}", str(source["commit"])) is None
        or not isinstance(source.get("version"), str)
        or not 0 < len(str(source["version"])) <= 128
    ):
        _fail("runtime source release identity is invalid")
    if runtime != {
        "cudaVersion": EXPECTED_CUDA_VERSION,
        "pythonVersion": EXPECTED_PYTHON_VERSION,
        "torchVersion": EXPECTED_TORCH_VERSION,
        "uv": runtime.get("uv"),
    }:
        _fail("runtime version contract drifted")
    uv = runtime.get("uv")
    if not isinstance(uv, dict) or set(uv) != {
        "archiveSha256",
        "binarySha256",
        "capturedPath",
        "url",
        "version",
    }:
        _fail("uv runtime provenance is invalid")
    if (
        uv.get("archiveSha256") != EXPECTED_UV_ARCHIVE_SHA256
        or uv.get("binarySha256") != EXPECTED_UV_BINARY_SHA256
        or uv.get("capturedPath") != f"{CONSTRUCTION_INPUTS}/uv"
        or uv.get("version") != EXPECTED_UV_VERSION
        or uv.get("url") != EXPECTED_UV_URL
    ):
        _fail("uv artifact contract drifted")
    uv_raw, uv_metadata = _read_stable_file(runtime_root / f"{CONSTRUCTION_INPUTS}/uv")
    if (
        _sha256_bytes(uv_raw) != EXPECTED_UV_BINARY_SHA256
        or uv_metadata.st_uid != 0
        or uv_metadata.st_gid != 0
        or stat.S_IMODE(uv_metadata.st_mode) != 0o755
    ):
        _fail("captured uv binary differs from the official artifact contract")

    release_path = source.get("manifestPath")
    if release_path != f"{CONSTRUCTION_INPUTS}/release-manifest.json":
        _fail("captured release manifest path drifted")
    release_raw, _ = _read_stable_file(runtime_root / str(release_path))
    if _sha256_bytes(release_raw) != _require_sha256(source.get("manifestSha256"), "release manifest digest"):
        _fail("captured release manifest content drifted")
    release_manifest = _decode_canonical_object(release_raw, "captured release manifest")
    _validate_release_manifest_identity(release_manifest, source)
    _validate_source_package(runtime_root, release_manifest)

    required_inputs = {
        "closure": (
            "trainers/recommendation/runtime-closure-cu130.json",
            f"{CONSTRUCTION_INPUTS}/runtime-closure-cu130.json",
        ),
        "hostCapabilityIdentity": (
            "scripts/trainer-host-capability-identity.sh",
            f"{CONSTRUCTION_INPUTS}/trainer-host-capability-identity.sh",
        ),
        "installer": (
            "scripts/install-trainer-runtime.sh",
            f"{CONSTRUCTION_INPUTS}/install-trainer-runtime.sh",
        ),
        "pythonSource": (
            "trainers/recommendation/runtime-python-cu130.json",
            f"{CONSTRUCTION_INPUTS}/runtime-python-cu130.json",
        ),
        "requirements": (
            "trainers/recommendation/runtime-requirements-cu130.lock",
            f"{CONSTRUCTION_INPUTS}/runtime-requirements-cu130.lock",
        ),
        "treeIdentity": (
            "scripts/trainer-runtime-tree-identity.sh",
            f"{CONSTRUCTION_INPUTS}/trainer-runtime-tree-identity.sh",
        ),
        "wheels": (
            "trainers/recommendation/runtime-wheels-cu130.json",
            f"{CONSTRUCTION_INPUTS}/runtime-wheels-cu130.json",
        ),
    }
    if set(construction_inputs) != required_inputs:
        _fail("runtime construction input set drifted")
    captured_values: dict[str, bytes] = {}
    for name in sorted(required_inputs):
        item = construction_inputs.get(name)
        if not isinstance(item, dict) or set(item) != {"capturedPath", "releasePath", "sha256"}:
            _fail(f"runtime construction input {name} is invalid")
        captured_path = item.get("capturedPath")
        release_path, expected_captured_path = required_inputs[name]
        if captured_path != expected_captured_path or item.get("releasePath") != release_path:
            _fail(f"runtime construction input {name} path is invalid")
        raw, _ = _read_stable_file(runtime_root / captured_path)
        if _sha256_bytes(raw) != _require_sha256(item.get("sha256"), f"{name} digest"):
            _fail(f"runtime construction input {name} drifted")
        captured_values[name] = raw

    _require_exact_keys(
        python_tree,
        {"algorithm", "directories", "files", "sha256", "symlinks"},
        "portable Python tree identity",
    )
    if (
        python_tree.get("algorithm") != TREE_ALGORITHM
        or any(
            not isinstance(python_tree.get(field), int)
            or isinstance(python_tree.get(field), bool)
            or int(python_tree[field]) <= 0
            for field in ("directories", "files", "symlinks")
        )
    ):
        _fail("portable Python tree identity is invalid")
    construction_document = {
        "closureSha256": construction_inputs["closure"]["sha256"],
        "hostCapabilityIdentitySha256": construction_inputs["hostCapabilityIdentity"]["sha256"],
        "hostCapabilitySha256": _sha256_bytes(canonical_json(host)),
        "installerSha256": construction_inputs["installer"]["sha256"],
        "pythonSourceSha256": construction_inputs["pythonSource"]["sha256"],
        "releaseManifestSha256": source["manifestSha256"],
        "requirementsSha256": construction_inputs["requirements"]["sha256"],
        "treeIdentitySha256": construction_inputs["treeIdentity"]["sha256"],
        "wheelsSha256": construction_inputs["wheels"]["sha256"],
        "uv": {
            "archiveSha256": uv["archiveSha256"],
            "binarySha256": uv["binarySha256"],
            "url": uv["url"],
            "version": uv["version"],
        },
    }
    if _sha256_bytes(canonical_json(construction_document)) != construction:
        _fail("runtime construction digest does not bind its exact inputs")

    actual_tree = _portable_python_tree_identity(runtime_root / "python")
    if actual_tree != python_tree:
        _fail("portable Python tree identity differs from runtime publication")
    tree_sha256 = _require_sha256(python_tree.get("sha256"), "portable Python tree digest")
    if tree_sha256 != environment.get("ASCENDANY_TRAINER_EXPECTED_RUNTIME_TREE_SHA256"):
        _fail("portable Python tree digest differs from the supervisor identity")
    _validate_wheel_contract(
        captured_values["requirements"],
        captured_values["closure"],
        captured_values["wheels"],
    )
    if _installed_closure() != captured_values["closure"]:
        _fail("installed distribution closure differs from captured provenance")

    host_sha256 = _sha256_bytes(canonical_json(host))
    if host_sha256 != environment.get("ASCENDANY_TRAINER_EXPECTED_HOST_CAPABILITY_SHA256"):
        _fail("host capability digest differs from the supervisor identity")
    return marker, marker_sha256


def attest_runtime(environment: dict[str, str] | None = None) -> dict[str, str]:
    values = dict(os.environ) if environment is None else environment
    if not RUNTIME_ENVIRONMENT_KEYS.issubset(values):
        _fail("runtime attestation environment is incomplete")
    runtime_root_text = values.get("ASCENDANY_TRAINER_RUNTIME_ROOT")
    if runtime_root_text != EXPECTED_RUNTIME_ROOT:
        _fail("runtime root differs from the production selector")
    runtime_root = Path(runtime_root_text)
    try:
        executable = Path(sys.executable).resolve(strict=True)
        prefix = Path(sys.prefix).resolve(strict=True)
        base_prefix = Path(sys.base_prefix).resolve(strict=True)
        expected_executable = (runtime_root / "python/bin/python3.14").resolve(strict=True)
        expected_prefix = (runtime_root / "python").resolve(strict=True)
    except OSError as error:
        raise TrainerRuntimeError("runtime interpreter paths cannot be resolved") from error
    if executable != expected_executable or prefix != expected_prefix or base_prefix != expected_prefix:
        _fail("runtime interpreter escaped the selected portable tree")
    for configured_path in (sysconfig.get_path("stdlib"), sysconfig.get_path("platstdlib")):
        if not Path(configured_path).resolve(strict=True).is_relative_to(expected_prefix):
            _fail("runtime standard library escaped the portable tree")
    if sys.version.split()[0] != EXPECTED_PYTHON_VERSION or not sys.flags.safe_path or sys.flags.hash_randomization:
        _fail("runtime interpreter flags or version drifted")
    if any(Path(path).exists() for path in SENSITIVE_PATHS):
        _fail("trainer namespace exposes a sensitive host path")
    try:
        interfaces = tuple(sorted(name for _, name in socket.if_nameindex()))
    except OSError as error:
        raise TrainerRuntimeError("trainer network namespace cannot be inspected") from error
    if interfaces != ("lo",):
        _fail("trainer network namespace exposes a non-loopback interface")
    actual_devices = tuple(
        sorted(
            (str(path) for path in Path("/dev").glob("nvidia*")),
            key=os.fsencode,
        )
    )
    if actual_devices != EXPECTED_DEVICE_PATHS:
        _fail("trainer namespace exposes an unexpected NVIDIA device set")
    for device in actual_devices:
        metadata = os.stat(device, follow_symlinks=False)
        if not stat.S_ISCHR(metadata.st_mode):
            _fail("trainer NVIDIA device is not one character device")

    marker, marker_sha256 = _validate_marker(runtime_root, values)
    host = marker["hostCapabilities"]
    assert isinstance(host, dict)
    if host.get("schema") != HOST_CAPABILITY_SCHEMA:
        _fail("host capability schema is unsupported")
    expected_mounts = host.get("sandboxMountTargets")
    if not isinstance(expected_mounts, list) or not all(isinstance(value, str) for value in expected_mounts):
        _fail("host capability mount set is invalid")
    actual_mounts = _mount_targets()
    training_mounts = tuple(
        sorted((*expected_mounts, "/output", "/trainer/recommendation"), key=os.fsencode)
    )
    if actual_mounts != training_mounts:
        _fail("trainer mount namespace differs from its published exact set")

    tensor = torch.zeros(1, device="cuda")
    torch.cuda.synchronize()
    if (
        str(torch.__version__) != EXPECTED_TORCH_VERSION
        or torch.version.cuda != EXPECTED_CUDA_VERSION
        or not torch.cuda.is_available()
        or torch.cuda.device_count() != 1
        or str(tensor.device) != "cuda:0"
    ):
        _fail("torch or CUDA runtime capability drifted")
    if _mapped_host_files(runtime_root) != host.get("mappedHostFiles"):
        _fail("mapped host library identity differs from runtime publication")
    driver = host.get("driver")
    if not isinstance(driver, dict) or set(driver) != {"kernelVersionFile", "version"}:
        _fail("host driver capability is invalid")
    version_file = driver.get("kernelVersionFile")
    if not isinstance(version_file, dict) or set(version_file) != {"resolvedPath", "sha256", "size"}:
        _fail("host driver version-file capability is invalid")
    if version_file.get("resolvedPath") != "/sys/module/nvidia/version":
        _fail("host driver version-file path drifted")
    driver_raw, driver_metadata = _read_stable_file(Path("/sys/module/nvidia/version"))
    if (
        version_file.get("sha256") != _sha256_bytes(driver_raw)
        or version_file.get("size") != driver_metadata.st_size
        or driver.get("version") != driver_raw.decode("ascii").strip()
    ):
        _fail("host kernel driver identity differs from runtime publication")

    python_tree = marker["pythonTree"]
    construction_inputs = marker["constructionInputs"]
    assert isinstance(python_tree, dict)
    assert isinstance(construction_inputs, dict)
    closure = construction_inputs["closure"]
    assert isinstance(closure, dict)
    attestation: dict[str, object] = {
        "closureSha256": closure["sha256"],
        "cudaVersion": EXPECTED_CUDA_VERSION,
        "devicePaths": list(EXPECTED_DEVICE_PATHS),
        "hostCapabilitySha256": _sha256_bytes(canonical_json(host)),
        "mountTargets": list(actual_mounts),
        "pythonVersion": EXPECTED_PYTHON_VERSION,
        "runtimeConstructionSha256": marker["constructionDigest"],
        "runtimeProvenanceSha256": marker_sha256,
        "runtimeTreeSha256": python_tree["sha256"],
        "schema": ATTESTATION_SCHEMA,
        "torchVersion": EXPECTED_TORCH_VERSION,
    }
    result = {
        "hostCapabilitySha256": str(attestation["hostCapabilitySha256"]),
        "runtimeAttestationSha256": _sha256_bytes(canonical_json(attestation)),
        "runtimeConstructionSha256": str(attestation["runtimeConstructionSha256"]),
        "runtimeProvenanceSha256": marker_sha256,
        "runtimeTreeSha256": str(attestation["runtimeTreeSha256"]),
    }
    return result
