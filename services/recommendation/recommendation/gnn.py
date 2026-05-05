from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import pandas as pd

from .db import BankProblem, StudentRecord, SubmissionRecord
from .graph import TrainingGraph
from .knowledge import build_parent_edges, build_prerequisite_edges


@dataclass(frozen=True)
class GNNTrainingResult:
    metrics: dict[str, float | int | str]
    student_bank_scores: dict[int, dict[str, float]]
    model_path: str


def run_gnn_training(
    *,
    students: list[StudentRecord],
    submissions: list[SubmissionRecord],
    bank_problems: list[BankProblem],
    graph: TrainingGraph,
    run_dir: Path,
    config: dict[str, Any],
) -> GNNTrainingResult:
    torch, _hetero_data_cls = _require_gnn_runtime()
    data = _build_hetero_data(torch, students, submissions, bank_problems, graph)
    graph_dir = run_dir / "graph"
    graph_dir.mkdir(parents=True, exist_ok=True)
    torch.save(data, graph_dir / "hetero_data.pt")
    torch.save(
        {
            "student_to_idx": graph.student_to_idx,
            "problem_to_idx": graph.problem_to_idx,
            "knowledge_to_idx": graph.knowledge_to_idx,
            "bank_problem_ids": graph.bank_problem_ids,
        },
        graph_dir / "id_mappings.pt",
    )

    from .training.data_splitter import DataSplitter
    from .training.trainer import Trainer

    submissions_df = _submissions_dataframe(submissions)
    if submissions_df.empty:
        raise ValueError("recommendation training requires at least one submission")

    splitter = DataSplitter(
        submissions=submissions_df,
        student_to_idx=graph.student_to_idx,
        problem_to_idx=graph.problem_to_idx,
        student_column="nickname",
        problem_column="global_problem_id",
    )
    if splitter.unique_edges.empty:
        raise ValueError("recommendation training produced no student-problem edges")

    split = str(config.get("split", "leave_k"))
    val_ratio = float(config.get("val_ratio", 0.1))
    if split == "random":
        train_df, val_df, test_df = splitter.split_random(
            train_ratio=float(config.get("train_ratio", 0.8)),
            val_ratio=val_ratio,
            seed=int(config.get("seed", 42)),
        )
    else:
        train_df, val_df, test_df = splitter.split_leave_k_out(
            k=int(config.get("leave_k", 1)),
            val_ratio=val_ratio,
            seed=int(config.get("seed", 42)),
        )
    if train_df.empty:
        train_df = splitter.unique_edges.copy()
        val_df = splitter.unique_edges.iloc[0:0].copy()
        test_df = splitter.unique_edges.iloc[0:0].copy()

    train_edges = splitter.to_edge_index(train_df)
    val_edges = splitter.to_edge_index(val_df)
    test_edges = splitter.to_edge_index(test_df)

    train_data = data.clone()
    train_data["student", "submitted", "problem"].edge_index = train_edges
    if hasattr(train_data["student", "submitted", "problem"], "edge_attr"):
        del train_data["student", "submitted", "problem"].edge_attr

    model_type = str(config.get("model_type", config.get("model", "rgcn"))).lower()
    if model_type == "han":
        from .models.han import HANRecommender

        model = HANRecommender.from_hetero_data(
            data,
            hidden_dim=int(config.get("hidden_dim", 64)),
            num_layers=int(config.get("num_layers", 1)),
            heads=int(config.get("heads", 4)),
            decoder_type=str(config.get("decoder_type", "bilinear")),
        )
    elif model_type == "rgcn":
        from .models.rgcn import RGCNRecommender

        model = RGCNRecommender.from_hetero_data(
            data,
            hidden_dim=int(config.get("hidden_dim", 64)),
            num_layers=int(config.get("num_layers", 2)),
            decoder_type=str(config.get("decoder_type", "bilinear")),
        )
    else:
        raise ValueError(f"unsupported recommendation model_type: {model_type}")

    pos_targets = None
    pos_weights = None
    if "score_rate" in train_df.columns:
        pos_targets = torch.tensor(
            train_df["score_rate"].fillna(0).clip(0, 1).to_numpy(),
            dtype=torch.float32,
        )
        if bool(config.get("use_score_rate_weight", True)):
            pos_weights = 0.5 + 0.5 * pos_targets

    num_negatives = int(config.get("num_negatives", 5))

    def negative_sampler():
        neg_df = splitter.generate_negative_samples(
            train_df,
            num_negatives=num_negatives,
            seed=int(config.get("seed", 42)),
        )
        return splitter.to_edge_index(neg_df)

    model_path = run_dir / "model.pt"
    trainer = Trainer(
        model=model,
        learning_rate=float(config.get("learning_rate", config.get("lr", 0.01))),
        weight_decay=float(config.get("weight_decay", 1e-5)),
        loss_type=str(config.get("loss", "listwise")),
        temperature=float(config.get("temperature", 1.0)),
        aux_score_rate_weight=float(config.get("aux_score_rate_weight", 0.0)),
        graph_reg_weight=float(config.get("graph_reg_weight", 0.0)),
        contrastive_weight=float(config.get("contrastive_weight", 0.0)),
        contrastive_temperature=float(config.get("contrastive_temp", 0.2)),
        contrastive_dropout=float(config.get("contrastive_dropout", 0.1)),
        contrastive_sample_size=int(config.get("contrastive_sample_size", 512)),
        device=str(config.get("device", "cpu")),
    )
    history = trainer.train(
        data=train_data,
        train_edges=train_edges,
        val_edges=val_edges,
        negative_sampler=negative_sampler,
        num_students=len(graph.student_to_idx),
        num_problems=len(graph.problem_to_idx),
        positive_weights=pos_weights,
        positive_targets=pos_targets,
        epochs=int(config.get("epochs", 100)),
        batch_size=int(config.get("batch_size", 512)),
        patience=int(config.get("patience", 10)),
        num_negatives=num_negatives,
        save_path=model_path,
        eval_ignore_empty=bool(config.get("eval_ignore_empty", True)),
    )
    if not model_path.exists():
        trainer.save_checkpoint(model_path)

    test_metrics = trainer.evaluate(
        train_data,
        test_edges,
        len(graph.student_to_idx),
        len(graph.problem_to_idx),
        exclude_edges=train_edges if train_edges.numel() else None,
        ignore_empty_students=bool(config.get("eval_ignore_empty", True)),
    )
    scores = _score_bank_candidates(
        torch=torch,
        model=trainer.model,
        data=train_data,
        graph=graph,
        submissions=submissions,
        device=str(config.get("device", "cpu")),
    )
    metrics: dict[str, float | int | str] = {
        "model_type": model_type,
        "requested_device": str(config.get("device", "cpu")),
        "training_device": str(trainer.device),
        "train_edges": int(train_edges.size(1)),
        "val_edges": int(val_edges.size(1)),
        "test_edges": int(test_edges.size(1)),
        "best_val_hit_rate@10": float(trainer.best_val_score),
    }
    if trainer.device.type == "cuda" and torch.cuda.is_available():
        metrics["cuda_device_name"] = torch.cuda.get_device_name(trainer.device)
    for key, value in test_metrics.items():
        metrics[f"test_{key}"] = float(value)
    (run_dir / "training_history.json").write_text(
        json.dumps(history, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )
    return GNNTrainingResult(
        metrics=metrics,
        student_bank_scores=scores,
        model_path=str(model_path),
    )


def _require_gnn_runtime():
    try:
        import torch
        from torch_geometric.data import HeteroData
    except ImportError as exc:
        raise RuntimeError(
            "Recommendation training requires torch and torch-geometric. "
            "Install the standalone recommendation environment with `uv sync`."
        ) from exc
    return torch, HeteroData


def _submissions_dataframe(submissions: list[SubmissionRecord]) -> pd.DataFrame:
    return pd.DataFrame(
        [
            {
                "nickname": row.student_key,
                "global_problem_id": row.practice_problem_id,
                "time": row.submitted_at,
                "is_correct": row.is_correct,
                "score": row.score,
                "score_rate": row.score_rate,
            }
            for row in submissions
        ]
    )


def _build_hetero_data(
    torch,
    students: list[StudentRecord],
    submissions: list[SubmissionRecord],
    bank_problems: list[BankProblem],
    graph: TrainingGraph,
):
    from torch_geometric.data import HeteroData

    data = HeteroData()
    data["student"].x = torch.tensor(
        _student_features(students, submissions),
        dtype=torch.float32,
    )
    data["problem"].x = torch.tensor(
        _problem_features(submissions, bank_problems, graph),
        dtype=torch.float32,
    )
    data["knowledge"].x = torch.tensor(
        _knowledge_features(graph),
        dtype=torch.float32,
    )
    submitted_index, submitted_attr = _submitted_edges(torch, submissions, graph)
    data["student", "submitted", "problem"].edge_index = submitted_index
    data["student", "submitted", "problem"].edge_attr = submitted_attr

    belongs_to = _belongs_to_edges(torch, graph)
    if belongs_to.size(1) > 0:
        data["problem", "belongs_to", "knowledge"].edge_index = belongs_to

    parent = _edge_index_from_pairs(torch, build_parent_edges(graph.knowledge_to_idx))
    if parent.numel() > 0:
        data["knowledge", "parent", "knowledge"].edge_index = parent

    prereq = _edge_index_from_pairs(torch, build_prerequisite_edges(graph.knowledge_to_idx))
    if prereq.numel() > 0:
        data["knowledge", "prerequisite", "knowledge"].edge_index = prereq

    return data


def _student_features(
    students: list[StudentRecord],
    submissions: list[SubmissionRecord],
) -> list[list[float]]:
    by_student: dict[int, list[SubmissionRecord]] = {}
    for row in submissions:
        by_student.setdefault(row.student_id, []).append(row)
    features = []
    for student in students:
        rows = by_student.get(student.student_id, [])
        scores = [row.score_rate for row in rows]
        unique_problems = {row.practice_problem_id for row in rows}
        features.append(
            [
                float(student.rating) / 2000.0,
                float(len(rows)),
                float(len(unique_problems)),
                sum(scores) / len(scores) if scores else 0.0,
                max(scores) if scores else 0.0,
                sum(1.0 for row in rows if row.is_correct) / len(rows)
                if rows
                else 0.0,
            ]
        )
    return features


def _problem_features(
    submissions: list[SubmissionRecord],
    bank_problems: list[BankProblem],
    graph: TrainingGraph,
) -> list[list[float]]:
    by_problem: dict[str, list[SubmissionRecord]] = {}
    for row in submissions:
        by_problem.setdefault(row.practice_problem_id, []).append(row)
    bank = {problem.problem_id: problem for problem in bank_problems}
    tag_counts = _problem_tag_counts(graph)
    features = []
    for problem_id in graph.problem_to_idx:
        rows = by_problem.get(problem_id, [])
        bank_problem = bank.get(problem_id)
        if bank_problem is not None:
            total = max(bank_problem.submission_count, 1.0)
            difficulty = 1.0 - max(0.0, min(1.0, bank_problem.pass_count / total))
            popularity = bank_problem.submission_count
            tag_count = tag_counts.get(problem_id, len(bank_problem.tags))
            text_len = len(bank_problem.description or "")
            is_bank = 1.0
        else:
            scores = [row.score_rate for row in rows]
            difficulty = 1.0 - (sum(scores) / len(scores) if scores else 0.5)
            popularity = float(len(rows))
            tag_count = tag_counts.get(problem_id, 0)
            text_len = len(rows[0].problem_title or "") if rows else 0
            is_bank = 0.0
        features.append(
            [
                float(difficulty),
                float(popularity),
                float(tag_count),
                float(is_bank),
                min(float(text_len) / 1000.0, 1.0),
                1.0,
            ]
        )
    return features


def _knowledge_features(graph: TrainingGraph) -> list[list[float]]:
    problem_counts: dict[str, int] = {key: 0 for key in graph.knowledge_to_idx}
    for edge in graph.edges:
        if edge.get("type") != "belongs_to":
            continue
        tag = str(edge.get("dst") or "")
        if tag in problem_counts:
            problem_counts[tag] += 1
    parent_edges = build_parent_edges(graph.knowledge_to_idx)
    prereq_edges = build_prerequisite_edges(graph.knowledge_to_idx)
    child_counts: dict[int, int] = {}
    prereq_counts: dict[int, int] = {}
    for child, parent in parent_edges:
        child_counts[parent] = child_counts.get(parent, 0) + 1
    for target, _ in prereq_edges:
        prereq_counts[target] = prereq_counts.get(target, 0) + 1
    inverse = {idx: key for key, idx in graph.knowledge_to_idx.items()}
    return [
        [
            float(problem_counts.get(inverse[idx], 0)),
            float(child_counts.get(idx, 0)),
            float(prereq_counts.get(idx, 0)),
            1.0,
        ]
        for idx in range(len(graph.knowledge_to_idx))
    ]


def _submitted_edges(torch, submissions: list[SubmissionRecord], graph: TrainingGraph):
    pairs: dict[tuple[int, int], list[SubmissionRecord]] = {}
    for row in submissions:
        student_idx = graph.student_to_idx.get(row.student_key)
        problem_idx = graph.problem_to_idx.get(row.practice_problem_id)
        if student_idx is None or problem_idx is None:
            continue
        pairs.setdefault((student_idx, problem_idx), []).append(row)
    src = []
    dst = []
    attrs = []
    for (student_idx, problem_idx), rows in pairs.items():
        rows = sorted(
            rows,
            key=lambda item: item.submitted_at.timestamp()
            if item.submitted_at is not None
            else 0.0,
        )
        best = max(rows, key=lambda item: item.score_rate)
        src.append(student_idx)
        dst.append(problem_idx)
        attrs.append(
            [
                1.0 if best.is_correct else 0.0,
                float(best.score_rate),
                float(len(rows)),
                1.0 if rows[0].is_correct else 0.0,
                1.0 if rows[-1].is_correct else 0.0,
                float(best.score),
                0.0,
            ]
        )
    return (
        torch.tensor([src, dst], dtype=torch.long),
        torch.tensor(attrs, dtype=torch.float32),
    )


def _problem_tag_counts(graph: TrainingGraph) -> dict[str, int]:
    counts: dict[str, int] = {}
    for edge in graph.edges:
        if edge.get("type") != "belongs_to":
            continue
        problem_id = str(edge.get("src") or "")
        if problem_id:
            counts[problem_id] = counts.get(problem_id, 0) + 1
    return counts


def _belongs_to_edges(torch, graph: TrainingGraph):
    src = []
    dst = []
    for edge in graph.edges:
        if edge.get("type") != "belongs_to":
            continue
        problem_idx = graph.problem_to_idx.get(str(edge.get("src") or ""))
        if problem_idx is None:
            continue
        knowledge_idx = graph.knowledge_to_idx.get(str(edge.get("dst") or ""))
        if knowledge_idx is not None:
            src.append(problem_idx)
            dst.append(knowledge_idx)
    return torch.tensor([src, dst], dtype=torch.long)


def _edge_index_from_pairs(torch, pairs: list[tuple[int, int]]):
    if not pairs:
        return torch.empty((2, 0), dtype=torch.long)
    return torch.tensor(pairs, dtype=torch.long).t().contiguous()


def _score_bank_candidates(
    *,
    torch,
    model,
    data,
    graph: TrainingGraph,
    submissions: list[SubmissionRecord],
    device: str,
) -> dict[int, dict[str, float]]:
    model.eval()
    data = data.to(device)
    done: dict[int, set[str]] = {}
    for row in submissions:
        done.setdefault(row.student_id, set()).add(row.practice_problem_id)
    idx_to_student = {idx: int(key) for key, idx in graph.student_to_idx.items()}
    with torch.no_grad():
        embeddings = model.get_embeddings(data)
        bank_pairs = [
            (problem_id, graph.problem_to_idx[problem_id])
            for problem_id in graph.bank_problem_ids
            if problem_id in graph.problem_to_idx
        ]
        if not bank_pairs:
            return {student_id: {} for student_id in idx_to_student.values()}
        bank_indices = [idx for _, idx in bank_pairs]
        bank_tensor = torch.tensor(bank_indices, dtype=torch.long, device=data["problem"].x.device)
        problem_emb = embeddings["problem"][bank_tensor]
        result: dict[int, dict[str, float]] = {}
        for student_idx, student_id in idx_to_student.items():
            student_emb = embeddings["student"][student_idx].unsqueeze(0)
            scores = model.predict_link(
                student_emb.expand(problem_emb.size(0), -1),
                problem_emb,
            )
            by_problem: dict[str, float] = {}
            for local_idx, (problem_id, _problem_idx) in enumerate(bank_pairs):
                if problem_id in done.get(student_id, set()):
                    continue
                by_problem[problem_id] = float(scores[local_idx].detach().cpu().item())
            result[student_id] = by_problem
    return result
