from __future__ import annotations

import json
import sys
from pathlib import Path
from types import SimpleNamespace

SERVICE_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SERVICE_ROOT))

from recommendation.db import BankProblem, StudentRecord, SubmissionRecord
from recommendation.graph import build_training_graph, infer_practice_problem_tags
from recommendation.gnn import run_gnn_training
from recommendation.pipeline import run_pipeline


class FakePipelineRepo:
    def __init__(self) -> None:
        self.running: tuple[int, str] | None = None
        self.finished: dict[str, object] | None = None
        self.recommendations: dict[int, list[dict[str, object]]] = {}
        self.paths: dict[int, dict[str, object]] = {}
        self.events: list[dict[str, object]] = []

    def mark_run_running(self, run_id: int, artifact_path: str) -> None:
        self.running = (run_id, artifact_path)

    def mark_run_finished(
        self,
        run_id: int,
        *,
        status: str,
        metrics: dict[str, object],
        error_message: str | None = None,
    ) -> None:
        self.finished = {
            "run_id": run_id,
            "status": status,
            "metrics": metrics,
            "error_message": error_message,
        }

    def add_run_event(
        self,
        run_id: int,
        *,
        level: str,
        message: str,
        data: dict[str, object] | None = None,
    ) -> None:
        self.events.append(
            {
                "run_id": run_id,
                "level": level,
                "message": message,
                "data": data or {},
            }
        )

    def load_students(self) -> list[StudentRecord]:
        return [
            StudentRecord(
                student_id=1,
                student_no="20230001",
                student_name="Alice",
                rating=900,
            )
        ]

    def load_submissions(self) -> list[SubmissionRecord]:
        return [
            SubmissionRecord(
                student_id=1,
                student_key="1",
                practice_problem_id="P_WEAK",
                problem_title="基础输入输出练习",
                submitted_at=None,
                score=20.0,
                max_score=100.0,
                score_rate=0.2,
                verdict="Wrong Answer",
                is_correct=False,
            )
        ]

    def load_bank_problems(self) -> list[BankProblem]:
        return [
            BankProblem(
                problem_id="P_WEAK",
                title="练习弱项",
                description="输入输出弱项",
                link=None,
                submission_count=50,
                pass_count=20,
                tags=["A"],
                active=True,
            ),
            BankProblem(
                problem_id="P1001",
                title="基础输入输出",
                description="读取整数并输出结果",
                link="https://example.test/P1001",
                submission_count=100,
                pass_count=80,
                tags=["A"],
                active=True,
            ),
            BankProblem(
                problem_id="P9999",
                title="禁用题",
                description="不应进入推荐",
                link=None,
                submission_count=100,
                pass_count=1,
                tags=["A"],
                active=False,
            ),
        ]

    def load_practice_problem_tags(self) -> dict[str, list[str]]:
        return {"P_WEAK": ["A"]}

    def replace_problem_recommendations(
        self, run_id: int, payloads: dict[int, list[dict[str, object]]]
    ) -> None:
        _ = run_id
        self.recommendations = payloads

    def replace_learning_paths(
        self, run_id: int, payloads: dict[int, dict[str, object]]
    ) -> None:
        _ = run_id
        self.paths = payloads

    def load_model_run_config(self, run_id: int) -> dict[str, object]:
        _ = run_id
        return {
            "model_type": "rgcn",
            "path_max_len": 8,
        }


def test_pipeline_writes_recommendation_and_path_snapshots(
    tmp_path: Path, monkeypatch
) -> None:
    fake_repo = FakePipelineRepo()
    seen_training_config: dict[str, object] = {}
    monkeypatch.setattr(
        "recommendation.pipeline.load_db_config",
        lambda: SimpleNamespace(dsn="dbname=test"),
    )
    monkeypatch.setattr(
        "recommendation.pipeline.RecommendationRepository",
        lambda dsn: fake_repo,
    )
    monkeypatch.setattr(
        "recommendation.pipeline.run_gnn_training",
        lambda **kwargs: (
            seen_training_config.update(kwargs["config"])
            or SimpleNamespace(
                metrics={
                    "model_type": "rgcn",
                    "train_edges": 1,
                    "val_edges": 0,
                    "test_edges": 0,
                    "best_val_hit_rate@10": 0.0,
                },
                student_bank_scores={1: {"P1001": 0.95}},
                model_path=str(tmp_path / "33" / "model.pt"),
            )
        ),
    )

    metrics = run_pipeline(
        33,
        tmp_path,
        top_k=10,
        config_override={
            "device": "cuda",
            "batch_size": 128,
            "recommendation": {"model_score_weight": 0.5},
        },
    )

    assert fake_repo.running == (33, str(tmp_path / "33"))
    assert fake_repo.finished is not None
    assert fake_repo.finished["status"] == "success"
    assert metrics["students"] == 1
    assert fake_repo.recommendations[1][0]["problemId"] == "P1001"
    assert all(
        item["problemId"] != "P9999" for item in fake_repo.recommendations[1]
    )
    assert fake_repo.paths[1]["path"]
    graph_file = tmp_path / "33" / "graph.json"
    graph_payload = json.loads(graph_file.read_text(encoding="utf-8"))
    assert {node["kind"] for node in graph_payload["nodes"]} == {
        "student",
        "problem",
        "knowledge",
    }
    assert metrics["graph_edges"] >= 2
    assert seen_training_config["device"] == "cuda"
    assert seen_training_config["batch_size"] == 128
    assert isinstance(seen_training_config["recommendation"], dict)
    assert seen_training_config["recommendation"]["model_score_weight"] == 0.5
    assert "recommendation graph built" in {
        str(event["message"]) for event in fake_repo.events
    }
    metrics_file = tmp_path / "33" / "metrics.json"
    assert json.loads(metrics_file.read_text(encoding="utf-8"))["snapshots"] == 1


def test_gnn_training_smoke_writes_graph_artifacts_and_scores(tmp_path: Path) -> None:
    students = [
        StudentRecord(student_id=1, student_no="S1", student_name="Alice", rating=900),
        StudentRecord(student_id=2, student_no="S2", student_name="Bob", rating=950),
    ]
    submissions = [
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_A",
            problem_title="输入输出 A",
            submitted_at=None,
            score=80,
            max_score=100,
            score_rate=0.8,
            verdict="Accepted",
            is_correct=True,
        ),
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_B",
            problem_title="数组 B",
            submitted_at=None,
            score=20,
            max_score=100,
            score_rate=0.2,
            verdict="Wrong Answer",
            is_correct=False,
        ),
        SubmissionRecord(
            student_id=2,
            student_key="2",
            practice_problem_id="P_B",
            problem_title="数组 B",
            submitted_at=None,
            score=100,
            max_score=100,
            score_rate=1.0,
            verdict="Accepted",
            is_correct=True,
        ),
        SubmissionRecord(
            student_id=2,
            student_key="2",
            practice_problem_id="P_C",
            problem_title="递归 C",
            submitted_at=None,
            score=40,
            max_score=100,
            score_rate=0.4,
            verdict="Wrong Answer",
            is_correct=False,
        ),
    ]
    bank_problems = [
        BankProblem("P_A", "A", "desc", None, 10, 8, ["A"], True),
        BankProblem("P_B", "B", "desc", None, 10, 3, ["B"], True),
        BankProblem("P_C", "C", "desc", None, 10, 6, ["C"], True),
        BankProblem("P_D", "D", "desc", None, 10, 5, ["A"], True),
    ]
    practice_tags = infer_practice_problem_tags(submissions, bank_problems)
    graph = build_training_graph(
        students,
        submissions,
        bank_problems,
        practice_problem_tags=practice_tags,
    )

    result = run_gnn_training(
        students=students,
        submissions=submissions,
        bank_problems=bank_problems,
        graph=graph,
        run_dir=tmp_path,
        config={
            "model_type": "rgcn",
            "device": "cpu",
            "epochs": 2,
            "batch_size": 2,
            "hidden_dim": 16,
            "num_layers": 1,
            "num_negatives": 1,
            "split": "random",
            "train_ratio": 0.75,
            "val_ratio": 0.25,
            "patience": 2,
        },
    )

    assert result.metrics["model_type"] == "rgcn"
    assert result.metrics["train_edges"] > 0
    assert Path(result.model_path).exists()
    assert (tmp_path / "graph" / "hetero_data.pt").exists()
    assert (tmp_path / "graph" / "id_mappings.pt").exists()
    assert set(result.student_bank_scores) == {1, 2}
    assert "P_D" in result.student_bank_scores[1]


def test_graph_uses_practice_tags_without_bank_problem() -> None:
    students = [
        StudentRecord(student_id=1, student_no="S1", student_name="Alice", rating=900)
    ]
    submissions = [
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="datastructure_exam_7-1-1",
            problem_title="链表练习",
            submitted_at=None,
            score=60,
            max_score=100,
            score_rate=0.6,
            verdict="Partial",
            is_correct=False,
        )
    ]
    graph = build_training_graph(
        students,
        submissions,
        [],
        practice_problem_tags={"datastructure_exam_7-1-1": ["链表", "线性结构"]},
    )

    assert "链表" in graph.knowledge_to_idx
    assert any(
        edge["type"] == "belongs_to"
        and edge["src"] == "datastructure_exam_7-1-1"
        and edge["dst"] == "链表"
        for edge in graph.edges
    )


def test_gnn_training_allows_empty_bank_problem_list(tmp_path: Path) -> None:
    students = [
        StudentRecord(student_id=1, student_no="S1", student_name="Alice", rating=900),
        StudentRecord(student_id=2, student_no="S2", student_name="Bob", rating=950),
    ]
    submissions = [
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_A",
            problem_title="输入输出 A",
            submitted_at=None,
            score=100,
            max_score=100,
            score_rate=1.0,
            verdict="Accepted",
            is_correct=True,
        ),
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_B",
            problem_title="链表 B",
            submitted_at=None,
            score=20,
            max_score=100,
            score_rate=0.2,
            verdict="Wrong Answer",
            is_correct=False,
        ),
        SubmissionRecord(
            student_id=2,
            student_key="2",
            practice_problem_id="P_A",
            problem_title="输入输出 A",
            submitted_at=None,
            score=60,
            max_score=100,
            score_rate=0.6,
            verdict="Partial",
            is_correct=False,
        ),
        SubmissionRecord(
            student_id=2,
            student_key="2",
            practice_problem_id="P_B",
            problem_title="链表 B",
            submitted_at=None,
            score=100,
            max_score=100,
            score_rate=1.0,
            verdict="Accepted",
            is_correct=True,
        ),
    ]
    graph = build_training_graph(
        students,
        submissions,
        [],
        practice_problem_tags={"P_A": ["输入输出"], "P_B": ["链表"]},
    )

    result = run_gnn_training(
        students=students,
        submissions=submissions,
        bank_problems=[],
        graph=graph,
        run_dir=tmp_path,
        config={
            "model_type": "rgcn",
            "device": "cpu",
            "epochs": 1,
            "batch_size": 2,
            "hidden_dim": 16,
            "num_layers": 1,
            "num_negatives": 1,
            "split": "random",
            "train_ratio": 0.75,
            "val_ratio": 0.25,
            "patience": 1,
        },
    )

    assert result.metrics["model_type"] == "rgcn"
    assert result.student_bank_scores == {1: {}, 2: {}}
    assert (tmp_path / "graph" / "hetero_data.pt").exists()
