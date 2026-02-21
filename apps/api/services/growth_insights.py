from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from math import ceil
from typing import Literal

from ..db.repository import (
    ApiRepository,
    ExamBandMediansRow,
    ExamMetricHistoryRow,
    ExamParticipantContextRow,
    ExamPreviousRankerRow,
    RatingHistoryRow,
)
from ..schemas.students import (
    MilestoneItemResponse,
    MilestoneStreakResponse,
    PeerComparisonResponse,
    PeerMetricGapResponse,
    PercentileBandComparisonResponse,
    PostExamSupportResponse,
    PreviousRankerComparisonResponse,
    ProgressExplanationResponse,
)
from .identity import ResolvedIdentity

MetricKey = Literal["knowledge", "accuracy", "quality", "flexibility", "proficiency"]

_METRIC_KEYS: tuple[MetricKey, ...] = (
    "knowledge",
    "accuracy",
    "quality",
    "flexibility",
    "proficiency",
)

_METRIC_LABELS: dict[MetricKey, str] = {
    "knowledge": "知识",
    "accuracy": "准确",
    "quality": "质量",
    "flexibility": "灵活",
    "proficiency": "熟练",
}

_IMPROVEMENT_REASONS: dict[MetricKey, str] = {
    "knowledge": "说明本次知识点命中更稳定",
    "accuracy": "说明提交策略更稳，低效尝试更少",
    "quality": "说明代码实现质量更稳，边界处理更扎实",
    "flexibility": "说明做题取舍更灵活，节奏把控更好",
    "proficiency": "说明编码速度更顺，熟练度提升",
}

_SETBACK_REASONS: dict[MetricKey, str] = {
    "knowledge": "说明知识点命中还不稳定",
    "accuracy": "说明无效提交偏多或策略波动",
    "quality": "说明实现细节质量波动，需要复盘边界与复杂度",
    "flexibility": "说明切题策略偏保守，建议优化做题节奏",
    "proficiency": "说明解题速度有回落，需要强化模板熟练度",
}

_PROGRESS_IMPROVEMENT_THRESHOLD = 3
_PROGRESS_SETBACK_THRESHOLD = -3
_RATING_MILESTONES: tuple[int, ...] = (900, 1000, 1200, 1400)
_METRIC_MILESTONES: tuple[int, ...] = (60, 70, 80, 90)
_STREAK_MILESTONES: tuple[int, ...] = (3, 5, 8)


@dataclass(slots=True)
class GrowthInsightsPayload:
    progress_explanation: ProgressExplanationResponse
    milestone_streak: MilestoneStreakResponse
    peer_comparison: PeerComparisonResponse
    post_exam_support: PostExamSupportResponse


@dataclass(slots=True)
class _MilestoneEvent:
    exam_id: int | None
    exam_date: str | None
    exam_time: datetime
    code: str
    label: str
    detail: str


def _now_floor() -> datetime:
    return datetime.min.replace(tzinfo=timezone.utc)


def _metric_value(value: Decimal | float | int | None) -> float | None:
    if value is None:
        return None
    if isinstance(value, Decimal):
        return float(value)
    return float(value)


def _metric_int(value: Decimal | float | int | None) -> int | None:
    parsed = _metric_value(value)
    if parsed is None:
        return None
    return int(round(parsed))


def _float_gap(current: Decimal | float | int | None, baseline: Decimal | float | int | None) -> float | None:
    cur = _metric_value(current)
    base = _metric_value(baseline)
    if cur is None or base is None:
        return None
    return round(cur - base, 1)


def _int_gap(current: Decimal | float | int | None, baseline: Decimal | float | int | None) -> int | None:
    cur = _metric_int(current)
    base = _metric_int(baseline)
    if cur is None or base is None:
        return None
    return cur - base


def _empty_peer_gap() -> PeerMetricGapResponse:
    return PeerMetricGapResponse(
        score=None,
        solved=None,
        knowledge=None,
        accuracy=None,
        quality=None,
        flexibility=None,
        proficiency=None,
    )


def build_empty_growth_insights() -> GrowthInsightsPayload:
    return GrowthInsightsPayload(
        progress_explanation=ProgressExplanationResponse(
            available=False,
            latestExamId=None,
            latestExamName=None,
            latestExamDate=None,
            ratingDelta=None,
            keyImprovements=[],
            keySetbacks=[],
            summary="暂无可用考试数据，完成下一场考试后可生成成长解释。",
        ),
        milestone_streak=MilestoneStreakResponse(
            available=False,
            currentPositiveStreak=0,
            bestPositiveStreak=0,
            newMilestones=[],
            recentMilestones=[],
            nextTargets=[],
        ),
        peer_comparison=PeerComparisonResponse(
            available=False,
            defaultMode="percentile_band",
            percentileBand=PercentileBandComparisonResponse(
                totalParticipants=0,
                myRank=None,
                myPercentile=None,
                bandCode=None,
                bandLabel="暂无可比较数据",
                gapVsBandMedian=_empty_peer_gap(),
            ),
            previousRanker=PreviousRankerComparisonResponse(
                available=False,
                rankGap=None,
                scoreGap=None,
                solvedGap=None,
                metricGapVsPrevious=_empty_peer_gap(),
            ),
        ),
        post_exam_support=PostExamSupportResponse(
            available=False,
            mode="steady",
            headline="先稳住节奏",
            message="当前暂无足够数据做完整判断，先完成下一场训练，再看趋势变化。",
            actionPlan=[
                "先复盘最近一次训练，标出 1 个可立即修正的问题。",
                "下一场练习只设 1 个核心目标，避免目标过多。",
                "训练后记录 3 句话：做对了什么、卡住了什么、下次怎么改。",
            ],
            checkInQuestion="下一场你最想先稳住哪一项能力？",
        ),
    )


class GrowthInsightsService:
    def __init__(self, repository: ApiRepository) -> None:
        self._repository = repository

    async def build(
        self,
        identity: ResolvedIdentity,
        rating_rows: list[RatingHistoryRow],
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> GrowthInsightsPayload:
        if not rating_rows and not exam_metric_rows:
            return build_empty_growth_insights()

        progress = self._build_progress_explanation(rating_rows, exam_metric_rows)
        milestones = self._build_milestone_streak(rating_rows, exam_metric_rows)
        peer = await self._build_peer_comparison(identity, exam_metric_rows)
        support = self._build_post_exam_support(progress, exam_metric_rows)

        return GrowthInsightsPayload(
            progress_explanation=progress,
            milestone_streak=milestones,
            peer_comparison=peer,
            post_exam_support=support,
        )

    def _build_progress_explanation(
        self,
        rating_rows: list[RatingHistoryRow],
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> ProgressExplanationResponse:
        latest_exam_metric = exam_metric_rows[0] if exam_metric_rows else None
        latest_rating = rating_rows[0] if rating_rows else None

        latest_exam_id: str | None = None
        latest_exam_name: str | None = None
        latest_exam_date: str | None = None

        if latest_exam_metric is not None:
            latest_exam_id = str(latest_exam_metric.exam_id)
            latest_exam_name = latest_exam_metric.exam_name
            latest_exam_date = latest_exam_metric.exam_time.date().isoformat()
        elif latest_rating is not None:
            latest_exam_id = str(latest_rating.exam_id)
            latest_exam_name = latest_rating.exam_name
            latest_exam_date = latest_rating.exam_time.date().isoformat()

        if latest_exam_id is None:
            return build_empty_growth_insights().progress_explanation

        rating_delta: int | None = None
        if latest_rating is not None:
            if latest_exam_metric is None or latest_rating.exam_id == latest_exam_metric.exam_id:
                rating_delta = latest_rating.delta
            else:
                for row in rating_rows:
                    if str(row.exam_id) == latest_exam_id:
                        rating_delta = row.delta
                        break

        latest = latest_exam_metric
        previous = exam_metric_rows[1] if len(exam_metric_rows) > 1 else None

        improvements: list[tuple[MetricKey, int]] = []
        setbacks: list[tuple[MetricKey, int]] = []

        if latest is not None:
            for key in _METRIC_KEYS:
                latest_value = _metric_int(getattr(latest, key))
                baseline_value = _metric_int(getattr(previous, key) if previous is not None else 0)
                if latest_value is None:
                    continue
                delta = latest_value - (baseline_value or 0)
                if delta >= _PROGRESS_IMPROVEMENT_THRESHOLD:
                    improvements.append((key, delta))
                elif delta <= _PROGRESS_SETBACK_THRESHOLD:
                    setbacks.append((key, delta))

        improvements.sort(key=lambda item: item[1], reverse=True)
        setbacks.sort(key=lambda item: item[1])

        improvement_lines = [
            f"{_METRIC_LABELS[key]} {delta:+d}，{_IMPROVEMENT_REASONS[key]}"
            for key, delta in improvements[:2]
        ]
        setback_lines = [
            f"{_METRIC_LABELS[key]} {delta:+d}，{_SETBACK_REASONS[key]}"
            for key, delta in setbacks[:2]
        ]

        summary = self._build_progress_summary(
            rating_delta=rating_delta,
            improvements=improvement_lines,
            setbacks=setback_lines,
            is_first_exam=latest is not None and previous is None,
        )

        return ProgressExplanationResponse(
            available=True,
            latestExamId=latest_exam_id,
            latestExamName=latest_exam_name,
            latestExamDate=latest_exam_date,
            ratingDelta=rating_delta,
            keyImprovements=improvement_lines,
            keySetbacks=setback_lines,
            summary=summary,
        )

    def _build_progress_summary(
        self,
        rating_delta: int | None,
        improvements: list[str],
        setbacks: list[str],
        is_first_exam: bool,
    ) -> str:
        if rating_delta is not None:
            if rating_delta > 0:
                if improvements:
                    return f"本场整体向好，Rating +{rating_delta}，关键增益来自{improvements[0].split('，')[0]}。"
                return f"本场 Rating +{rating_delta}，整体趋势在上行。"
            if rating_delta < 0:
                if setbacks:
                    return f"本场出现回落，Rating {rating_delta}，主要波动在{setbacks[0].split('，')[0]}。"
                return f"本场 Rating {rating_delta}，建议先稳住做题节奏。"
            return "本场 Rating 持平，能力结构有变化，建议保留有效策略并修正波动点。"

        if improvements and not setbacks:
            prefix = "首场考试表现积极" if is_first_exam else "本场能力表现向好"
            return f"{prefix}，亮点集中在{improvements[0].split('，')[0]}。"
        if setbacks and not improvements:
            prefix = "首场考试出现波动" if is_first_exam else "本场能力有回落"
            return f"{prefix}，优先关注{setbacks[0].split('，')[0]}。"
        if improvements and setbacks:
            return "本场有进步也有波动，建议保留有效策略并优先修正退步项。"
        if is_first_exam:
            return "这是首场考试，当前数据将作为后续成长对比基线。"
        return "本场整体变化不大，建议保持节奏并观察下一场趋势。"

    def _build_milestone_streak(
        self,
        rating_rows: list[RatingHistoryRow],
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> MilestoneStreakResponse:
        current_positive_streak, best_positive_streak = self._compute_positive_streaks(
            rating_rows
        )
        events = self._collect_milestone_events(rating_rows, exam_metric_rows)

        latest_exam_id = (
            exam_metric_rows[0].exam_id
            if exam_metric_rows
            else (rating_rows[0].exam_id if rating_rows else None)
        )

        new_milestones = [
            self._to_milestone_item(event)
            for event in events
            if latest_exam_id is not None and event.exam_id == latest_exam_id
        ]

        recent_milestones = [
            self._to_milestone_item(event)
            for event in sorted(events, key=lambda item: item.exam_time, reverse=True)[:5]
        ]

        next_targets = self._build_next_targets(rating_rows, exam_metric_rows)

        return MilestoneStreakResponse(
            available=bool(rating_rows or exam_metric_rows),
            currentPositiveStreak=current_positive_streak,
            bestPositiveStreak=best_positive_streak,
            newMilestones=new_milestones,
            recentMilestones=recent_milestones,
            nextTargets=next_targets,
        )

    def _compute_positive_streaks(
        self,
        rating_rows: list[RatingHistoryRow],
    ) -> tuple[int, int]:
        current = 0
        for row in rating_rows:
            if row.delta > 0:
                current += 1
                continue
            break

        best = 0
        running = 0
        for row in sorted(rating_rows, key=lambda item: (item.exam_time, item.exam_id)):
            if row.delta > 0:
                running += 1
                best = max(best, running)
            else:
                running = 0

        return current, best

    def _collect_milestone_events(
        self,
        rating_rows: list[RatingHistoryRow],
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> list[_MilestoneEvent]:
        events: list[_MilestoneEvent] = []

        rating_chronological = sorted(
            rating_rows,
            key=lambda row: (row.exam_time, row.exam_id),
        )
        for row in rating_chronological:
            for threshold in _RATING_MILESTONES:
                if row.old_rating < threshold <= row.new_rating:
                    events.append(
                        _MilestoneEvent(
                            exam_id=row.exam_id,
                            exam_date=row.exam_time.date().isoformat(),
                            exam_time=row.exam_time,
                            code=f"rating_{threshold}",
                            label=f"Rating 达到 {threshold}",
                            detail=f"本场后 Rating 达到 {row.new_rating}",
                        )
                    )

        streak = 0
        for row in rating_chronological:
            previous_streak = streak
            streak = streak + 1 if row.delta > 0 else 0
            for threshold in _STREAK_MILESTONES:
                if previous_streak < threshold <= streak:
                    events.append(
                        _MilestoneEvent(
                            exam_id=row.exam_id,
                            exam_date=row.exam_time.date().isoformat(),
                            exam_time=row.exam_time,
                            code=f"streak_{threshold}",
                            label=f"正增连胜达到 {threshold} 场",
                            detail=f"已连续 {streak} 场 Rating 正增长",
                        )
                    )

        metric_chronological = sorted(
            exam_metric_rows,
            key=lambda row: (row.exam_time, row.exam_id),
        )
        previous_row: ExamMetricHistoryRow | None = None
        for row in metric_chronological:
            for key in _METRIC_KEYS:
                current = _metric_int(getattr(row, key))
                if current is None:
                    continue
                baseline = _metric_int(getattr(previous_row, key) if previous_row is not None else 0)
                previous_value = baseline or 0
                for threshold in _METRIC_MILESTONES:
                    if previous_value < threshold <= current:
                        label = _METRIC_LABELS[key]
                        events.append(
                            _MilestoneEvent(
                                exam_id=row.exam_id,
                                exam_date=row.exam_time.date().isoformat(),
                                exam_time=row.exam_time,
                                code=f"{key}_{threshold}",
                                label=f"{label}达到 {threshold}",
                                detail=f"本场 {label}来到 {current}",
                            )
                        )
            previous_row = row

        return sorted(events, key=lambda item: (item.exam_time, item.code))

    def _to_milestone_item(self, event: _MilestoneEvent) -> MilestoneItemResponse:
        return MilestoneItemResponse(
            code=event.code,
            label=event.label,
            detail=event.detail,
            examId=str(event.exam_id) if event.exam_id is not None else None,
            examDate=event.exam_date,
        )

    def _build_next_targets(
        self,
        rating_rows: list[RatingHistoryRow],
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> list[str]:
        targets: list[str] = []

        latest_rating = rating_rows[0].new_rating if rating_rows else None
        if latest_rating is not None:
            next_rating = next(
                (threshold for threshold in _RATING_MILESTONES if threshold > latest_rating),
                None,
            )
            if next_rating is not None:
                targets.append(f"再 +{next_rating - latest_rating} rating 到 {next_rating}")

        if exam_metric_rows:
            latest = exam_metric_rows[0]
            metric_candidate: tuple[MetricKey, int, int] | None = None
            for key in _METRIC_KEYS:
                value = _metric_int(getattr(latest, key))
                if value is None:
                    continue
                next_metric_threshold = next(
                    (threshold for threshold in _METRIC_MILESTONES if threshold > value),
                    None,
                )
                if next_metric_threshold is None:
                    continue
                gap = next_metric_threshold - value
                if metric_candidate is None or gap < metric_candidate[2]:
                    metric_candidate = (key, next_metric_threshold, gap)
            if metric_candidate is not None:
                key, threshold, gap = metric_candidate
                targets.append(f"{_METRIC_LABELS[key]}再 +{gap} 到 {threshold}")

        if not targets:
            targets.append("保持节奏，争取在下一场考试刷新个人最佳表现")

        return targets[:2]

    async def _build_peer_comparison(
        self,
        identity: ResolvedIdentity,
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> PeerComparisonResponse:
        latest_exam = exam_metric_rows[0] if exam_metric_rows else None
        if latest_exam is None:
            return build_empty_growth_insights().peer_comparison

        context_fetcher = getattr(self._repository, "fetch_exam_participant_context", None)
        band_fetcher = getattr(self._repository, "fetch_exam_band_medians", None)
        previous_fetcher = getattr(self._repository, "fetch_exam_previous_ranker", None)
        if not callable(context_fetcher) or not callable(band_fetcher) or not callable(previous_fetcher):
            return build_empty_growth_insights().peer_comparison

        student_ids = list(identity.student_entity_ids or (identity.student_entity_id,))
        context = await context_fetcher(latest_exam.exam_id, student_ids)
        if context is None or context.total_participants <= 0:
            return build_empty_growth_insights().peer_comparison

        band_code, band_label, pos_start, pos_end = self._resolve_band(
            total=context.total_participants,
            my_pos=context.position,
        )
        band_medians = await band_fetcher(latest_exam.exam_id, pos_start, pos_end)
        percentile_band = self._build_percentile_band_payload(
            context=context,
            latest_exam=latest_exam,
            band_code=band_code,
            band_label=band_label,
            band_medians=band_medians,
        )

        previous_ranker_row = await previous_fetcher(latest_exam.exam_id, context.position)
        previous_ranker = self._build_previous_ranker_payload(
            context=context,
            latest_exam=latest_exam,
            previous=previous_ranker_row,
        )

        return PeerComparisonResponse(
            available=True,
            defaultMode="percentile_band",
            percentileBand=percentile_band,
            previousRanker=previous_ranker,
        )

    def _resolve_band(self, total: int, my_pos: int) -> tuple[str, str, int, int]:
        top10_end = max(1, ceil(total * 0.1))
        top30_end = max(top10_end + 1, ceil(total * 0.3))
        median_end = max(top30_end + 1, ceil(total * 0.7))

        if my_pos <= top10_end:
            return "top_10", "Top 10%", 1, top10_end
        if my_pos <= top30_end:
            return "top_30", "Top 30%", top10_end + 1, top30_end
        if my_pos <= median_end:
            return "median_zone", "中位区间", top30_end + 1, median_end
        return "improve_zone", "提升区间", median_end + 1, total

    def _build_percentile_band_payload(
        self,
        context: ExamParticipantContextRow,
        latest_exam: ExamMetricHistoryRow,
        band_code: str,
        band_label: str,
        band_medians: ExamBandMediansRow | None,
    ) -> PercentileBandComparisonResponse:
        total = context.total_participants
        my_percentile = round(((total - context.position + 1) / total) * 100, 1)

        gap_payload = _empty_peer_gap()
        if band_medians is not None:
            gap_payload = PeerMetricGapResponse(
                score=_float_gap(context.total_score, band_medians.total_score_median),
                solved=_int_gap(context.solved_count, band_medians.solved_count_median),
                knowledge=_int_gap(latest_exam.knowledge, band_medians.knowledge_median),
                accuracy=_int_gap(latest_exam.accuracy, band_medians.accuracy_median),
                quality=_int_gap(latest_exam.quality, band_medians.quality_median),
                flexibility=_int_gap(latest_exam.flexibility, band_medians.flexibility_median),
                proficiency=_int_gap(latest_exam.proficiency, band_medians.proficiency_median),
            )

        return PercentileBandComparisonResponse(
            totalParticipants=total,
            myRank=context.rank if context.rank is not None else context.position,
            myPercentile=my_percentile,
            bandCode=band_code,
            bandLabel=band_label,
            gapVsBandMedian=gap_payload,
        )

    def _build_previous_ranker_payload(
        self,
        context: ExamParticipantContextRow,
        latest_exam: ExamMetricHistoryRow,
        previous: ExamPreviousRankerRow | None,
    ) -> PreviousRankerComparisonResponse:
        if previous is None:
            return PreviousRankerComparisonResponse(
                available=False,
                rankGap=None,
                scoreGap=None,
                solvedGap=None,
                metricGapVsPrevious=_empty_peer_gap(),
            )

        if context.rank is not None and previous.rank is not None:
            rank_gap = context.rank - previous.rank
        else:
            rank_gap = context.position - previous.position

        return PreviousRankerComparisonResponse(
            available=True,
            rankGap=rank_gap,
            scoreGap=_float_gap(previous.total_score, context.total_score),
            solvedGap=_int_gap(previous.solved_count, context.solved_count),
            metricGapVsPrevious=PeerMetricGapResponse(
                score=None,
                solved=None,
                knowledge=_int_gap(previous.knowledge, latest_exam.knowledge),
                accuracy=_int_gap(previous.accuracy, latest_exam.accuracy),
                quality=_int_gap(previous.quality, latest_exam.quality),
                flexibility=_int_gap(previous.flexibility, latest_exam.flexibility),
                proficiency=_int_gap(previous.proficiency, latest_exam.proficiency),
            ),
        )

    def _build_post_exam_support(
        self,
        progress: ProgressExplanationResponse,
        exam_metric_rows: list[ExamMetricHistoryRow],
    ) -> PostExamSupportResponse:
        if not progress.available:
            return build_empty_growth_insights().post_exam_support

        improvement_count = 0
        setback_count = 0
        if exam_metric_rows:
            latest = exam_metric_rows[0]
            previous = exam_metric_rows[1] if len(exam_metric_rows) > 1 else None
            for key in _METRIC_KEYS:
                current = _metric_int(getattr(latest, key))
                baseline = _metric_int(getattr(previous, key) if previous is not None else 0)
                if current is None:
                    continue
                delta = current - (baseline or 0)
                if delta >= _PROGRESS_IMPROVEMENT_THRESHOLD:
                    improvement_count += 1
                elif delta <= _PROGRESS_SETBACK_THRESHOLD:
                    setback_count += 1

        rating_delta = progress.ratingDelta or 0

        if rating_delta <= -10 or setback_count >= 3:
            return PostExamSupportResponse(
                available=True,
                mode="recovery",
                headline="先止损，再反弹",
                message="这次波动不代表能力定型，先把失分来源收敛，下一场就有机会稳住回升。",
                actionPlan=[
                    "先挑 1 道最可惜的错题，补齐触发错误的知识点。",
                    "下一次练习限制无效提交次数，先想后交。",
                    "练习结束后写下 1 条“我下次会怎么做”的执行句。",
                ],
                checkInQuestion="如果下一场只改 1 件事，你最想先改哪一件？",
            )

        if rating_delta >= 10 and improvement_count >= 3:
            return PostExamSupportResponse(
                available=True,
                mode="reinforce",
                headline="保持优势，固化方法",
                message="当前上升势头很明确，关键是把这次有效策略沉淀成可复用流程。",
                actionPlan=[
                    "复盘本场最有效的 2 个策略，并写成固定检查清单。",
                    "下一场沿用同样节奏，再增加 1 个小挑战目标。",
                    "保留做对题目的模板，形成个人题型解法库。",
                ],
                checkInQuestion="这次最值得复用的做题策略是哪一条？",
            )

        return PostExamSupportResponse(
            available=True,
            mode="steady",
            headline="稳步推进",
            message="整体状态可控，先维持有效节奏，再逐步抬高短板能力。",
            actionPlan=[
                "保持当前做题节奏，优先减少 1 类重复失误。",
                "为下一场设定 1 个可量化小目标（如准确 +3）。",
                "训练后复盘 10 分钟，记录可复用步骤。",
            ],
            checkInQuestion="下一场你希望先把哪项指标提升一点点？",
        )
