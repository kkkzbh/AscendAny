from __future__ import annotations

import argparse
import json
import traceback
from pathlib import Path
from typing import Any

import yaml

from .config import load_db_config
from .db import RecommendationRepository
from .gnn import run_gnn_training
from .graph import build_training_graph, infer_practice_problem_tags
from .pathing import build_learning_paths
from .scoring import BankRecommendationConfig, build_profiles, score_recommendations


def run_pipeline(
    run_id: int,
    artifacts_dir: Path,
    *,
    top_k: int = 20,
    config_override: dict[str, Any] | None = None,
) -> dict[str, Any]:
    artifacts_dir.mkdir(parents=True, exist_ok=True)
    run_dir = artifacts_dir / str(run_id)
    run_dir.mkdir(parents=True, exist_ok=True)

    repo = RecommendationRepository(load_db_config().dsn)
    repo.mark_run_running(run_id, str(run_dir))
    run_config = repo.load_model_run_config(run_id)
    if config_override:
        run_config = _deep_merge(run_config, config_override)
    repo.add_run_event(
        run_id,
        level="info",
        message="recommendation data export started",
        data={
            "model_type": run_config.get(
                "model_type",
                run_config.get("model", "rgcn"),
            )
        },
    )
    raw_rec_config = run_config.get("recommendation")
    rec_config = BankRecommendationConfig.from_dict(
        raw_rec_config if isinstance(raw_rec_config, dict) else None
    )
    students = repo.load_students()
    submissions = repo.load_submissions()
    bank_problems = repo.load_bank_problems()
    practice_tags = repo.load_practice_problem_tags()
    repo.add_run_event(
        run_id,
        level="info",
        message="recommendation data export completed",
        data={
            "students": len(students),
            "submissions": len(submissions),
            "bank_problems": len(bank_problems),
            "practice_problem_tags": sum(len(tags) for tags in practice_tags.values()),
        },
    )
    inferred_tags = infer_practice_problem_tags(submissions, bank_problems)
    for problem_id, tags in inferred_tags.items():
        if problem_id not in practice_tags:
            practice_tags[problem_id] = tags
    graph = build_training_graph(
        students,
        submissions,
        bank_problems,
        practice_problem_tags=practice_tags,
    )
    repo.add_run_event(
        run_id,
        level="info",
        message="recommendation graph built",
        data={"nodes": graph.node_count, "edges": graph.edge_count},
    )

    training = run_gnn_training(
        students=students,
        submissions=submissions,
        bank_problems=bank_problems,
        graph=graph,
        run_dir=run_dir,
        config=run_config,
    )
    repo.add_run_event(
        run_id,
        level="info",
        message="recommendation model training completed",
        data=training.metrics,
    )
    profiles = build_profiles(
        students,
        submissions,
        practice_tags,
        config=rec_config,
    )

    recommendations = score_recommendations(
        profiles,
        bank_problems,
        model_scores=training.student_bank_scores,
        top_k=top_k,
        config=rec_config,
    )
    paths = build_learning_paths(
        profiles,
        config=rec_config,
        max_targets=int(run_config.get("path_top_n_targets", 5)),
        max_path_len=int(run_config.get("path_max_len", 8)),
        min_evidence=int(run_config.get("path_min_evidence", 1)),
        include_mastered=bool(run_config.get("path_include_mastered", False)),
        mastered_threshold=float(run_config.get("path_mastered_threshold", 0.8)),
    )

    repo.replace_problem_recommendations(run_id, recommendations)
    repo.replace_learning_paths(run_id, paths)
    repo.add_run_event(
        run_id,
        level="info",
        message="recommendation snapshots written",
        data={
            "recommendation_snapshots": len(recommendations),
            "learning_path_snapshots": len(paths),
        },
    )

    (run_dir / "graph.json").write_text(
        json.dumps(
            {
                "nodes": graph.nodes,
                "edges": graph.edges,
                "student_to_idx": graph.student_to_idx,
                "problem_to_idx": graph.problem_to_idx,
                "knowledge_to_idx": graph.knowledge_to_idx,
                "bank_problem_ids": graph.bank_problem_ids,
            },
            ensure_ascii=False,
            indent=2,
            default=str,
        ),
        encoding="utf-8",
    )
    metrics = {
        "students": len(students),
        "submissions": len(submissions),
        "bank_problems": len(bank_problems),
        "practice_problem_tag_rows": sum(len(tags) for tags in practice_tags.values()),
        "graph_nodes": graph.node_count,
        "graph_edges": graph.edge_count,
        "snapshots": len(recommendations),
        "model_type": training.metrics.get(
            "model_type",
            run_config.get("model_type", "rgcn"),
        ),
        "model_path": training.model_path,
        **training.metrics,
    }
    (run_dir / "metrics.json").write_text(
        json.dumps(metrics, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )
    repo.mark_run_finished(run_id, status="success", metrics=metrics)
    repo.add_run_event(
        run_id,
        level="info",
        message="recommendation pipeline finished",
        data={"metrics_path": str(run_dir / "metrics.json")},
    )
    return metrics


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-id", type=int, required=True)
    parser.add_argument("--artifacts-dir", type=Path, required=True)
    parser.add_argument("--top-k", type=int, default=20)
    parser.add_argument(
        "--config",
        type=Path,
        help="Optional YAML/JSON training config merged over the DB run config.",
    )
    args = parser.parse_args(argv)

    try:
        config_override = _load_config_file(args.config) if args.config else None
        run_pipeline(
            args.run_id,
            args.artifacts_dir,
            top_k=args.top_k,
            config_override=config_override,
        )
    except Exception as exc:  # noqa: BLE001
        repo = RecommendationRepository(load_db_config().dsn)
        try:
            repo.add_run_event(
                args.run_id,
                level="error",
                message="recommendation pipeline failed",
                data={"error": str(exc)[:2000]},
            )
        except Exception:
            pass
        repo.mark_run_finished(
            args.run_id,
            status="failed",
            metrics={},
            error_message=str(exc)[:2000],
        )
        trace_path = args.artifacts_dir / str(args.run_id) / "error.txt"
        trace_path.parent.mkdir(parents=True, exist_ok=True)
        trace_path.write_text(traceback.format_exc(), encoding="utf-8")
        return 1
    return 0


def _load_config_file(path: Path) -> dict[str, Any]:
    raw = path.read_text(encoding="utf-8")
    if path.suffix.lower() == ".json":
        payload = json.loads(raw)
    else:
        payload = yaml.safe_load(raw)
    if payload is None:
        return {}
    if not isinstance(payload, dict):
        raise ValueError(f"training config must be a mapping: {path}")
    return payload


def _deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        current = merged.get(key)
        if isinstance(current, dict) and isinstance(value, dict):
            merged[key] = _deep_merge(current, value)
        else:
            merged[key] = value
    return merged


if __name__ == "__main__":
    raise SystemExit(main())
