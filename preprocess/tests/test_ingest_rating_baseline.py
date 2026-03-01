from __future__ import annotations

from contextlib import contextmanager
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from preprocess.config import Settings
from preprocess.models import ExamBundle, ExamMeta, ExamUnit, ParticipantRow

from preprocess.derive.rating import RatingResult
import preprocess.load.ingest_service as ingest_module
from preprocess.load.ingest_service import IngestService


class _FakeConn:
    @contextmanager
    def transaction(self):  # type: ignore[no-untyped-def]
        yield


class _FakeRepo:
    def __init__(self) -> None:
        self.conn = _FakeConn()
        self.fetch_ratings_before_exam_called = 0
        self.fetch_current_ratings_called = 0
        self._exam_id = 100
        self._latest_rating_by_student: dict[int, int] = {}
        self.saved_rating_rows: list[dict[str, int]] = []

    def create_ingest_run(self, meta: dict[str, Any]) -> int:
        _ = meta
        return 1

    def finish_ingest_run(self, ingest_run_id: int, status: str, meta: dict[str, Any]) -> None:
        _ = (ingest_run_id, status, meta)

    def get_latest_success_fingerprint(self, exam_type: str, source_path: str) -> str | None:
        _ = (exam_type, source_path)
        return None

    def upsert_exam(self, exam_type: str, source_path: str, meta: ExamMeta) -> int:
        _ = (exam_type, source_path, meta)
        self._exam_id += 1
        return self._exam_id

    def insert_exam_files(self, exam_id: int, files: list[Any]) -> None:
        _ = (exam_id, files)

    def upsert_exam_problem(self, exam_id: int, problem: Any) -> None:
        _ = (exam_id, problem)

    def upsert_student_identity(self, source: str, external_id: str, external_name: str | None) -> int:
        _ = (source, external_name)
        return int(external_id)

    def upsert_exam_participant(self, exam_id: int, participant: ParticipantRow) -> None:
        _ = (exam_id, participant)

    def insert_submission(self, exam_id: int, submission: Any) -> None:
        _ = (exam_id, submission)

    def upsert_exam_student_metric(self, **kwargs: Any) -> None:
        _ = kwargs

    def fetch_ratings_before_exam(
        self,
        exam_id: int,
        student_ids: list[int],
        default_rating: int,
    ) -> dict[int, int]:
        _ = exam_id
        self.fetch_ratings_before_exam_called += 1
        return {student_id: default_rating for student_id in student_ids}

    def fetch_current_ratings(self, student_ids: list[int]) -> dict[int, int]:
        self.fetch_current_ratings_called += 1
        return {student_id: 1600 for student_id in student_ids}

    def upsert_rating_history(
        self,
        exam_id: int,
        student_id: int,
        old_rating: int,
        delta: int,
        new_rating: int,
        rank: int,
        seed: float,
        performance: float,
        details: dict[str, Any],
    ) -> None:
        _ = (exam_id, rank, seed, performance, details)
        self.saved_rating_rows.append(
            {
                "student_id": student_id,
                "old_rating": old_rating,
                "delta": delta,
                "new_rating": new_rating,
            }
        )
        self._latest_rating_by_student[student_id] = new_rating

    def fetch_metric_history(self, student_id: int) -> list[dict[str, Any]]:
        _ = student_id
        return []

    def fetch_latest_rating(self, student_id: int, default_rating: int) -> int:
        return self._latest_rating_by_student.get(student_id, default_rating)

    def upsert_student_current_metrics(
        self,
        student_id: int,
        metrics: dict[str, float | None],
        rating: int,
    ) -> None:
        _ = metrics
        self._latest_rating_by_student[student_id] = rating

    def recompute_achievements_for_students(
        self,
        student_ids: list[int],
        source: str = "ingest",
    ) -> int:
        _ = source
        return len(set(student_ids))

    def record_ingest_exam_run(
        self,
        ingest_run_id: int,
        exam_id: int,
        fingerprint: str,
        status: str,
        error_message: str | None,
    ) -> None:
        _ = (ingest_run_id, exam_id, fingerprint, status, error_message)

    def cleanup_orphan_students(self) -> dict[str, int]:
        return {
            "submissions_unlinked": 0,
            "participants_deleted": 0,
            "metrics_deleted": 0,
            "ratings_deleted": 0,
            "current_metrics_deleted": 0,
            "students_deleted": 0,
        }


def test_ingest_uses_rating_before_exam_baseline(monkeypatch) -> None:
    unit = ExamUnit(
        exam_type="datastructure",
        source_path="2024-04/mock-exam",
        absolute_path=Path("/tmp/mock-exam"),
        files=[],
        fingerprint="fp-1",
    )
    bundle = ExamBundle(
        unit=unit,
        exam_meta=ExamMeta(
            title="Mock Exam",
            starts_at=datetime(2024, 4, 1, tzinfo=UTC),
            ends_at=None,
            duration_seconds=None,
            total_points=100.0,
            meta={},
        ),
        problems=[],
        participants=[
            ParticipantRow(
                identity_source="test_student_no",
                external_id="1",
                display_name="Student 1",
                user_group="G1",
                rank=1,
                total_score=100.0,
                time_used_seconds=60,
                solved_count=1,
                absent=False,
                problem_stats={},
            )
        ],
        submissions=[],
    )

    monkeypatch.setattr(ingest_module, "discover_exam_units", lambda **kwargs: [unit])
    monkeypatch.setattr(ingest_module, "parse_exam_bundle", lambda **kwargs: bundle)
    monkeypatch.setattr(ingest_module, "compute_exam_metrics", lambda **kwargs: [])

    def _fake_compute_exam_rating(
        participants: list[ParticipantRow],
        current_ratings: dict[int, int],
        cfg: Any,
    ) -> list[RatingResult]:
        _ = cfg
        student_id = participants[0].student_id
        assert student_id is not None
        old = current_ratings[student_id]
        return [
            RatingResult(
                student_id=student_id,
                old_rating=old,
                delta=0,
                new_rating=old,
                rank=1,
                seed=1.0,
                performance=float(old),
                details={},
            )
        ]

    monkeypatch.setattr(ingest_module, "compute_exam_rating", _fake_compute_exam_rating)

    repo = _FakeRepo()
    service = IngestService(repo=repo, settings=Settings())
    summary = service.run()

    assert summary.succeeded == 1
    assert summary.failed == 0
    assert repo.fetch_ratings_before_exam_called == 1
    assert repo.fetch_current_ratings_called == 0
    assert repo.saved_rating_rows[0]["old_rating"] == 800
