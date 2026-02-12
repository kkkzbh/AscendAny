from __future__ import annotations

from dataclasses import dataclass
from typing import Any

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

    def discover_changed_units(self, exam_types: list[str] | None = None, limit: int | None = None) -> list[ExamUnit]:
        discovered = discover_exam_units(
            practice_root=self.settings.practice_root,
            exam_types=exam_types,
            fingerprint_roles=set(self.settings.ingest.fingerprint_roles),
        )
        changed: list[ExamUnit] = []
        for unit in discovered:
            previous = self.repo.get_latest_success_fingerprint(unit.exam_type, unit.source_path)
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
    ) -> RunSummary:
        discovered = discover_exam_units(
            practice_root=self.settings.practice_root,
            exam_types=exam_types,
            fingerprint_roles=set(self.settings.ingest.fingerprint_roles),
        )
        changed: list[ExamUnit] = []
        for unit in discovered:
            previous = self.repo.get_latest_success_fingerprint(unit.exam_type, unit.source_path)
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
                }
            )

        succeeded = 0
        failed = 0
        errors: list[str] = []

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
                )
                exam_meta_for_failure = bundle.exam_meta

                with self.repo.conn.transaction():
                    exam_id = self.repo.upsert_exam(unit.exam_type, unit.source_path, bundle.exam_meta)
                    self.repo.insert_exam_files(exam_id, unit.files)
                    for problem in bundle.problems:
                        self.repo.upsert_exam_problem(exam_id, problem)

                    participants = self._materialize_participants(bundle.participants)
                    for participant in participants:
                        self.repo.upsert_exam_participant(exam_id, participant)

                    for submission in bundle.submissions:
                        self.repo.insert_submission(exam_id, submission)

                    metric_results = compute_exam_metrics(
                        participants=participants,
                        total_points=bundle.exam_meta.total_points,
                        winsor_low=self.settings.metrics.winsor_low,
                        winsor_high=self.settings.metrics.winsor_high,
                        flexibility_mode=self.settings.metrics.flexibility_mode_default,
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

                    participant_ids = [item.student_id for item in participants if item.student_id is not None]
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

        with self.repo.conn.transaction():
            self.repo.finish_ingest_run(
                ingest_run_id=ingest_run_id,
                status=status,
                meta={
                    "succeeded": succeeded,
                    "failed": failed,
                    "errors": errors,
                },
            )

        return RunSummary(
            ingest_run_id=ingest_run_id,
            scanned=len(discovered),
            skipped=len(discovered) - len(changed),
            succeeded=succeeded,
            failed=failed,
            errors=errors,
        )

    def _materialize_participants(self, participants: list[ParticipantRow]) -> list[ParticipantRow]:
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
            metrics = compute_current_metrics(history_rows=history, half_life_days=half_life)
            rating = self.repo.fetch_latest_rating(student_id, default_rating=self.settings.rating.initial_rating)
            self.repo.upsert_student_current_metrics(student_id=student_id, metrics=metrics, rating=rating)
