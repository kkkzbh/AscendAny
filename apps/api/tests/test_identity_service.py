from __future__ import annotations

import asyncio

import pytest

from apps.api.core.errors import AppError
from apps.api.db.repository import StudentIdentityMatch, StudentNoMatch
from apps.api.services.identity import StudentIdentityService


class FakeIdentityRepo:
    def __init__(self) -> None:
        self.student_no_map: dict[str, list[StudentIdentityMatch]] = {}
        self.name_matches: dict[str, list[StudentNoMatch]] = {}
        self.submission_name_hits: dict[str, bool] = {}
        self.records_by_student_ids: dict[tuple[int, ...], bool] = {}

    async def find_students_by_student_no(
        self, student_no: str
    ) -> list[StudentIdentityMatch]:
        return self.student_no_map.get(student_no, [])

    async def find_student_nos_by_name(self, student_name: str) -> list[StudentNoMatch]:
        return self.name_matches.get(student_name, [])

    async def exists_pta_submission_by_actor_name(self, actor_name: str) -> bool:
        return self.submission_name_hits.get(actor_name, False)

    async def exists_learning_records_for_student_ids(
        self, student_ids: list[int]
    ) -> bool:
        return self.records_by_student_ids.get(tuple(student_ids), False)


def test_student_id_defaults_pta_nickname_and_marks_no_submission() -> None:
    repo = FakeIdentityRepo()
    repo.student_no_map["20230001"] = [
        StudentIdentityMatch(student_id=11, student_no="20230001", student_name="Alice")
    ]
    repo.submission_name_hits["Alice"] = False

    service = StudentIdentityService(repository=repo)
    identity = asyncio.run(service.resolve(student_id="20230001", pta_nickname=None))

    assert identity.student_entity_id == 11
    assert identity.student_id == "20230001"
    assert identity.pta_nickname == "Alice"
    assert identity.no_submission_records is True
    assert identity.matched_by == "student_id"
    assert identity.student_entity_ids == (11,)


def test_student_id_merges_multiple_entities_for_same_student_no() -> None:
    repo = FakeIdentityRepo()
    repo.student_no_map["20231202047"] = [
        StudentIdentityMatch(
            student_id=100,
            student_no="20231202047",
            student_name="王浩然",
        ),
        StudentIdentityMatch(
            student_id=200,
            student_no="20231202047",
            student_name="王浩然",
        ),
    ]
    repo.records_by_student_ids[(100, 200)] = True

    service = StudentIdentityService(repository=repo)
    identity = asyncio.run(service.resolve(student_id="20231202047", pta_nickname=None))

    assert identity.student_entity_id == 100
    assert identity.student_entity_ids == (100, 200)
    assert identity.no_submission_records is False


def test_student_id_uses_learning_records_even_when_pta_name_has_no_hit() -> None:
    repo = FakeIdentityRepo()
    repo.student_no_map["20231901030"] = [
        StudentIdentityMatch(
            student_id=300,
            student_no="20231901030",
            student_name="付子祎",
        )
    ]
    repo.submission_name_hits["付子祎"] = False
    repo.records_by_student_ids[(300,)] = True

    service = StudentIdentityService(repository=repo)
    identity = asyncio.run(
        service.resolve(student_id="20231901030", pta_nickname="付子祎")
    )

    assert identity.student_entity_ids == (300,)
    assert identity.no_submission_records is False


def test_pta_nickname_ambiguous_student_ids_returns_409() -> None:
    repo = FakeIdentityRepo()
    repo.name_matches["Alice"] = [
        StudentNoMatch(student_id=10, student_no="20230001", student_name="Alice"),
        StudentNoMatch(student_id=20, student_no="20230002", student_name="Alice"),
    ]

    service = StudentIdentityService(repository=repo)

    with pytest.raises(AppError) as exc_info:
        asyncio.run(service.resolve(student_id=None, pta_nickname="Alice"))

    assert exc_info.value.status_code == 409
    assert exc_info.value.code == "MULTIPLE_STUDENT_IDS_FOR_NICKNAME"


def test_missing_identifiers_returns_400() -> None:
    service = StudentIdentityService(repository=FakeIdentityRepo())

    with pytest.raises(AppError) as exc_info:
        asyncio.run(service.resolve(student_id="", pta_nickname=""))

    assert exc_info.value.status_code == 400
    assert exc_info.value.code == "MISSING_STUDENT_IDENTIFIER"
