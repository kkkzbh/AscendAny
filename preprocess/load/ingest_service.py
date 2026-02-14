from __future__ import annotations

from dataclasses import dataclass
import json
import os
from typing import Any
from urllib import error as urllib_error
from urllib import request as urllib_request

from ..config import Settings
from ..derive import compute_current_metrics, compute_exam_metrics, compute_exam_rating
from ..discover import discover_exam_units
from ..extract import parse_exam_bundle
from ..models import ExamMeta, ExamUnit, ParticipantRow
from .repository import Repository


@dataclass(slots=True)
class RunSummary:
    ingest_run_id: int | None
    scanned: int
    skipped: int
    succeeded: int
    failed: int
    errors: list[str]


class IngestService:
    def __init__(self, repo: Repository, settings: Settings) -> None:
        self.repo = repo
        self.settings = settings

    def discover_changed_units(
        self, exam_types: list[str] | None = None, limit: int | None = None
    ) -> list[ExamUnit]:
        discovered = discover_exam_units(
            practice_root=self.settings.practice_root,
            exam_types=exam_types,
            fingerprint_roles=set(self.settings.ingest.fingerprint_roles),
        )
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
        limit: int | None = None,
        dry_run: bool = False,
        force: bool = False,
    ) -> RunSummary:
        discovered = discover_exam_units(
            practice_root=self.settings.practice_root,
            exam_types=exam_types,
            fingerprint_roles=set(self.settings.ingest.fingerprint_roles),
        )
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
            return RunSummary(
                ingest_run_id=None,
                scanned=len(discovered),
                skipped=len(discovered) - len(changed),
                succeeded=0,
                failed=0,
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
        errors: list[str] = []
        successful_exam_ids: list[int] = []

        for unit in changed:
            exam_id = None
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
                    current_ratings = self.repo.fetch_current_ratings(participant_ids)
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
                    self.repo.record_ingest_exam_run(
                        ingest_run_id=ingest_run_id,
                        exam_id=exam_id,
                        fingerprint=unit.fingerprint,
                        status="success",
                        error_message=None,
                    )
                succeeded += 1
                successful_exam_ids.append(exam_id)
            except Exception as exc:  # noqa: BLE001
                failed += 1
                message = f"{unit.source_path}: {exc}"
                errors.append(message)
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
                    "errors": errors,
                    "cleanup": cleanup_stats,
                },
            )

        self._trigger_auto_analysis_prewarm(successful_exam_ids, errors)

        return RunSummary(
            ingest_run_id=ingest_run_id,
            scanned=len(discovered),
            skipped=len(discovered) - len(changed),
            succeeded=succeeded,
            failed=failed,
            errors=errors,
        )

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
