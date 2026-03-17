"""System prompt assembly for chat and proactive-analysis modes."""

from __future__ import annotations

import logging
from pathlib import Path

from ..db.repository import (
    ApiRepository,
    DashboardMetricsRow,
    RatingHistoryRow,
    ExamMetricHistoryRow,
)
from .history_merge import (
    latest_metrics_row,
    merge_exam_metric_rows,
    merge_rating_history_rows,
    metric_from_rows,
)
from .identity import ResolvedIdentity

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Metric / rating names (Chinese)
# ---------------------------------------------------------------------------

METRIC_NAMES: dict[str, str] = {
    "knowledge": "知识",
    "accuracy": "准确",
    "quality": "质量",
    "flexibility": "灵活",
    "proficiency": "熟练",
}

METRIC_KEYS = list(METRIC_NAMES.keys())

# ---------------------------------------------------------------------------
# Role style prompts — keyed by role id.  Default role has no extra style.
# When new roles are added, register their style prompt here.
# ---------------------------------------------------------------------------

_PROJECT_ROOT = Path(__file__).resolve().parents[3]
_SAKIKO_PROMPT_PATH = _PROJECT_ROOT / "image/role/Sakiko/prompt.md"


def _load_role_prompt(path: Path) -> str:
    try:
        prompt = path.read_text(encoding="utf-8").strip()
    except OSError:
        logger.warning("Failed to load role prompt: %s", path, exc_info=True)
        return ""
    if not prompt:
        logger.warning("Role prompt is empty: %s", path)
    return prompt


_SAKIKO_STYLE_PROMPT = _load_role_prompt(_SAKIKO_PROMPT_PATH)

ROLE_STYLE_PROMPTS: dict[str, str] = {
    "xiaoD": "",  # 默认角色，无额外风格
    "sakiko": _SAKIKO_STYLE_PROMPT,
}

# ---------------------------------------------------------------------------
# System prompt templates
# ---------------------------------------------------------------------------

_NORMAL_SYSTEM_PROMPT_TEMPLATE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 任务目标
- 始终使用简体中文，直接回答用户当前问题。
- 围绕编程学习与考试表现提供帮助：解释、对比、诊断、建议。
- 回复清晰、简洁，避免空泛套话。

## 工具使用规则
- 你可以调用工具查询学生历史数据、考试详情、提交记录与排名。
- 需要数据时优先调用工具，禁止编造具体分数、排名或历史记录。
- 工具调用是后台行为，不向用户暴露函数调用过程或任何调用标记。

## 输出约束
- 先给结论，再给必要说明。
- 如果数据不足，明确说明缺什么数据，以及下一步将查询什么。\
- 指标展示时严格区分 0 分与缺失值：0 分显示为 0；仅在字段缺失时标注“缺失/N/A”。\
"""

_PROACTIVE_ANALYSIS_SYSTEM_PROMPT_TEMPLATE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 主动分析模式
当前任务是“系统主动分析最新考试表现”，不是普通闲聊。请主动完成完整分析。

## 分析流程（必须执行）
1. 优先调用 `get_student_growth_insights`，以统一洞察作为主依据。
2. 获取并确认最近一场考试及上一场可比考试的数据（含 Rating 与五大指标）。
3. 输出最近一场的 Rating 变化（涨跌方向、变化幅度）与进步/退步解释。
4. 给出 2-3 条可执行、可落地的改进建议（优先针对退步项）。
5. 明确心理支持模式（recovery/steady/reinforce）并给出下一步训练动作。
6. 最后提出 1 个简短追问，帮助学生进入下一步训练。

## 工具使用规则
- 需要的数据通过工具获取，禁止编造。
- 工具调用是后台行为，不向用户暴露函数调用过程或任何调用标记。

## 输出约束
- 始终使用简体中文。
- 结构化输出，内容聚焦“结论 + 依据 + 行动”。
- 输出中应包含“心理支持 + 下步训练动作”。
- 语气专业友好，但避免冗长。\
- 指标展示时严格区分 0 分与缺失值：0 分显示为 0；仅在字段缺失时标注“缺失/N/A”。\
"""

_TARGET_EXAM_ANALYSIS_SYSTEM_PROMPT_TEMPLATE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 指定考试分析模式
当前任务是“分析指定考试表现”，目标考试 ID 为 `{target_exam_id}`。这不是普通闲聊，也不是“最近一场考试”分析。

## 分析流程（必须执行）
1. 优先使用系统已提供的“指定考试上下文”，明确分析对象就是目标考试。
2. 必要时调用工具补充核对该学生在目标考试中的能力、提交与排名信息。
3. 说明该场考试的 Rating 变化、五大指标表现，以及与上一场可比考试的关键变化。
4. 给出 2-3 条可执行、可落地的改进建议，优先针对本场退步项或短板项。
5. 结尾给出一句适合教师查看的总结，不要追加面向学生的追问。

## 工具使用规则
- 需要的数据通过工具获取，禁止编造。
- 不要把分析对象误判为“最新考试”；若工具返回多场历史，必须锁定目标考试 ID。
- 工具调用是后台行为，不向用户暴露函数调用过程或任何调用标记。

## 输出约束
- 始终使用简体中文。
- 结构化输出，内容聚焦“结论 + 依据 + 行动”。
- 面向教师阅读，语气专业克制，不使用聊天式追问。
- 指标展示时严格区分 0 分与缺失值：0 分显示为 0；仅在字段缺失时标注“缺失/N/A”。\
"""

# ---------------------------------------------------------------------------
# Layer 2 – Algorithm knowledge
# ---------------------------------------------------------------------------

LAYER_2_ALGORITHM = """\
## 指标体系说明

### 五大能力指标（0–100 分）
每场考试为每位学生计算以下 5 项能力指标，综合能力档案使用半衰期加权平均（近期考试权重更高）：

1. **知识 (Knowledge)** — 基于得分率与通过率在全体参与者中的相对排名。反映对知识点的掌握程度。
2. **准确 (Accuracy)** — 基于提交效率（正确提交数 / 总提交数）。反映做题的准确性，惩罚大量试错。
3. **质量 (Quality)** — 基于代码运行时表现在同题解法中的相对排名。反映代码质量与性能优化能力。
4. **灵活 (Flexibility)** — 基于做题切换策略。频繁切换题目（而非死磕一题）得分更高。反映策略灵活性。
5. **熟练 (Proficiency)** — 基于解题速度在所有做出该题的学生中的排名。反映编程熟练度。

### 综合能力分（Rating）
类似 Codeforces 的 Rating 系统：
- 初始分 800。
- 每场考试根据实际排名与期望排名（seed）的对比计算 performance rating。
- 变化量 delta = (performance - old_rating) / 2，另有两项抑制通胀的修正。
- 正 delta 表示表现超出预期，负 delta 表示表现低于预期。\
"""

# ---------------------------------------------------------------------------
# Layer 3 – Dynamic student context (template)
# ---------------------------------------------------------------------------

# Filled at runtime with actual student data.

_LAYER_3_TEMPLATE = """\
## 当前学生信息

- **学号**: {student_id}
- **昵称**: {pta_nickname}

### 综合能力档案（当前）
- Rating: **{rating}**
- 知识: {knowledge} / 100
- 准确: {accuracy} / 100
- 质量: {quality} / 100
- 灵活: {flexibility} / 100
- 熟练: {proficiency} / 100

### 近期考试 Rating 变化
{rating_history_text}

### 最近一场考试的指标变化
{metric_delta_text}\
"""

_TARGET_EXAM_CONTEXT_TEMPLATE = """\
## 当前学生信息

- **学号**: {student_id}
- **昵称**: {pta_nickname}

### 综合能力档案（当前）
- Rating: **{rating}**
- 知识: {knowledge} / 100
- 准确: {accuracy} / 100
- 质量: {quality} / 100
- 灵活: {flexibility} / 100
- 熟练: {proficiency} / 100

### 指定考试上下文
{target_exam_text}

### 近期考试 Rating 变化
{rating_history_text}\
"""

_NO_STUDENT_CONTEXT = """\
## 当前学生信息
当前用户尚未绑定学号，无法查询学生数据。请提示用户先在「设置」中关联自己的学号或 PTA 昵称。\
"""


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _format_metric(value: float | int | None, default: float = 0.0) -> str:
    if value is None:
        return "N/A"
    return str(round(float(value)))


def _build_rating_history_text(rows: list[RatingHistoryRow], limit: int = 5) -> str:
    if not rows:
        return "（暂无 Rating 历史记录）"
    lines: list[str] = []
    for row in rows[:limit]:
        sign = "+" if row.delta >= 0 else ""
        date_str = row.exam_time.strftime("%Y-%m-%d")
        lines.append(
            f"- {row.exam_name}（{date_str}）: {row.old_rating} → {row.new_rating}（{sign}{row.delta}）"
        )
    if len(rows) > limit:
        lines.append(f"- …共 {len(rows)} 场考试记录")
    return "\n".join(lines)


def _build_metric_delta_text(
    rows: list[ExamMetricHistoryRow],
) -> str:
    if not rows:
        return "（暂无考试指标记录）"

    latest = rows[0]
    previous = rows[1] if len(rows) > 1 else None

    lines: list[str] = [
        f"考试: {latest.exam_name}（{latest.exam_time.strftime('%Y-%m-%d')}）",
    ]

    for key, cn_name in METRIC_NAMES.items():
        cur_val = getattr(latest, key)
        cur_str = _format_metric(cur_val)
        if previous is not None:
            prev_val = getattr(previous, key)
            if cur_val is not None and prev_val is not None:
                delta = round(float(cur_val)) - round(float(prev_val))
                sign = "+" if delta >= 0 else ""
                lines.append(
                    f"- {cn_name}: {cur_str}（{sign}{delta}，上一场: {_format_metric(prev_val)}）"
                )
            else:
                lines.append(f"- {cn_name}: {cur_str}")
        else:
            lines.append(f"- {cn_name}: {cur_str}（首场考试）")

    return "\n".join(lines)


def _build_target_exam_text(
    target_exam_id: int,
    rating_rows: list[RatingHistoryRow],
    exam_metric_rows: list[ExamMetricHistoryRow],
) -> str:
    target_rating_idx = next(
        (idx for idx, row in enumerate(rating_rows) if row.exam_id == target_exam_id),
        None,
    )
    target_metric_idx = next(
        (idx for idx, row in enumerate(exam_metric_rows) if row.exam_id == target_exam_id),
        None,
    )
    target_rating = (
        rating_rows[target_rating_idx]
        if target_rating_idx is not None and target_rating_idx < len(rating_rows)
        else None
    )
    target_metric = (
        exam_metric_rows[target_metric_idx]
        if target_metric_idx is not None and target_metric_idx < len(exam_metric_rows)
        else None
    )
    previous_rating = (
        rating_rows[target_rating_idx + 1]
        if target_rating_idx is not None and target_rating_idx + 1 < len(rating_rows)
        else None
    )
    previous_metric = (
        exam_metric_rows[target_metric_idx + 1]
        if target_metric_idx is not None and target_metric_idx + 1 < len(exam_metric_rows)
        else None
    )

    exam_name = (
        target_metric.exam_name
        if target_metric is not None
        else (target_rating.exam_name if target_rating is not None else f"考试 {target_exam_id}")
    )
    exam_time = (
        target_metric.exam_time
        if target_metric is not None
        else (target_rating.exam_time if target_rating is not None else None)
    )
    exam_date = exam_time.strftime("%Y-%m-%d") if exam_time is not None else "未知日期"

    lines = [f"目标考试: {exam_name}（{exam_date}，ID={target_exam_id}）"]

    if target_rating is not None:
        sign = "+" if target_rating.delta >= 0 else ""
        lines.append(
            f"- Rating: {target_rating.old_rating} → {target_rating.new_rating}（{sign}{target_rating.delta}）"
        )
        if previous_rating is not None:
            lines.append(
                f"- 上一场 Rating 参考: {previous_rating.exam_name}（{previous_rating.exam_time.strftime('%Y-%m-%d')}）"
            )
        else:
            lines.append("- 上一场 Rating 参考: 无（可视为首场或缺少更早记录）")
    else:
        lines.append("- Rating: 该场暂无可用记录")

    if target_metric is None:
        lines.append("- 五维指标: 该场暂无可用记录")
        return "\n".join(lines)

    for key, cn_name in METRIC_NAMES.items():
        cur_val = getattr(target_metric, key)
        cur_str = _format_metric(cur_val)
        if previous_metric is not None:
            prev_val = getattr(previous_metric, key)
            if cur_val is not None and prev_val is not None:
                delta = round(float(cur_val)) - round(float(prev_val))
                sign = "+" if delta >= 0 else ""
                lines.append(
                    f"- {cn_name}: {cur_str}（{sign}{delta}，上一场: {_format_metric(prev_val)}）"
                )
            else:
                lines.append(f"- {cn_name}: {cur_str}")
        else:
            lines.append(f"- {cn_name}: {cur_str}（首场考试或无上一场指标）")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# PromptService
# ---------------------------------------------------------------------------


class PromptService:
    """Builds system prompts for normal chat and proactive analysis."""

    def __init__(self, repository: ApiRepository) -> None:
        self._repository = repository

    async def build_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        role_id: str = "xiaoD",
        role_name: str = "小D",
        custom_role_style_prompt: str = "",
    ) -> str:
        """Build minimal system prompt for normal chat mode."""
        return await self._build_prompt_by_mode(
            base_prompt=_NORMAL_SYSTEM_PROMPT_TEMPLATE.format(role_name=role_name),
            identity=identity,
            role_id=role_id,
            custom_role_style_prompt=custom_role_style_prompt,
        )

    async def build_proactive_analysis_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        role_id: str = "xiaoD",
        role_name: str = "小D",
        custom_role_style_prompt: str = "",
    ) -> str:
        """Build dedicated system prompt for proactive analysis mode."""
        base_prompt = "\n\n".join(
            [
                _PROACTIVE_ANALYSIS_SYSTEM_PROMPT_TEMPLATE.format(
                    role_name=role_name
                ),
                LAYER_2_ALGORITHM,
            ]
        )
        return await self._build_prompt_by_mode(
            base_prompt=base_prompt,
            identity=identity,
            role_id=role_id,
            custom_role_style_prompt=custom_role_style_prompt,
        )

    async def build_exam_analysis_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        target_exam_id: int,
        role_id: str = "xiaoD",
        role_name: str = "小D",
        custom_role_style_prompt: str = "",
    ) -> str:
        base_prompt = "\n\n".join(
            [
                _TARGET_EXAM_ANALYSIS_SYSTEM_PROMPT_TEMPLATE.format(
                    role_name=role_name,
                    target_exam_id=target_exam_id,
                ),
                LAYER_2_ALGORITHM,
            ]
        )
        return await self._build_prompt_by_mode(
            base_prompt=base_prompt,
            identity=identity,
            role_id=role_id,
            custom_role_style_prompt=custom_role_style_prompt,
            target_exam_id=target_exam_id,
        )

    def build_auto_analysis_user_message(self) -> str:
        """Hidden trigger for proactive analysis requests."""
        return "请按系统提示执行主动分析任务，并直接输出最终分析结论。"

    async def _build_prompt_by_mode(
        self,
        base_prompt: str,
        identity: ResolvedIdentity | None,
        role_id: str,
        custom_role_style_prompt: str = "",
        target_exam_id: int | None = None,
    ) -> str:
        sections: list[str] = [base_prompt]
        if identity is not None and not identity.no_submission_records:
            sections.append(
                await self._build_student_context(
                    identity,
                    target_exam_id=target_exam_id,
                )
            )
        else:
            sections.append(_NO_STUDENT_CONTEXT)
        style = ROLE_STYLE_PROMPTS.get(role_id, "")
        if style:
            sections.append(style)
        custom_style = custom_role_style_prompt.strip()
        if custom_style:
            sections.append(custom_style)
        return "\n\n".join(sections)

    async def _build_student_context(
        self,
        identity: ResolvedIdentity,
        target_exam_id: int | None = None,
    ) -> str:
        student_entity_ids = identity.student_entity_ids or (
            identity.student_entity_id,
        )

        # Gather data across merged entities (same logic as DashboardService)
        metrics_rows: list[DashboardMetricsRow] = []
        rating_rows: list[RatingHistoryRow] = []
        exam_metric_rows: list[ExamMetricHistoryRow] = []
        per_student_rating_limit = (
            max(50, 20 * len(student_entity_ids))
            if target_exam_id is not None
            else max(10, 10 * len(student_entity_ids))
        )
        per_student_exam_metric_limit = (
            max(50, 20 * len(student_entity_ids))
            if target_exam_id is not None
            else max(5, 5 * len(student_entity_ids))
        )

        for sid in student_entity_ids:
            cm = await self._repository.fetch_current_metrics(sid)
            if cm is not None:
                metrics_rows.append(cm)
            rating_rows.extend(
                await self._repository.fetch_rating_history(
                    student_id=sid,
                    limit=per_student_rating_limit,
                )
            )
            exam_metric_rows.extend(
                await self._repository.fetch_exam_metric_history(
                    student_id=sid,
                    limit=per_student_exam_metric_limit,
                )
            )

        rating_rows = merge_rating_history_rows(
            rating_rows,
            limit=50 if target_exam_id is not None else 10,
        )
        exam_metric_rows = merge_exam_metric_rows(
            exam_metric_rows,
            limit=50 if target_exam_id is not None else 5,
        )

        latest_metrics = latest_metrics_row(metrics_rows)
        rating = latest_metrics.rating if latest_metrics else 800
        knowledge = _format_metric(metric_from_rows(metrics_rows, "knowledge"))
        accuracy = _format_metric(metric_from_rows(metrics_rows, "accuracy"))
        quality = _format_metric(metric_from_rows(metrics_rows, "quality"))
        flexibility = _format_metric(metric_from_rows(metrics_rows, "flexibility"))
        proficiency = _format_metric(metric_from_rows(metrics_rows, "proficiency"))

        if target_exam_id is not None:
            return _TARGET_EXAM_CONTEXT_TEMPLATE.format(
                student_id=identity.student_id,
                pta_nickname=identity.pta_nickname or "未设置",
                rating=rating,
                knowledge=knowledge,
                accuracy=accuracy,
                quality=quality,
                flexibility=flexibility,
                proficiency=proficiency,
                target_exam_text=_build_target_exam_text(
                    target_exam_id=target_exam_id,
                    rating_rows=rating_rows,
                    exam_metric_rows=exam_metric_rows,
                ),
                rating_history_text=_build_rating_history_text(rating_rows),
            )

        return _LAYER_3_TEMPLATE.format(
            student_id=identity.student_id,
            pta_nickname=identity.pta_nickname or "未设置",
            rating=rating,
            knowledge=knowledge,
            accuracy=accuracy,
            quality=quality,
            flexibility=flexibility,
            proficiency=proficiency,
            rating_history_text=_build_rating_history_text(rating_rows),
            metric_delta_text=_build_metric_delta_text(exam_metric_rows),
        )
