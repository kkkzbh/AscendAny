from .repository import Repository

__all__ = ["IngestService", "RunSummary", "Repository"]


def __getattr__(name: str):
    if name in {"IngestService", "RunSummary"}:
        from .ingest_service import IngestService, RunSummary

        exports = {
            "IngestService": IngestService,
            "RunSummary": RunSummary,
        }
        return exports[name]
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
