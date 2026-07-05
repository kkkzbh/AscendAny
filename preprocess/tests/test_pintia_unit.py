from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path

import pytest

from preprocess.discover import discover_exam_units
from preprocess.extract import parse_exam_bundle


def _unit_payload() -> dict[str, object]:
    code = "#include <stdio.h>\nint main(){puts(\"ok\");}\n"
    code_sha256 = hashlib.sha256(code.encode("utf-8")).hexdigest()
    return {
        "schema": "ascendany.pintia.unit.v1",
        "exporter": {"name": "test-exporter"},
        "exam": {
            "platform": "pintia",
            "problemSetId": "ps-1",
            "title": "Pintia Mock",
            "sourceUrl": "https://pintia.cn/problem-sets/ps-1/submissions",
        },
        "problems": [
            {
                "id": "psp-1",
                "problemId": "p-1",
                "title": "Same Display Code",
                "type": "PROGRAMMING",
                "score": 10,
                "problemPoolIndex": 1,
                "label": "",
                "content": "content",
                "problemConfig": {"programmingProblemConfig": {"timeLimit": 400}},
                "judgeConfig": {"programmingJudgeConfig": {"testDataVersion": 1}},
                "knowledgePointPaths": [
                    {
                        "knowledgePoints": [
                            {"id": "1", "name": "C程序设计", "isLeaf": False},
                            {"id": "2", "name": "数组", "isLeaf": True},
                        ]
                    }
                ],
            }
        ],
        "participants": [
            {"id": "student-user-1", "studentNumber": "20240001", "name": "Alice"}
        ],
        "rankings": [
            {
                "rank": 1,
                "user": {"userId": "user-1", "studentUserId": "student-user-1"},
                "totalScore": 10,
                "solvingTime": 60,
                "startAt": "2026-04-04T01:00:03Z",
                "problemScoreByProblemSetProblemId": {
                    "psp-1": {"score": 10, "validSubmitCount": 1}
                },
            }
        ],
        "submissions": [
            {
                "submissionId": "sub-1",
                "problemSetProblemId": "psp-1",
                "userId": "user-1",
                "studentNo": "20240001",
                "name": "Alice",
                "submittedAt": "2026-04-04T01:01:03Z",
                "language": "GXX",
                "status": "PARTIAL_ACCEPTED",
                "score": 5,
                "timeMs": 3,
                "memoryKb": 780,
                "compiler": "GXX",
                "code": code,
                "codeSha256": code_sha256,
                "caseResults": {"case-1": {"result": "WRONG_ANSWER"}},
                "compileLog": "",
                "raw": {"listItem": {"id": "sub-1"}},
            }
        ],
        "rawIndexes": {
            "studentUserById": {
                "student-user-1": {
                    "id": "student-user-1",
                    "studentNumber": "20240001",
                    "name": "Alice",
                }
            }
        },
        "integrity": {
            "problemCount": 1,
            "participantCount": 1,
            "rankingCount": 1,
            "submissionCount": 1,
            "submissionDetailCount": 1,
            "codeCount": 1,
            "warnings": [],
        },
    }


def _write_payload(path: Path, payload: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")


def test_discover_recursively_detects_pintia_schema_json(tmp_path: Path) -> None:
    _write_payload(tmp_path / "nested" / "unit.json", _unit_payload())
    (tmp_path / "legacy" / "提交记录").mkdir(parents=True)
    (tmp_path / "legacy" / "提交记录" / "提交记录.csv").write_text("old", encoding="utf-8")
    (tmp_path / "other.json").write_text('{"schema":"other"}', encoding="utf-8")

    units = discover_exam_units(tmp_path)

    assert len(units) == 1
    assert units[0].exam_type == "pintia"
    assert units[0].source_path == "nested/unit.json"
    assert units[0].files[0].file_role == "ascendany_pintia_unit_json"


def test_parse_pintia_unit_maps_stable_problem_and_submission_identities(tmp_path: Path) -> None:
    _write_payload(tmp_path / "unit.json", _unit_payload())
    unit = discover_exam_units(tmp_path)[0]

    bundle = parse_exam_bundle(unit, encodings=["utf-8"], timezone_name="Asia/Shanghai")

    assert bundle.exam_meta.title == "Pintia Mock"
    assert bundle.exam_meta.meta["problemSetId"] == "ps-1"
    assert bundle.problems[0].problem_code == "psp-1"
    assert bundle.problems[0].meta["display_code"] == "7-1"
    assert bundle.external_problems[0].external_problem_id == "p-1"
    assert [tag.knowledge_point for tag in bundle.external_problem_tags] == [
        "数组",
    ]
    assert bundle.external_problem_tags[0].raw["rejectedTags"] == [
        {"value": "C程序设计", "reason": "out_of_scope"}
    ]
    assert bundle.exam_problem_external_links[0].problem_set_problem_id == "psp-1"
    assert bundle.participants[0].identity_source == "pintia_student_no"
    assert bundle.participants[0].external_id == "20240001"
    assert bundle.submissions[0].problem_code == "psp-1"
    assert bundle.submissions[0].verdict == "部分正确"
    assert bundle.submission_codes[0].external_submission_id == "sub-1"
    assert bundle.submission_codes[0].code_content.endswith("\n")
    assert bundle.participants[0].problem_stats["psp-1"]["solved"] is True
    assert bundle.participants[0].problem_stats["psp-1"]["attempts"] == 1
    assert bundle.participants[0].problem_stats["psp-1"]["wrong_before_ac"] == 0


def test_parse_pintia_unit_uses_passed_flag_when_score_is_incomplete(tmp_path: Path) -> None:
    payload = copy.deepcopy(_unit_payload())
    payload["rankings"][0]["problemScoreByProblemSetProblemId"] = {  # type: ignore[index]
        "psp-1": {"score": 9.0, "validSubmitCount": 3, "passed": False, "raw": "9.0/10.0"}
    }
    _write_payload(tmp_path / "unit.json", payload)
    unit = discover_exam_units(tmp_path)[0]

    bundle = parse_exam_bundle(unit, encodings=["utf-8"], timezone_name="Asia/Shanghai")

    assert bundle.participants[0].problem_stats["psp-1"]["solved"] is False
    assert bundle.participants[0].problem_stats["psp-1"]["attempts"] == 3
    assert bundle.participants[0].problem_stats["psp-1"]["wrong_before_ac"] == 3
    assert bundle.participants[0].solved_count == 0


def test_parse_pintia_unit_rejects_unknown_submission_problem(tmp_path: Path) -> None:
    payload = copy.deepcopy(_unit_payload())
    payload["submissions"][0]["problemSetProblemId"] = "missing"  # type: ignore[index]
    _write_payload(tmp_path / "unit.json", payload)
    unit = discover_exam_units(tmp_path)[0]

    with pytest.raises(ValueError, match="references unknown problem"):
        parse_exam_bundle(unit, encodings=["utf-8"], timezone_name="Asia/Shanghai")


def test_parse_pintia_unit_rejects_code_hash_mismatch(tmp_path: Path) -> None:
    payload = copy.deepcopy(_unit_payload())
    payload["submissions"][0]["codeSha256"] = "bad"  # type: ignore[index]
    _write_payload(tmp_path / "unit.json", payload)
    unit = discover_exam_units(tmp_path)[0]

    with pytest.raises(ValueError, match="codeSha256 mismatch"):
        parse_exam_bundle(unit, encodings=["utf-8"], timezone_name="Asia/Shanghai")
