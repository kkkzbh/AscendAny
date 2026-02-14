from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable

from ..config import Settings
from ..derive import compute_current_metrics
from ..derive.metrics import compute_flexibility_scores
from .actor_linker import CandidateIdentity, SubmissionActor, plan_submission_links


@dataclass(slots=True)
class LinkActorsSummary:
    scanned_exams: int
    processed_exams: int
    matched: int
    ambiguous: int
    unmatched: int
    updated: int
    metrics_updated: int
    remaining_unmatched: int


class LinkActorsService:
    def __init__(self, repo: Any, settings: Settings) -> None:
        self.repo = repo
        self.settings = settings

    def run(
        self,
        exam_types: list[str] | None = None,
        limit: int | None = None,
        dry_run: bool = False,
        on_progress: Callable[[str, dict[str, Any]], None] | None = None,
    ) -> LinkActorsSummary:
        exams = self.repo.list_exams_with_unlinked_submissions(
            exam_types=exam_types,
            actor_sources=self.settings.mapping.actor_sources,
            limit=limit,
        )
        scanned_exams = len(exams)
        processed_exams = 0
        matched = 0
        ambiguous = 0
        unmatched = 0
        updated = 0
        metrics_updated = 0

        for exam in exams:
            exam_id = int(exam["exam_id"])
            if on_progress:
                idx = exams.index(exam) + 1
                on_progress("progress", {
                    "current": idx,
                    "total": scanned_exams,
                    "phase": "linking",
                })
                on_progress("log", {"level": "info", "message": f"[{idx}/{scanned_exams}] Linking actors for exam {exam_id} ..."})
            candidate_rows = [
                CandidateIdentity(
                    student_id=int(row["student_id"]),
                    canonical_name=row.get("canonical_name"),
                    identity_source=row.get("identity_source"),
                    external_id=row.get("external_id"),
                    external_name=row.get("external_name"),
                )
                for row in self.repo.fetch_exam_link_candidates(exam_id=exam_id)
            ]
            submission_rows = [
                SubmissionActor(
                    submission_id=int(row["submission_id"]),
                    actor_source=str(row.get("actor_source") or ""),
                    actor_external_id=str(row.get("actor_external_id") or ""),
                    actor_name=row.get("actor_name"),
                    student_id=row.get("student_id"),
                    raw=dict(row.get("raw") or {}),
                )
                for row in self.repo.fetch_exam_unlinked_submissions(
                    exam_id=exam_id,
                    actor_sources=self.settings.mapping.actor_sources,
                )
            ]
            if not submission_rows:
                continue

            processed_exams += 1
            exam_result = plan_submission_links(
                submissions=submission_rows,
                candidates=candidate_rows,
                cfg=self.settings.mapping,
                write_unresolved_details=True,
            )
            matched += exam_result.matched
            ambiguous += exam_result.ambiguous
            unmatched += exam_result.unmatched

            if dry_run:
                updated += exam_result.updated
                continue

            with self.repo.conn.transaction():
                changed_rows = 0
                for update in exam_result.updates:
                    applied = self.repo.update_submission_linking(
                        submission_id=update.submission_id,
                        student_id=update.student_id,
                        linking_payload=update.link_payload,
                    )
                    if applied:
                        changed_rows += 1
                if changed_rows > 0:
                    refreshed_students = self._refresh_exam_flexibility(exam_id=exam_id)
                    metrics_updated += len(refreshed_students)
                    self._refresh_current_profiles(student_ids=sorted(refreshed_students))
                updated += changed_rows

        remaining_unmatched = self.repo.count_unlinked_submissions(
            exam_types=exam_types,
            actor_sources=self.settings.mapping.actor_sources,
        )
        summary = LinkActorsSummary(
            scanned_exams=scanned_exams,
            processed_exams=processed_exams,
            matched=matched,
            ambiguous=ambiguous,
            unmatched=unmatched,
            updated=updated,
            metrics_updated=metrics_updated,
            remaining_unmatched=remaining_unmatched,
        )
        if on_progress:
            on_progress("done", {
                "scannedExams": summary.scanned_exams,
                "processedExams": summary.processed_exams,
                "matched": summary.matched,
                "ambiguous": summary.ambiguous,
                "unmatched": summary.unmatched,
                "updated": summary.updated,
                "metricsUpdated": summary.metrics_updated,
                "remainingUnmatched": summary.remaining_unmatched,
            })
        return summary

    def _refresh_exam_flexibility(self, exam_id: int) -> set[int]:
        rows = self.repo.fetch_exam_metric_rows(exam_id=exam_id)
        if not rows:
            return set()

        approx_signals: dict[int, float] = {}
        details_by_student: dict[int, dict[str, Any]] = {}
        current_scores: dict[int, int | None] = {}

        for row in rows:
            student_id = int(row["student_id"])
            details = row.get("details")
            if isinstance(details, dict):
                current_details = dict(details)
            else:
                current_details = {}
            details_by_student[student_id] = current_details
            current_scores[student_id] = row.get("flexibility")
            maybe_signal = current_details.get("flexibility_signal")
            if maybe_signal is None:
                maybe_signal = current_details.get("flexibility_signal_approx")
            try:
                if maybe_signal is not None:
                    approx_signals[student_id] = float(maybe_signal)
            except (TypeError, ValueError):
                continue

        timeline = self.repo.fetch_exam_submission_timeline(exam_id=exam_id)
        scores, mode_details = compute_flexibility_scores(
            approx_signals=approx_signals,
            timeline_by_student=timeline,
            winsor_low=self.settings.metrics.winsor_low,
            winsor_high=self.settings.metrics.winsor_high,
            fallback_mode=self.settings.metrics.flexibility_mode_default,
        )

        changed: set[int] = set()
        for student_id, current_details in details_by_student.items():
            if current_details.get("absent") is True:
                continue
            patch = mode_details.get(student_id, {"flexibility_mode": "none", "flexibility_signal": None})
            merged_details = dict(current_details)
            merged_details.update(patch)
            new_score = scores.get(student_id)
            if current_scores.get(student_id) == new_score and merged_details == current_details:
                continue
            applied = self.repo.update_exam_metric_flexibility(
                exam_id=exam_id,
                student_id=student_id,
                flexibility=new_score,
                details=merged_details,
            )
            if applied:
                changed.add(student_id)
        return changed

    def _refresh_current_profiles(self, student_ids: list[int]) -> None:
        if not student_ids:
            return
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
                history_rows=history,
                half_life_days=half_life,
            )
            rating = self.repo.fetch_latest_rating(student_id, default_rating=self.settings.rating.initial_rating)
            self.repo.upsert_student_current_metrics(
                student_id=student_id,
                metrics=metrics,
                rating=rating,
            )
