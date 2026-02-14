from __future__ import annotations

from datetime import datetime, timezone
from decimal import Decimal

from ..db.repository import (
    ApiRepository,
    DashboardMetricsRow,
    ExamMetricHistoryRow,
    RatingHistoryRow,
)
from ..schemas.students import (
    MetricDeltaInfoResponse,
    MetricDeltaItemResponse,
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
        exam_metric_rows: list[ExamMetricHistoryRow] = []
        per_student_history_limit = max(
            self._rating_history_limit,
            self._rating_history_limit * len(student_entity_ids),
        )
        per_student_exam_metric_limit = max(
            6,
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
            exam_metric_rows.extend(
                await self._repository.fetch_exam_metric_history(
                    student_id=student_entity_id,
                    limit=per_student_exam_metric_limit,
                )
            )

        merged_history_rows = self._merge_history_rows(history_rows)
        merged_exam_metric_rows = self._merge_exam_metric_rows(exam_metric_rows)

        if not metrics_rows and not merged_history_rows and not merged_exam_metric_rows:
            return self._build_empty(identity)

        metrics = self._build_metrics(metrics_rows)
        rating = self._build_rating(metrics_rows, merged_history_rows)
        metric_delta = self._build_metric_delta(merged_exam_metric_rows)
        return StudentDashboardResponse(
            metrics=metrics,
            rating=rating,
            metricDelta=metric_delta,
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
            metricDelta=self._empty_metric_delta(),
            identity=ResolvedIdentityResponse(
                studentId=identity.student_id,
                ptaNickname=identity.pta_nickname,
                noSubmissionRecords=True,
            ),
        )

    def _empty_metric_delta(self) -> MetricDeltaInfoResponse:
        return MetricDeltaInfoResponse(
            latestExamId=None,
            latestExamName=None,
            latestExamDate=None,
            baseline="zero",
            values=MetricDeltaItemResponse(
                knowledge=0,
                accuracy=0,
                quality=0,
                flexibility=0,
                proficiency=0,
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

    def _merge_exam_metric_rows(
        self, rows: list[ExamMetricHistoryRow]
    ) -> list[ExamMetricHistoryRow]:
        if not rows:
            return []
        ordered = sorted(
            rows,
            key=lambda row: (
                row.exam_time,
                row.exam_id,
                row.computed_at
                if row.computed_at is not None
                else datetime.min.replace(tzinfo=timezone.utc),
            ),
            reverse=True,
        )
        grouped_by_exam: dict[int, list[ExamMetricHistoryRow]] = {}
        for row in ordered:
            grouped_by_exam.setdefault(row.exam_id, []).append(row)

        merged: list[ExamMetricHistoryRow] = []
        for exam_rows in grouped_by_exam.values():
            rows_by_recency = sorted(
                exam_rows,
                key=lambda row: row.computed_at
                if row.computed_at is not None
                else datetime.min.replace(tzinfo=timezone.utc),
                reverse=True,
            )
            head = rows_by_recency[0]
            merged.append(
                ExamMetricHistoryRow(
                    exam_id=head.exam_id,
                    exam_name=head.exam_name,
                    exam_time=head.exam_time,
                    knowledge=self._metric_from_exam_rows(rows_by_recency, "knowledge"),
                    accuracy=self._metric_from_exam_rows(rows_by_recency, "accuracy"),
                    quality=self._metric_from_exam_rows(rows_by_recency, "quality"),
                    flexibility=self._metric_from_exam_rows(
                        rows_by_recency, "flexibility"
                    ),
                    proficiency=self._metric_from_exam_rows(
                        rows_by_recency, "proficiency"
                    ),
                    computed_at=head.computed_at,
                )
            )
            if len(merged) >= self._rating_history_limit:
                break
        return merged

    def _metric_from_exam_rows(
        self, rows: list[ExamMetricHistoryRow], key: str
    ) -> Decimal | float | int | None:
        for row in rows:
            value = getattr(row, key)
            if value is not None:
                return value
        return None

    def _build_metric_delta(
        self, rows: list[ExamMetricHistoryRow]
    ) -> MetricDeltaInfoResponse:
        if not rows:
            return self._empty_metric_delta()

        latest = rows[0]
        previous = rows[1] if len(rows) > 1 else None
        baseline = "previous_exam" if previous is not None else "zero"
        return MetricDeltaInfoResponse(
            latestExamId=str(latest.exam_id),
            latestExamName=latest.exam_name,
            latestExamDate=latest.exam_time.date().isoformat(),
            baseline=baseline,
            values=MetricDeltaItemResponse(
                knowledge=self._metric_delta_value(
                    latest.knowledge, previous.knowledge if previous else None
                ),
                accuracy=self._metric_delta_value(
                    latest.accuracy, previous.accuracy if previous else None
                ),
                quality=self._metric_delta_value(
                    latest.quality, previous.quality if previous else None
                ),
                flexibility=self._metric_delta_value(
                    latest.flexibility, previous.flexibility if previous else None
                ),
                proficiency=self._metric_delta_value(
                    latest.proficiency, previous.proficiency if previous else None
                ),
            ),
        )

    def _metric_delta_value(
        self,
        latest: Decimal | float | int | None,
        previous: Decimal | float | int | None,
    ) -> int:
        return self._metric_int(latest) - self._metric_int(previous if previous is not None else 0)

    def _metric_value(self, value: Decimal | float | int | None) -> float:
        if value is None:
            return self._default_metric
        if isinstance(value, Decimal):
            return float(value)
        return float(value)

    def _metric_int(self, value: Decimal | float | int | None) -> int:
        return int(round(self._metric_value(value)))
