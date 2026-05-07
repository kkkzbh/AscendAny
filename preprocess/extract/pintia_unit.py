from __future__ import annotations

from datetime import datetime
import json
from pathlib import Path
from typing import Any

from ..discover import PINTIA_UNIT_ROLE, PINTIA_UNIT_SCHEMA
from ..models import (
    ExamBundle,
    ExamMeta,
    ExamProblemExternalLinkRow,
    ExamUnit,
    ExternalProblemRow,
    ExternalProblemTagRow,
    ExternalProblemVersionRow,
    ParticipantRow,
    ProblemInfo,
    SubmissionCodeRow,
    SubmissionRow,
)
from ..utils import (
    clean_text,
    parse_optional_float,
    parse_optional_int,
    sha256_text,
    stable_json_hash,
)
from services.recommendation.recommendation.knowledge import canonicalize_knowledge_tags


_STATUS_ALIASES = {
    "ACCEPTED": "答案正确",
    "PARTIAL_ACCEPTED": "部分正确",
    "WRONG_ANSWER": "答案错误",
    "COMPILE_ERROR": "编译错误",
    "TIME_LIMIT_EXCEEDED": "运行超时",
    "MEMORY_LIMIT_EXCEEDED": "内存超限",
    "RUNTIME_ERROR": "运行错误",
    "PRESENTATION_ERROR": "格式错误",
    "MULTIPLE_ERRORS": "多个错误",
}


def parse_pintia_unit(unit: ExamUnit) -> ExamBundle:
    source = _single_pintia_source(unit)
    payload = _load_payload(source.absolute_path)
    _validate_top_level(payload)

    exam = _expect_mapping(payload.get("exam"), "exam")
    problems_raw = _expect_list(payload.get("problems"), "problems")
    participants_raw = _expect_list(payload.get("participants"), "participants")
    rankings_raw = _expect_list(payload.get("rankings"), "rankings")
    submissions_raw = _expect_list(payload.get("submissions"), "submissions")
    integrity = _expect_mapping(payload.get("integrity"), "integrity")
    raw_indexes = payload.get("rawIndexes") if isinstance(payload.get("rawIndexes"), dict) else {}

    problem_set_id = _required_text(exam.get("problemSetId"), "exam.problemSetId")
    _validate_integrity(
        integrity=integrity,
        problems=problems_raw,
        participants=participants_raw,
        rankings=rankings_raw,
        submissions=submissions_raw,
    )

    problems, external_problems, versions, tags, links, problem_points = _parse_problems(
        problems_raw=problems_raw,
        problem_set_id=problem_set_id,
    )
    problem_ids = {problem.problem_code for problem in problems}
    participants = _parse_participants(
        participants_raw=participants_raw,
        rankings_raw=rankings_raw,
        raw_indexes=raw_indexes,
        problem_points=problem_points,
    )
    submissions, submission_codes = _parse_submissions(
        submissions_raw=submissions_raw,
        problem_set_id=problem_set_id,
        problem_ids=problem_ids,
        problem_by_code={item.problem_code: item for item in problems},
    )

    total_points = sum(item for item in problem_points.values() if item is not None)
    exam_meta = ExamMeta(
        title=clean_text(exam.get("title")) or f"pintia-{problem_set_id}",
        starts_at=_first_datetime(rankings_raw, "startAt"),
        ends_at=None,
        duration_seconds=None,
        total_points=total_points if total_points > 0 else None,
        meta={
            "schema": PINTIA_UNIT_SCHEMA,
            "platform": "pintia",
            "problemSetId": problem_set_id,
            "sourceUrl": clean_text(exam.get("sourceUrl")) or None,
            "exporter": payload.get("exporter")
            if isinstance(payload.get("exporter"), dict)
            else {},
            "integrity": integrity,
            "raw": exam.get("raw") if isinstance(exam.get("raw"), dict) else {},
            "metric_scope": "pintia_programming",
            "problem_kind_by_code": {
                problem.problem_code: problem.problem_kind
                for problem in problems
                if problem.problem_kind
            },
        },
    )

    return ExamBundle(
        unit=unit,
        exam_meta=exam_meta,
        problems=problems,
        participants=participants,
        submissions=submissions,
        external_problems=external_problems,
        external_problem_versions=versions,
        external_problem_tags=tags,
        exam_problem_external_links=links,
        submission_codes=submission_codes,
    )


def _single_pintia_source(unit: ExamUnit):
    sources = [item for item in unit.files if item.file_role == PINTIA_UNIT_ROLE]
    if len(sources) != 1:
        raise ValueError(f"expected exactly one {PINTIA_UNIT_ROLE}, got {len(sources)}")
    return sources[0]


def _load_payload(path: Path) -> dict[str, Any]:
    try:
        with path.open("r", encoding="utf-8") as file_obj:
            payload = json.load(file_obj)
    except json.JSONDecodeError as exc:
        raise ValueError(f"invalid Pintia JSON: {exc}") from exc
    if not isinstance(payload, dict):
        raise ValueError("Pintia unit must be a JSON object")
    return payload


def _validate_top_level(payload: dict[str, Any]) -> None:
    schema = payload.get("schema")
    if schema != PINTIA_UNIT_SCHEMA:
        raise ValueError(f"unsupported schema: {schema!r}")
    for key in ("exam", "problems", "participants", "rankings", "submissions", "integrity"):
        if key not in payload:
            raise ValueError(f"missing top-level field: {key}")


def _validate_integrity(
    integrity: dict[str, Any],
    problems: list[Any],
    participants: list[Any],
    rankings: list[Any],
    submissions: list[Any],
) -> None:
    expected = {
        "problemCount": len(problems),
        "participantCount": len(participants),
        "rankingCount": len(rankings),
        "submissionCount": len(submissions),
        "submissionDetailCount": len(submissions),
    }
    for key, value in expected.items():
        actual = parse_optional_int(integrity.get(key))
        if actual != value:
            raise ValueError(f"integrity.{key}={actual!r} does not match {value}")
    code_count = sum(
        1
        for item in submissions
        if isinstance(item, dict) and isinstance(item.get("code"), str)
    )
    actual_code_count = parse_optional_int(integrity.get("codeCount"))
    if actual_code_count != code_count:
        raise ValueError(f"integrity.codeCount={actual_code_count!r} does not match {code_count}")


def _parse_problems(
    problems_raw: list[Any],
    problem_set_id: str,
) -> tuple[
    list[ProblemInfo],
    list[ExternalProblemRow],
    list[ExternalProblemVersionRow],
    list[ExternalProblemTagRow],
    list[ExamProblemExternalLinkRow],
    dict[str, float | None],
]:
    problems: list[ProblemInfo] = []
    external_problems: list[ExternalProblemRow] = []
    versions: list[ExternalProblemVersionRow] = []
    tags: list[ExternalProblemTagRow] = []
    links: list[ExamProblemExternalLinkRow] = []
    problem_points: dict[str, float | None] = {}
    seen_problem_set_problem_ids: set[str] = set()
    seen_versions: set[tuple[str, str]] = set()
    seen_external: set[str] = set()

    for index, raw in enumerate(problems_raw, start=1):
        item = _expect_mapping(raw, f"problems[{index - 1}]")
        problem_set_problem_id = _required_text(item.get("id"), f"problems[{index - 1}].id")
        external_problem_id = _required_text(
            item.get("problemId"),
            f"problems[{index - 1}].problemId",
        )
        if problem_set_problem_id in seen_problem_set_problem_ids:
            raise ValueError(f"duplicate problem.id: {problem_set_problem_id}")
        seen_problem_set_problem_ids.add(problem_set_problem_id)

        title = clean_text(item.get("title")) or None
        problem_type = clean_text(item.get("type")) or None
        score = parse_optional_float(item.get("score"))
        display_code = _display_code(item, index)
        content_hash_payload = _problem_version_payload(item)
        content_sha256 = stable_json_hash(content_hash_payload)
        problem_points[problem_set_problem_id] = score

        problems.append(
            ProblemInfo(
                problem_code=problem_set_problem_id,
                problem_title=title,
                problem_kind=_normalize_problem_kind(problem_type),
                points=score,
                order_idx=parse_optional_int(item.get("problemPoolIndex")) or index,
                meta={
                    "source_platform": "pintia",
                    "problem_set_id": problem_set_id,
                    "problem_set_problem_id": problem_set_problem_id,
                    "external_problem_id": external_problem_id,
                    "content_sha256": content_sha256,
                    "display_code": display_code,
                    "label": clean_text(item.get("label")) or None,
                    "problemPoolIndex": item.get("problemPoolIndex"),
                    "indexInProblemPool": item.get("indexInProblemPool"),
                    "acceptCount": item.get("acceptCount"),
                    "submitCount": item.get("submitCount"),
                    "raw": item,
                },
            )
        )

        if external_problem_id not in seen_external:
            seen_external.add(external_problem_id)
            knowledge_point_paths = item.get("knowledgePointPaths")
            external_problems.append(
                ExternalProblemRow(
                    source_platform="pintia",
                    external_problem_id=external_problem_id,
                    title=title,
                    problem_type=problem_type,
                    difficulty=parse_optional_int(item.get("difficulty")),
                    meta={
                        "author": item.get("author"),
                        "authorOrganizationId": item.get("authorOrganizationId"),
                        "creatorId": item.get("creatorId"),
                        "knowledgePointPaths": knowledge_point_paths,
                    },
                )
            )
            tags.extend(
                _extract_external_problem_tags(
                    source_platform="pintia",
                    external_problem_id=external_problem_id,
                    knowledge_point_paths=knowledge_point_paths,
                )
            )

        version_key = (external_problem_id, content_sha256)
        if version_key not in seen_versions:
            seen_versions.add(version_key)
            versions.append(
                ExternalProblemVersionRow(
                    source_platform="pintia",
                    external_problem_id=external_problem_id,
                    content_sha256=content_sha256,
                    title=title,
                    problem_type=problem_type,
                    score=score,
                    meta=content_hash_payload | {
                        "source_platform": "pintia",
                        "external_problem_id": external_problem_id,
                    },
                )
            )

        links.append(
            ExamProblemExternalLinkRow(
                problem_code=problem_set_problem_id,
                source_platform="pintia",
                external_problem_id=external_problem_id,
                content_sha256=content_sha256,
                problem_set_id=problem_set_id,
                problem_set_problem_id=problem_set_problem_id,
                display_code=display_code,
                raw={
                    "problemPoolIndex": item.get("problemPoolIndex"),
                    "indexInProblemPool": item.get("indexInProblemPool"),
                    "label": item.get("label"),
                },
            )
        )

    return problems, external_problems, versions, tags, links, problem_points


def _extract_external_problem_tags(
    *,
    source_platform: str,
    external_problem_id: str,
    knowledge_point_paths: Any,
) -> list[ExternalProblemTagRow]:
    if not isinstance(knowledge_point_paths, list):
        return []
    result: list[ExternalProblemTagRow] = []
    seen: set[str] = set()
    for path in knowledge_point_paths:
        if not isinstance(path, dict):
            continue
        raw_points = path.get("knowledgePoints")
        if not isinstance(raw_points, list):
            continue
        path_names: list[str] = []
        path_rejected: list[dict[str, str]] = []
        for point in raw_points:
            if not isinstance(point, dict):
                continue
            name = clean_text(point.get("name"))
            if not name:
                continue
            path_names.append(name)
            normalized = canonicalize_knowledge_tags(name)
            rejected = [
                {"value": item.value, "reason": item.reason}
                for item in normalized.rejected
            ]
            path_rejected.extend(rejected)
            for tag in normalized.tags:
                if tag in seen:
                    continue
                seen.add(tag)
                result.append(
                    ExternalProblemTagRow(
                        source_platform=source_platform,
                        external_problem_id=external_problem_id,
                        knowledge_point=tag,
                        raw={
                            "path": list(path_names),
                            "sourceName": name,
                            "rejectedTags": list(path_rejected),
                            "id": point.get("id"),
                            "isLeaf": point.get("isLeaf"),
                            "enName": point.get("enName"),
                        },
                    )
                )
    return result


def _parse_participants(
    participants_raw: list[Any],
    rankings_raw: list[Any],
    raw_indexes: dict[str, Any],
    problem_points: dict[str, float | None],
) -> list[ParticipantRow]:
    student_user_by_id = raw_indexes.get("studentUserById")
    if not isinstance(student_user_by_id, dict):
        student_user_by_id = {}
    participant_by_id = {
        clean_text(item.get("id")): item
        for item in participants_raw
        if isinstance(item, dict) and clean_text(item.get("id"))
    }
    rows: list[ParticipantRow] = []
    seen_identity: set[tuple[str, str]] = set()

    for index, raw in enumerate(rankings_raw):
        ranking = _expect_mapping(raw, f"rankings[{index}]")
        user = ranking.get("user") if isinstance(ranking.get("user"), dict) else {}
        student_user_id = clean_text(user.get("studentUserId"))
        participant = {}
        if student_user_id:
            candidate = student_user_by_id.get(student_user_id) or participant_by_id.get(
                student_user_id
            )
            if isinstance(candidate, dict):
                participant = candidate
        student_no = clean_text(participant.get("studentNumber"))
        display_name = clean_text(participant.get("name")) or None
        user_id = clean_text(user.get("userId")) or student_user_id
        identity_source = "pintia_student_no" if student_no else "pintia_user_id"
        external_id = student_no or user_id
        if not external_id:
            raise ValueError(f"rankings[{index}] has no student number or user id")
        identity_key = (identity_source, external_id)
        if identity_key in seen_identity:
            continue
        seen_identity.add(identity_key)

        stats_raw = ranking.get("problemScoreByProblemSetProblemId")
        problem_stats = stats_raw if isinstance(stats_raw, dict) else {}
        solved_count = 0
        for problem_code, stats in problem_stats.items():
            if not isinstance(stats, dict):
                continue
            score = parse_optional_float(stats.get("score")) or 0.0
            max_score = problem_points.get(str(problem_code))
            if max_score is not None and max_score > 0 and score >= max_score:
                solved_count += 1

        rows.append(
            ParticipantRow(
                identity_source=identity_source,
                external_id=external_id,
                display_name=display_name,
                user_group=clean_text(user.get("userGroupId")) or None,
                rank=parse_optional_int(ranking.get("rank")),
                total_score=parse_optional_float(ranking.get("totalScore")),
                time_used_seconds=parse_optional_int(ranking.get("solvingTime")),
                solved_count=solved_count,
                absent=False,
                problem_stats={
                    str(problem_code): dict(stats) if isinstance(stats, dict) else {"raw": stats}
                    for problem_code, stats in problem_stats.items()
                },
                raw={
                    "source_platform": "pintia",
                    "userId": user_id,
                    "studentUserId": student_user_id or None,
                    "studentNumber": student_no or None,
                    "ranking": ranking,
                    "participant": participant,
                },
            )
        )
    return rows


def _parse_submissions(
    submissions_raw: list[Any],
    problem_set_id: str,
    problem_ids: set[str],
    problem_by_code: dict[str, ProblemInfo],
) -> tuple[list[SubmissionRow], list[SubmissionCodeRow]]:
    rows: list[SubmissionRow] = []
    code_rows: list[SubmissionCodeRow] = []
    seen_submission_ids: set[str] = set()

    for index, raw in enumerate(submissions_raw):
        item = _expect_mapping(raw, f"submissions[{index}]")
        submission_id = _required_text(
            item.get("submissionId"),
            f"submissions[{index}].submissionId",
        )
        if submission_id in seen_submission_ids:
            raise ValueError(f"duplicate submissionId: {submission_id}")
        seen_submission_ids.add(submission_id)
        problem_set_problem_id = _required_text(
            item.get("problemSetProblemId"),
            f"submissions[{index}].problemSetProblemId",
        )
        if problem_set_problem_id not in problem_ids:
            raise ValueError(
                f"submission {submission_id} references unknown problem {problem_set_problem_id}"
            )
        code_raw = item.get("code")
        if not isinstance(code_raw, str):
            raise ValueError(f"submissions[{index}].code must be a string")
        code = code_raw
        expected_code_hash = _required_text(
            item.get("codeSha256"),
            f"submissions[{index}].codeSha256",
        )
        actual_code_hash = sha256_text(code)
        if actual_code_hash != expected_code_hash:
            raise ValueError(f"submission {submission_id} codeSha256 mismatch")

        student_no = clean_text(item.get("studentNo"))
        user_id = clean_text(item.get("userId"))
        actor_source = "pintia_student_no" if student_no else "pintia_user_id"
        actor_external_id = student_no or user_id
        if not actor_external_id:
            raise ValueError(f"submission {submission_id} has no student number or user id")
        row_hash = sha256_text(f"{PINTIA_UNIT_SCHEMA}|{problem_set_id}|{submission_id}")
        language = clean_text(item.get("language")) or clean_text(item.get("compiler")) or None
        problem = problem_by_code[problem_set_problem_id]

        rows.append(
            SubmissionRow(
                actor_source=actor_source,
                actor_external_id=actor_external_id,
                actor_name=clean_text(item.get("name")) or None,
                submitted_at=_parse_datetime(item.get("submittedAt")),
                verdict=_normalize_status(item.get("status")),
                score=parse_optional_float(item.get("score")),
                problem_code=problem_set_problem_id,
                language=language,
                memory_kb=parse_optional_int(item.get("memoryKb")),
                time_ms=parse_optional_int(item.get("timeMs")),
                row_hash=row_hash,
                raw={
                    "source_platform": "pintia",
                    "pintia_submission_id": submission_id,
                    "problem_set_id": problem_set_id,
                    "problem_set_problem_id": problem_set_problem_id,
                    "external_problem_id": problem.meta.get("external_problem_id"),
                    "display_code": problem.meta.get("display_code"),
                    "code_sha256": expected_code_hash,
                    "status": item.get("status"),
                    "raw": item.get("raw") if isinstance(item.get("raw"), dict) else {},
                },
            )
        )
        code_rows.append(
            SubmissionCodeRow(
                submission_row_hash=row_hash,
                source_platform="pintia",
                external_submission_id=submission_id,
                language=language,
                code_content=code,
                code_sha256=expected_code_hash,
                compile_log=clean_text(item.get("compileLog")) or None,
                case_results=item.get("caseResults")
                if isinstance(item.get("caseResults"), dict)
                else {},
                raw={
                    "problem_set_id": problem_set_id,
                    "problem_set_problem_id": problem_set_problem_id,
                    "external_problem_id": problem.meta.get("external_problem_id"),
                },
            )
        )
    return rows, code_rows


def _problem_version_payload(item: dict[str, Any]) -> dict[str, Any]:
    return {
        "title": item.get("title"),
        "type": item.get("type"),
        "score": item.get("score"),
        "content": item.get("content"),
        "description": item.get("description"),
        "problemConfig": item.get("problemConfig"),
        "judgeConfig": item.get("judgeConfig"),
        "knowledgePointPaths": item.get("knowledgePointPaths"),
        "compiler": item.get("compiler"),
        "difficulty": item.get("difficulty"),
    }


def _display_code(item: dict[str, Any], index: int) -> str:
    label = clean_text(item.get("label"))
    if label:
        return label
    pool_index = parse_optional_int(item.get("problemPoolIndex"))
    if pool_index is not None:
        return f"7-{pool_index}"
    return str(index)


def _normalize_problem_kind(problem_type: str | None) -> str | None:
    if problem_type == "PROGRAMMING":
        return "编程题"
    return problem_type


def _normalize_status(value: Any) -> str | None:
    status = clean_text(value)
    if not status:
        return None
    return _STATUS_ALIASES.get(status, status)


def _first_datetime(items: list[Any], key: str) -> datetime | None:
    for item in items:
        if not isinstance(item, dict):
            continue
        parsed = _parse_datetime(item.get(key))
        if parsed is not None:
            return parsed
    return None


def _parse_datetime(value: Any) -> datetime | None:
    text = clean_text(value)
    if not text:
        return None
    try:
        return datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"invalid datetime: {text}") from exc


def _required_text(value: Any, field_name: str) -> str:
    text = clean_text(value)
    if not text:
        raise ValueError(f"missing required field: {field_name}")
    return text


def _expect_mapping(value: Any, field_name: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ValueError(f"{field_name} must be an object")
    return value


def _expect_list(value: Any, field_name: str) -> list[Any]:
    if not isinstance(value, list):
        raise ValueError(f"{field_name} must be an array")
    return value
