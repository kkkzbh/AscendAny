"""Deterministic optimization and serialization of knowledge_mirt_v1."""

from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal
import math

import torch
from torch.nn import functional as functional

from .contract import (
    ACTOR_FEATURE_IDS,
    ALGORITHM,
    MODEL_SCHEMA,
    OUTPUT_PROTOCOL,
    PARAMETER_SCHEMA,
    PROBLEM_FEATURE_IDS,
    TrainerOutputError,
    ValidatedBundle,
    require_sha256,
    sha256_canonical,
    validate_input_bundle,
)
from .model import KnowledgeMIRT, configure_torch


MAXIMUM_PARAMETER_ABSOLUTE_VALUE = 100.0


@dataclass(frozen=True)
class CorpusTensors:
    actor_features: torch.Tensor
    problem_features: torch.Tensor
    problem_knowledge_weights: torch.Tensor
    train_actor_indices: torch.Tensor
    train_problem_indices: torch.Tensor
    train_targets: torch.Tensor
    validation_actor_indices: torch.Tensor
    validation_problem_indices: torch.Tensor
    validation_targets: torch.Tensor
    actor_means: tuple[float, ...]
    actor_scales: tuple[float, ...]
    problem_means: tuple[float, ...]
    problem_scales: tuple[float, ...]


def _decimal(
    value: float,
    label: str,
    *,
    minimum: float | None = None,
    maximum_absolute: float | None = MAXIMUM_PARAMETER_ABSOLUTE_VALUE,
) -> Decimal:
    if not math.isfinite(value) or (
        maximum_absolute is not None and abs(value) > maximum_absolute
    ):
        raise TrainerOutputError(f"{label} is non-finite or exceeds its numerical bound")
    if minimum is not None and value < minimum:
        raise TrainerOutputError(f"{label} is below its model contract range")
    return Decimal(repr(float(value)))


def _tensor_vector(value: torch.Tensor, label: str) -> list[Decimal]:
    return [_decimal(item, label) for item in value.detach().cpu().tolist()]


def _tensor_matrix(value: torch.Tensor, label: str) -> list[list[Decimal]]:
    return [
        [_decimal(item, f"{label}[{row_index}]") for item in row]
        for row_index, row in enumerate(value.detach().cpu().tolist())
    ]


def _normalize_features(
    rows: list[tuple[float, ...]],
) -> tuple[list[list[float]], tuple[float, ...], tuple[float, ...]]:
    means = tuple(
        math.fsum(row[column] for row in rows) / len(rows)
        for column in range(len(rows[0]))
    )
    scales = tuple(
        math.sqrt(
            math.fsum((row[column] - means[column]) ** 2 for row in rows) / len(rows)
        )
        for column in range(len(rows[0]))
    )
    scales = tuple(scale if scale != 0.0 else 1.0 for scale in scales)
    normalized = [
        [(value - means[column]) / scales[column] for column, value in enumerate(row)]
        for row in rows
    ]
    return normalized, means, scales


def _build_tensors(bundle: ValidatedBundle, device: torch.device) -> CorpusTensors:
    actor_index = {actor.actor_id: index for index, actor in enumerate(bundle.actors)}
    problem_index = {problem.problem_key: index for index, problem in enumerate(bundle.problems)}
    train = bundle.train_interactions
    validation = bundle.validation_interactions

    normalized_actor_features, actor_means, actor_scales = _normalize_features(
        [actor.features for actor in bundle.actors]
    )
    normalized_problem_features, problem_means, problem_scales = _normalize_features(
        [problem.features for problem in bundle.problems]
    )
    actor_features = torch.tensor(normalized_actor_features, device=device, dtype=torch.float64)
    problem_features = torch.tensor(normalized_problem_features, device=device, dtype=torch.float64)
    problem_knowledge_weights = torch.tensor(
        [problem.knowledge_weights for problem in bundle.problems], device=device, dtype=torch.float64
    )
    train_actor_indices = torch.tensor(
        [actor_index[item.actor_id] for item in train], device=device, dtype=torch.int64
    )
    train_problem_indices = torch.tensor(
        [problem_index[item.problem_key] for item in train], device=device, dtype=torch.int64
    )
    train_targets = torch.tensor(
        [item.target_score_rate for item in train], device=device, dtype=torch.float64
    )
    validation_actor_indices = torch.tensor(
        [actor_index[item.actor_id] for item in validation], device=device, dtype=torch.int64
    )
    validation_problem_indices = torch.tensor(
        [problem_index[item.problem_key] for item in validation], device=device, dtype=torch.int64
    )
    validation_targets = torch.tensor(
        [item.target_score_rate for item in validation], device=device, dtype=torch.float64
    )
    return CorpusTensors(
        actor_features=actor_features,
        problem_features=problem_features,
        problem_knowledge_weights=problem_knowledge_weights,
        train_actor_indices=train_actor_indices,
        train_problem_indices=train_problem_indices,
        train_targets=train_targets,
        validation_actor_indices=validation_actor_indices,
        validation_problem_indices=validation_problem_indices,
        validation_targets=validation_targets,
        actor_means=actor_means,
        actor_scales=actor_scales,
        problem_means=problem_means,
        problem_scales=problem_scales,
    )


def _initial_problem_difficulties(bundle: ValidatedBundle) -> tuple[list[float], list[float]]:
    scores: dict[str, list[float]] = {problem.problem_key: [] for problem in bundle.problems}
    for interaction in bundle.train_interactions:
        scores[interaction.problem_key].append(interaction.target_score_rate)
    difficulties: list[float] = []
    probabilities: list[float] = []
    for problem in bundle.problems:
        values = scores[problem.problem_key]
        probability = (math.fsum(values) + 1.0) / (len(values) + 2.0)
        probabilities.append(probability)
        difficulties.append(-math.log(probability / (1.0 - probability)))
    return difficulties, probabilities


def _model_logits(
    model: KnowledgeMIRT,
    actor_indices: torch.Tensor,
    problem_indices: torch.Tensor,
    actor_features: torch.Tensor,
    problem_features: torch.Tensor,
    knowledge_weights: torch.Tensor,
) -> torch.Tensor:
    return model(
        actor_indices,
        problem_indices,
        actor_features,
        problem_features,
        knowledge_weights,
    )


def _log_loss(logits: torch.Tensor, targets: torch.Tensor) -> torch.Tensor:
    return functional.binary_cross_entropy_with_logits(logits, targets, reduction="mean")


def _evaluate(
    model: KnowledgeMIRT,
    actor_indices: torch.Tensor,
    problem_indices: torch.Tensor,
    targets: torch.Tensor,
    actor_features: torch.Tensor,
    problem_features: torch.Tensor,
    knowledge_weights: torch.Tensor,
) -> tuple[float, float]:
    model.eval()
    with torch.inference_mode():
        logits = _model_logits(
            model,
            actor_indices,
            problem_indices,
            actor_features,
            problem_features,
            knowledge_weights,
        )
        loss = float(_log_loss(logits, targets).item())
        brier = float(torch.mean((torch.sigmoid(logits) - targets) ** 2).item())
    return loss, brier


def _baseline_validation_metrics(
    bundle: ValidatedBundle,
    problem_probabilities: list[float],
) -> tuple[float, float]:
    problem_index = {problem.problem_key: index for index, problem in enumerate(bundle.problems)}
    loss_terms: list[float] = []
    brier_terms: list[float] = []
    for interaction in bundle.validation_interactions:
        probability = problem_probabilities[problem_index[interaction.problem_key]]
        target = interaction.target_score_rate
        loss_terms.append(-(target * math.log(probability) + (1.0 - target) * math.log1p(-probability)))
        brier_terms.append((probability - target) ** 2)
    return math.fsum(loss_terms) / len(loss_terms), math.fsum(brier_terms) / len(brier_terms)


def _fit(
    bundle: ValidatedBundle,
) -> tuple[KnowledgeMIRT, dict[str, int | float], CorpusTensors]:
    configuration = bundle.configuration
    device = configure_torch(configuration.accelerator, configuration.seed)
    tensors = _build_tensors(bundle, device)
    difficulties, problem_probabilities = _initial_problem_difficulties(bundle)
    model = KnowledgeMIRT(
        actor_count=len(bundle.actors),
        problem_count=len(bundle.problems),
        knowledge_count=len(bundle.knowledge_points),
        actor_feature_count=len(ACTOR_FEATURE_IDS),
        problem_feature_count=len(PROBLEM_FEATURE_IDS),
        initial_problem_difficulties=difficulties,
        device=device,
    )
    initial_train_loss, _ = _evaluate(
        model,
        tensors.train_actor_indices,
        tensors.train_problem_indices,
        tensors.train_targets,
        tensors.actor_features,
        tensors.problem_features,
        tensors.problem_knowledge_weights,
    )
    baseline_validation_loss, _ = _baseline_validation_metrics(bundle, problem_probabilities)
    optimizer = torch.optim.AdamW(
        model.parameters(), lr=configuration.learning_rate, weight_decay=configuration.weight_decay
    )
    permutation_generator = torch.Generator(device="cpu")
    permutation_generator.manual_seed(configuration.seed)
    best_validation_loss = math.inf
    best_epoch = 0
    best_state: dict[str, torch.Tensor] | None = None
    stale_epochs = 0
    epochs_completed = 0

    for epoch in range(1, configuration.epochs + 1):
        model.train()
        permutation = torch.randperm(len(tensors.train_targets), generator=permutation_generator)
        for start in range(0, len(permutation), configuration.batch_size):
            batch = permutation[start : start + configuration.batch_size].to(device=device)
            optimizer.zero_grad(set_to_none=True)
            logits = _model_logits(
                model,
                tensors.train_actor_indices.index_select(0, batch),
                tensors.train_problem_indices.index_select(0, batch),
                tensors.actor_features,
                tensors.problem_features,
                tensors.problem_knowledge_weights,
            )
            loss = _log_loss(logits, tensors.train_targets.index_select(0, batch))
            loss.backward()
            optimizer.step()
        epochs_completed = epoch
        validation_loss, _ = _evaluate(
            model,
            tensors.validation_actor_indices,
            tensors.validation_problem_indices,
            tensors.validation_targets,
            tensors.actor_features,
            tensors.problem_features,
            tensors.problem_knowledge_weights,
        )
        if validation_loss < best_validation_loss:
            best_validation_loss = validation_loss
            best_epoch = epoch
            best_state = {
                name: parameter.detach().cpu().clone()
                for name, parameter in model.state_dict().items()
            }
            stale_epochs = 0
        else:
            stale_epochs += 1
            if stale_epochs >= configuration.patience:
                break

    if best_state is None or best_epoch == 0:
        raise TrainerOutputError("training completed without a finite best epoch")
    model.load_state_dict(best_state)
    final_train_loss, _ = _evaluate(
        model,
        tensors.train_actor_indices,
        tensors.train_problem_indices,
        tensors.train_targets,
        tensors.actor_features,
        tensors.problem_features,
        tensors.problem_knowledge_weights,
    )
    validation_loss, validation_brier = _evaluate(
        model,
        tensors.validation_actor_indices,
        tensors.validation_problem_indices,
        tensors.validation_targets,
        tensors.actor_features,
        tensors.problem_features,
        tensors.problem_knowledge_weights,
    )
    diagnostics: dict[str, int | float] = {
        "epochsCompleted": epochs_completed,
        "bestEpoch": best_epoch,
        "initialTrainLogLoss": initial_train_loss,
        "finalTrainLogLoss": final_train_loss,
        "reportedBaselineValidationLogLoss": baseline_validation_loss,
        "reportedValidationLogLoss": validation_loss,
        "reportedValidationBrier": validation_brier,
    }
    return model, diagnostics, tensors


def _serialize_parameters(
    model: KnowledgeMIRT,
    bundle: ValidatedBundle,
    tensors: CorpusTensors,
) -> dict[str, object]:
    parameters: dict[str, object] = {
        "normalization": {
            "actorMeans": [_decimal(value, "actor feature mean") for value in tensors.actor_means],
            "actorScales": [_decimal(value, "actor feature scale") for value in tensors.actor_scales],
            "problemMeans": [
                _decimal(value, "problem feature mean") for value in tensors.problem_means
            ],
            "problemScales": [
                _decimal(value, "problem feature scale") for value in tensors.problem_scales
            ],
        },
        "studentFeatureWeights": _tensor_matrix(
            model.student_feature_weights, "student feature weight"
        ),
        "actorResiduals": [
            {
                "actorId": actor.actor_id,
                "values": _tensor_vector(model.actor_residuals[index], "actor residual"),
            }
            for index, actor in enumerate(bundle.actors)
        ],
        "problemFeatureWeights": _tensor_vector(
            model.problem_feature_weights, "problem feature weight"
        ),
        "problems": [
            {
                "problemKey": problem.problem_key,
                "difficultyResidual": _decimal(
                    float(model.problem_difficulty_residuals[index].detach().cpu().item()),
                    "problem difficulty residual",
                ),
                "rawDiscrimination": _decimal(
                    float(model.problem_raw_discriminations[index].detach().cpu().item()),
                    "problem raw discrimination",
                ),
            }
            for index, problem in enumerate(bundle.problems)
        ],
    }
    return parameters


def train(
    value: dict[str, object],
    expected_manifest_sha256: str,
    runtime_attestation: dict[str, str],
) -> dict[str, object]:
    attestation_fields = {
        "hostCapabilitySha256",
        "runtimeAttestationSha256",
        "runtimeConstructionSha256",
        "runtimeProvenanceSha256",
        "runtimeTreeSha256",
    }
    if set(runtime_attestation) != attestation_fields:
        raise TrainerOutputError("runtime attestation field set is invalid")
    for field in sorted(attestation_fields):
        require_sha256(runtime_attestation[field], f"runtime attestation {field}")
    bundle = validate_input_bundle(value, expected_manifest_sha256)
    model, raw_diagnostics, tensors = _fit(bundle)
    parameters = _serialize_parameters(model, bundle, tensors)
    parameter_sha256 = sha256_canonical(parameters)
    manifest = bundle.manifest
    diagnostics = {
        "epochsCompleted": Decimal(raw_diagnostics["epochsCompleted"]),
        "bestEpoch": Decimal(raw_diagnostics["bestEpoch"]),
        "initialTrainLogLoss": _decimal(
            float(raw_diagnostics["initialTrainLogLoss"]),
            "initial train log-loss",
            minimum=0.0,
            maximum_absolute=None,
        ),
        "finalTrainLogLoss": _decimal(
            float(raw_diagnostics["finalTrainLogLoss"]),
            "final train log-loss",
            minimum=0.0,
            maximum_absolute=None,
        ),
        "reportedBaselineValidationLogLoss": _decimal(
            float(raw_diagnostics["reportedBaselineValidationLogLoss"]),
            "baseline validation log-loss",
            minimum=0.0,
            maximum_absolute=None,
        ),
        "reportedValidationLogLoss": _decimal(
            float(raw_diagnostics["reportedValidationLogLoss"]),
            "validation log-loss",
            minimum=0.0,
            maximum_absolute=None,
        ),
        "reportedValidationBrier": _decimal(
            float(raw_diagnostics["reportedValidationBrier"]),
            "validation Brier score",
            minimum=0.0,
            maximum_absolute=None,
        ),
    }
    for key in (
        "initialTrainLogLoss",
        "finalTrainLogLoss",
        "reportedBaselineValidationLogLoss",
        "reportedValidationLogLoss",
    ):
        if diagnostics[key] <= Decimal(0):
            raise TrainerOutputError(f"{key} must be positive")
    if diagnostics["reportedValidationBrier"] > Decimal(1):
        raise TrainerOutputError("validation Brier score exceeds one")
    return {
        "protocol": OUTPUT_PROTOCOL,
        "inputManifestSha256": expected_manifest_sha256,
        "model": {
            "schema": MODEL_SCHEMA,
            "manifest": {
                "algorithm": ALGORITHM,
                "parameterSchema": PARAMETER_SCHEMA,
                "parameterSha256": parameter_sha256,
                "inputManifestSha256": expected_manifest_sha256,
                "trainingConfigurationSha256": bundle.configuration_document_sha256,
                "knowledgeCatalogSha256": bundle.catalog_document_sha256,
                "featureSchemaSha256": bundle.feature_schema_sha256,
                "splitSha256": bundle.split_sha256,
                "knowledgePointCount": manifest["knowledgePointCount"],
                "actorFeatureCount": Decimal(len(ACTOR_FEATURE_IDS)),
                "problemFeatureCount": Decimal(len(PROBLEM_FEATURE_IDS)),
                "actorCount": manifest["actorCount"],
                "problemCount": manifest["problemCount"],
                "trainInteractionCount": manifest["trainInteractionCount"],
                "validationInteractionCount": manifest["validationInteractionCount"],
                "runtimeConstructionSha256": runtime_attestation[
                    "runtimeConstructionSha256"
                ],
                "runtimeProvenanceSha256": runtime_attestation[
                    "runtimeProvenanceSha256"
                ],
                "runtimeTreeSha256": runtime_attestation["runtimeTreeSha256"],
                "hostCapabilitySha256": runtime_attestation[
                    "hostCapabilitySha256"
                ],
                "runtimeAttestationSha256": runtime_attestation[
                    "runtimeAttestationSha256"
                ],
                "torchVersion": str(torch.__version__),
                "accelerator": bundle.configuration.accelerator,
                "seed": Decimal(bundle.configuration.seed),
                "configuredEpochs": Decimal(bundle.configuration.epochs),
                "bestEpoch": Decimal(raw_diagnostics["bestEpoch"]),
                "actorFeatureIds": list(ACTOR_FEATURE_IDS),
                "problemFeatureIds": list(PROBLEM_FEATURE_IDS),
            },
            "parameters": parameters,
            "diagnostics": diagnostics,
        },
    }
