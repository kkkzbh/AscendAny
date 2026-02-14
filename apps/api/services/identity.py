from __future__ import annotations

from dataclasses import dataclass

from ..core.errors import AppError
from ..db.repository import ApiRepository


def _clean(value: str | None) -> str:
    if value is None:
        return ""
    return value.strip()


@dataclass(slots=True)
class ResolvedIdentity:
    student_entity_id: int
    student_id: str
    pta_nickname: str | None
    no_submission_records: bool
    matched_by: str
    student_entity_ids: tuple[int, ...] = ()


class StudentIdentityService:
    def __init__(self, repository: ApiRepository) -> None:
        self._repository = repository

    async def resolve(
        self, student_id: str | None, pta_nickname: str | None
    ) -> ResolvedIdentity:
        clean_student_id = _clean(student_id)
        clean_pta_nickname = _clean(pta_nickname)

        if not clean_student_id and not clean_pta_nickname:
            raise AppError(
                status_code=400,
                code="MISSING_STUDENT_IDENTIFIER",
                message="At least one of studentId or ptaNickname is required.",
            )

        if clean_student_id:
            return await self._resolve_from_student_id(
                student_id=clean_student_id,
                pta_nickname=clean_pta_nickname or None,
                matched_by="student_id",
            )

        return await self._resolve_from_pta_nickname(clean_pta_nickname)

    async def _resolve_from_student_id(
        self,
        student_id: str,
        pta_nickname: str | None,
        matched_by: str,
    ) -> ResolvedIdentity:
        matches = await self._repository.find_students_by_student_no(student_id)
        if not matches:
            raise AppError(
                status_code=404,
                code="STUDENT_NOT_FOUND",
                message="studentId was not found.",
            )

        primary_match = matches[0]
        merged_student_entity_ids = tuple(
            dict.fromkeys(match.student_id for match in matches)
        )
        effective_nickname = pta_nickname or _clean(primary_match.student_name) or None
        no_submission_records = await self._is_no_submission_records(
            student_entity_ids=merged_student_entity_ids,
            pta_nickname=effective_nickname,
        )

        return ResolvedIdentity(
            student_entity_id=primary_match.student_id,
            student_entity_ids=merged_student_entity_ids,
            student_id=primary_match.student_no,
            pta_nickname=effective_nickname,
            no_submission_records=no_submission_records,
            matched_by=matched_by,
        )

    async def _resolve_from_pta_nickname(self, pta_nickname: str) -> ResolvedIdentity:
        matches = await self._repository.find_student_nos_by_name(pta_nickname)
        if not matches:
            raise AppError(
                status_code=404,
                code="STUDENT_ID_NOT_FOUND_BY_NICKNAME",
                message="ptaNickname did not map to any studentId. Please provide studentId manually.",
            )

        if len(matches) > 1:
            raise AppError(
                status_code=409,
                code="MULTIPLE_STUDENT_IDS_FOR_NICKNAME",
                message="ptaNickname maps to multiple studentIds. Please provide studentId.",
            )

        inferred_student_id = matches[0].student_no
        return await self._resolve_from_student_id(
            student_id=inferred_student_id,
            pta_nickname=pta_nickname,
            matched_by="pta_nickname",
        )

    async def _is_no_submission_records(
        self,
        student_entity_ids: tuple[int, ...],
        pta_nickname: str | None,
    ) -> bool:
        records_checker = getattr(
            self._repository, "exists_learning_records_for_student_ids", None
        )
        if callable(records_checker):
            has_records = await records_checker(list(student_entity_ids))
            return not has_records

        # Backward-compatible fallback for simplified test repositories.
        if not pta_nickname:
            return True
        has_submission = await self._repository.exists_pta_submission_by_actor_name(
            pta_nickname
        )
        return not has_submission
