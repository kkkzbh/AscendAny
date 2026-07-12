"""The sole numerical model implemented by the isolated trainer."""

from __future__ import annotations

import math
import os

import torch
from torch import nn
from torch.nn import functional as functional

from .contract import TrainerRuntimeError


DISCRIMINATION_EPSILON = 1e-6


def configure_torch(accelerator: str, seed: int) -> torch.device:
    """Select the configured capability and make stochastic operations repeatable."""

    if accelerator == "cuda":
        if not torch.cuda.is_available():
            raise TrainerRuntimeError("configured CUDA accelerator is unavailable")
        if os.environ.get("CUBLAS_WORKSPACE_CONFIG") != ":4096:8":
            raise TrainerRuntimeError("deterministic CUDA workspace configuration is unavailable")
        if os.environ.get("CUDA_VISIBLE_DEVICES") != "0" or any(
            key not in os.environ
            for key in ("MKL_NUM_THREADS", "OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS")
        ):
            raise TrainerRuntimeError("production CUDA compute environment is incomplete")
        device = torch.device("cuda")
        torch.cuda.manual_seed_all(seed)
        torch.backends.cuda.matmul.allow_tf32 = False
        torch.backends.cudnn.allow_tf32 = False
    elif accelerator == "cpu":
        device = torch.device("cpu")
    else:  # The contract validator owns the public error for this condition.
        raise TrainerRuntimeError("configured accelerator is unsupported")
    torch.manual_seed(seed)
    torch.use_deterministic_algorithms(True)
    return device


def unit_discrimination_raw() -> float:
    return math.log(math.expm1(1.0 - DISCRIMINATION_EPSILON))


class KnowledgeMIRT(nn.Module):
    """Multidimensional knowledge IRT with shared feature regressions."""

    def __init__(
        self,
        *,
        actor_count: int,
        problem_count: int,
        knowledge_count: int,
        actor_feature_count: int,
        problem_feature_count: int,
        initial_problem_difficulties: list[float],
        device: torch.device,
    ) -> None:
        super().__init__()
        options = {"device": device, "dtype": torch.float64}
        self.student_feature_weights = nn.Parameter(
            torch.zeros((knowledge_count, actor_feature_count), **options)
        )
        self.actor_residuals = nn.Parameter(torch.zeros((actor_count, knowledge_count), **options))
        self.problem_feature_weights = nn.Parameter(torch.zeros(problem_feature_count, **options))
        self.problem_difficulty_residuals = nn.Parameter(
            torch.tensor(initial_problem_difficulties, **options)
        )
        self.problem_raw_discriminations = nn.Parameter(
            torch.full((problem_count,), unit_discrimination_raw(), **options)
        )

    def forward(
        self,
        actor_indices: torch.Tensor,
        problem_indices: torch.Tensor,
        actor_features: torch.Tensor,
        problem_features: torch.Tensor,
        problem_knowledge_weights: torch.Tensor,
    ) -> torch.Tensor:
        selected_actor_features = actor_features.index_select(0, actor_indices)
        selected_actor_residuals = self.actor_residuals.index_select(0, actor_indices)
        theta = selected_actor_features @ self.student_feature_weights.transpose(0, 1)
        theta = theta + selected_actor_residuals

        selected_problem_features = problem_features.index_select(0, problem_indices)
        difficulty = selected_problem_features @ self.problem_feature_weights
        difficulty = difficulty + self.problem_difficulty_residuals.index_select(0, problem_indices)
        discrimination = functional.softplus(
            self.problem_raw_discriminations.index_select(0, problem_indices)
        ) + DISCRIMINATION_EPSILON
        knowledge_weights = problem_knowledge_weights.index_select(0, problem_indices)
        ability = (knowledge_weights * theta).sum(dim=1)
        return discrimination * (ability - difficulty)
