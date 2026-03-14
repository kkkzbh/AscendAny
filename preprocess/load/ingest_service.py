from __future__ import annotations

from dataclasses import dataclass
from fnmatch import fnmatchcase
import json
import os
from typing import Any, Callable
from urllib import error as urllib_error
from urllib import request as urllib_request

from ..config import Settings
from ..derive import compute_current_metrics, compute_exam_metrics, compute_exam_rating
from ..discover import discover_exam_units
from ..extract import parse_exam_bundle
from ..models import ExamMeta, ExamUnit, ParticipantRow
from ..utils import clean_text
from .repository import Repository


@dataclass(slots=True)
class RunSummary:
    ingest_run_id: int | None
    scanned: int
    skipped: int
    succeeded: int
    failed: int
    submissions_bound: int
    submissions_pending_claim: int
    nickname_conflicts: int
    achievements_recomputed_students: int
    errors: list[str]


class IngestService:
    def __init__(self, repo: Repository, settings: Settings) -> None:
        self.repo = repo
        self.settings = settings

    def discover_changed_units(
        self,
        exam_types: list[str] | None = None,
        source_paths: list[str] | None = None,
        limit: int | None = None,
    ) -> list[ExamUnit]:
        discovered = discover_exam_units(
            practice_root=self.settings.practice_root,
            exam_types=exam_types,
            fingerprint_roles=set(self.settings.ingest.fingerprint_roles),
        )
        source_path_filter = self._normalized_source_path_filter(source_paths)
        if source_path_filter is not None:
            discovered = [
                unit for unit in discovered if unit.source_path in source_path_filter
            ]
        changed: list[ExamUnit] = []
        for unit in discovered:
            previous = self.repo.get_latest_success_fingerprint(
                unit.exam_type, unit.source_path
            )
            if previous == unit.fingerprint:
                continue
            changed.append(unit)
        changed = sorted(changed, key=lambda item: (item.exam_type, item.source_path))
        if limit is not None:
            changed = changed[:limit]
        return changed

    def run(
        self,
        exam_types: list[str] | None = None,
        source_paths: list[str] | None = None,
        limit: int | None = None,
        dry_run: bool = False,
        force: bool = False,
        on_progress: Callable[[str, dict[str, Any]], None] | None = None,
    ) -> RunSummary:
        discovered = discover_exam_units(
            practice_root=self.settings.practice_root,
            exam_types=exam_types,
            fingerprint_roles=set(self.settings.ingest.fingerprint_roles),
        )
        source_path_filter = self._normalized_source_path_filter(source_paths)
        if source_path_filter is not None:
            discovered = [
                unit for unit in discovered if unit.source_path in source_path_filter
            ]
        included_problem_kinds = set(self.settings.metrics.included_problem_kinds)
        if force:
            changed = list(discovered)
        else:
            changed = []
            for unit in discovered:
                previous = self.repo.get_latest_success_fingerprint(
                    unit.exam_type, unit.source_path
                )
                if previous == unit.fingerprint:
                    continue
                changed.append(unit)
        changed = sorted(changed, key=lambda item: (item.exam_type, item.source_path))
        if limit is not None:
            changed = changed[:limit]

        if dry_run:
            if on_progress:
                on_progress("log", {"level": "info", "message": f"Dry run: {len(changed)} exam(s) would be processed out of {len(discovered)} discovered."})
                on_progress(
                    "done",
                    {
                        "ingestRunId": None,
                        "scanned": len(discovered),
                        "skipped": len(discovered) - len(changed),
                        "succeeded": 0,
                        "failed": 0,
                        "submissionsBound": 0,
                        "submissionsPendingClaim": 0,
                        "nicknameConflicts": 0,
                        "achievementsRecomputedStudents": 0,
                        "errors": [],
                    },
                )
            return RunSummary(
                ingest_run_id=None,
                scanned=len(discovered),
                skipped=len(discovered) - len(changed),
                succeeded=0,
                failed=0,
                submissions_bound=0,
                submissions_pending_claim=0,
                nickname_conflicts=0,
                achievements_recomputed_students=0,
                errors=[],
            )

        with self.repo.conn.transaction():
            ingest_run_id = self.repo.create_ingest_run(
                {
                    "practice_root": str(self.settings.practice_root),
                    "scanned": len(discovered),
                    "to_process": len(changed),
                    "metric_scope": "function_programming_only"
                    if included_problem_kinds
                    else "all_problem_kinds",
                    "metric_scope_version": "v2_function_programming_only",
                }
            )

        succeeded = 0
        failed = 0
        submissions_bound = 0
        submissions_pending_claim = 0
        nickname_conflicts = 0
        achievements_recomputed_students = 0
        errors: list[str] = []
        successful_exam_ids: list[int] = []

        for unit in changed:
            exam_id = None
            if on_progress:
                idx = changed.index(unit) + 1
                on_progress("progress", {
                    "current": idx,
                    "total": len(changed),
                    "examType": unit.exam_type,
                    "sourcePath": unit.source_path,
                    "phase": "processing",
                })
                on_progress("log", {"level": "info", "message": f"[{idx}/{len(changed)}] Processing {unit.exam_type}/{unit.source_path} ..."})
            exam_meta_for_failure = ExamMeta(
                title=unit.source_path.split("/")[-1],
                starts_at=None,
                ends_at=None,
                duration_seconds=None,
                total_points=None,
                meta={"source_path": unit.source_path, "placeholder": True},
            )
            try:
                bundle = parse_exam_bundle(
                    unit=unit,
                    encodings=self.settings.ingest.encodings,
                    timezone_name=self.settings.ingest.timezone,
                    included_problem_kinds=included_problem_kinds,
                )
                exam_meta_for_failure = bundle.exam_meta

                with self.repo.conn.transaction():
                    exam_id = self.repo.upsert_exam(
                        unit.exam_type, unit.source_path, bundle.exam_meta
                    )
                    self.repo.insert_exam_files(exam_id, unit.files)
                    for problem in bundle.problems:
                        self.repo.upsert_exam_problem(exam_id, problem)

                    participants = self._materialize_participants(bundle.participants)
                    for participant in participants:
                        self.repo.upsert_exam_participant(exam_id, participant)

                    bound_count, pending_count, conflict_count = self._bind_submission_claims(
                        bundle.submissions,
                        participants=participants,
                    )
                    submissions_bound += bound_count
                    submissions_pending_claim += pending_count
                    nickname_conflicts += conflict_count

                    for submission in bundle.submissions:
                        self.repo.insert_submission(exam_id, submission)

                    timeline_by_student = self._build_submission_timeline(
                        bundle.submissions
                    )
                    exam_meta_data = (
                        bundle.exam_meta.meta
                        if isinstance(bundle.exam_meta.meta, dict)
                        else {}
                    )
                    slot_count_by_kind_raw = exam_meta_data.get("slot_count_by_kind")
                    slot_count_by_kind: dict[str, int] = {}
                    if isinstance(slot_count_by_kind_raw, dict):
                        for key, value in slot_count_by_kind_raw.items():
                            try:
                                normalized = int(value)
                            except (TypeError, ValueError):
                                continue
                            if normalized > 0:
                                slot_count_by_kind[str(key)] = normalized

                    problem_kind_by_code_raw = exam_meta_data.get("problem_kind_by_code")
                    problem_kind_by_code: dict[str, str] = {}
                    if isinstance(problem_kind_by_code_raw, dict):
                        for key, value in problem_kind_by_code_raw.items():
                            if isinstance(value, str):
                                kind = value
                            elif isinstance(value, dict):
                                kind = value.get("problem_kind")
                            else:
                                kind = None
                            if kind is None:
                                continue
                            problem_kind_by_code[str(key)] = str(kind)
                    metric_results = compute_exam_metrics(
                        participants=participants,
                        total_points=bundle.exam_meta.total_points,
                        winsor_low=self.settings.metrics.winsor_low,
                        winsor_high=self.settings.metrics.winsor_high,
                        flexibility_mode=self.settings.metrics.flexibility_mode_default,
                        timeline_by_student=timeline_by_student,
                        included_problem_kinds=self.settings.metrics.included_problem_kinds,
                        random_exam_mode=bool(exam_meta_data.get("is_random_exam")),
                        random_exam_slots_by_kind=slot_count_by_kind,
                        random_exam_missing_drawn_set_policy=self.settings.metrics.random_exam_missing_drawn_set_policy,
                        problem_kind_by_code=problem_kind_by_code,
                    )
                    for metric in metric_results:
                        self.repo.upsert_exam_student_metric(
                            exam_id=exam_id,
                            student_id=metric.student_id,
                            knowledge=metric.knowledge,
                            accuracy=metric.accuracy,
                            quality=metric.quality,
                            flexibility=metric.flexibility,
                            proficiency=metric.proficiency,
                            details=metric.details,
                        )

                    participant_ids = [
                        item.student_id
                        for item in participants
                        if item.student_id is not None
                    ]
                    current_ratings = self.repo.fetch_ratings_before_exam(
                        exam_id=exam_id,
                        student_ids=participant_ids,
                        default_rating=self.settings.rating.initial_rating,
                    )
                    rating_results = compute_exam_rating(
                        participants=participants,
                        current_ratings=current_ratings,
                        cfg=self.settings.rating,
                    )
                    for rating in rating_results:
                        self.repo.upsert_rating_history(
                            exam_id=exam_id,
                            student_id=rating.student_id,
                            old_rating=rating.old_rating,
                            delta=rating.delta,
                            new_rating=rating.new_rating,
                            rank=rating.rank,
                            seed=rating.seed,
                            performance=rating.performance,
                            details=rating.details,
                        )

                    self._refresh_current_profiles(participant_ids)
                    achievements_recomputed_students += (
                        self.repo.recompute_achievements_for_students(
                            participant_ids,
                            source="ingest",
                        )
                    )
                    self.repo.record_ingest_exam_run(
                        ingest_run_id=ingest_run_id,
                        exam_id=exam_id,
                        fingerprint=unit.fingerprint,
                        status="success",
                        error_message=None,
                    )
                succeeded += 1
                successful_exam_ids.append(exam_id)
                if on_progress:
                    on_progress("log", {"level": "success", "message": f"✓ {unit.exam_type}/{unit.source_path} imported successfully."})
            except Exception as exc:  # noqa: BLE001
                failed += 1
                message = f"{unit.source_path}: {exc}"
                errors.append(message)
                if on_progress:
                    on_progress("log", {"level": "error", "message": f"✗ {unit.source_path}: {exc}"})
                with self.repo.conn.transaction():
                    exam_id = self.repo.upsert_exam(
                        unit.exam_type,
                        unit.source_path,
                        exam_meta_for_failure,
                    )
                    self.repo.record_ingest_exam_run(
                        ingest_run_id=ingest_run_id,
                        exam_id=exam_id,
                        fingerprint=unit.fingerprint,
                        status="failed",
                        error_message=str(exc)[:2000],
                    )

        status = "success"
        if failed > 0 and succeeded > 0:
            status = "partial_success"
        elif failed > 0 and succeeded == 0:
            status = "failed"

        cleanup_stats = {
            "submissions_unlinked": 0,
            "participants_deleted": 0,
            "metrics_deleted": 0,
            "ratings_deleted": 0,
            "current_metrics_deleted": 0,
            "students_deleted": 0,
        }
        with self.repo.conn.transaction():
            cleanup_stats = self.repo.cleanup_orphan_students()
            self.repo.finish_ingest_run(
                ingest_run_id=ingest_run_id,
                status=status,
                meta={
                    "succeeded": succeeded,
                    "failed": failed,
                    "submissions_bound": submissions_bound,
                    "submissions_pending_claim": submissions_pending_claim,
                    "nickname_conflicts": nickname_conflicts,
                    "achievements_recomputed_students": achievements_recomputed_students,
                    "errors": errors,
                    "cleanup": cleanup_stats,
                },
            )

        self._trigger_auto_analysis_prewarm(successful_exam_ids, errors)

        summary = RunSummary(
            ingest_run_id=ingest_run_id,
            scanned=len(discovered),
            skipped=len(discovered) - len(changed),
            succeeded=succeeded,
            failed=failed,
            submissions_bound=submissions_bound,
            submissions_pending_claim=submissions_pending_claim,
            nickname_conflicts=nickname_conflicts,
            achievements_recomputed_students=achievements_recomputed_students,
            errors=errors,
        )
        if on_progress:
            on_progress("done", {
                "ingestRunId": ingest_run_id,
                "scanned": summary.scanned,
                "skipped": summary.skipped,
                "succeeded": summary.succeeded,
                "failed": summary.failed,
                "submissionsBound": summary.submissions_bound,
                "submissionsPendingClaim": summary.submissions_pending_claim,
                "nicknameConflicts": summary.nickname_conflicts,
                "achievementsRecomputedStudents": summary.achievements_recomputed_students,
                "errors": summary.errors,
            })
        return summary

    @staticmethod
    def _normalized_source_path_filter(
        source_paths: list[str] | None,
    ) -> set[str] | None:
        if not source_paths:
            return None
        normalized: set[str] = set()
        for source_path in source_paths:
            item = clean_text(source_path).replace("\\", "/").strip("/")
            if item:
                normalized.add(item)
        return normalized or None

    def _materialize_participants(
        self, participants: list[ParticipantRow]
    ) -> list[ParticipantRow]:
        for participant in participants:
            student_id = self.repo.upsert_student_identity(
                source=participant.identity_source,
                external_id=participant.external_id,
                external_name=participant.display_name,
            )
            participant.student_id = student_id
        return participants

    def _refresh_current_profiles(self, student_ids: list[int]) -> None:
        unique_student_ids = sorted(set(student_ids))
        half_life = {
            "knowledge": self.settings.fusion.half_life_days.knowledge,
            "accuracy": self.settings.fusion.half_life_days.accuracy,
            "quality": self.settings.fusion.half_life_days.quality,
            "flexibility": self.settings.fusion.half_life_days.flexibility,
            "proficiency": self.settings.fusion.half_life_days.proficiency,
        }
        for student_id in unique_student_ids:
            history = self.repo.fetch_metric_history(student_id)
            metrics = compute_current_metrics(
                history_rows=history, half_life_days=half_life
            )
            rating = self.repo.fetch_latest_rating(
                student_id, default_rating=self.settings.rating.initial_rating
            )
            self.repo.upsert_student_current_metrics(
                student_id=student_id, metrics=metrics, rating=rating
            )

    @staticmethod
    def _build_submission_timeline(
        submissions: list[Any],
    ) -> dict[int, list[dict[str, Any]]]:
        timeline: dict[int, list[dict[str, Any]]] = {}
        for row in submissions:
            if row.student_id is None:
                continue
            timeline.setdefault(row.student_id, []).append(
                {
                    "submitted_at": row.submitted_at,
                    "problem_code": row.problem_code,
                    "verdict": row.verdict,
                    "score": row.score,
                }
            )
        return timeline

    def _bind_submission_claims(
        self,
        submissions: list[Any],
        participants: list[ParticipantRow] | None = None,
    ) -> tuple[int, int, int]:
        if not submissions:
            return 0, 0, 0

        actor_sources = self.settings.mapping.actor_sources
        if not self.settings.mapping.auto_bind_on_ingest:
            return 0, 0, 0

        student_identity_map: dict[tuple[str, str], int] = {}
        for participant in participants or []:
            student_id = participant.student_id
            identity_source = clean_text(participant.identity_source)
            external_id = clean_text(participant.external_id)
            if student_id is None or not identity_source or not external_id:
                continue
            student_identity_map[(identity_source, external_id)] = student_id

        nickname_values = []
        for row in submissions:
            actor_source = clean_text(getattr(row, "actor_source", ""))
            actor_external_id = clean_text(getattr(row, "actor_external_id", ""))
            if actor_source.endswith("_student_no") and actor_external_id:
                continue
            if not self._source_allowed(getattr(row, "actor_source", ""), actor_sources):
                continue
            nickname = self._resolve_submission_nickname(row)
            if nickname:
                nickname_values.append(nickname)

        claims = self.repo.fetch_active_nickname_claims(nickname_values)
        bound = 0
        pending = 0

        for row in submissions:
            actor_source = clean_text(getattr(row, "actor_source", ""))
            actor_external_id = clean_text(getattr(row, "actor_external_id", ""))
            if actor_source.endswith("_student_no") and actor_external_id:
                student_id = student_identity_map.get((actor_source, actor_external_id))
                if student_id is None:
                    row.student_id = None
                    row.raw["linking"] = self._build_linking_payload(
                        status="pending_identity",
                        reason="no_matching_student_no_identity",
                        row=row,
                        student_id=None,
                    )
                    pending += 1
                    continue
                row.student_id = student_id
                row.raw["linking"] = self._build_linking_payload(
                    status="bound_by_identity",
                    reason="matching_student_no_identity",
                    row=row,
                    student_id=student_id,
                )
                bound += 1
                continue
            if not self._source_allowed(getattr(row, "actor_source", ""), actor_sources):
                continue
            nickname = self._resolve_submission_nickname(row)
            if not nickname:
                row.student_id = None
                row.raw["linking"] = self._build_linking_payload(
                    status="pending_claim",
                    reason="missing_actor_nickname",
                    row=row,
                    student_id=None,
                )
                pending += 1
                continue
            student_id = claims.get(nickname.casefold())
            if student_id is None:
                row.student_id = None
                row.raw["linking"] = self._build_linking_payload(
                    status="pending_claim",
                    reason="no_active_claim",
                    row=row,
                    student_id=None,
                )
                pending += 1
                continue
            row.student_id = student_id
            row.raw["linking"] = self._build_linking_payload(
                status="bound_by_claim",
                reason="active_nickname_claim",
                row=row,
                student_id=student_id,
            )
            bound += 1

        return bound, pending, 0

    @staticmethod
    def _source_allowed(actor_source: str, patterns: list[str]) -> bool:
        source = clean_text(actor_source)
        if not source:
            return False
        for pattern in patterns:
            if fnmatchcase(source, clean_text(pattern)):
                return True
        return False

    @staticmethod
    def _resolve_submission_nickname(row: Any) -> str:
        actor_name = clean_text(getattr(row, "actor_name", None))
        if actor_name:
            return actor_name
        return clean_text(getattr(row, "actor_external_id", None))

    @staticmethod
    def _build_linking_payload(
        status: str,
        reason: str,
        row: Any,
        student_id: int | None,
    ) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "status": status,
            "reason": reason,
            "actor_source": getattr(row, "actor_source", None),
            "actor_external_id": getattr(row, "actor_external_id", None),
            "actor_name": getattr(row, "actor_name", None),
        }
        if student_id is not None:
            payload["student_id"] = student_id
        return payload

    def _trigger_auto_analysis_prewarm(
        self,
        successful_exam_ids: list[int],
        errors: list[str],
    ) -> None:
        warmup = self.settings.warmup
        if not warmup.enabled:
            return
        base_url = (warmup.api_base_url or "").strip().rstrip("/")
        if not base_url:
            errors.append("warmup enabled but api_base_url is empty")
            return
        token = os.getenv(warmup.token_env, "").strip()
        if not token:
            errors.append(f"warmup enabled but env {warmup.token_env} is empty")
            return

        endpoint = f"{base_url}/api/v1/chat/auto-analysis/precompute-exam"
        timeout = max(1.0, float(warmup.timeout_seconds))

        for exam_id in successful_exam_ids:
            payload = json.dumps(
                {
                    "examId": exam_id,
                    "roleId": warmup.role_id,
                },
                ensure_ascii=False,
            ).encode("utf-8")
            req = urllib_request.Request(
                endpoint,
                data=payload,
                method="POST",
                headers={
                    "Content-Type": "application/json",
                    "X-AscendAny-Prewarm-Token": token,
                },
            )
            try:
                with urllib_request.urlopen(req, timeout=timeout) as response:
                    if response.status < 200 or response.status >= 300:
                        errors.append(
                            f"warmup exam_id={exam_id} failed with status={response.status}"
                        )
            except urllib_error.HTTPError as exc:
                errors.append(
                    f"warmup exam_id={exam_id} http_error={exc.code}"
                )
            except Exception as exc:  # noqa: BLE001
                errors.append(
                    f"warmup exam_id={exam_id} error={exc}"
                )
