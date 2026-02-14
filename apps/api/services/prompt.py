"""System prompt assembly for the chat Agent.

Four-layer architecture:
  Layer 1 – Base instructions (language, persona, output style)
  Layer 2 – Algorithm knowledge (metric & rating definitions)
  Layer 3 – Dynamic student context (current values from DB)
  Layer 4 – Role style (extra persona prompt from role config)
"""

from __future__ import annotations

from ..db.repository import (
    ApiRepository,
    DashboardMetricsRow,
    RatingHistoryRow,
    ExamMetricHistoryRow,
)
from .identity import ResolvedIdentity

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

ROLE_STYLE_PROMPTS: dict[str, str] = {
    "xiaoD": "",  # 默认角色，无额外风格
}

# ---------------------------------------------------------------------------
# Layer 1 – Base instructions
# ---------------------------------------------------------------------------

LAYER_1_BASE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 核心规则
- 始终使用**简体中文**回复。
- 你的任务是帮助学生理解自己在编程练习中的表现，提供鼓励和有针对性的建议。
- 回复简洁清晰，适合大学生阅读。使用适当的 Markdown 格式（加粗、列表等）提升可读性。
- 在分析中保持客观，用数据支撑观点。既要肯定进步，也要直面不足。
- 不要编造数据。如果缺少某项信息，明确说明。
- 你可以调用工具来查询学生的历史数据、考试详情和提交记录。需要时主动使用这些工具。\
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


# ---------------------------------------------------------------------------
# PromptService
# ---------------------------------------------------------------------------


class PromptService:
    """Assembles the multi-layer system prompt for LLM requests."""

    def __init__(self, repository: ApiRepository) -> None:
        self._repository = repository

    async def build_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        role_id: str = "xiaoD",
        role_name: str = "小D",
    ) -> str:
        """Build the full system prompt.

        Parameters
        ----------
        identity
            Resolved student identity. ``None`` if the user is anonymous or
            hasn't linked a student profile yet.
        role_id
            Role identifier (used to look up extra style prompt).
        role_name
            Display name injected into layer-1 persona.
        """
        layers: list[str] = []

        # Layer 1
        layers.append(LAYER_1_BASE.format(role_name=role_name))

        # Layer 2
        layers.append(LAYER_2_ALGORITHM)

        # Layer 3 – dynamic student context
        if identity is not None and not identity.no_submission_records:
            layer3 = await self._build_student_context(identity)
        else:
            layer3 = _NO_STUDENT_CONTEXT
        layers.append(layer3)

        # Layer 4 – role style
        style = ROLE_STYLE_PROMPTS.get(role_id, "")
        if style:
            layers.append(style)

        return "\n\n".join(layers)

    def build_auto_analysis_user_message(self) -> str:
        """Build the hidden user message that triggers auto-analysis.

        The frontend hides this message; only the assistant response is shown,
        making it look like the assistant initiated the conversation.
        """
        return (
            "系统检测到有新的考试数据导入。请你主动分析我最近一场考试的表现，包括：\n"
            "1. Rating 变化（是涨了还是跌了，幅度如何）\n"
            "2. 五大能力指标相比上一场的变化，哪些进步了、哪些退步了\n"
            "3. 对进步的方面给予肯定\n"
            "4. 对退步或薄弱的方面给出简短但具体的改进建议\n\n"
            "请用简洁友好的语气回复，像是主动和我打招呼一样。"
        )

    async def _build_student_context(self, identity: ResolvedIdentity) -> str:
        student_entity_ids = identity.student_entity_ids or (
            identity.student_entity_id,
        )

        # Gather data across merged entities (same logic as DashboardService)
        metrics_row: DashboardMetricsRow | None = None
        rating_rows: list[RatingHistoryRow] = []
        exam_metric_rows: list[ExamMetricHistoryRow] = []

        for sid in student_entity_ids:
            cm = await self._repository.fetch_current_metrics(sid)
            if cm is not None and metrics_row is None:
                metrics_row = cm
            rating_rows.extend(
                await self._repository.fetch_rating_history(student_id=sid, limit=10)
            )
            exam_metric_rows.extend(
                await self._repository.fetch_exam_metric_history(
                    student_id=sid, limit=5
                )
            )

        # De-duplicate & sort
        rating_rows = _dedup_rating(rating_rows)
        exam_metric_rows = _dedup_exam_metrics(exam_metric_rows)

        rating = metrics_row.rating if metrics_row else 800
        knowledge = _format_metric(metrics_row.knowledge if metrics_row else None)
        accuracy = _format_metric(metrics_row.accuracy if metrics_row else None)
        quality = _format_metric(metrics_row.quality if metrics_row else None)
        flexibility = _format_metric(metrics_row.flexibility if metrics_row else None)
        proficiency = _format_metric(metrics_row.proficiency if metrics_row else None)

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


def _dedup_rating(rows: list[RatingHistoryRow]) -> list[RatingHistoryRow]:
    seen: set[int] = set()
    result: list[RatingHistoryRow] = []
    ordered = sorted(rows, key=lambda r: (r.exam_time, r.exam_id), reverse=True)
    for r in ordered:
        if r.exam_id not in seen:
            seen.add(r.exam_id)
            result.append(r)
    return result


def _dedup_exam_metrics(rows: list[ExamMetricHistoryRow]) -> list[ExamMetricHistoryRow]:
    seen: set[int] = set()
    result: list[ExamMetricHistoryRow] = []
    ordered = sorted(rows, key=lambda r: (r.exam_time, r.exam_id), reverse=True)
    for r in ordered:
        if r.exam_id not in seen:
            seen.add(r.exam_id)
            result.append(r)
    return result
