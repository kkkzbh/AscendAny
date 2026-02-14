from __future__ import annotations

from datetime import datetime, timezone
from decimal import Decimal
from typing import Literal

from ..db.repository import DashboardMetricsRow, ExamMetricHistoryRow, RatingHistoryRow

MetricKey = Literal["knowledge", "accuracy", "quality", "flexibility", "proficiency"]
MetricValue = Decimal | float | int | None

_MIN_SORT_TIME = datetime.min.replace(tzinfo=timezone.utc)


def _safe_sort_time(value: datetime | None) -> datetime:
    return value if value is not None else _MIN_SORT_TIME


def latest_metrics_row(
    rows: list[DashboardMetricsRow],
) -> DashboardMetricsRow | None:
    if not rows:
        return None
    return max(rows, key=lambda row: _safe_sort_time(row.updated_at))


def metric_from_rows(rows: list[DashboardMetricsRow], key: MetricKey) -> MetricValue:
    ordered = sorted(
        rows,
        key=lambda row: _safe_sort_time(row.updated_at),
        reverse=True,
    )
    for row in ordered:
        value = getattr(row, key)
        if value is not None:
            return value
    return None


def merge_rating_history_rows(
    rows: list[RatingHistoryRow],
    limit: int,
) -> list[RatingHistoryRow]:
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
        if len(deduplicated) >= limit:
            break
    return deduplicated


def merge_exam_metric_rows(
    rows: list[ExamMetricHistoryRow],
    limit: int,
) -> list[ExamMetricHistoryRow]:
    ordered = sorted(
        rows,
        key=lambda row: (
            row.exam_time,
            row.exam_id,
            _safe_sort_time(row.computed_at),
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
            key=lambda row: _safe_sort_time(row.computed_at),
            reverse=True,
        )
        head = rows_by_recency[0]
        merged.append(
            ExamMetricHistoryRow(
                exam_id=head.exam_id,
                exam_name=head.exam_name,
                exam_time=head.exam_time,
                knowledge=_metric_from_exam_rows(rows_by_recency, "knowledge"),
                accuracy=_metric_from_exam_rows(rows_by_recency, "accuracy"),
                quality=_metric_from_exam_rows(rows_by_recency, "quality"),
                flexibility=_metric_from_exam_rows(rows_by_recency, "flexibility"),
                proficiency=_metric_from_exam_rows(rows_by_recency, "proficiency"),
                computed_at=head.computed_at,
            )
        )
        if not _has_any_metric(merged[-1]):
            merged.pop()
            continue
        if len(merged) >= limit:
            break
    return merged


def _metric_from_exam_rows(rows: list[ExamMetricHistoryRow], key: MetricKey) -> MetricValue:
    for row in rows:
        value = getattr(row, key)
        if value is not None:
            return value
    return None


def _has_any_metric(row: ExamMetricHistoryRow) -> bool:
    return any(
        getattr(row, key) is not None
        for key in ("knowledge", "accuracy", "quality", "flexibility", "proficiency")
    )
