"""Canonical JSON and strict value validation for the trainer protocol."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from decimal import Decimal
import hashlib
import json
import math
import re
from typing import NoReturn


MAXIMUM_DEPTH = 64
MAXIMUM_NUMBER_BYTES = 128
MAXIMUM_NUMBER_EXPONENT = 8192
MAXIMUM_NUMBER_PRECISION = 4096
MAXIMUM_CANONICAL_NUMBER_BYTES = 8192
MAXIMUM_COLLECTION_SIZE = 1_000_000
MAXIMUM_KNOWLEDGE_POINTS = 1_024
MAXIMUM_ACTORS = 20_000
MAXIMUM_PROBLEMS = 10_000
MAXIMUM_INTERACTIONS = 200_000
MAXIMUM_STATEMENT_BYTES = 1 << 20
MAXIMUM_STATEMENT_TOTAL_BYTES = 32 << 20

SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
CANONICAL_ID_PATTERN = re.compile(r"^[1-9][0-9]*$")
SCHEMA_ID_PATTERN = re.compile(r"^ascendany[.][a-z][a-z0-9_.-]{0,126}[.]v[1-9][0-9]*$")
CONFIGURATION_KEY_PATTERN = re.compile(r"^[a-z][a-z0-9_.-]{0,127}$")
KNOWLEDGE_POINT_ID_PATTERN = re.compile(r"^[a-z][a-z0-9_.-]{0,127}$")
PROBLEM_KEY_PATTERN = re.compile(r"^pintia:([^:]+):([0-9a-f]{64})$")
UTC_TIMESTAMP_PATTERN = re.compile(
    r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:[.][0-9]{1,9})?Z$"
)

INPUT_PROTOCOL = "ascendany.recommendation.training-bundle.v2"
OUTPUT_PROTOCOL = "ascendany.recommendation.training-output.v2"
MODEL_SCHEMA = "ascendany.recommendation.model.v2"
PARAMETER_SCHEMA = "ascendany.recommendation.parameters.knowledge-mirt.v1"
CONFIGURATION_SCHEMA = "ascendany.training.recommendation.v2"
CATALOG_SCHEMA = "ascendany.knowledge_catalog.recommendation.v1"
ALGORITHM = "knowledge_mirt_v1"

ACTOR_FEATURE_IDS = (
    "log1p_train_interaction_count",
    "train_pass_rate_beta11",
    "train_mean_score_rate",
    "train_mean_log1p_submission_count",
)
PROBLEM_FEATURE_IDS = (
    "train_acceptance_logit_beta11",
    "log1p_train_actor_count",
    "log1p_train_submission_count",
)
FEATURE_COMPARISON_TOLERANCE = 1e-12


@dataclass(frozen=True)
class ValidationConfiguration:
    minimum_actors: int
    minimum_interactions: int
    minimum_relative_log_loss_improvement: float


@dataclass(frozen=True)
class TrainingConfiguration:
    accelerator: str
    seed: int
    epochs: int
    patience: int
    batch_size: int
    learning_rate: float
    weight_decay: float
    minimum_train_interactions: int
    minimum_actor_interactions: int
    minimum_problem_interactions: int
    validation: ValidationConfiguration


@dataclass(frozen=True)
class Actor:
    actor_id: str
    current_rating: float
    features: tuple[float, ...]


@dataclass(frozen=True)
class KnowledgePoint:
    knowledge_point_id: str
    label: str
    description: str
    prerequisite_ids: tuple[str, ...]


@dataclass(frozen=True)
class Problem:
    problem_key: str
    features: tuple[float, ...]
    knowledge_weights: tuple[float, ...]
    train_actor_count: int
    train_submission_count: int


@dataclass(frozen=True)
class Interaction:
    interaction_id: str
    snapshot_id: str
    actor_id: str
    problem_key: str
    target_score_rate: float
    passed: bool
    submission_count: int
    valid_submission_count: int
    first_submitted_at: tuple[datetime, Decimal]
    last_submitted_at: tuple[datetime, Decimal]
    last_submitted_at_text: str
    split: str


@dataclass(frozen=True)
class ValidatedBundle:
    manifest: dict[str, object]
    configuration_document_sha256: str
    catalog_document_sha256: str
    feature_schema_sha256: str
    split_sha256: str
    knowledge_points: tuple[KnowledgePoint, ...]
    actors: tuple[Actor, ...]
    problems: tuple[Problem, ...]
    interactions: tuple[Interaction, ...]
    configuration: TrainingConfiguration

    @property
    def train_interactions(self) -> tuple[Interaction, ...]:
        return tuple(item for item in self.interactions if item.split == "train")

    @property
    def validation_interactions(self) -> tuple[Interaction, ...]:
        return tuple(item for item in self.interactions if item.split == "validation")


class TrainerInputError(ValueError):
    """The immutable input, process environment, or CLI contract is invalid."""


class TrainerOutputError(RuntimeError):
    """The trainer could not produce or publish a valid output artifact."""


class TrainerRuntimeError(RuntimeError):
    """The explicitly selected training capability is unavailable or failed."""


def _reject_constant(_: str) -> NoReturn:
    raise TrainerInputError("non-finite JSON numbers are forbidden")


def _decode_number(raw: str) -> Decimal:
    if len(raw) > MAXIMUM_NUMBER_BYTES:
        raise TrainerInputError("JSON number exceeds 128 bytes")
    exponent_marker = max(raw.rfind("e"), raw.rfind("E"))
    if exponent_marker >= 0:
        try:
            exponent = int(raw[exponent_marker + 1 :])
        except ValueError as error:
            raise TrainerInputError("JSON number exponent is invalid") from error
        if not -MAXIMUM_NUMBER_EXPONENT <= exponent <= MAXIMUM_NUMBER_EXPONENT:
            raise TrainerInputError("JSON number exponent exceeds 8192 decimal places")
    try:
        return Decimal(raw)
    except Exception as error:
        raise TrainerInputError("JSON number is invalid") from error


def _decode_object(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise TrainerInputError(f"duplicate JSON object key {key!r}")
        result[key] = value
    return result


def decode_canonical_object(raw: bytes) -> dict[str, object]:
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError as error:
        raise TrainerInputError("input must be valid UTF-8") from error
    try:
        value = json.loads(
            text,
            object_pairs_hook=_decode_object,
            parse_float=_decode_number,
            parse_int=_decode_number,
            parse_constant=_reject_constant,
        )
    except TrainerInputError:
        raise
    except (json.JSONDecodeError, RecursionError) as error:
        raise TrainerInputError("input must contain one JSON document") from error
    if not isinstance(value, dict):
        raise TrainerInputError("input JSON root must be an object")
    if canonical_json(value) != raw:
        raise TrainerInputError("input JSON bytes must already be canonical")
    return value


def canonical_json(value: object) -> bytes:
    chunks: list[str] = []
    _encode_value(value, chunks, 0)
    return "".join(chunks).encode("utf-8")


def _encode_value(value: object, chunks: list[str], depth: int) -> None:
    if depth > MAXIMUM_DEPTH:
        raise TrainerInputError("JSON nesting exceeds 64 levels")
    if value is None:
        chunks.append("null")
        return
    if value is True:
        chunks.append("true")
        return
    if value is False:
        chunks.append("false")
        return
    if isinstance(value, Decimal):
        chunks.append(_canonical_decimal(value))
        return
    if isinstance(value, int):
        chunks.append(str(value))
        return
    if isinstance(value, float):
        if value == float("inf") or value == float("-inf") or value != value:
            raise TrainerOutputError("model output contains a non-finite number")
        chunks.append(_canonical_decimal(Decimal(str(value))))
        return
    if isinstance(value, str):
        if "\x00" in value:
            raise TrainerInputError("JSON string contains NUL")
        chunks.append(_encode_string(value))
        return
    if isinstance(value, list):
        chunks.append("[")
        for index, item in enumerate(value):
            if index:
                chunks.append(",")
            _encode_value(item, chunks, depth + 1)
        chunks.append("]")
        return
    if isinstance(value, dict):
        chunks.append("{")
        for index, key in enumerate(sorted(value)):
            if not isinstance(key, str):
                raise TrainerInputError("JSON object keys must be strings")
            if "\x00" in key:
                raise TrainerInputError("JSON object key contains NUL")
            if index:
                chunks.append(",")
            chunks.append(_encode_string(key))
            chunks.append(":")
            _encode_value(value[key], chunks, depth + 1)
        chunks.append("}")
        return
    raise TrainerInputError("unsupported JSON value")


def _encode_string(value: str) -> str:
    if any(0xD800 <= ord(character) <= 0xDFFF for character in value):
        raise TrainerInputError("JSON string contains a surrogate code point")
    encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    return (
        encoded.replace("&", "\\u0026")
        .replace("<", "\\u003c")
        .replace(">", "\\u003e")
        .replace("\u2028", "\\u2028")
        .replace("\u2029", "\\u2029")
    )


def _canonical_decimal(value: Decimal) -> str:
    if not value.is_finite():
        raise TrainerInputError("JSON number must be finite")
    if value.is_zero():
        return "0"
    sign, digits_tuple, exponent = value.as_tuple()
    digits = "".join(str(digit) for digit in digits_tuple)
    while digits.endswith("0"):
        digits = digits[:-1]
        exponent += 1
    if exponent >= 0:
        canonical = digits + ("0" * exponent)
    else:
        precision = -exponent
        if precision > MAXIMUM_NUMBER_PRECISION:
            raise TrainerInputError("JSON number precision exceeds 4096 decimal places")
        point = len(digits) + exponent
        if point > 0:
            canonical = digits[:point] + "." + digits[point:]
        else:
            canonical = "0." + ("0" * -point) + digits
    if sign:
        canonical = "-" + canonical
    if len(canonical) > MAXIMUM_CANONICAL_NUMBER_BYTES:
        raise TrainerInputError("canonical JSON number exceeds 8192 bytes")
    return canonical


def require_exact_object(value: object, keys: set[str], label: str) -> dict[str, object]:
    if not isinstance(value, dict) or set(value) != keys:
        raise TrainerInputError(f"{label} has invalid fields")
    return value


def require_array(
    value: object,
    label: str,
    *,
    minimum: int = 0,
    maximum: int = MAXIMUM_COLLECTION_SIZE,
) -> list[object]:
    if not isinstance(value, list) or not minimum <= len(value) <= maximum:
        raise TrainerInputError(f"{label} has an invalid item count")
    return value


def require_string(value: object, label: str, *, maximum_bytes: int = 1024) -> str:
    if not isinstance(value, str) or not value or value.strip() != value or "\x00" in value:
        raise TrainerInputError(f"{label} must be a non-empty unpadded string")
    if len(value.encode("utf-8")) > maximum_bytes:
        raise TrainerInputError(f"{label} exceeds {maximum_bytes} UTF-8 bytes")
    return value


def require_sha256(value: object, label: str) -> str:
    text = require_string(value, label, maximum_bytes=64)
    if SHA256_PATTERN.fullmatch(text) is None:
        raise TrainerInputError(f"{label} must be a lowercase SHA-256 digest")
    return text


def require_schema_id(value: object, label: str) -> str:
    text = require_string(value, label, maximum_bytes=128)
    if SCHEMA_ID_PATTERN.fullmatch(text) is None:
        raise TrainerInputError(f"{label} is invalid")
    return text


def require_integer(
    value: object,
    label: str,
    *,
    minimum: int,
    maximum: int,
) -> int:
    if not isinstance(value, Decimal) or value != value.to_integral_value():
        raise TrainerInputError(f"{label} must be an integer")
    integer = int(value)
    if not minimum <= integer <= maximum:
        raise TrainerInputError(f"{label} is outside the supported range")
    return integer


def require_canonical_id(value: object, label: str) -> str:
    text = require_string(value, label, maximum_bytes=19)
    if CANONICAL_ID_PATTERN.fullmatch(text) is None or int(text) > (1 << 63) - 1:
        raise TrainerInputError(f"{label} must be a positive signed 64-bit canonical decimal")
    return text


def require_canonical_decimal_id(value: object, label: str) -> str:
    text = require_string(value, label, maximum_bytes=256)
    if CANONICAL_ID_PATTERN.fullmatch(text) is None:
        raise TrainerInputError(f"{label} must be a positive canonical decimal")
    return text


def require_number(
    value: object,
    label: str,
    *,
    minimum: Decimal | None = None,
    maximum: Decimal | None = None,
    minimum_exclusive: bool = False,
) -> Decimal:
    if not isinstance(value, Decimal) or not value.is_finite():
        raise TrainerInputError(f"{label} must be a finite JSON number")
    if minimum is not None:
        invalid_minimum = value <= minimum if minimum_exclusive else value < minimum
        if invalid_minimum:
            raise TrainerInputError(f"{label} is below its supported range")
    if maximum is not None and value > maximum:
        raise TrainerInputError(f"{label} exceeds its supported range")
    return value


def require_number_vector(
    value: object,
    label: str,
    *,
    size: int,
    minimum: Decimal | None = None,
    maximum: Decimal | None = None,
) -> tuple[float, ...]:
    array = require_array(value, label, minimum=size, maximum=size)
    return tuple(
        decimal_to_finite_float(
            require_number(item, f"{label}[{index}]", minimum=minimum, maximum=maximum),
            f"{label}[{index}]",
        )
        for index, item in enumerate(array)
    )


def decimal_to_finite_float(value: Decimal, label: str) -> float:
    converted = float(value)
    if not math.isfinite(converted) or (not value.is_zero() and converted == 0.0):
        raise TrainerInputError(f"{label} is not representable as a finite nonzero float64")
    return converted


def sha256_canonical(value: object) -> str:
    return hashlib.sha256(canonical_json(value)).hexdigest()


def _require_boolean(value: object, label: str) -> bool:
    if not isinstance(value, bool):
        raise TrainerInputError(f"{label} must be a boolean")
    return value


def _require_nullable_nonnegative_integer(value: object, label: str) -> int | None:
    if value is None:
        return None
    return require_integer(value, label, minimum=0, maximum=(1 << 63) - 1)


def _require_timestamp(value: object, label: str) -> tuple[str, tuple[datetime, Decimal]]:
    text = require_string(value, label, maximum_bytes=64)
    if UTC_TIMESTAMP_PATTERN.fullmatch(text) is None:
        raise TrainerInputError(f"{label} must be a UTC RFC3339 timestamp ending in Z")
    without_zone = text[:-1]
    second_text, separator, fractional_text = without_zone.partition(".")
    try:
        parsed = datetime.fromisoformat(second_text + "+00:00")
    except ValueError as error:
        raise TrainerInputError(f"{label} is not a valid calendar timestamp") from error
    fractional_second = Decimal("0." + fractional_text) if separator else Decimal(0)
    return text, (parsed, fractional_second)


def _require_provenance(value: object, label: str) -> dict[str, object]:
    provenance = require_exact_object(
        value,
        {"versionId", "key", "versionNumber", "schemaId", "documentSha256"},
        label,
    )
    require_canonical_id(provenance["versionId"], f"{label} version ID")
    key = require_string(provenance["key"], f"{label} key", maximum_bytes=128)
    if CONFIGURATION_KEY_PATTERN.fullmatch(key) is None:
        raise TrainerInputError(f"{label} key is invalid")
    require_integer(
        provenance["versionNumber"],
        f"{label} version number",
        minimum=1,
        maximum=(1 << 63) - 1,
    )
    require_schema_id(provenance["schemaId"], f"{label} schema")
    require_sha256(provenance["documentSha256"], f"{label} document digest")
    return provenance


def _require_configuration(
    value: object,
    provenance: dict[str, object],
) -> tuple[TrainingConfiguration, dict[str, object]]:
    wrapper = require_exact_object(value, {"schemaId", "document"}, "training configuration")
    if wrapper["schemaId"] != CONFIGURATION_SCHEMA or provenance["schemaId"] != CONFIGURATION_SCHEMA:
        raise TrainerInputError("training configuration schema is unsupported")
    document = require_exact_object(
        wrapper["document"],
        {
            "algorithm",
            "knowledgeCatalogVersionId",
            "accelerator",
            "seed",
            "epochs",
            "patience",
            "batchSize",
            "learningRate",
            "weightDecay",
            "minTrainInteractions",
            "minActorInteractions",
            "minProblemInteractions",
            "validation",
            "pathPolicy",
            "rankingWeights",
        },
        "training configuration document",
    )
    if document["algorithm"] != ALGORITHM:
        raise TrainerInputError("training configuration algorithm is unsupported")
    require_canonical_id(document["knowledgeCatalogVersionId"], "knowledge catalog version ID")
    accelerator = require_string(document["accelerator"], "training accelerator", maximum_bytes=4)
    if accelerator not in {"cpu", "cuda"}:
        raise TrainerInputError("training accelerator must be cpu or cuda")
    seed = require_integer(document["seed"], "training seed", minimum=0, maximum=(1 << 31) - 1)
    epochs = require_integer(document["epochs"], "training epochs", minimum=1, maximum=10_000)
    patience = require_integer(document["patience"], "training patience", minimum=1, maximum=epochs)
    minimum_train_interactions = require_integer(
        document["minTrainInteractions"],
        "minimum train interactions",
        minimum=2,
        maximum=MAXIMUM_INTERACTIONS,
    )
    batch_size = require_integer(
        document["batchSize"],
        "training batch size",
        minimum=1,
        maximum=minimum_train_interactions,
    )
    learning_rate = decimal_to_finite_float(
        require_number(
            document["learningRate"],
            "training learning rate",
            minimum=Decimal(0),
            maximum=Decimal(1),
            minimum_exclusive=True,
        ),
        "training learning rate",
    )
    weight_decay = decimal_to_finite_float(
        require_number(
            document["weightDecay"],
            "training weight decay",
            minimum=Decimal(0),
            maximum=Decimal(1),
        ),
        "training weight decay",
    )
    if weight_decay >= 1:
        raise TrainerInputError("training weight decay must be less than one")
    minimum_actor_interactions = require_integer(
        document["minActorInteractions"],
        "minimum actor interactions",
        minimum=2,
        maximum=MAXIMUM_INTERACTIONS,
    )
    minimum_problem_interactions = require_integer(
        document["minProblemInteractions"],
        "minimum problem interactions",
        minimum=1,
        maximum=MAXIMUM_INTERACTIONS,
    )

    validation = require_exact_object(
        document["validation"],
        {"minActors", "minInteractions", "minRelativeLogLossImprovement"},
        "validation configuration",
    )
    validation_configuration = ValidationConfiguration(
        minimum_actors=require_integer(
            validation["minActors"], "minimum validation actors", minimum=1, maximum=MAXIMUM_ACTORS
        ),
        minimum_interactions=require_integer(
            validation["minInteractions"],
            "minimum validation interactions",
            minimum=1,
            maximum=MAXIMUM_INTERACTIONS,
        ),
        minimum_relative_log_loss_improvement=decimal_to_finite_float(
            require_number(
                validation["minRelativeLogLossImprovement"],
                "minimum relative validation log-loss improvement",
                minimum=Decimal(0),
                maximum=Decimal(1),
            ),
            "minimum relative validation log-loss improvement",
        ),
    )
    if validation_configuration.minimum_relative_log_loss_improvement >= 1:
        raise TrainerInputError("minimum relative validation log-loss improvement must be less than one")

    path_policy = require_exact_object(
        document["pathPolicy"],
        {
            "targetMastery",
            "maxKnowledgeTargets",
            "minSteps",
            "maxSteps",
            "problemsPerStep",
            "targetSuccessProbability",
        },
        "path policy",
    )
    for key in ("targetMastery", "targetSuccessProbability"):
        require_number(
            path_policy[key],
            f"path policy {key}",
            minimum=Decimal(0),
            maximum=Decimal(1),
            minimum_exclusive=True,
        )
        if path_policy[key] == Decimal(1):
            raise TrainerInputError(f"path policy {key} must be less than one")
    require_integer(
        path_policy["maxKnowledgeTargets"],
        "maximum knowledge targets",
        minimum=1,
        maximum=MAXIMUM_KNOWLEDGE_POINTS,
    )
    minimum_steps = require_integer(path_policy["minSteps"], "minimum path steps", minimum=2, maximum=8)
    require_integer(path_policy["maxSteps"], "maximum path steps", minimum=minimum_steps, maximum=8)
    require_integer(path_policy["problemsPerStep"], "problems per path step", minimum=1, maximum=20)

    ranking_weights = require_exact_object(
        document["rankingWeights"], {"knowledgeGap", "successDistance"}, "ranking weights"
    )
    for key in ("knowledgeGap", "successDistance"):
        require_number(
            ranking_weights[key],
            f"ranking weight {key}",
            minimum=Decimal(0),
            maximum=Decimal(100),
            minimum_exclusive=True,
        )

    if sha256_canonical(document) != provenance["documentSha256"]:
        raise TrainerInputError("training configuration document differs from its digest")
    return (
        TrainingConfiguration(
            accelerator=accelerator,
            seed=seed,
            epochs=epochs,
            patience=patience,
            batch_size=batch_size,
            learning_rate=learning_rate,
            weight_decay=weight_decay,
            minimum_train_interactions=minimum_train_interactions,
            minimum_actor_interactions=minimum_actor_interactions,
            minimum_problem_interactions=minimum_problem_interactions,
            validation=validation_configuration,
        ),
        document,
    )


def _parse_knowledge_points(value: object, label: str) -> tuple[KnowledgePoint, ...]:
    array = require_array(value, label, minimum=1, maximum=MAXIMUM_KNOWLEDGE_POINTS)
    points: list[KnowledgePoint] = []
    previous_id = ""
    for index, raw_point in enumerate(array):
        point = require_exact_object(raw_point, {"id", "label", "description", "prerequisiteIds"}, label)
        point_id = require_string(point["id"], f"{label}[{index}] ID", maximum_bytes=128)
        if KNOWLEDGE_POINT_ID_PATTERN.fullmatch(point_id) is None:
            raise TrainerInputError(f"{label}[{index}] ID is invalid")
        if point_id <= previous_id:
            raise TrainerInputError(f"{label} IDs must be unique and UTF-8 lexical ascending")
        previous_id = point_id
        prerequisites_raw = require_array(point["prerequisiteIds"], f"{label}[{index}] prerequisites")
        prerequisites: list[str] = []
        previous_prerequisite = ""
        for prerequisite_index, raw_prerequisite in enumerate(prerequisites_raw):
            prerequisite = require_string(
                raw_prerequisite,
                f"{label}[{index}] prerequisite[{prerequisite_index}]",
                maximum_bytes=128,
            )
            if KNOWLEDGE_POINT_ID_PATTERN.fullmatch(prerequisite) is None or prerequisite <= previous_prerequisite:
                raise TrainerInputError(f"{label}[{index}] prerequisites must be valid, unique, and sorted")
            previous_prerequisite = prerequisite
            prerequisites.append(prerequisite)
        points.append(
            KnowledgePoint(
                knowledge_point_id=point_id,
                label=require_string(point["label"], f"{label}[{index}] label", maximum_bytes=256),
                description=require_string(
                    point["description"], f"{label}[{index}] description", maximum_bytes=4096
                ),
                prerequisite_ids=tuple(prerequisites),
            )
        )
    point_ids = {point.knowledge_point_id for point in points}
    for point in points:
        if point.knowledge_point_id in point.prerequisite_ids or any(
            prerequisite not in point_ids for prerequisite in point.prerequisite_ids
        ):
            raise TrainerInputError("knowledge prerequisites contain a self or dangling reference")
    _validate_knowledge_dag(points)
    return tuple(points)


def _validate_knowledge_dag(points: list[KnowledgePoint]) -> None:
    prerequisites = {point.knowledge_point_id: point.prerequisite_ids for point in points}
    state: dict[str, int] = {}

    def visit(point_id: str) -> None:
        marker = state.get(point_id, 0)
        if marker == 1:
            raise TrainerInputError("knowledge prerequisites contain a cycle")
        if marker == 2:
            return
        state[point_id] = 1
        for prerequisite in prerequisites[point_id]:
            visit(prerequisite)
        state[point_id] = 2

    for point in points:
        visit(point.knowledge_point_id)


def _parse_sparse_knowledge_weights(
    value: object,
    label: str,
    knowledge_index: dict[str, int],
) -> tuple[tuple[float, ...], list[dict[str, object]]]:
    array = require_array(value, label, minimum=1, maximum=len(knowledge_index))
    dense = [0.0] * len(knowledge_index)
    preserved: list[dict[str, object]] = []
    previous_id = ""
    total = Decimal(0)
    for index, raw_weight in enumerate(array):
        weight = require_exact_object(raw_weight, {"knowledgePointId", "weight"}, f"{label}[{index}]")
        point_id = require_string(weight["knowledgePointId"], f"{label}[{index}] knowledge ID", maximum_bytes=128)
        if point_id not in knowledge_index or point_id <= previous_id:
            raise TrainerInputError(f"{label} knowledge IDs must be known, unique, and sorted")
        previous_id = point_id
        number = require_number(
            weight["weight"],
            f"{label}[{index}] weight",
            minimum=Decimal(0),
            maximum=Decimal(1),
            minimum_exclusive=True,
        )
        total += number
        dense[knowledge_index[point_id]] = decimal_to_finite_float(number, f"{label}[{index}] weight")
        preserved.append(weight)
    if total != Decimal(1):
        raise TrainerInputError(f"{label} weights must have an exact decimal sum of one")
    return tuple(dense), preserved


def _parse_catalog(
    value: object,
    provenance: dict[str, object],
) -> tuple[tuple[KnowledgePoint, ...], dict[tuple[str, str, str], list[dict[str, object]]], dict[str, object]]:
    wrapper = require_exact_object(value, {"schemaId", "document"}, "knowledge catalog")
    if wrapper["schemaId"] != CATALOG_SCHEMA or provenance["schemaId"] != CATALOG_SCHEMA:
        raise TrainerInputError("knowledge catalog schema is unsupported")
    document = require_exact_object(
        wrapper["document"],
        {"taxonomyId", "knowledgePoints", "problemAssignments"},
        "knowledge catalog document",
    )
    taxonomy_id = require_string(document["taxonomyId"], "knowledge taxonomy ID", maximum_bytes=128)
    if CONFIGURATION_KEY_PATTERN.fullmatch(taxonomy_id) is None:
        raise TrainerInputError("knowledge taxonomy ID is invalid")
    points = _parse_knowledge_points(document["knowledgePoints"], "catalog knowledge points")
    knowledge_index = {point.knowledge_point_id: index for index, point in enumerate(points)}
    assignments_raw = require_array(document["problemAssignments"], "catalog problem assignments", minimum=1)
    assignments: dict[tuple[str, str, str], list[dict[str, object]]] = {}
    previous_key: tuple[str, str, str] | None = None
    for index, raw_assignment in enumerate(assignments_raw):
        assignment = require_exact_object(
            raw_assignment,
            {"platform", "problemId", "problemFactSha256", "knowledge"},
            f"catalog problem assignment[{index}]",
        )
        platform = require_string(assignment["platform"], "catalog problem platform", maximum_bytes=32)
        if platform != "pintia":
            raise TrainerInputError("catalog problem platform must be pintia")
        problem_id = require_string(assignment["problemId"], "catalog problem ID", maximum_bytes=256)
        if ":" in problem_id:
            raise TrainerInputError("catalog problem ID cannot contain a colon")
        fact_sha256 = require_sha256(assignment["problemFactSha256"], "catalog problem fact digest")
        key = (platform, problem_id, fact_sha256)
        if previous_key is not None and key <= previous_key:
            raise TrainerInputError("catalog problem assignments must be unique and sorted")
        previous_key = key
        _, preserved = _parse_sparse_knowledge_weights(
            assignment["knowledge"], f"catalog problem assignment[{index}] weights", knowledge_index
        )
        assignments[key] = preserved
    if sha256_canonical(document) != provenance["documentSha256"]:
        raise TrainerInputError("knowledge catalog document differs from its digest")
    return points, assignments, document


def _parse_actors(value: object) -> tuple[Actor, ...]:
    array = require_array(value, "actors", minimum=1, maximum=MAXIMUM_ACTORS)
    actors: list[Actor] = []
    previous_id = 0
    for index, raw_actor in enumerate(array):
        actor = require_exact_object(raw_actor, {"actorId", "currentRating", "features"}, f"actor[{index}]")
        actor_id = require_canonical_id(actor["actorId"], f"actor[{index}] ID")
        numeric_id = int(actor_id)
        if numeric_id <= previous_id:
            raise TrainerInputError("actor IDs must be unique and numeric ascending")
        previous_id = numeric_id
        actors.append(
            Actor(
                actor_id=actor_id,
                current_rating=decimal_to_finite_float(
                    require_number(
                        actor["currentRating"],
                        f"actor[{index}] current rating",
                        minimum=Decimal(0),
                        maximum=Decimal(1_000_000),
                    ),
                    f"actor[{index}] current rating",
                ),
                features=require_number_vector(actor["features"], f"actor[{index}] features", size=len(ACTOR_FEATURE_IDS)),
            )
        )
    return tuple(actors)


def _parse_problems(
    value: object,
    knowledge_points: tuple[KnowledgePoint, ...],
    catalog_assignments: dict[tuple[str, str, str], list[dict[str, object]]],
) -> tuple[Problem, ...]:
    array = require_array(value, "problems", minimum=1, maximum=MAXIMUM_PROBLEMS)
    knowledge_index = {point.knowledge_point_id: index for index, point in enumerate(knowledge_points)}
    problems: list[Problem] = []
    previous_key = ""
    statement_total_bytes = 0
    for index, raw_problem in enumerate(array):
        problem = require_exact_object(
            raw_problem,
            {
                "problemKey",
                "sourceProblemKey",
                "problemFactSha256",
                "platform",
                "problemId",
                "title",
                "statementText",
                "sourceProblemSets",
                "maxScore",
                "timeLimitMs",
                "memoryLimitBytes",
                "knowledgeWeights",
                "features",
                "trainActorCount",
                "trainSubmissionCount",
            },
            f"problem[{index}]",
        )
        problem_key = require_string(problem["problemKey"], f"problem[{index}] key", maximum_bytes=512)
        if problem_key <= previous_key:
            raise TrainerInputError("problem keys must be unique and UTF-8 lexical ascending")
        previous_key = problem_key
        platform = require_string(problem["platform"], f"problem[{index}] platform", maximum_bytes=32)
        if platform != "pintia":
            raise TrainerInputError("problem platform must be pintia")
        problem_id = require_string(problem["problemId"], f"problem[{index}] ID", maximum_bytes=256)
        if ":" in problem_id:
            raise TrainerInputError("problem ID cannot contain a colon")
        fact_sha256 = require_sha256(problem["problemFactSha256"], f"problem[{index}] fact digest")
        if problem["sourceProblemKey"] != f"pintia:{problem_id}" or problem_key != f"pintia:{problem_id}:{fact_sha256}":
            raise TrainerInputError("problem key or source problem key differs from its Pintia identity")
        require_string(problem["title"], f"problem[{index}] title", maximum_bytes=4096)
        statement_text = problem["statementText"]
        if not isinstance(statement_text, str):
            raise TrainerInputError(f"problem[{index}] statement text must be a string")
        statement_bytes = len(statement_text.encode("utf-8"))
        if statement_bytes > MAXIMUM_STATEMENT_BYTES:
            raise TrainerInputError(f"problem[{index}] statement text exceeds one MiB")
        statement_total_bytes += statement_bytes
        if statement_total_bytes > MAXIMUM_STATEMENT_TOTAL_BYTES:
            raise TrainerInputError("problem statement text total exceeds 32 MiB")
        source_sets = require_array(problem["sourceProblemSets"], f"problem[{index}] source problem sets", minimum=1)
        previous_source_key: tuple[int, str, str] | None = None
        for source_index, raw_source in enumerate(source_sets):
            source = require_exact_object(
                raw_source, {"problemSetId", "sourceUrl"}, f"problem[{index}] source[{source_index}]"
            )
            set_id = require_canonical_decimal_id(
                source["problemSetId"], f"problem[{index}] source problem set ID"
            )
            source_url = require_string(source["sourceUrl"], f"problem[{index}] source URL", maximum_bytes=4096)
            pintia_prefix = "https://pintia.cn"
            suffix = source_url[len(pintia_prefix) :] if source_url.startswith(pintia_prefix) else ""
            if (
                not source_url.startswith(pintia_prefix)
                or (suffix and suffix[0] not in {"/", "?"})
                or "#" in suffix
                or "\\" in suffix
            ):
                raise TrainerInputError("problem source URL must be an absolute Pintia HTTPS URL")
            source_key = (len(set_id), set_id, source_url)
            if previous_source_key is not None and source_key <= previous_source_key:
                raise TrainerInputError("source problem sets must be unique and canonically sorted")
            previous_source_key = source_key
        require_number(
            problem["maxScore"],
            f"problem[{index}] maximum score",
            minimum=Decimal(0),
            minimum_exclusive=True,
        )
        _require_nullable_nonnegative_integer(problem["timeLimitMs"], f"problem[{index}] time limit")
        _require_nullable_nonnegative_integer(problem["memoryLimitBytes"], f"problem[{index}] memory limit")
        dense_weights, preserved_weights = _parse_sparse_knowledge_weights(
            problem["knowledgeWeights"], f"problem[{index}] knowledge weights", knowledge_index
        )
        assignment_key = (platform, problem_id, fact_sha256)
        if assignment_key not in catalog_assignments or canonical_json(preserved_weights) != canonical_json(
            catalog_assignments[assignment_key]
        ):
            raise TrainerInputError("problem knowledge weights differ from the versioned catalog")
        problems.append(
            Problem(
                problem_key=problem_key,
                features=require_number_vector(
                    problem["features"], f"problem[{index}] features", size=len(PROBLEM_FEATURE_IDS)
                ),
                knowledge_weights=dense_weights,
                train_actor_count=require_integer(
                    problem["trainActorCount"],
                    f"problem[{index}] train actor count",
                    minimum=1,
                    maximum=MAXIMUM_COLLECTION_SIZE,
                ),
                train_submission_count=require_integer(
                    problem["trainSubmissionCount"],
                    f"problem[{index}] train submission count",
                    minimum=1,
                    maximum=(1 << 63) - 1,
                ),
            )
        )
    return tuple(problems)


def _parse_interactions(
    value: object,
    actor_ids: set[str],
    problem_keys: set[str],
) -> tuple[Interaction, ...]:
    array = require_array(value, "interactions", minimum=1, maximum=MAXIMUM_INTERACTIONS)
    interactions: list[Interaction] = []
    previous_id = ""
    for index, raw_interaction in enumerate(array):
        item = require_exact_object(
            raw_interaction,
            {
                "interactionId",
                "snapshotId",
                "actorId",
                "problemKey",
                "firstSubmittedAt",
                "lastSubmittedAt",
                "submissionCount",
                "validSubmissionCount",
                "targetScoreRate",
                "passed",
                "split",
            },
            f"interaction[{index}]",
        )
        interaction_id = require_sha256(item["interactionId"], f"interaction[{index}] ID")
        if interaction_id <= previous_id:
            raise TrainerInputError("interaction IDs must be unique and UTF-8 lexical ascending")
        previous_id = interaction_id
        snapshot_id = require_canonical_id(item["snapshotId"], f"interaction[{index}] snapshot ID")
        actor_id = require_canonical_id(item["actorId"], f"interaction[{index}] actor ID")
        problem_key = require_string(item["problemKey"], f"interaction[{index}] problem key", maximum_bytes=512)
        if actor_id not in actor_ids or problem_key not in problem_keys:
            raise TrainerInputError("interaction contains a dangling actor or problem reference")
        expected_id = sha256_canonical(
            {"actorId": actor_id, "problemKey": problem_key, "snapshotId": snapshot_id}
        )
        if interaction_id != expected_id:
            raise TrainerInputError("interaction ID differs from its canonical identity preimage")
        _, first_timestamp = _require_timestamp(
            item["firstSubmittedAt"], f"interaction[{index}] first submission timestamp"
        )
        last_text, last_timestamp = _require_timestamp(
            item["lastSubmittedAt"], f"interaction[{index}] last submission timestamp"
        )
        if first_timestamp > last_timestamp:
            raise TrainerInputError("interaction submission timestamps are out of order")
        submission_count = require_integer(
            item["submissionCount"],
            f"interaction[{index}] submission count",
            minimum=1,
            maximum=(1 << 63) - 1,
        )
        valid_submission_count = require_integer(
            item["validSubmissionCount"],
            f"interaction[{index}] valid submission count",
            minimum=0,
            maximum=submission_count,
        )
        target_score_rate_decimal = require_number(
            item["targetScoreRate"],
            f"interaction[{index}] target score rate",
            minimum=Decimal(0),
            maximum=Decimal(1),
        )
        passed = _require_boolean(item["passed"], f"interaction[{index}] passed")
        if passed != (target_score_rate_decimal == Decimal(1)):
            raise TrainerInputError("interaction passed flag differs from its target score rate")
        split = require_string(item["split"], f"interaction[{index}] split", maximum_bytes=10)
        if split not in {"train", "validation"}:
            raise TrainerInputError("interaction split must be train or validation")
        interactions.append(
            Interaction(
                interaction_id=interaction_id,
                snapshot_id=snapshot_id,
                actor_id=actor_id,
                problem_key=problem_key,
                target_score_rate=decimal_to_finite_float(
                    target_score_rate_decimal,
                    f"interaction[{index}] target score rate",
                ),
                passed=passed,
                submission_count=submission_count,
                valid_submission_count=valid_submission_count,
                first_submitted_at=first_timestamp,
                last_submitted_at=last_timestamp,
                last_submitted_at_text=last_text,
                split=split,
            )
        )
    return tuple(interactions)


def _require_feature_schema(value: object) -> dict[str, object]:
    schema = require_exact_object(value, {"actorFeatureIds", "problemFeatureIds"}, "feature schema")
    if schema["actorFeatureIds"] != list(ACTOR_FEATURE_IDS) or schema["problemFeatureIds"] != list(
        PROBLEM_FEATURE_IDS
    ):
        raise TrainerInputError("feature schema differs from knowledge_mirt_v1")
    return schema


def _compare_feature_vector(actual: tuple[float, ...], expected: tuple[float, ...], label: str) -> None:
    for index, (actual_value, expected_value) in enumerate(zip(actual, expected, strict=True)):
        if not math.isclose(
            actual_value,
            expected_value,
            rel_tol=FEATURE_COMPARISON_TOLERANCE,
            abs_tol=FEATURE_COMPARISON_TOLERANCE,
        ):
            raise TrainerInputError(f"{label}[{index}] differs from the train-only formula")


def _validate_corpus(
    actors: tuple[Actor, ...],
    problems: tuple[Problem, ...],
    interactions: tuple[Interaction, ...],
    configuration: TrainingConfiguration,
) -> None:
    actor_interactions = {actor.actor_id: [] for actor in actors}
    problem_interactions = {problem.problem_key: [] for problem in problems}
    for interaction in interactions:
        actor_interactions[interaction.actor_id].append(interaction)
        problem_interactions[interaction.problem_key].append(interaction)

    train_interactions = [interaction for interaction in interactions if interaction.split == "train"]
    validation_interactions = [interaction for interaction in interactions if interaction.split == "validation"]
    if len(train_interactions) < configuration.minimum_train_interactions:
        raise TrainerInputError("training corpus is below minTrainInteractions")
    validation_actor_ids = {interaction.actor_id for interaction in validation_interactions}
    if len(validation_interactions) < configuration.validation.minimum_interactions:
        raise TrainerInputError("validation corpus is below validation.minInteractions")
    if len(validation_actor_ids) < configuration.validation.minimum_actors:
        raise TrainerInputError("validation corpus is below validation.minActors")

    for actor in actors:
        items = actor_interactions[actor.actor_id]
        if len(items) < configuration.minimum_actor_interactions:
            raise TrainerInputError("actor corpus is below minActorInteractions")
        validation = [item for item in items if item.split == "validation"]
        if len(validation) != 1:
            raise TrainerInputError("each actor must own exactly one validation interaction")
        latest = max(items, key=lambda item: (item.last_submitted_at, item.interaction_id))
        if latest.interaction_id != validation[0].interaction_id:
            raise TrainerInputError("an actor validation interaction is not its chronologically latest interaction")
        training = [item for item in items if item.split == "train"]
        if not training:
            raise TrainerInputError("each actor must own at least one train interaction")
        expected_features = (
            math.log1p(len(training)),
            (sum(1 for item in training if item.passed) + 1) / (len(training) + 2),
            math.fsum(item.target_score_rate for item in training) / len(training),
            math.fsum(math.log1p(item.submission_count) for item in training) / len(training),
        )
        _compare_feature_vector(actor.features, expected_features, f"actor {actor.actor_id} feature")

    for problem in problems:
        training = [item for item in problem_interactions[problem.problem_key] if item.split == "train"]
        if len(training) < configuration.minimum_problem_interactions:
            raise TrainerInputError("problem corpus is below minProblemInteractions")
        actor_count = len({item.actor_id for item in training})
        submission_count = sum(item.submission_count for item in training)
        if problem.train_actor_count != actor_count or problem.train_submission_count != submission_count:
            raise TrainerInputError("problem train counts differ from the train split")
        acceptance = (sum(1 for item in training if item.passed) + 1) / (len(training) + 2)
        expected_features = (
            math.log(acceptance / (1 - acceptance)),
            math.log1p(actor_count),
            math.log1p(submission_count),
        )
        _compare_feature_vector(problem.features, expected_features, f"problem {problem.problem_key} feature")


def validate_input_bundle(value: dict[str, object], expected_manifest_sha256: str) -> ValidatedBundle:
    bundle = require_exact_object(
        value,
        {
            "protocol",
            "manifest",
            "analyticsInputManifest",
            "featureSchema",
            "trainingConfiguration",
            "knowledgeCatalog",
            "actors",
            "knowledgePoints",
            "problems",
            "interactions",
        },
        "input bundle",
    )
    if bundle["protocol"] != INPUT_PROTOCOL:
        raise TrainerInputError("input bundle protocol is unsupported")
    manifest = require_exact_object(
        bundle["manifest"],
        {
            "protocol",
            "source",
            "trainingConfiguration",
            "knowledgeCatalog",
            "featureSchemaSha256",
            "knowledgePointCount",
            "knowledgePointSetSha256",
            "actorCount",
            "actorSetSha256",
            "problemCount",
            "problemSetSha256",
            "interactionCount",
            "interactionSetSha256",
            "trainInteractionCount",
            "validationInteractionCount",
            "splitSha256",
        },
        "input manifest",
    )
    if manifest["protocol"] != INPUT_PROTOCOL or sha256_canonical(manifest) != expected_manifest_sha256:
        raise TrainerInputError("input manifest differs from the process contract")
    source = require_exact_object(
        manifest["source"],
        {
            "analyticsGenerationId",
            "analyticsHeadRevision",
            "analyticsInputManifestSha256",
            "algorithmVersion",
            "analyticsConfigurationSha256",
        },
        "analytics source manifest",
    )
    require_canonical_id(source["analyticsGenerationId"], "analytics generation ID")
    require_integer(
        source["analyticsHeadRevision"],
        "analytics head revision",
        minimum=1,
        maximum=(1 << 63) - 1,
    )
    analytics_digest = require_sha256(source["analyticsInputManifestSha256"], "analytics input manifest digest")
    require_sha256(source["analyticsConfigurationSha256"], "analytics configuration digest")
    require_string(source["algorithmVersion"], "analytics algorithm version", maximum_bytes=128)
    if not isinstance(bundle["analyticsInputManifest"], dict) or sha256_canonical(
        bundle["analyticsInputManifest"]
    ) != analytics_digest:
        raise TrainerInputError("analytics input manifest differs from its provenance")

    training_provenance = _require_provenance(manifest["trainingConfiguration"], "training configuration")
    catalog_provenance = _require_provenance(manifest["knowledgeCatalog"], "knowledge catalog")
    configuration, configuration_document = _require_configuration(
        bundle["trainingConfiguration"], training_provenance
    )
    if configuration_document["knowledgeCatalogVersionId"] != catalog_provenance["versionId"]:
        raise TrainerInputError("training configuration references a different knowledge catalog version")
    catalog_points, catalog_assignments, catalog_document = _parse_catalog(
        bundle["knowledgeCatalog"], catalog_provenance
    )
    root_points = _parse_knowledge_points(bundle["knowledgePoints"], "knowledge points")
    if root_points != catalog_points or canonical_json(bundle["knowledgePoints"]) != canonical_json(
        catalog_document["knowledgePoints"]
    ):
        raise TrainerInputError("training knowledge points differ from the versioned catalog")
    feature_schema = _require_feature_schema(bundle["featureSchema"])
    actors = _parse_actors(bundle["actors"])
    problems = _parse_problems(bundle["problems"], root_points, catalog_assignments)
    interactions = _parse_interactions(
        bundle["interactions"],
        {actor.actor_id for actor in actors},
        {problem.problem_key for problem in problems},
    )
    _validate_corpus(actors, problems, interactions, configuration)

    feature_schema_sha256 = require_sha256(manifest["featureSchemaSha256"], "feature schema digest")
    if feature_schema_sha256 != sha256_canonical(feature_schema):
        raise TrainerInputError("feature schema differs from its digest")
    collection_contracts = (
        (
            "knowledge point",
            root_points,
            manifest["knowledgePointCount"],
            manifest["knowledgePointSetSha256"],
            {"knowledgePointIds": [point.knowledge_point_id for point in root_points]},
        ),
        (
            "actor",
            actors,
            manifest["actorCount"],
            manifest["actorSetSha256"],
            {"actorIds": [actor.actor_id for actor in actors]},
        ),
        (
            "problem",
            problems,
            manifest["problemCount"],
            manifest["problemSetSha256"],
            {"problemKeys": [problem.problem_key for problem in problems]},
        ),
        (
            "interaction",
            interactions,
            manifest["interactionCount"],
            manifest["interactionSetSha256"],
            {"interactionIds": [interaction.interaction_id for interaction in interactions]},
        ),
    )
    for label, collection, raw_count, raw_digest, preimage in collection_contracts:
        count = require_integer(raw_count, f"{label} count", minimum=1, maximum=MAXIMUM_COLLECTION_SIZE)
        if count != len(collection) or require_sha256(raw_digest, f"{label} set digest") != sha256_canonical(preimage):
            raise TrainerInputError(f"{label} collection differs from the input manifest")
    train_count = sum(interaction.split == "train" for interaction in interactions)
    validation_count = len(interactions) - train_count
    if require_integer(
        manifest["trainInteractionCount"],
        "train interaction count",
        minimum=1,
        maximum=MAXIMUM_COLLECTION_SIZE,
    ) != train_count or require_integer(
        manifest["validationInteractionCount"],
        "validation interaction count",
        minimum=1,
        maximum=MAXIMUM_COLLECTION_SIZE,
    ) != validation_count:
        raise TrainerInputError("interaction split counts differ from the input manifest")
    split_preimage = {
        "interactions": [
            {"interactionId": interaction.interaction_id, "split": interaction.split}
            for interaction in interactions
        ]
    }
    split_sha256 = require_sha256(manifest["splitSha256"], "interaction split digest")
    if split_sha256 != sha256_canonical(split_preimage):
        raise TrainerInputError("interaction split differs from the input manifest")
    return ValidatedBundle(
        manifest=manifest,
        configuration_document_sha256=training_provenance["documentSha256"],
        catalog_document_sha256=catalog_provenance["documentSha256"],
        feature_schema_sha256=feature_schema_sha256,
        split_sha256=split_sha256,
        knowledge_points=root_points,
        actors=actors,
        problems=problems,
        interactions=interactions,
        configuration=configuration,
    )
