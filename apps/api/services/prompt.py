"""System prompt assembly for chat and proactive-analysis modes."""

from __future__ import annotations

import logging
import string
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from ..core.errors import AppError
from ..db.repository import ApiRepository
from .identity import ResolvedIdentity
from .tools import (
    FACT_TOOL_PROMPT_LINES,
    NOTES_TOOL_PROMPT_LINES,
    UI_TOOL_PROMPT_LINES,
)

logger = logging.getLogger(__name__)

METRIC_NAMES: dict[str, str] = {
    "knowledge": "知识",
    "accuracy": "准确",
    "quality": "质量",
    "flexibility": "灵活",
    "proficiency": "熟练",
}

METRIC_KEYS = list(METRIC_NAMES.keys())

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
    "xiaoD": "",
    "sakiko": _SAKIKO_STYLE_PROMPT,
}

PromptCategory = str


@dataclass(frozen=True, slots=True)
class PromptDefinition:
    key: str
    title: str
    description: str
    category: PromptCategory
    default_content: str
    allowed_variables: tuple[str, ...] = ()
    required_variables: tuple[str, ...] = ()
    sample_variables: dict[str, str] | None = None

_TOOL_RULES = """\
## 工具使用规则
- 你可以调用事实工具获取学生学习画像、指定考试全体学生榜单指标、提交记录、题目推荐和学习路径。
- 需要具体分数、排名、Rating、五维指标、提交细节时必须先调用工具，禁止编造。
- 用户请求推荐题目、刷什么题时，直接调用 `get_problem_recommendations`；无明确条件时只传 `top_k` 或空参数，不要编造过滤参数。
- 用户请求学习路线、先学什么、知识点路径时，直接调用 `get_learning_path`；无明确主题或条数时传空参数。
- 工具只返回事实数据；对比、诊断、建议、心理支持和训练动作都由你基于事实自行完成。
- 工具调用是后台行为，不向用户暴露函数调用过程或任何调用标记。

## 可用事实工具
{fact_tool_lines}

## 长期笔记工具
你可以读写一份跨会话持久化的「长期笔记」（用户当前激活的那一份；其内容已在系统提示中给出）。
{notes_tool_lines}
- 用户要求整理、精简、补充、删除、改写笔记时，必须调用 `update_notes`。
- 成功调用 `update_notes` 之前，不要说笔记已经修改完成。
- 写笔记时保留仍有价值的用户内容，不要无故清空或丢失手写记录。

## UI 富组件工具
聊天气泡支持结构化富块。能用结构化块时不要再用 markdown 罗列：
- 提到具体题目、推荐题目时调用 `emit_problem_card`，不要在文字里手写"题目：xxx, 难度：xxx"。
- 给学生出选择题考核时调用 `emit_choice`；用户选择后，UI 会把题干、选项和用户选择作为隐藏用户输入继续触发一轮回复。
- 多步公式推导调用 `emit_math_steps`，每一步一条 tex；不要把整段 LaTeX 揉在普通段落里。
- 输出代码用 `emit_code` 而不是 markdown ``` 代码围栏，前端会渲染成带语言徽章和复制按钮的卡。
- 提到一个知识点时（尤其用户的学习路径里出现的）配套调用 `emit_node_ref`，让用户点击直接跳到右侧地图详情。
- 强提示/警告/提示性短语优先用 `emit_callout`（info/warn/tip），比 emoji 列表更突出。
- 想让学生注意到当前路径上的某个节点时，调用 `focus_knowledge_node` 让地图自动展开，再开始讲。
{ui_tool_lines}
- UI 工具的调用是 UI 信号，不返回业务数据；调用前后仍需要把"为什么推荐"用文字解释清楚。
- 不要重复造卡：同一道题、同一个知识点不要在一次回复里反复 `emit_*`。\
""".format(
    fact_tool_lines="\n".join(FACT_TOOL_PROMPT_LINES),
    notes_tool_lines="\n".join(NOTES_TOOL_PROMPT_LINES),
    ui_tool_lines="\n".join(UI_TOOL_PROMPT_LINES),
)

_NORMAL_SYSTEM_PROMPT_TEMPLATE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 任务目标
- 始终使用简体中文，直接回答用户当前问题。
- 围绕编程学习与考试表现提供帮助：解释、对比、诊断、建议。
- 回复清晰、简洁，避免空泛套话。
- 维护一份跨会话的「长期笔记」：在适当时机用 `update_notes` 增量记录关键摘要、知识点、用户偏好与阶段性总结，让未来的自己延续上下文。

{tool_rules}

## 输出约束
- 先给结论，再给必要说明。
- 如果数据不足，明确说明缺什么数据，以及下一步将查询什么。
- 指标展示时严格区分 0 分与缺失值：0 分显示为 0；仅在字段缺失时标注“缺失/N/A”。\
"""

_PROACTIVE_ANALYSIS_SYSTEM_PROMPT_TEMPLATE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 主动分析模式
当前任务是“系统主动分析最新考试表现”，不是普通闲聊。请主动完成完整分析。

## 分析流程（必须执行）
1. 先调用 `get_student_learning_profile` 获取当前学生画像和近期考试历史。
2. 根据用户消息或历史记录确认目标考试 ID，再调用 `get_exam_participant_metrics` 获取该考试全体学生事实数据。
3. 必要时调用 `get_exam_submissions` 核对具体题目提交表现。
4. 基于工具事实输出目标考试的 Rating 变化、五维指标表现、同场对比和进步/退步解释。
5. 给出 2-3 条可执行、可落地的改进建议，并明确心理支持模式（recovery/steady/reinforce）和下一步训练动作。
6. 最后提出 1 个简短追问，帮助学生进入下一步训练。
7. 若得出可沿用至后续会话的关键结论或训练计划，调用 `update_notes` 追加到长期笔记，便于下一次延续。

{tool_rules}

## 输出约束
- 始终使用简体中文。
- 结构化输出，内容聚焦“结论 + 依据 + 行动”。
- 输出中应包含“心理支持 + 下步训练动作”。
- 语气专业友好，但避免冗长。
- 指标展示时严格区分 0 分与缺失值：0 分显示为 0；仅在字段缺失时标注“缺失/N/A”。\
"""

_TARGET_EXAM_ANALYSIS_SYSTEM_PROMPT_TEMPLATE = """\
你是一位专业的编程学习分析助手，名叫「{role_name}」。

## 指定考试分析模式
当前任务是“分析指定考试表现”，目标考试 ID 为 `{target_exam_id}`。这不是普通闲聊，也不是“最近一场考试”分析。

## 分析流程（必须执行）
1. 先调用 `get_student_learning_profile` 获取当前学生画像和近期考试历史。
2. 必须调用 `get_exam_participant_metrics`，参数 `exam_id={target_exam_id}`，获取目标考试全体学生事实数据。
3. 必要时调用 `get_exam_submissions` 核对目标学生在该考试中的具体提交表现。
4. 基于工具事实说明该场考试的 Rating 变化、五大指标表现、同场排名/分数对比，以及与上一场可比考试的关键变化。
5. 给出 2-3 条可执行、可落地的改进建议，优先针对本场退步项或短板项。
6. 结尾给出一句适合教师查看的总结，不要追加面向学生的追问。
7. 若发现可沉淀的知识点或共性短板，调用 `update_notes` 增量记录到长期笔记。

{tool_rules}

## 输出约束
- 始终使用简体中文。
- 结构化输出，内容聚焦“结论 + 依据 + 行动”。
- 面向教师阅读，语气专业克制，不使用聊天式追问。
- 指标展示时严格区分 0 分与缺失值：0 分显示为 0；仅在字段缺失时标注“缺失/N/A”。\
"""

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

_STUDENT_CONTEXT_TEMPLATE = """\
## 当前学生身份
- 学号: {student_id}
- 昵称: {pta_nickname}
- 匹配方式: {matched_by}
- 内部学生实体 ID: {student_entity_ids}

注意：本节只提供身份绑定信息，不包含分数、排名、Rating 或五维指标。需要这些事实时必须调用工具。\
"""

_TARGET_EXAM_CONTEXT_TEMPLATE = """\
## 指定考试上下文
- 目标考试 ID: {target_exam_id}

注意：目标考试的分数、排名、Rating 和五维指标必须通过 `get_exam_participant_metrics` 查询。\
"""

_NO_STUDENT_CONTEXT = """\
## 当前学生身份
当前用户尚未绑定学号，无法查询学生数据。请提示用户先在「设置」中关联自己的学号或 PTA 昵称。\
"""

_NOTES_CONTEXT_TEMPLATE = """\
## 长期笔记（跨会话持久；当前激活：{notes_title}）
{notes_content}

注意：以上是用户当前激活的那一份长期笔记。需要核对最新版本时调用 `read_notes`；
得出值得沉淀的结论后调用 `update_notes` 写回，保留仍有价值的内容。\
"""

_AUTO_ANALYSIS_USER_MESSAGE_TEMPLATE = """\
请按系统提示执行主动分析任务。{target_exam_instruction}请调用工具获取事实数据后直接输出最终分析结论。\
"""

PROMPT_DEFINITIONS: tuple[PromptDefinition, ...] = (
    PromptDefinition(
        key="chat.normal_system",
        title="普通聊天系统提示词",
        description="学生端日常对话的基础系统提示词。",
        category="chat",
        default_content=_NORMAL_SYSTEM_PROMPT_TEMPLATE,
        allowed_variables=("role_name", "tool_rules"),
        required_variables=("role_name", "tool_rules"),
        sample_variables={
            "role_name": "小D",
            "tool_rules": _TOOL_RULES,
        },
    ),
    PromptDefinition(
        key="chat.proactive_analysis_system",
        title="主动分析系统提示词",
        description="学生端自动分析最新考试表现时使用。",
        category="chat",
        default_content=_PROACTIVE_ANALYSIS_SYSTEM_PROMPT_TEMPLATE,
        allowed_variables=("role_name", "tool_rules"),
        required_variables=("role_name", "tool_rules"),
        sample_variables={
            "role_name": "小D",
            "tool_rules": _TOOL_RULES,
        },
    ),
    PromptDefinition(
        key="chat.exam_analysis_system",
        title="指定考试分析系统提示词",
        description="管理员批量生成某场考试学生分析时使用。",
        category="chat",
        default_content=_TARGET_EXAM_ANALYSIS_SYSTEM_PROMPT_TEMPLATE,
        allowed_variables=("role_name", "target_exam_id", "tool_rules"),
        required_variables=("role_name", "target_exam_id", "tool_rules"),
        sample_variables={
            "role_name": "小D",
            "target_exam_id": "3",
            "tool_rules": _TOOL_RULES,
        },
    ),
    PromptDefinition(
        key="chat.tool_rules",
        title="工具使用规则",
        description="统一描述模型可调用的事实工具与约束。",
        category="chat",
        default_content=_TOOL_RULES,
    ),
    PromptDefinition(
        key="chat.metric_algorithm",
        title="指标体系说明",
        description="Rating 与五大能力指标说明。",
        category="chat",
        default_content=LAYER_2_ALGORITHM,
    ),
    PromptDefinition(
        key="chat.student_context",
        title="学生身份上下文",
        description="已绑定学生时注入的身份上下文，不含成绩事实。",
        category="context",
        default_content=_STUDENT_CONTEXT_TEMPLATE,
        allowed_variables=(
            "student_id",
            "pta_nickname",
            "matched_by",
            "student_entity_ids",
        ),
        required_variables=(
            "student_id",
            "pta_nickname",
            "matched_by",
            "student_entity_ids",
        ),
        sample_variables={
            "student_id": "20231202047",
            "pta_nickname": "student47",
            "matched_by": "student_no",
            "student_entity_ids": "101, 202",
        },
    ),
    PromptDefinition(
        key="chat.no_student_context",
        title="未绑定学生上下文",
        description="用户未绑定学号或 PTA 昵称时注入。",
        category="context",
        default_content=_NO_STUDENT_CONTEXT,
    ),
    PromptDefinition(
        key="chat.target_exam_context",
        title="指定考试上下文",
        description="指定考试分析时额外注入的目标考试 ID。",
        category="context",
        default_content=_TARGET_EXAM_CONTEXT_TEMPLATE,
        allowed_variables=("target_exam_id",),
        required_variables=("target_exam_id",),
        sample_variables={"target_exam_id": "3"},
    ),
    PromptDefinition(
        key="chat.notes_context",
        title="长期笔记上下文",
        description="将用户当前激活的长期笔记（标题与正文）注入系统提示。",
        category="context",
        default_content=_NOTES_CONTEXT_TEMPLATE,
        allowed_variables=("notes_title", "notes_content"),
        required_variables=("notes_title", "notes_content"),
        sample_variables={
            "notes_title": "刷题策略",
            "notes_content": "- 图论是当前弱项\n- 习惯遗漏边界条件",
        },
    ),
    PromptDefinition(
        key="chat.auto_analysis_user_message",
        title="主动分析触发消息",
        description="系统自动触发分析时发送给模型的隐藏用户消息。",
        category="chat",
        default_content=_AUTO_ANALYSIS_USER_MESSAGE_TEMPLATE,
        allowed_variables=("target_exam_instruction",),
        required_variables=("target_exam_instruction",),
        sample_variables={"target_exam_instruction": "目标考试 ID 为 3，"},
    ),
    PromptDefinition(
        key="role.xiaoD.style",
        title="小D角色风格",
        description="默认助手角色的额外风格提示词。",
        category="role",
        default_content=ROLE_STYLE_PROMPTS["xiaoD"],
    ),
    PromptDefinition(
        key="role.sakiko.style",
        title="Sakiko角色风格",
        description="丰川祥子（Sakiko）角色的额外风格提示词。",
        category="role",
        default_content=ROLE_STYLE_PROMPTS["sakiko"],
    ),
)

PROMPT_REGISTRY: dict[str, PromptDefinition] = {
    definition.key: definition for definition in PROMPT_DEFINITIONS
}


def list_prompt_definitions() -> tuple[PromptDefinition, ...]:
    return PROMPT_DEFINITIONS


def get_prompt_definition(key: str) -> PromptDefinition:
    try:
        return PROMPT_REGISTRY[key]
    except KeyError:
        raise AppError(
            status_code=404,
            code="PROMPT_NOT_FOUND",
            message="Prompt template was not found.",
        ) from None


def _extract_template_variables(content: str) -> set[str]:
    variables: set[str] = set()
    try:
        for _, field_name, _, _ in string.Formatter().parse(content):
            if field_name is None:
                continue
            normalized = field_name.strip()
            if not normalized:
                continue
            if not normalized.isidentifier():
                raise AppError(
                    status_code=422,
                    code="INVALID_PROMPT_TEMPLATE",
                    message=f"Prompt variable is not allowed: {normalized}",
                )
            variables.add(normalized)
    except ValueError as exc:
        raise AppError(
            status_code=422,
            code="INVALID_PROMPT_TEMPLATE",
            message=f"Prompt template syntax is invalid: {exc}",
        ) from exc
    return variables


def validate_prompt_content(definition: PromptDefinition, content: str) -> None:
    if not definition.allowed_variables and not definition.required_variables:
        return
    used_variables = _extract_template_variables(content)
    allowed = set(definition.allowed_variables)
    unknown = sorted(used_variables - allowed)
    if unknown:
        raise AppError(
            status_code=422,
            code="INVALID_PROMPT_TEMPLATE",
            message=f"Unknown prompt variable(s): {', '.join(unknown)}",
        )
    missing = sorted(set(definition.required_variables) - used_variables)
    if missing:
        raise AppError(
            status_code=422,
            code="INVALID_PROMPT_TEMPLATE",
            message=f"Missing required prompt variable(s): {', '.join(missing)}",
        )


def render_prompt_content(
    definition: PromptDefinition,
    content: str,
    variables: dict[str, Any],
) -> str:
    validate_prompt_content(definition, content)
    if not definition.allowed_variables and not definition.required_variables:
        return content.strip()
    allowed = set(definition.allowed_variables)
    render_vars = {
        key: "" if value is None else str(value)
        for key, value in variables.items()
        if key in allowed
    }
    missing_values = [
        key for key in definition.required_variables if key not in render_vars
    ]
    if missing_values:
        raise AppError(
            status_code=422,
            code="INVALID_PROMPT_RENDER",
            message=f"Missing prompt render variable(s): {', '.join(missing_values)}",
        )
    return content.format(**render_vars).strip()


class PromptService:
    """Builds system prompts for normal chat and analysis modes."""

    def __init__(self, repository: ApiRepository) -> None:
        self._repository = repository

    async def build_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        role_id: str = "xiaoD",
        role_name: str = "小D",
        custom_role_style_prompt: str = "",
        notes: str = "",
        notes_title: str = "",
    ) -> str:
        tool_rules = await self._render_prompt("chat.tool_rules", {})
        base_prompt = await self._render_prompt(
            "chat.normal_system",
            {
                "role_name": role_name,
                "tool_rules": tool_rules,
            },
        )
        return await self._build_prompt_by_mode(
            base_prompt=base_prompt,
            identity=identity,
            role_id=role_id,
            custom_role_style_prompt=custom_role_style_prompt,
            notes=notes,
            notes_title=notes_title,
        )

    async def build_proactive_analysis_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        role_id: str = "xiaoD",
        role_name: str = "小D",
        custom_role_style_prompt: str = "",
        notes: str = "",
        notes_title: str = "",
    ) -> str:
        tool_rules = await self._render_prompt("chat.tool_rules", {})
        base_prompt = "\n\n".join(
            [
                await self._render_prompt(
                    "chat.proactive_analysis_system",
                    {
                        "role_name": role_name,
                        "tool_rules": tool_rules,
                    },
                ),
                await self._render_prompt("chat.metric_algorithm", {}),
            ]
        )
        return await self._build_prompt_by_mode(
            base_prompt=base_prompt,
            identity=identity,
            role_id=role_id,
            custom_role_style_prompt=custom_role_style_prompt,
            notes=notes,
            notes_title=notes_title,
        )

    async def build_exam_analysis_system_prompt(
        self,
        identity: ResolvedIdentity | None,
        target_exam_id: int,
        role_id: str = "xiaoD",
        role_name: str = "小D",
        custom_role_style_prompt: str = "",
        notes: str = "",
        notes_title: str = "",
    ) -> str:
        tool_rules = await self._render_prompt("chat.tool_rules", {})
        base_prompt = "\n\n".join(
            [
                await self._render_prompt(
                    "chat.exam_analysis_system",
                    {
                        "role_name": role_name,
                        "target_exam_id": target_exam_id,
                        "tool_rules": tool_rules,
                    },
                ),
                await self._render_prompt("chat.metric_algorithm", {}),
            ]
        )
        return await self._build_prompt_by_mode(
            base_prompt=base_prompt,
            identity=identity,
            role_id=role_id,
            custom_role_style_prompt=custom_role_style_prompt,
            target_exam_id=target_exam_id,
            notes=notes,
            notes_title=notes_title,
        )

    async def build_auto_analysis_user_message(
        self,
        target_exam_id: int | None = None,
    ) -> str:
        target_exam_instruction = (
            f"目标考试 ID 为 {target_exam_id}，" if target_exam_id is not None else ""
        )
        return await self._render_prompt(
            "chat.auto_analysis_user_message",
            {"target_exam_instruction": target_exam_instruction},
        )

    async def _build_prompt_by_mode(
        self,
        base_prompt: str,
        identity: ResolvedIdentity | None,
        role_id: str,
        custom_role_style_prompt: str = "",
        target_exam_id: int | None = None,
        notes: str = "",
        notes_title: str = "",
    ) -> str:
        sections: list[str] = [base_prompt]
        if identity is not None and not identity.no_submission_records:
            sections.append(await self._build_student_context(identity))
        else:
            sections.append(await self._render_prompt("chat.no_student_context", {}))
        if target_exam_id is not None:
            sections.append(
                await self._render_prompt(
                    "chat.target_exam_context",
                    {"target_exam_id": target_exam_id},
                )
            )
        sections.append(await self._build_notes_context(notes, notes_title))
        style = await self._build_role_style_prompt(role_id)
        if style:
            sections.append(style)
        custom_style = custom_role_style_prompt.strip()
        if custom_style:
            sections.append(custom_style)
        return "\n\n".join(sections)

    async def _build_notes_context(self, notes: str, notes_title: str) -> str:
        title = (notes_title or "").strip() or "未命名笔记"
        body = (notes or "").strip() or "（暂无笔记内容；可在得出值得沉淀的结论后调用 update_notes 写入。）"
        return await self._render_prompt(
            "chat.notes_context",
            {
                "notes_title": title,
                "notes_content": body,
            },
        )

    async def _build_student_context(self, identity: ResolvedIdentity) -> str:
        student_entity_ids = identity.student_entity_ids or (
            identity.student_entity_id,
        )
        return await self._render_prompt(
            "chat.student_context",
            {
                "student_id": identity.student_id,
                "pta_nickname": identity.pta_nickname or "未设置",
                "matched_by": identity.matched_by,
                "student_entity_ids": ", ".join(
                    str(item) for item in student_entity_ids
                ),
            },
        )

    async def _build_role_style_prompt(self, role_id: str) -> str:
        key = f"role.{role_id}.style"
        if key not in PROMPT_REGISTRY:
            return ROLE_STYLE_PROMPTS.get(role_id, "").strip()
        return await self._render_prompt(key, {})

    async def _render_prompt(self, key: str, variables: dict[str, Any]) -> str:
        definition = get_prompt_definition(key)
        content = await self._load_prompt_content(definition)
        try:
            return render_prompt_content(definition, content, variables)
        except Exception:
            logger.warning("Falling back to default prompt: %s", key, exc_info=True)
            return render_prompt_content(definition, definition.default_content, variables)

    async def _load_prompt_content(self, definition: PromptDefinition) -> str:
        fetcher = getattr(self._repository, "fetch_ai_prompt_template", None)
        if not callable(fetcher):
            return definition.default_content
        try:
            row = await fetcher(definition.key)
        except Exception:
            logger.debug(
                "Failed to load prompt template from repository: %s",
                definition.key,
                exc_info=True,
            )
            return definition.default_content
        content = getattr(row, "content", None) if row is not None else None
        if isinstance(content, str):
            return content
        return definition.default_content
