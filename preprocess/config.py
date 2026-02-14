from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml


@dataclass(slots=True)
class DatabaseConfig:
    host: str = "127.0.0.1"
    port: int = 6432
    dbname: str = "AscendAny"
    user: str = "AscendAny"
    password_env: str = "ASCENDANY_DB_PASSWORD"
    dsn: str | None = None

    def build_dsn(self) -> str:
        if self.dsn:
            return self.dsn
        password = os.getenv(self.password_env)
        parts = [
            f"host={self.host}",
            f"port={self.port}",
            f"dbname={self.dbname}",
            f"user={self.user}",
        ]
        if password:
            parts.append(f"password={password}")
        return " ".join(parts)


@dataclass(slots=True)
class IngestConfig:
    encodings: list[str] = field(default_factory=lambda: ["utf-8", "utf-8-sig", "gb18030"])
    fingerprint_roles: list[str] = field(
        default_factory=lambda: ["answer_html", "submission_csv", "scoreboard_xlsx"]
    )
    timezone: str = "Asia/Shanghai"


@dataclass(slots=True)
class MetricsConfig:
    winsor_low: float = 0.05
    winsor_high: float = 0.95
    flexibility_mode_default: str = "approx"
    included_problem_kinds: list[str] = field(
        default_factory=lambda: ["函数题", "编程题"]
    )
    random_exam_missing_drawn_set_policy: str = "max_passed_fill_unanswered"
    random_exam_slot_source_priority: list[str] = field(
        default_factory=lambda: ["html_pool_choose_k", "max_passed_count"]
    )


@dataclass(slots=True)
class MappingConfig:
    primary_keys: list[str] = field(default_factory=lambda: ["student_no", "name"])
    actor_sources: list[str] = field(
        default_factory=lambda: [
            "datastructure_nickname",
            "pta_*_account",
        ]
    )
    strict_mode: bool = True


@dataclass(slots=True)
class FusionHalfLife:
    knowledge: float = 45.0
    accuracy: float = 21.0
    quality: float = 45.0
    flexibility: float = 21.0
    proficiency: float = 21.0


@dataclass(slots=True)
class FusionConfig:
    half_life_days: FusionHalfLife = field(default_factory=FusionHalfLife)


@dataclass(slots=True)
class RatingConfig:
    initial_rating: int = 800
    max_binary_search_rating: int = 8000
    min_binary_search_rating: int = -2000
    binary_search_steps: int = 30


@dataclass(slots=True)
class Settings:
    practice_root: Path = Path("/home/kkkzbh/code/Ascend/data/practice")
    db: DatabaseConfig = field(default_factory=DatabaseConfig)
    ingest: IngestConfig = field(default_factory=IngestConfig)
    metrics: MetricsConfig = field(default_factory=MetricsConfig)
    mapping: MappingConfig = field(default_factory=MappingConfig)
    fusion: FusionConfig = field(default_factory=FusionConfig)
    rating: RatingConfig = field(default_factory=RatingConfig)


def _merge_dict(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    result = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = _merge_dict(result[key], value)
        else:
            result[key] = value
    return result


def _as_dict(settings: Settings) -> dict[str, Any]:
    return {
        "practice_root": str(settings.practice_root),
        "db": {
            "host": settings.db.host,
            "port": settings.db.port,
            "dbname": settings.db.dbname,
            "user": settings.db.user,
            "password_env": settings.db.password_env,
            "dsn": settings.db.dsn,
        },
        "ingest": {
            "encodings": settings.ingest.encodings,
            "fingerprint_roles": settings.ingest.fingerprint_roles,
            "timezone": settings.ingest.timezone,
        },
        "metrics": {
            "winsor_low": settings.metrics.winsor_low,
            "winsor_high": settings.metrics.winsor_high,
            "flexibility_mode_default": settings.metrics.flexibility_mode_default,
            "included_problem_kinds": settings.metrics.included_problem_kinds,
            "random_exam_missing_drawn_set_policy": settings.metrics.random_exam_missing_drawn_set_policy,
            "random_exam_slot_source_priority": settings.metrics.random_exam_slot_source_priority,
        },
        "mapping": {
            "primary_keys": settings.mapping.primary_keys,
            "actor_sources": settings.mapping.actor_sources,
            "strict_mode": settings.mapping.strict_mode,
        },
        "fusion": {
            "half_life_days": {
                "knowledge": settings.fusion.half_life_days.knowledge,
                "accuracy": settings.fusion.half_life_days.accuracy,
                "quality": settings.fusion.half_life_days.quality,
                "flexibility": settings.fusion.half_life_days.flexibility,
                "proficiency": settings.fusion.half_life_days.proficiency,
            }
        },
        "rating": {
            "initial_rating": settings.rating.initial_rating,
            "max_binary_search_rating": settings.rating.max_binary_search_rating,
            "min_binary_search_rating": settings.rating.min_binary_search_rating,
            "binary_search_steps": settings.rating.binary_search_steps,
        },
    }


def _from_dict(data: dict[str, Any]) -> Settings:
    db = data.get("db", {})
    ingest = data.get("ingest", {})
    metrics = data.get("metrics", {})
    mapping = data.get("mapping", {})
    fusion = data.get("fusion", {})
    rating = data.get("rating", {})
    half_life = fusion.get("half_life_days", {})

    return Settings(
        practice_root=Path(data.get("practice_root")),
        db=DatabaseConfig(
            host=db.get("host", "127.0.0.1"),
            port=int(db.get("port", 6432)),
            dbname=db.get("dbname", "AscendAny"),
            user=db.get("user", "AscendAny"),
            password_env=db.get("password_env", "ASCENDANY_DB_PASSWORD"),
            dsn=db.get("dsn"),
        ),
        ingest=IngestConfig(
            encodings=list(ingest.get("encodings", ["utf-8", "utf-8-sig", "gb18030"])),
            fingerprint_roles=list(
                ingest.get("fingerprint_roles", ["answer_html", "submission_csv", "scoreboard_xlsx"])
            ),
            timezone=ingest.get("timezone", "Asia/Shanghai"),
        ),
        metrics=MetricsConfig(
            winsor_low=float(metrics.get("winsor_low", 0.05)),
            winsor_high=float(metrics.get("winsor_high", 0.95)),
            flexibility_mode_default=metrics.get("flexibility_mode_default", "approx"),
            included_problem_kinds=list(
                metrics.get("included_problem_kinds", ["函数题", "编程题"])
            ),
            random_exam_missing_drawn_set_policy=metrics.get(
                "random_exam_missing_drawn_set_policy",
                "max_passed_fill_unanswered",
            ),
            random_exam_slot_source_priority=list(
                metrics.get(
                    "random_exam_slot_source_priority",
                    ["html_pool_choose_k", "max_passed_count"],
                )
            ),
        ),
        mapping=MappingConfig(
            primary_keys=list(mapping.get("primary_keys", ["student_no", "name"])),
            actor_sources=list(mapping.get("actor_sources", ["datastructure_nickname", "pta_*_account"])),
            strict_mode=bool(mapping.get("strict_mode", True)),
        ),
        fusion=FusionConfig(
            half_life_days=FusionHalfLife(
                knowledge=float(half_life.get("knowledge", 45.0)),
                accuracy=float(half_life.get("accuracy", 21.0)),
                quality=float(half_life.get("quality", 45.0)),
                flexibility=float(half_life.get("flexibility", 21.0)),
                proficiency=float(half_life.get("proficiency", 21.0)),
            )
        ),
        rating=RatingConfig(
            initial_rating=int(rating.get("initial_rating", 800)),
            max_binary_search_rating=int(rating.get("max_binary_search_rating", 8000)),
            min_binary_search_rating=int(rating.get("min_binary_search_rating", -2000)),
            binary_search_steps=int(rating.get("binary_search_steps", 30)),
        ),
    )


def load_settings(config_path: Path | None = None) -> Settings:
    defaults = _as_dict(Settings())
    if config_path is None:
        config_path = Path("preprocess/config/default.yaml")

    loaded: dict[str, Any] = {}
    if config_path.exists():
        loaded = yaml.safe_load(config_path.read_text(encoding="utf-8")) or {}

    env_override = {}
    if os.getenv("PRACTICE_DATA_ROOT"):
        env_override["practice_root"] = os.getenv("PRACTICE_DATA_ROOT")

    merged = _merge_dict(defaults, loaded)
    merged = _merge_dict(merged, env_override)
    return _from_dict(merged)
