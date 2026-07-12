"""Strict stdin/stdout adapter for the isolated numerical trainer."""

from __future__ import annotations

import os
import stat
import sys

from .attestation import RUNTIME_ENVIRONMENT_KEYS, attest_runtime
from .contract import (
    TrainerInputError,
    TrainerOutputError,
    TrainerRuntimeError,
    canonical_json,
    decode_canonical_object,
    require_sha256,
)
from .train import train


OUTPUT_FILENAME = "output.json"
MAXIMUM_INPUT_LIMIT = 1 << 30
MAXIMUM_DIAGNOSTIC_BYTES = 2048

FIXED_ENVIRONMENT_KEYS = frozenset(
    {
        "ASCENDANY_TRAINER_INPUT_MANIFEST_SHA256",
        "ASCENDANY_TRAINER_MAX_INPUT_BYTES",
        "ASCENDANY_TRAINER_MAX_OUTPUT_BYTES",
        "ASCENDANY_TRAINER_OUTPUT_DIR",
        "HOME",
        "LANG",
        "LC_ALL",
        "PWD",
        "PYTHONHASHSEED",
        "TZ",
    }
) | RUNTIME_ENVIRONMENT_KEYS
COMPUTE_ENVIRONMENT_KEYS = frozenset(
    {
        "CUBLAS_WORKSPACE_CONFIG",
        "CUDA_VISIBLE_DEVICES",
        "MKL_NUM_THREADS",
        "OMP_NUM_THREADS",
        "OPENBLAS_NUM_THREADS",
    }
)


def _require_byte_limit(raw: str, label: str) -> int:
    if not raw.isascii() or not raw.isdecimal() or raw.startswith("0"):
        raise TrainerInputError(f"{label} must be a canonical positive decimal")
    value = int(raw)
    if value <= 0 or value > MAXIMUM_INPUT_LIMIT:
        raise TrainerInputError(f"{label} is outside the supported range")
    return value


def validate_environment(environment: dict[str, str]) -> tuple[str, int, int, str]:
    expected_keys = FIXED_ENVIRONMENT_KEYS | (set(environment) & COMPUTE_ENVIRONMENT_KEYS)
    if set(environment) != expected_keys:
        raise TrainerInputError("process environment contains unexpected or missing variables")
    if (
        environment["HOME"] != "/nonexistent"
        or environment["LANG"] != "C.UTF-8"
        or environment["LC_ALL"] != "C.UTF-8"
    ):
        raise TrainerInputError("locale or home environment is invalid")
    if environment["PYTHONHASHSEED"] != "0" or environment["TZ"] != "UTC":
        raise TrainerInputError("deterministic process environment is invalid")
    manifest_sha256 = require_sha256(
        environment["ASCENDANY_TRAINER_INPUT_MANIFEST_SHA256"],
        "expected input manifest",
    )
    maximum_input_bytes = _require_byte_limit(
        environment["ASCENDANY_TRAINER_MAX_INPUT_BYTES"], "maximum input bytes"
    )
    maximum_output_bytes = _require_byte_limit(
        environment["ASCENDANY_TRAINER_MAX_OUTPUT_BYTES"], "maximum output bytes"
    )
    for key in COMPUTE_ENVIRONMENT_KEYS & set(environment):
        value = environment[key]
        if not value or len(value.encode("utf-8")) > 4096 or "=" in value:
            raise TrainerInputError("compute environment value is invalid")
    if (
        "CUBLAS_WORKSPACE_CONFIG" in environment
        and environment["CUBLAS_WORKSPACE_CONFIG"] != ":4096:8"
    ):
        raise TrainerInputError("CUDA deterministic workspace configuration is invalid")
    if "CUDA_VISIBLE_DEVICES" in environment and environment["CUDA_VISIBLE_DEVICES"] != "0":
        raise TrainerInputError("CUDA visible device configuration is invalid")
    for key in ("MKL_NUM_THREADS", "OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS"):
        if key not in environment:
            continue
        value = environment[key]
        if (
            not value.isascii()
            or not value.isdecimal()
            or value.startswith("0")
            or not 1 <= int(value) <= 256
        ):
            raise TrainerInputError(f"{key} must be a canonical integer from 1 to 256")
    output_directory = environment["ASCENDANY_TRAINER_OUTPUT_DIR"]
    if (
        not os.path.isabs(output_directory)
        or os.path.normpath(output_directory) != output_directory
        or output_directory == os.path.sep
    ):
        raise TrainerInputError("output directory must be a normalized absolute path")
    if environment["PWD"] != output_directory:
        raise TrainerInputError("process working directory differs from the output directory")
    try:
        information = os.stat(output_directory, follow_symlinks=False)
    except OSError as error:
        raise TrainerInputError("output directory is unavailable") from error
    if not stat.S_ISDIR(information.st_mode) or information.st_mode & 0o777 != 0o700:
        raise TrainerInputError("output directory must be a real directory with mode 0700")
    if information.st_uid != os.geteuid():
        raise TrainerInputError("output directory must be owned by the trainer identity")
    return manifest_sha256, maximum_input_bytes, maximum_output_bytes, output_directory


def publish_output(output_directory: str, value: bytes) -> None:
    path = os.path.join(output_directory, OUTPUT_FILENAME)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    descriptor = -1
    try:
        descriptor = os.open(path, flags, 0o600)
        with os.fdopen(descriptor, "wb", closefd=True) as output:
            descriptor = -1
            output.write(value)
            output.flush()
            os.fsync(output.fileno())
        directory_descriptor = os.open(
            output_directory, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0)
        )
        try:
            os.fsync(directory_descriptor)
        finally:
            os.close(directory_descriptor)
    except OSError as error:
        if descriptor >= 0:
            os.close(descriptor)
        raise TrainerOutputError("failed to publish output.json") from error


def _diagnostic(message: str) -> None:
    value = message.encode("utf-8", errors="replace")[:MAXIMUM_DIAGNOSTIC_BYTES]
    sys.stderr.buffer.write(value + b"\n")
    sys.stderr.buffer.flush()


def run(environment: dict[str, str], runtime_attestation: dict[str, str]) -> bytes:
    if len(sys.argv) != 1:
        raise TrainerInputError("trainer CLI does not accept arguments")
    manifest_sha256, maximum_input_bytes, maximum_output_bytes, output_directory = validate_environment(
        environment
    )
    raw = sys.stdin.buffer.read(maximum_input_bytes + 1)
    if not raw or len(raw) > maximum_input_bytes:
        raise TrainerInputError("stdin is empty or exceeds the configured limit")
    value = decode_canonical_object(raw)
    result = canonical_json(train(value, manifest_sha256, runtime_attestation))
    if len(result) > maximum_output_bytes:
        raise TrainerOutputError("training output exceeds the configured limit")
    publish_output(output_directory, result)
    return result


def main() -> int:
    try:
        environment = dict(os.environ)
        runtime_attestation = attest_runtime(environment)
        output = run(environment, runtime_attestation)
        sys.stdout.buffer.write(output)
        sys.stdout.buffer.flush()
        return 0
    except TrainerInputError as error:
        _diagnostic(f"invalid trainer contract: {error}")
        return 2
    except TrainerOutputError as error:
        _diagnostic(str(error))
        return 3
    except TrainerRuntimeError as error:
        _diagnostic(f"trainer runtime capability failure: {error}")
        return 70
    except Exception:
        _diagnostic("internal trainer failure")
        return 70
