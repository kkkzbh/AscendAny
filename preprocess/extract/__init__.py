from __future__ import annotations

from ..discover import PINTIA_UNIT_ROLE
from ..models import ExamBundle, ExamUnit
from .pintia_unit import parse_pintia_unit


def parse_exam_bundle(
    unit: ExamUnit,
    encodings: list[str],
    timezone_name: str,
    included_problem_kinds: set[str] | None = None,
) -> ExamBundle:
    _ = encodings, timezone_name, included_problem_kinds
    if any(item.file_role == PINTIA_UNIT_ROLE for item in unit.files):
        return parse_pintia_unit(unit)
    roles = sorted({item.file_role for item in unit.files})
    raise ValueError(
        "unsupported exam input; expected ascendany.pintia.unit.v1 JSON "
        f"({PINTIA_UNIT_ROLE}), got roles={roles}"
    )
