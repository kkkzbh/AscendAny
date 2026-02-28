from __future__ import annotations

import argparse
import json
from pathlib import Path

from .config import load_settings
from .db import connect
from .discover import discover_exam_units
from .load import IngestService, Repository


def _apply_overrides(args: argparse.Namespace):
    settings = load_settings(config_path=Path(args.config) if args.config else None)
    if args.practice_root:
        settings.practice_root = Path(args.practice_root)
    if args.db_dsn:
        settings.db.dsn = args.db_dsn
    if args.db_host:
        settings.db.host = args.db_host
    if args.db_port:
        settings.db.port = int(args.db_port)
    if args.db_name:
        settings.db.dbname = args.db_name
    if args.db_user:
        settings.db.user = args.db_user
    return settings


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="AscendAny preprocess CLI")
    parser.add_argument("--config", default="preprocess/config/default.yaml")
    parser.add_argument("--practice-root")
    parser.add_argument("--db-dsn")
    parser.add_argument("--db-host")
    parser.add_argument("--db-port", type=int)
    parser.add_argument("--db-name")
    parser.add_argument("--db-user")

    subparsers = parser.add_subparsers(dest="command", required=True)

    run_parser = subparsers.add_parser("run", help="run incremental ingest")
    run_parser.add_argument("--exam-type", action="append", default=None)
    run_parser.add_argument("--limit", type=int)
    run_parser.add_argument("--dry-run", action="store_true")
    run_parser.add_argument(
        "--force",
        action="store_true",
        help="reprocess all discovered exams regardless of fingerprint",
    )

    discover_parser = subparsers.add_parser("discover", help="list discovered exams")
    discover_parser.add_argument("--exam-type", action="append", default=None)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    settings = _apply_overrides(args)

    if args.command == "discover":
        units = discover_exam_units(
            practice_root=settings.practice_root,
            exam_types=args.exam_type,
            fingerprint_roles=set(settings.ingest.fingerprint_roles),
        )
        rows = [
            {
                "exam_type": unit.exam_type,
                "source_path": unit.source_path,
                "fingerprint": unit.fingerprint,
                "file_count": len(unit.files),
            }
            for unit in units
        ]
        print(json.dumps(rows, ensure_ascii=False, indent=2))
        return 0

    with connect(settings) as conn:
        repo = Repository(conn)
        service = IngestService(repo=repo, settings=settings)
        summary = service.run(
            exam_types=args.exam_type,
            limit=args.limit,
            dry_run=args.dry_run,
            force=args.force,
        )
        print(
            json.dumps(
                {
                    "ingest_run_id": summary.ingest_run_id,
                    "scanned": summary.scanned,
                    "skipped": summary.skipped,
                    "succeeded": summary.succeeded,
                    "failed": summary.failed,
                    "submissions_bound": summary.submissions_bound,
                    "submissions_pending_claim": summary.submissions_pending_claim,
                    "nickname_conflicts": summary.nickname_conflicts,
                    "achievements_recomputed_students": summary.achievements_recomputed_students,
                    "errors": summary.errors,
                },
                ensure_ascii=False,
                indent=2,
            )
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
