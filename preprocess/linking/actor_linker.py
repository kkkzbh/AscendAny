from __future__ import annotations

from dataclasses import dataclass, field
from fnmatch import fnmatchcase
from typing import Any

from ..config import MappingConfig
from ..utils import clean_text


def _normalize_token(value: str | None) -> str:
    text = clean_text(value)
    if not text:
        return ""
    return text.casefold()


def _is_student_no_identity(source: str | None) -> bool:
    return "student_no" in clean_text(source).casefold()


def _normalize_name_fallback(value: str | None) -> str:
    text = clean_text(value)
    if text.startswith("name::"):
        return clean_text(text[6:])
    return text


@dataclass(slots=True)
class CandidateIdentity:
    student_id: int
    canonical_name: str | None
    identity_source: str | None
    external_id: str | None
    external_name: str | None


@dataclass(slots=True)
class SubmissionActor:
    submission_id: int
    actor_source: str
    actor_external_id: str
    actor_name: str | None
    student_id: int | None
    raw: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class LinkDecision:
    status: str
    reason: str
    student_id: int | None
    matched_key: str | None = None
    candidates: list[int] = field(default_factory=list)


@dataclass(slots=True)
class SubmissionLinkUpdate:
    submission_id: int
    student_id: int | None
    link_payload: dict[str, Any]
    status: str


@dataclass(slots=True)
class LinkExamResult:
    matched: int = 0
    ambiguous: int = 0
    unmatched: int = 0
    updated: int = 0
    updates: list[SubmissionLinkUpdate] = field(default_factory=list)


class CandidateIndex:
    def __init__(self) -> None:
        self._index: dict[str, dict[str, set[int]]] = {
            "student_no": {},
            "name": {},
        }

    def add(self, key: str, value: str | None, student_id: int) -> None:
        normalized = _normalize_token(value)
        if not normalized:
            return
        bucket = self._index.setdefault(key, {})
        bucket.setdefault(normalized, set()).add(student_id)

    def lookup(self, key: str, values: list[str]) -> set[int]:
        bucket = self._index.get(key, {})
        candidates: set[int] = set()
        for value in values:
            normalized = _normalize_token(value)
            if not normalized:
                continue
            candidates.update(bucket.get(normalized, set()))
        return candidates


def build_candidate_index(rows: list[CandidateIdentity], primary_keys: list[str]) -> CandidateIndex:
    index = CandidateIndex()
    supported = set(primary_keys)
    for row in rows:
        if "name" in supported:
            index.add("name", row.canonical_name, row.student_id)
            index.add("name", row.external_name, row.student_id)
            if row.identity_source and "name_fallback" in row.identity_source:
                index.add("name", _normalize_name_fallback(row.external_id), row.student_id)
        if "student_no" in supported and _is_student_no_identity(row.identity_source):
            index.add("student_no", row.external_id, row.student_id)
    return index


def _source_allowed(actor_source: str, patterns: list[str]) -> bool:
    source = clean_text(actor_source)
    for pattern in patterns:
        if fnmatchcase(source, clean_text(pattern)):
            return True
    return False


def _probe_values(key: str, row: SubmissionActor) -> list[str]:
    if key == "student_no":
        return [row.actor_external_id, row.actor_name or ""]
    if key == "name":
        return [row.actor_name or "", _normalize_name_fallback(row.actor_external_id)]
    return []


def resolve_submission_actor(row: SubmissionActor, index: CandidateIndex, cfg: MappingConfig) -> LinkDecision:
    if not _source_allowed(row.actor_source, cfg.actor_sources):
        return LinkDecision(
            status="unmatched",
            reason="actor_source_filtered",
            student_id=None,
        )

    keyed_matches: list[tuple[str, set[int]]] = []
    for key in cfg.primary_keys:
        candidates = index.lookup(key, _probe_values(key, row))
        if candidates:
            keyed_matches.append((key, candidates))

    if not keyed_matches:
        return LinkDecision(
            status="unmatched",
            reason="no_candidate",
            student_id=None,
        )

    singleton_by_key: dict[str, int] = {}
    for key, candidates in keyed_matches:
        if len(candidates) == 1:
            singleton_by_key[key] = next(iter(candidates))

    if singleton_by_key:
        candidate_ids = set(singleton_by_key.values())
        if len(candidate_ids) == 1:
            student_id = next(iter(candidate_ids))
            matched_key = next(key for key in cfg.primary_keys if singleton_by_key.get(key) == student_id)
            return LinkDecision(
                status="matched",
                reason="unique_match",
                student_id=student_id,
                matched_key=matched_key,
                candidates=[student_id],
            )
        if cfg.strict_mode:
            return LinkDecision(
                status="ambiguous",
                reason="conflicting_primary_keys",
                student_id=None,
                candidates=sorted(candidate_ids),
            )
        for key in cfg.primary_keys:
            student_id = singleton_by_key.get(key)
            if student_id is not None:
                return LinkDecision(
                    status="matched",
                    reason=f"non_strict_{key}",
                    student_id=student_id,
                    matched_key=key,
                    candidates=sorted(candidate_ids),
                )

    overlap = set.intersection(*[candidates for _, candidates in keyed_matches])
    if len(overlap) == 1:
        student_id = next(iter(overlap))
        return LinkDecision(
            status="matched",
            reason="overlap_match",
            student_id=student_id,
            matched_key="overlap",
            candidates=[student_id],
        )

    union_candidates = set().union(*[candidates for _, candidates in keyed_matches])
    if not union_candidates:
        return LinkDecision(
            status="unmatched",
            reason="no_candidate",
            student_id=None,
        )

    if cfg.strict_mode:
        return LinkDecision(
            status="ambiguous",
            reason="multiple_candidates",
            student_id=None,
            candidates=sorted(union_candidates),
        )

    student_id = min(union_candidates)
    return LinkDecision(
        status="matched",
        reason="non_strict_multiple_candidates",
        student_id=student_id,
        matched_key="non_strict",
        candidates=sorted(union_candidates),
    )


def _build_link_payload(row: SubmissionActor, decision: LinkDecision) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "status": decision.status,
        "reason": decision.reason,
        "actor_source": row.actor_source,
        "actor_external_id": row.actor_external_id,
        "actor_name": row.actor_name,
    }
    if decision.student_id is not None:
        payload["student_id"] = decision.student_id
    if decision.matched_key:
        payload["matched_key"] = decision.matched_key
    if decision.candidates:
        payload["candidates"] = decision.candidates
    return payload


def plan_submission_links(
    submissions: list[SubmissionActor],
    candidates: list[CandidateIdentity],
    cfg: MappingConfig,
    write_unresolved_details: bool = True,
) -> LinkExamResult:
    index = build_candidate_index(candidates, primary_keys=cfg.primary_keys)
    result = LinkExamResult()

    for submission in submissions:
        decision = resolve_submission_actor(submission, index=index, cfg=cfg)
        payload = _build_link_payload(submission, decision)
        previous_payload = submission.raw.get("linking")
        payload_changed = previous_payload != payload

        if decision.status == "matched":
            result.matched += 1
            new_student_id = decision.student_id
            if new_student_id is None:
                continue
            if submission.student_id != new_student_id or payload_changed:
                result.updated += 1
                result.updates.append(
                    SubmissionLinkUpdate(
                        submission_id=submission.submission_id,
                        student_id=new_student_id,
                        link_payload=payload,
                        status=decision.status,
                    )
                )
            continue

        if decision.status == "ambiguous":
            result.ambiguous += 1
        else:
            result.unmatched += 1

        if write_unresolved_details and payload_changed:
            result.updated += 1
            result.updates.append(
                SubmissionLinkUpdate(
                    submission_id=submission.submission_id,
                    student_id=submission.student_id,
                    link_payload=payload,
                    status=decision.status,
                )
            )

    return result
