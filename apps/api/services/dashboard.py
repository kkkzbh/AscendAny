from __future__ import annotations

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
    MetricMissingItemResponse,
    RatingInfoResponse,
    RatingPointResponse,
    ResolvedIdentityResponse,
    StudentDashboardResponse,
    StudentMetricsResponse,
)
from .history_merge import (
    latest_metrics_row,
    merge_exam_metric_rows,
    merge_rating_history_rows,
    metric_from_rows,
)
from .growth_insights import (
    GrowthInsightsService,
    build_empty_growth_insights,
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
        self._growth_insights = GrowthInsightsService(repository)

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
        growth_insights = await self._growth_insights.build(
            identity=identity,
            rating_rows=merged_history_rows,
            exam_metric_rows=merged_exam_metric_rows,
        )
        return StudentDashboardResponse(
            metrics=metrics,
            metricMissing=self._build_metric_missing(metrics_rows),
            rating=rating,
            metricDelta=metric_delta,
            identity=ResolvedIdentityResponse(
                studentId=identity.student_id,
                ptaNickname=identity.pta_nickname,
                noSubmissionRecords=False,
            ),
            progressExplanation=growth_insights.progress_explanation,
            milestoneStreak=growth_insights.milestone_streak,
            peerComparison=growth_insights.peer_comparison,
            postExamSupport=growth_insights.post_exam_support,
        )

    def _build_empty(self, identity: ResolvedIdentity) -> StudentDashboardResponse:
        empty_growth = build_empty_growth_insights()
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
            metricMissing=MetricMissingItemResponse(
                knowledge=True,
                accuracy=True,
                quality=True,
                flexibility=True,
                proficiency=True,
            ),
            rating=rating,
            metricDelta=self._empty_metric_delta(),
            identity=ResolvedIdentityResponse(
                studentId=identity.student_id,
                ptaNickname=identity.pta_nickname,
                noSubmissionRecords=True,
            ),
            progressExplanation=empty_growth.progress_explanation,
            milestoneStreak=empty_growth.milestone_streak,
            peerComparison=empty_growth.peer_comparison,
            postExamSupport=empty_growth.post_exam_support,
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

    def _build_metric_missing(
        self, rows: list[DashboardMetricsRow]
    ) -> MetricMissingItemResponse:
        if not rows:
            return MetricMissingItemResponse(
                knowledge=True,
                accuracy=True,
                quality=True,
                flexibility=True,
                proficiency=True,
            )
        return MetricMissingItemResponse(
            knowledge=metric_from_rows(rows, "knowledge") is None,
            accuracy=metric_from_rows(rows, "accuracy") is None,
            quality=metric_from_rows(rows, "quality") is None,
            flexibility=metric_from_rows(rows, "flexibility") is None,
            proficiency=metric_from_rows(rows, "proficiency") is None,
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
        return latest_metrics_row(rows)

    def _metric_from_rows(self, rows: list[DashboardMetricsRow], key: str) -> float:
        value = metric_from_rows(rows, key)
        if value is not None:
            return self._metric_value(value)
        return self._default_metric

    def _merge_history_rows(
        self, rows: list[RatingHistoryRow]
    ) -> list[RatingHistoryRow]:
        return merge_rating_history_rows(rows, limit=self._rating_history_limit)

    def _merge_exam_metric_rows(
        self, rows: list[ExamMetricHistoryRow]
    ) -> list[ExamMetricHistoryRow]:
        return merge_exam_metric_rows(rows, limit=self._rating_history_limit)

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
