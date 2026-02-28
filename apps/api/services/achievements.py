from __future__ import annotations

from ..db.repository import ApiRepository
from ..schemas.students import (
    AchievementItemResponse,
    AchievementSummaryResponse,
    ResolvedIdentityResponse,
    StudentAchievementsResponse,
)
from .identity import ResolvedIdentity


def _to_float(value: object) -> float:
    if value is None:
        return 0.0
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


def _evaluate_tier(
    progress: float,
    bronze_target: float,
    silver_target: float,
    gold_target: float,
) -> int:
    if progress >= gold_target:
        return 3
    if progress >= silver_target:
        return 2
    if progress >= bronze_target:
        return 1
    return 0


class AchievementsService:
    def __init__(self, repository: ApiRepository) -> None:
        self._repository = repository

    async def build(self, identity: ResolvedIdentity) -> StudentAchievementsResponse:
        definitions = await self._repository.fetch_achievement_definitions(enabled_only=True)
        if not definitions:
            return StudentAchievementsResponse(
                identity=ResolvedIdentityResponse(
                    studentId=identity.student_id,
                    ptaNickname=identity.pta_nickname,
                    noSubmissionRecords=identity.no_submission_records,
                ),
                summary=AchievementSummaryResponse(
                    total=0,
                    locked=0,
                    bronze=0,
                    silver=0,
                    gold=0,
                ),
                items=[],
            )

        student_entity_ids = tuple(
            sorted(
                {
                    int(student_id)
                    for student_id in (
                        identity.student_entity_ids or (identity.student_entity_id,)
                    )
                    if int(student_id) > 0
                }
            )
        )

        state_rows = await self._repository.fetch_aggregated_achievement_states(
            list(student_entity_ids)
        )
        state_by_code = {row.achievement_code: row for row in state_rows}

        activity_counters = await self._repository.fetch_student_activity_counters(
            list(student_entity_ids)
        )
        ai_dialogue_count = max(activity_counters.values(), default=0)

        items: list[AchievementItemResponse] = []
        for definition in definitions:
            bronze_target = _to_float(definition.bronze_target)
            silver_target = _to_float(definition.silver_target)
            gold_target = _to_float(definition.gold_target)

            if definition.source == "realtime":
                if definition.progress_key == "ai_dialogue_count":
                    progress = float(ai_dialogue_count)
                else:
                    progress = 0.0
                tier = _evaluate_tier(
                    progress=progress,
                    bronze_target=bronze_target,
                    silver_target=silver_target,
                    gold_target=gold_target,
                )
            else:
                state = state_by_code.get(definition.achievement_code)
                progress = _to_float(state.progress_value) if state is not None else 0.0
                tier = int(state.tier) if state is not None else 0
                tier = max(
                    tier,
                    _evaluate_tier(
                        progress=progress,
                        bronze_target=bronze_target,
                        silver_target=silver_target,
                        gold_target=gold_target,
                    ),
                )

            items.append(
                AchievementItemResponse(
                    code=definition.achievement_code,
                    title=definition.title,
                    description=definition.description,
                    tier=tier,
                    progress=progress,
                    bronzeTarget=bronze_target,
                    silverTarget=silver_target,
                    goldTarget=gold_target,
                    sortOrder=definition.sort_order,
                )
            )

        summary = AchievementSummaryResponse(
            total=len(items),
            locked=sum(1 for item in items if item.tier == 0),
            bronze=sum(1 for item in items if item.tier == 1),
            silver=sum(1 for item in items if item.tier == 2),
            gold=sum(1 for item in items if item.tier == 3),
        )

        return StudentAchievementsResponse(
            identity=ResolvedIdentityResponse(
                studentId=identity.student_id,
                ptaNickname=identity.pta_nickname,
                noSubmissionRecords=identity.no_submission_records,
            ),
            summary=summary,
            items=items,
        )
