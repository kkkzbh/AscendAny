from __future__ import annotations

from datetime import datetime

from preprocess.config import Settings
from preprocess.load.ingest_service import IngestService
from preprocess.models import SubmissionRow


class _FakeRepo:
    def __init__(self, claims: dict[str, int]) -> None:
        self.claims = claims

    def fetch_active_nickname_claims(self, nicknames: list[str]) -> dict[str, int]:
        _ = nicknames
        return dict(self.claims)


def _build_submission(
    actor_source: str,
    actor_external_id: str,
    actor_name: str | None,
) -> SubmissionRow:
    return SubmissionRow(
        actor_source=actor_source,
        actor_external_id=actor_external_id,
        actor_name=actor_name,
        submitted_at=datetime(2026, 1, 1, 12, 0, 0),
        verdict="Accepted",
        score=1.0,
        problem_code="6-1",
        language="C",
        memory_kb=128,
        time_ms=16,
        row_hash=f"hash-{actor_source}-{actor_external_id}",
        raw={},
        student_id=None,
    )


def test_bind_submission_claims_marks_bound_and_pending() -> None:
    settings = Settings()
    service = IngestService(repo=_FakeRepo(claims={"alice": 101}), settings=settings)
    rows = [
        _build_submission(
            actor_source="datastructure_nickname",
            actor_external_id="Alice",
            actor_name="Alice",
        ),
        _build_submission(
            actor_source="datastructure_nickname",
            actor_external_id="Bob",
            actor_name="Bob",
        ),
    ]

    bound, pending, conflicts = service._bind_submission_claims(rows)

    assert bound == 1
    assert pending == 1
    assert conflicts == 0
    assert rows[0].student_id == 101
    assert rows[0].raw["linking"]["status"] == "bound_by_claim"
    assert rows[1].student_id is None
    assert rows[1].raw["linking"]["status"] == "pending_claim"
