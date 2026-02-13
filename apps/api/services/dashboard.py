from __future__ import annotations

from datetime import datetime, timezone
from decimal import Decimal

from ..db.repository import ApiRepository, DashboardMetricsRow, RatingHistoryRow
from ..schemas.students import (
    RatingInfoResponse,
    RatingPointResponse,
    ResolvedIdentityResponse,
    StudentDashboardResponse,
    StudentMetricsResponse,
)
from .identity import ResolvedIdentity


class DashboardService:
    def __init__(
        self,
        repository: ApiRepository,
        default_rating: int,
        default_metric: float,
        rating_history_limit: int,
    ) -> None:
        self._repository = repository
        self._default_rating = default_rating
        self._default_metric = default_metric
        self._rating_history_limit = rating_history_limit

    async def build(self, identity: ResolvedIdentity) -> StudentDashboardResponse:
        if identity.no_submission_records:
            return self._build_empty(identity)

        student_entity_ids = identity.student_entity_ids or (
            identity.student_entity_id,
        )
        metrics_rows: list[DashboardMetricsRow] = []
        history_rows: list[RatingHistoryRow] = []
        per_student_history_limit = max(
            self._rating_history_limit,
            self._rating_history_limit * len(student_entity_ids),
        )

        for student_entity_id in student_entity_ids:
            current_metrics = await self._repository.fetch_current_metrics(
                student_entity_id
            )
            if current_metrics is not None:
                metrics_rows.append(current_metrics)
            history_rows.extend(
                await self._repository.fetch_rating_history(
                    student_id=student_entity_id,
                    limit=per_student_history_limit,
                )
            )

        merged_history_rows = self._merge_history_rows(history_rows)

        if not metrics_rows and not merged_history_rows:
            return self._build_empty(identity)

        metrics = self._build_metrics(metrics_rows)
        rating = self._build_rating(metrics_rows, merged_history_rows)
        return StudentDashboardResponse(
            metrics=metrics,
            rating=rating,
            identity=ResolvedIdentityResponse(
                studentId=identity.student_id,
                ptaNickname=identity.pta_nickname,
                noSubmissionRecords=False,
            ),
        )

    def _build_empty(self, identity: ResolvedIdentity) -> StudentDashboardResponse:
        metrics = StudentMetricsResponse(
            knowledge=self._default_metric,
            accuracy=self._default_metric,
            quality=self._default_metric,
            flexibility=self._default_metric,
            proficiency=self._default_metric,
        )
        rating = RatingInfoResponse(
            current=self._default_rating,
            lastDelta=None,
            history=[],
        )
        return StudentDashboardResponse(
            metrics=metrics,
            rating=rating,
            identity=ResolvedIdentityResponse(
                studentId=identity.student_id,
                ptaNickname=identity.pta_nickname,
                noSubmissionRecords=True,
            ),
        )

    def _build_metrics(self, rows: list[DashboardMetricsRow]) -> StudentMetricsResponse:
        if not rows:
            return StudentMetricsResponse(
                knowledge=self._default_metric,
                accuracy=self._default_metric,
                quality=self._default_metric,
                flexibility=self._default_metric,
                proficiency=self._default_metric,
            )
        return StudentMetricsResponse(
            knowledge=self._metric_from_rows(rows, "knowledge"),
            accuracy=self._metric_from_rows(rows, "accuracy"),
            quality=self._metric_from_rows(rows, "quality"),
            flexibility=self._metric_from_rows(rows, "flexibility"),
            proficiency=self._metric_from_rows(rows, "proficiency"),
        )

    def _build_rating(
        self,
        metrics_rows: list[DashboardMetricsRow],
        history_rows: list[RatingHistoryRow],
    ) -> RatingInfoResponse:
        if history_rows:
            current_rating = int(history_rows[0].new_rating)
        else:
            latest_metrics_row = self._latest_metrics_row(metrics_rows)
            if latest_metrics_row is not None:
                current_rating = int(latest_metrics_row.rating)
            else:
                current_rating = self._default_rating

        history = [
            RatingPointResponse(
                examId=str(row.exam_id),
                examName=row.exam_name,
                date=row.exam_time.date().isoformat(),
                oldRating=row.old_rating,
                delta=row.delta,
                newRating=row.new_rating,
            )
            for row in history_rows
        ]
        return RatingInfoResponse(
            current=current_rating,
            lastDelta=history_rows[0].delta if history_rows else None,
            history=history,
        )

    def _latest_metrics_row(
        self, rows: list[DashboardMetricsRow]
    ) -> DashboardMetricsRow | None:
        if not rows:
            return None
        return max(
            rows,
            key=lambda row: row.updated_at
            if row.updated_at is not None
            else datetime.min.replace(tzinfo=timezone.utc),
        )

    def _metric_from_rows(self, rows: list[DashboardMetricsRow], key: str) -> float:
        ordered = sorted(
            rows,
            key=lambda row: row.updated_at
            if row.updated_at is not None
            else datetime.min.replace(tzinfo=timezone.utc),
            reverse=True,
        )
        for row in ordered:
            value = getattr(row, key)
            if value is not None:
                return self._metric_value(value)
        return self._default_metric

    def _merge_history_rows(
        self, rows: list[RatingHistoryRow]
    ) -> list[RatingHistoryRow]:
        if not rows:
            return []
        ordered = sorted(
            rows,
            key=lambda row: (row.exam_time, row.exam_id),
            reverse=True,
        )
        deduplicated: list[RatingHistoryRow] = []
        seen_exam_ids: set[int] = set()
        for row in ordered:
            if row.exam_id in seen_exam_ids:
                continue
            seen_exam_ids.add(row.exam_id)
            deduplicated.append(row)
            if len(deduplicated) >= self._rating_history_limit:
                break
        return deduplicated

    def _metric_value(self, value: Decimal | float | int | None) -> float:
        if value is None:
            return self._default_metric
        if isinstance(value, Decimal):
            return float(value)
        return float(value)
