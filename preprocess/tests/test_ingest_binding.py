from __future__ import annotations

from datetime import datetime

from preprocess.config import Settings
from preprocess.load.ingest_service import IngestService
from preprocess.models import ParticipantRow, SubmissionRow


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


def _build_participant(
    identity_source: str,
    external_id: str,
    student_id: int,
) -> ParticipantRow:
    return ParticipantRow(
        identity_source=identity_source,
        external_id=external_id,
        display_name="Student",
        user_group=None,
        rank=1,
        total_score=100.0,
        time_used_seconds=60,
        solved_count=1,
        absent=False,
        problem_stats={},
        raw={},
        student_id=student_id,
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


def test_bind_submission_claims_binds_matching_student_no_identity() -> None:
    settings = Settings()
    service = IngestService(repo=_FakeRepo(claims={}), settings=settings)
    rows = [
        _build_submission(
            actor_source="datastructure_student_no",
            actor_external_id="20251202099",
            actor_name="常根全",
        )
    ]
    participants = [
        _build_participant(
            identity_source="datastructure_student_no",
            external_id="20251202099",
            student_id=101,
        )
    ]

    bound, pending, conflicts = service._bind_submission_claims(
        rows,
        participants=participants,
    )

    assert bound == 1
    assert pending == 0
    assert conflicts == 0
    assert rows[0].student_id == 101
    assert rows[0].raw["linking"]["status"] == "bound_by_identity"
