from __future__ import annotations

import asyncio
import json
from datetime import datetime, timezone
from decimal import Decimal
from types import SimpleNamespace

from apps.api.db.repository import (
    DashboardMetricsRow,
    ExamInfoRow,
    ExamMetricHistoryRow,
    ExamParticipantMetricRow,
    ExamSubmissionRow,
    LearningPathSnapshotRow,
    ProblemRecommendationSnapshotRow,
    RatingHistoryRow,
    StudentIdentityMatch,
)
from apps.api.services.identity import ResolvedIdentity
from apps.api.services.prompt import PromptService
from apps.api.services.tools import (
    FACT_TOOL_PROMPT_LINES,
    TOOL_DEFINITIONS,
    ToolExecutor,
)


class FakeFactRepo:
    def __init__(self) -> None:
        self.metrics_by_student: dict[int, DashboardMetricsRow | None] = {}
        self.rating_by_student: dict[int, list[RatingHistoryRow]] = {}
        self.exam_metrics_by_student: dict[int, list[ExamMetricHistoryRow]] = {}
        self.exam_info_by_id: dict[int, ExamInfoRow] = {}
        self.exam_participant_metrics_by_exam: dict[
            int, list[ExamParticipantMetricRow]
        ] = {}
        self.submissions_by_exam_student: dict[
            tuple[int, int], list[ExamSubmissionRow]
        ] = {}
        self.student_matches_by_no: dict[str, list[StudentIdentityMatch]] = {}
        self.problem_recommendations_by_students: dict[
            tuple[int, ...], ProblemRecommendationSnapshotRow
        ] = {}
        self.learning_paths_by_students: dict[
            tuple[int, ...], LearningPathSnapshotRow
        ] = {}

    async def fetch_current_metrics(
        self, student_id: int
    ) -> DashboardMetricsRow | None:
        return self.metrics_by_student.get(student_id)

    async def fetch_rating_history(
        self, student_id: int, limit: int = 50
    ) -> list[RatingHistoryRow]:
        return self.rating_by_student.get(student_id, [])[:limit]

    async def fetch_exam_metric_history(
        self, student_id: int, limit: int = 50
    ) -> list[ExamMetricHistoryRow]:
        return self.exam_metrics_by_student.get(student_id, [])[:limit]

    async def fetch_exam_info(self, exam_id: int) -> ExamInfoRow | None:
        return self.exam_info_by_id.get(exam_id)

    async def fetch_exam_participant_metrics(
        self, exam_id: int, limit: int = 10000
    ) -> list[ExamParticipantMetricRow]:
        return self.exam_participant_metrics_by_exam.get(exam_id, [])[:limit]

    async def fetch_exam_submissions_for_student(
        self, exam_id: int, student_id: int, limit: int = 100
    ) -> list[ExamSubmissionRow]:
        return self.submissions_by_exam_student.get((exam_id, student_id), [])[:limit]

    async def find_students_by_student_no(
        self, student_no: str
    ) -> list[StudentIdentityMatch]:
        return self.student_matches_by_no.get(student_no, [])

    async def fetch_latest_problem_recommendations(
        self, student_ids: list[int], top_k: int = 10
    ) -> ProblemRecommendationSnapshotRow | None:
        snapshot = self.problem_recommendations_by_students.get(tuple(student_ids))
        if snapshot is None:
            return None
        return ProblemRecommendationSnapshotRow(
            student_id=snapshot.student_id,
            model_run_id=snapshot.model_run_id,
            items=snapshot.items[:top_k],
            generated_at=snapshot.generated_at,
        )

    async def fetch_latest_learning_path(
        self, student_ids: list[int]
    ) -> LearningPathSnapshotRow | None:
        return self.learning_paths_by_students.get(tuple(student_ids))


class ExplodingRepo:
    async def fetch_ai_prompt_template(self, prompt_key: str):
        _ = prompt_key
        return None

    def __getattr__(self, name: str):
        raise AssertionError(f"PromptService should not query repository: {name}")


class PromptOverrideRepo(ExplodingRepo):
    def __init__(self, prompts: dict[str, str]) -> None:
        self.prompts = prompts

    async def fetch_ai_prompt_template(self, prompt_key: str):
        content = self.prompts.get(prompt_key)
        if content is None:
            return None
        return SimpleNamespace(content=content)


def _dt(day: int) -> datetime:
    return datetime(2026, 2, day, 9, 0, tzinfo=timezone.utc)


def _build_identity() -> ResolvedIdentity:
    return ResolvedIdentity(
        student_entity_id=101,
        student_entity_ids=(101, 202),
        student_id="20231202047",
        pta_nickname="王浩然",
        no_submission_records=False,
        matched_by="student_id",
    )


def _build_repo() -> FakeFactRepo:
    repo = FakeFactRepo()
    repo.metrics_by_student[101] = DashboardMetricsRow(
        knowledge=Decimal("96"),
        accuracy=Decimal("22"),
        quality=None,
        flexibility=Decimal("28"),
        proficiency=Decimal("28"),
        rating=1097,
        updated_at=_dt(8),
    )
    repo.metrics_by_student[202] = DashboardMetricsRow(
        knowledge=Decimal("75"),
        accuracy=Decimal("32"),
        quality=Decimal("64"),
        flexibility=Decimal("40"),
        proficiency=Decimal("53"),
        rating=1097,
        updated_at=datetime(2026, 2, 8, 10, 5, tzinfo=timezone.utc),
    )
    repo.rating_by_student[101] = [
        RatingHistoryRow(3, "精选3", _dt(8), 1080, 17, 1097),
        RatingHistoryRow(2, "精选2", _dt(1), 1050, 30, 1080),
    ]
    repo.rating_by_student[202] = [
        RatingHistoryRow(3, "精选3", _dt(8), 900, 10, 910),
    ]
    repo.exam_metrics_by_student[101] = [
        ExamMetricHistoryRow(3, "精选3", _dt(8), 96, 22, None, 28, 28, _dt(8)),
        ExamMetricHistoryRow(2, "精选2", _dt(1), 48, 40, 64, 31, 60, _dt(1)),
    ]
    repo.exam_metrics_by_student[202] = [
        ExamMetricHistoryRow(29, "精进3", _dt(9), None, None, None, None, None, _dt(9)),
        ExamMetricHistoryRow(
            3,
            "精选3",
            _dt(8),
            75,
            None,
            64,
            40,
            53,
            datetime(2026, 2, 8, 10, 5, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            2,
            "精选2",
            _dt(1),
            None,
            32,
            None,
            None,
            None,
            datetime(2026, 2, 1, 10, 5, tzinfo=timezone.utc),
        ),
    ]
    repo.exam_info_by_id[3] = ExamInfoRow(
        exam_id=3,
        exam_type="pta",
        source_path="/practice/精选3",
        title="精选3",
        starts_at=_dt(8),
        ends_at=datetime(2026, 2, 8, 12, 0, tzinfo=timezone.utc),
        duration_seconds=10800,
        problem_count=6,
        participant_count=2,
    )
    repo.exam_participant_metrics_by_exam[3] = [
        ExamParticipantMetricRow(
            student_entity_id=303,
            student_no="20231202001",
            student_name="前一名同学",
            rank=6,
            total_score=Decimal("380"),
            solved_count=5,
            old_rating=1120,
            new_rating=1130,
            rating_delta=10,
            knowledge=90,
            accuracy=32,
            quality=50,
            flexibility=26,
            proficiency=31,
            total_participants=2,
        ),
        ExamParticipantMetricRow(
            student_entity_id=101,
            student_no="20231202047",
            student_name="王浩然",
            rank=7,
            total_score=Decimal("370"),
            solved_count=5,
            old_rating=1080,
            new_rating=1097,
            rating_delta=17,
            knowledge=96,
            accuracy=22,
            quality=64,
            flexibility=28,
            proficiency=28,
            total_participants=2,
        ),
    ]
    repo.submissions_by_exam_student[(3, 101)] = [
        ExamSubmissionRow(
            submission_id=10,
            problem_code="A",
            submitted_at=_dt(8),
            verdict="Accepted",
            score=Decimal("100"),
            language="C++",
            time_ms=12,
            memory_kb=256,
        )
    ]
    repo.submissions_by_exam_student[(3, 303)] = [
        ExamSubmissionRow(
            submission_id=11,
            problem_code="B",
            submitted_at=datetime(2026, 2, 8, 9, 30, tzinfo=timezone.utc),
            verdict="Wrong Answer",
            score=Decimal("30"),
            language="C++",
            time_ms=18,
            memory_kb=512,
        )
    ]
    repo.student_matches_by_no["20231202001"] = [
        StudentIdentityMatch(
            student_id=303,
            student_no="20231202001",
            student_name="前一名同学",
        )
    ]
    return repo


def test_prompt_service_does_not_inject_numeric_student_data() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "## 当前学生身份" in prompt
    assert "学号: 20231202047" in prompt
    assert "get_student_learning_profile" in prompt
    assert "get_exam_participant_metrics" in prompt
    assert "get_student_growth_insights" not in prompt
    assert "Rating: **" not in prompt
    assert "知识: 75 / 100" not in prompt
    assert "精选3" not in prompt


def test_prompt_service_proactive_mode_uses_fact_tools() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(
        prompt_service.build_proactive_analysis_system_prompt(identity=_build_identity())
    )

    assert "## 主动分析模式" in prompt
    assert "## 分析流程（必须执行）" in prompt
    assert "## 指标体系说明" in prompt
    assert "get_student_learning_profile" in prompt
    assert "get_exam_participant_metrics" in prompt
    assert "get_student_growth_insights" not in prompt


def test_prompt_service_exam_mode_only_injects_target_exam_id() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(
        prompt_service.build_exam_analysis_system_prompt(
            identity=_build_identity(),
            target_exam_id=3,
        )
    )

    assert "目标考试 ID 为 `3`" in prompt
    assert "目标考试 ID: 3" in prompt
    assert "精选3" not in prompt
    assert "1080" not in prompt


def test_prompt_service_sakiko_role_includes_style_prompt() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(
        prompt_service.build_system_prompt(
            identity=None,
            role_id="sakiko",
            role_name="丰川祥子（Sakiko）",
        )
    )

    assert "你是一位专业的编程学习分析助手，名叫「丰川祥子（Sakiko）」" in prompt
    assert "你的中文名：丰川祥子" in prompt
    assert "每次调用工具之前" not in prompt
    assert "一次只调一个工具" not in prompt


def test_prompt_requires_native_tool_calls_and_hides_markup() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "原生 tool-calling API" in prompt
    assert "禁止输出 `<tool_call>`" in prompt
    assert "DSML 标记" in prompt


def test_prompt_service_uses_admin_prompt_overrides() -> None:
    prompt_service = PromptService(
        repository=PromptOverrideRepo(
            {
                "chat.tool_rules": "## 自定义工具规则\n- 必须调用事实工具。",
                "chat.normal_system": "角色={role_name}\n\n{tool_rules}",
                "chat.student_context": "绑定学生：{student_id}/{pta_nickname}/{matched_by}/{student_entity_ids}",
                "role.xiaoD.style": "使用更克制的教师语气。",
            }
        )  # type: ignore[arg-type]
    )

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "角色=小D" in prompt
    assert "## 自定义工具规则" in prompt
    assert "绑定学生：20231202047/王浩然/student_id/101, 202" in prompt
    assert "使用更克制的教师语气。" in prompt
    assert "## 任务目标" not in prompt


def test_prompt_service_falls_back_when_admin_prompt_is_invalid() -> None:
    prompt_service = PromptService(
        repository=PromptOverrideRepo(
            {
                "chat.normal_system": "这个模板包含非法变量 {unknown}",
            }
        )  # type: ignore[arg-type]
    )

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "## 任务目标" in prompt
    assert "这个模板包含非法变量" not in prompt


def test_prompt_service_lists_recommendation_tools_and_rules() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "get_problem_recommendations" in prompt
    assert "get_learning_path" in prompt
    assert "无明确条件时只传 `top_k` 或空参数，不要编造过滤参数" in prompt
    assert "用户请求学习路线、先学什么、知识点路径时" in prompt


def test_prompt_uses_markdown_instead_of_ui_emit_tools() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))
    tool_names = [item["function"]["name"] for item in TOOL_DEFINITIONS]

    assert "标准 fenced code" in prompt
    assert "emit_" not in "\n".join(tool_names)
    assert "focus_knowledge_node" not in tool_names
    assert "UI 富组件工具" not in prompt


def test_prompt_service_auto_analysis_message_uses_admin_template() -> None:
    prompt_service = PromptService(
        repository=PromptOverrideRepo(
            {
                "chat.auto_analysis_user_message": "自动分析：{target_exam_instruction}开始。",
            }
        )  # type: ignore[arg-type]
    )

    message = asyncio.run(prompt_service.build_auto_analysis_user_message(8))

    assert message == "自动分析：目标考试 ID 为 8，开始。"


def test_tool_definitions_match_registered_handlers() -> None:
    names = [item["function"]["name"] for item in TOOL_DEFINITIONS]

    assert names == [
        "get_student_learning_profile",
        "get_exam_participant_metrics",
        "get_exam_submissions",
        "get_problem_recommendations",
        "get_learning_path",
        "read_notes",
        "update_notes",
    ]


def test_fact_tool_prompt_lines_match_tool_definitions() -> None:
    tool_names = [item["function"]["name"] for item in TOOL_DEFINITIONS]
    prompt_names = [line.split("`", 2)[1] for line in FACT_TOOL_PROMPT_LINES]

    assert prompt_names == tool_names[: len(prompt_names)]


def test_recommendation_tool_schema_discourages_invented_filters() -> None:
    recommendation = next(
        item
        for item in TOOL_DEFINITIONS
        if item["function"]["name"] == "get_problem_recommendations"
    )
    function = recommendation["function"]
    properties = function["parameters"]["properties"]

    assert "只读取快照" in function["description"]
    assert "不触发训练" in function["description"]
    assert "不要编造过滤参数" in function["description"]
    assert "仅当用户明确指定知识点时使用" in properties["knowledge_point"]["description"]
    assert "仅当用户明确指定最低难度时使用" in properties["min_difficulty"]["description"]
    assert "仅当用户明确指定最高难度时使用" in properties["max_difficulty"]["description"]
    assert "仅当用户明确给出刚做过或不想看的题目时使用" in properties["exclude_problem_ids"]["description"]


def test_prompt_service_injects_notes_context() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(
        prompt_service.build_system_prompt(
            identity=_build_identity(),
            notes="- 图论是当前弱项\n- 习惯遗漏边界条件",
            notes_title="刷题策略",
        )
    )

    assert "## 长期笔记" in prompt
    assert "刷题策略" in prompt
    assert "图论是当前弱项" in prompt
    assert "read_notes" in prompt
    assert "update_notes" in prompt
    assert "可整篇读取，也可按关键词搜索" in prompt
    assert "小改用 patch，大改或全文重构用 replace" in prompt
    assert "成功调用 `update_notes` 之前，不要说笔记已经修改完成" in prompt


def test_prompt_service_renders_empty_notes_with_placeholder() -> None:
    prompt_service = PromptService(repository=ExplodingRepo())  # type: ignore[arg-type]

    prompt = asyncio.run(
        prompt_service.build_system_prompt(identity=_build_identity())
    )

    assert "## 长期笔记" in prompt
    assert "未命名笔记" in prompt
    assert "（暂无笔记内容" in prompt


def test_tool_executor_learning_profile_merges_fact_history() -> None:
    executor = ToolExecutor(repository=_build_repo(), identity=_build_identity())

    result = asyncio.run(
        executor.execute("get_student_learning_profile", {"history_limit": 2})
    )
    payload = json.loads(result)

    assert payload["student"]["student_no"] == "20231202047"
    assert payload["current"]["rating"] == 1097
    assert payload["current"]["knowledge"] == 75
    assert payload["current"]["accuracy"] == 32
    assert payload["current"]["quality"] == 64
    assert payload["history"][0]["exam_id"] == 3
    assert payload["history"][0]["new_rating"] == 1097
    assert payload["history"][0]["knowledge"] == 75
    assert payload["history"][1]["exam_id"] == 2
    assert payload["history"][1]["accuracy"] == 32
    assert all(item["exam_id"] != 29 for item in payload["history"])


def test_tool_executor_exam_participant_metrics_returns_full_fact_rows() -> None:
    executor = ToolExecutor(repository=_build_repo(), identity=_build_identity())

    result = asyncio.run(
        executor.execute("get_exam_participant_metrics", {"exam_id": 3})
    )
    payload = json.loads(result)

    assert payload["exam"]["exam_id"] == 3
    assert payload["participant_count"] == 2
    assert payload["returned_count"] == 2
    assert payload["truncated"] is False
    assert payload["participants"][0]["student_no"] == "20231202001"
    assert payload["participants"][0]["rank"] == 6
    assert payload["participants"][0]["rating_delta"] == 10
    assert payload["participants"][1]["student_name"] == "王浩然"
    assert payload["participants"][1]["is_current_student"] is True
    assert payload["participants"][1]["knowledge"] == 96


def test_tool_executor_exam_submissions_defaults_to_bound_student() -> None:
    executor = ToolExecutor(repository=_build_repo(), identity=_build_identity())

    result = asyncio.run(executor.execute("get_exam_submissions", {"exam_id": 3}))
    payload = json.loads(result)

    assert payload["student_entity_ids"] == [101, 202]
    assert payload["returned_count"] == 1
    assert payload["submissions"][0]["problem_code"] == "A"


def test_tool_executor_exam_submissions_can_target_student_no() -> None:
    executor = ToolExecutor(repository=_build_repo(), identity=_build_identity())

    result = asyncio.run(
        executor.execute(
            "get_exam_submissions",
            {"exam_id": 3, "student_no": "20231202001"},
        )
    )
    payload = json.loads(result)

    assert payload["student_entity_ids"] == [303]
    assert payload["submissions"][0]["problem_code"] == "B"


def test_tool_executor_problem_recommendations_do_not_return_path() -> None:
    repo = _build_repo()
    repo.problem_recommendations_by_students[(101, 202)] = (
        ProblemRecommendationSnapshotRow(
            student_id=101,
            model_run_id=7,
            items=[
                {
                    "problemId": "P1001",
                    "title": "最短路",
                    "knowledgePoints": ["图论"],
                    "score": 0.91,
                    "rank": 1,
                }
            ],
            generated_at=_dt(9),
        )
    )
    executor = ToolExecutor(repository=repo, identity=_build_identity())

    result = asyncio.run(
        executor.execute("get_problem_recommendations", {"top_k": 5})
    )
    payload = json.loads(result)

    assert payload["model_run_id"] == 7
    assert payload["items"][0]["problemId"] == "P1001"
    assert "path" not in payload
    assert "targets" not in payload


def test_tool_executor_learning_path_does_not_return_recommendations() -> None:
    repo = _build_repo()
    repo.learning_paths_by_students[(101, 202)] = LearningPathSnapshotRow(
        student_id=101,
        model_run_id=8,
        targets=["动态规划"],
        path=["递推", "背包", "区间 DP"],
        explanations={"递推": "基础状态转移不稳定。"},
        generated_at=_dt(10),
    )
    executor = ToolExecutor(repository=repo, identity=_build_identity())

    result = asyncio.run(executor.execute("get_learning_path", {}))
    payload = json.loads(result)

    assert payload["model_run_id"] == 8
    assert payload["path"] == ["递推", "背包", "区间 DP"]
    assert "items" not in payload
    assert "recommendations" not in payload


def test_tool_executor_requires_identity_for_default_student_tools() -> None:
    executor = ToolExecutor(repository=_build_repo(), identity=None)

    profile = json.loads(
        asyncio.run(executor.execute("get_student_learning_profile", {}))
    )
    submissions = json.loads(
        asyncio.run(executor.execute("get_exam_submissions", {"exam_id": 3}))
    )

    assert "未绑定学生身份" in profile["error"]
    assert "未绑定学生身份" in submissions["error"]
