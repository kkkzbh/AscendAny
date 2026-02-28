from __future__ import annotations

import asyncio

from apps.api.db.repository import (
    AchievementDefinitionRow,
    AggregatedAchievementStateRow,
)
from apps.api.services.achievements import AchievementsService
from apps.api.services.identity import ResolvedIdentity


class _FakeRepo:
    async def fetch_achievement_definitions(
        self,
        source: str | None = None,
        enabled_only: bool = True,
    ):
        _ = source, enabled_only
        return [
            AchievementDefinitionRow(
                achievement_code="exam_count_first",
                title="初试锋芒",
                description="累计参赛次数达到 1 / 3 / 8 场。",
                source="ingest",
                progress_key="exam_count",
                bronze_target=1,
                silver_target=3,
                gold_target=8,
                sort_order=1,
            ),
            AchievementDefinitionRow(
                achievement_code="ai_dialogue_count",
                title="AI陪练",
                description="与 AI 成功对话次数达到 3 / 15 / 40 次。",
                source="realtime",
                progress_key="ai_dialogue_count",
                bronze_target=3,
                silver_target=15,
                gold_target=40,
                sort_order=2,
            ),
        ]

    async def fetch_aggregated_achievement_states(self, student_ids: list[int]):
        if set(student_ids) == {10, 20}:
            return [
                AggregatedAchievementStateRow(
                    achievement_code="exam_count_first",
                    progress_value=9,
                    tier=3,
                )
            ]
        return []

    async def fetch_student_activity_counters(self, student_ids: list[int]):
        if set(student_ids) == {10, 20}:
            return {10: 2, 20: 16}
        return {}


def test_achievements_service_merges_identity_entities_and_realtime_progress() -> None:
    service = AchievementsService(repository=_FakeRepo())
    identity = ResolvedIdentity(
        student_entity_id=10,
        student_entity_ids=(10, 20),
        student_id="20231202047",
        pta_nickname="王浩然",
        no_submission_records=False,
        matched_by="student_id",
    )

    payload = asyncio.run(service.build(identity))

    assert payload.summary.total == 2
    assert payload.summary.gold == 1
    assert payload.summary.silver == 1
    assert payload.items[0].code == "exam_count_first"
    assert payload.items[0].progress == 9
    assert payload.items[0].tier == 3
    assert payload.items[1].code == "ai_dialogue_count"
    assert payload.items[1].progress == 16
    assert payload.items[1].tier == 2
