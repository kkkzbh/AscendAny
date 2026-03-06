from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

DEFAULT_CONFIG_PATH = Path("apps/api/config/default.yaml")


@dataclass(slots=True)
class DatabaseConfig:
    host: str = "127.0.0.1"
    port: int = 6432
    dbname: str = "AscendAny"
    user: str = "AscendAny"
    password_env: str = "ASCENDANY_DB_PASSWORD"
    dsn: str | None = None
    pool_min_size: int = 2
    pool_max_size: int = 32
    pool_timeout_seconds: float = 10.0

    def build_dsn(self) -> str:
        if self.dsn:
            return self.dsn
        password = os.getenv(self.password_env, "").strip()
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
class ApiConfig:
    cors_origins: list[str] = field(default_factory=lambda: ["*"])


@dataclass(slots=True)
class DashboardConfig:
    default_rating: int = 800
    default_metric: float = 0.0
    rating_history_limit: int = 50


@dataclass(slots=True)
class LLMProviderConfig:
    label: str
    mode: str
    base_url: str
    model: str
    api_key_env: str
    enabled: bool = True


@dataclass(slots=True)
class LLMServerDefaultConfig:
    mode: str = "openai_compatible"
    base_url: str = "https://api.deepseek.com"
    model: str = "deepseek-chat"
    api_key_env: str = "DEFAULT_API_KEY"


def _default_providers() -> dict[str, LLMProviderConfig]:
    return {
        "openai": LLMProviderConfig(
            label="OpenAI",
            mode="openai_compatible",
            base_url="https://api.openai.com/v1",
            model="gpt-4o-mini",
            api_key_env="OPENAI_API_KEY",
            enabled=True,
        ),
        "anthropic": LLMProviderConfig(
            label="Anthropic",
            mode="anthropic",
            base_url="https://api.anthropic.com",
            model="claude-sonnet-4-20250514",
            api_key_env="ANTHROPIC_API_KEY",
            enabled=False,
        ),
        "deepseek": LLMProviderConfig(
            label="DeepSeek",
            mode="openai_compatible",
            base_url="https://api.deepseek.com/v1",
            model="deepseek-chat",
            api_key_env="DEEPSEEK_API_KEY",
            enabled=False,
        ),
        "gemini": LLMProviderConfig(
            label="Gemini",
            mode="gemini",
            base_url="https://generativelanguage.googleapis.com/v1beta",
            model="gemini-2.0-flash",
            api_key_env="GEMINI_API_KEY",
            enabled=False,
        ),
    }


@dataclass(slots=True)
class LLMConfig:
    server_default: LLMServerDefaultConfig = field(
        default_factory=LLMServerDefaultConfig
    )
    request_timeout_seconds: float = 60.0
    providers: dict[str, LLMProviderConfig] = field(default_factory=_default_providers)


@dataclass(slots=True)
class AuthConfig:
    enabled: bool = True
    provider: str = "internal"
    signup_policy: str = "username_password_only"
    jwt_secret_env: str = "ASCENDANY_AUTH_JWT_SECRET"
    jwt_secret: str = "ascendany-dev-insecure-secret"
    access_ttl_minutes: int = 15
    refresh_ttl_days: int = 30
    password_pepper_env: str = "ASCENDANY_AUTH_PASSWORD_PEPPER"
    allow_stored_password_direct_login: bool = False

    # External auth provider (MySQL app01_user) integration.
    # When provider != "internal", `username`/`password` verification is delegated.
    app01_db_config_path: str | None = None
    app01_db_config_path_env: str = "ASCENDANY_APP01_DB_CONFIG_PATH"


@dataclass(slots=True)
class Settings:
    db: DatabaseConfig = field(default_factory=DatabaseConfig)
    api: ApiConfig = field(default_factory=ApiConfig)
    dashboard: DashboardConfig = field(default_factory=DashboardConfig)
    llm: LLMConfig = field(default_factory=LLMConfig)
    auth: AuthConfig = field(default_factory=AuthConfig)


def _merge_dict(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(merged.get(key), dict):
            merged[key] = _merge_dict(merged[key], value)
            continue
        merged[key] = value
    return merged


def _providers_to_dict(
    providers: dict[str, LLMProviderConfig],
) -> dict[str, dict[str, Any]]:
    return {
        key: {
            "label": value.label,
            "mode": value.mode,
            "base_url": value.base_url,
            "model": value.model,
            "api_key_env": value.api_key_env,
            "enabled": value.enabled,
        }
        for key, value in providers.items()
    }


def _as_dict(settings: Settings) -> dict[str, Any]:
    return {
        "db": {
            "host": settings.db.host,
            "port": settings.db.port,
            "dbname": settings.db.dbname,
            "user": settings.db.user,
            "password_env": settings.db.password_env,
            "dsn": settings.db.dsn,
            "pool_min_size": settings.db.pool_min_size,
            "pool_max_size": settings.db.pool_max_size,
            "pool_timeout_seconds": settings.db.pool_timeout_seconds,
        },
        "api": {
            "cors_origins": list(settings.api.cors_origins),
        },
        "dashboard": {
            "default_rating": settings.dashboard.default_rating,
            "default_metric": settings.dashboard.default_metric,
            "rating_history_limit": settings.dashboard.rating_history_limit,
        },
        "llm": {
            "server_default": {
                "mode": settings.llm.server_default.mode,
                "base_url": settings.llm.server_default.base_url,
                "model": settings.llm.server_default.model,
                "api_key_env": settings.llm.server_default.api_key_env,
            },
            "request_timeout_seconds": settings.llm.request_timeout_seconds,
            "providers": _providers_to_dict(settings.llm.providers),
        },
        "auth": {
            "enabled": settings.auth.enabled,
            "provider": settings.auth.provider,
            "signup_policy": settings.auth.signup_policy,
            "jwt_secret_env": settings.auth.jwt_secret_env,
            "jwt_secret": settings.auth.jwt_secret,
            "access_ttl_minutes": settings.auth.access_ttl_minutes,
            "refresh_ttl_days": settings.auth.refresh_ttl_days,
            "password_pepper_env": settings.auth.password_pepper_env,
            "allow_stored_password_direct_login": settings.auth.allow_stored_password_direct_login,
            "app01_db_config_path": settings.auth.app01_db_config_path,
            "app01_db_config_path_env": settings.auth.app01_db_config_path_env,
        },
    }


def _build_provider_map(provider_data: dict[str, Any]) -> dict[str, LLMProviderConfig]:
    defaults = _default_providers()
    ordered_keys = [
        *defaults.keys(),
        *[item for item in provider_data.keys() if item not in defaults],
    ]
    providers: dict[str, LLMProviderConfig] = {}
    for key in ordered_keys:
        loaded = provider_data.get(key, {}) or {}
        baseline = defaults.get(
            key,
            LLMProviderConfig(
                label=key,
                mode="openai_compatible",
                base_url="",
                model="",
                api_key_env="",
                enabled=False,
            ),
        )
        providers[key] = LLMProviderConfig(
            label=str(loaded.get("label", baseline.label)),
            mode=str(loaded.get("mode", baseline.mode)),
            base_url=str(loaded.get("base_url", baseline.base_url)),
            model=str(loaded.get("model", baseline.model)),
            api_key_env=str(loaded.get("api_key_env", baseline.api_key_env)),
            enabled=bool(loaded.get("enabled", baseline.enabled)),
        )
    return providers


def _build_server_default_config(
    llm_data: dict[str, Any],
    providers: dict[str, LLMProviderConfig],
) -> LLMServerDefaultConfig:
    baseline = LLMServerDefaultConfig()
    loaded = llm_data.get("server_default", {}) or {}
    if isinstance(loaded, dict) and loaded:
        return LLMServerDefaultConfig(
            mode=str(loaded.get("mode", baseline.mode)),
            base_url=str(loaded.get("base_url", baseline.base_url)),
            model=str(loaded.get("model", baseline.model)),
            api_key_env=str(loaded.get("api_key_env", baseline.api_key_env)),
        )

    # Legacy compatibility: if old default_provider exists, project it into server_default.
    legacy_provider_key = str(llm_data.get("default_provider", "")).strip()
    if legacy_provider_key:
        legacy = providers.get(legacy_provider_key)
        if legacy is not None:
            return LLMServerDefaultConfig(
                mode=legacy.mode,
                base_url=legacy.base_url,
                model=legacy.model,
                api_key_env=legacy.api_key_env,
            )

    return baseline


def _from_dict(raw: dict[str, Any]) -> Settings:
    db = raw.get("db", {})
    api = raw.get("api", {})
    dashboard = raw.get("dashboard", {})
    llm = raw.get("llm", {})
    auth = raw.get("auth", {})

    providers = _build_provider_map(llm.get("providers", {}) or {})
    server_default = _build_server_default_config(llm, providers)

    return Settings(
        db=DatabaseConfig(
            host=str(db.get("host", "127.0.0.1")),
            port=int(db.get("port", 6432)),
            dbname=str(db.get("dbname", "AscendAny")),
            user=str(db.get("user", "AscendAny")),
            password_env=str(db.get("password_env", "ASCENDANY_DB_PASSWORD")),
            dsn=db.get("dsn"),
            pool_min_size=max(1, int(db.get("pool_min_size", 2))),
            pool_max_size=max(1, int(db.get("pool_max_size", 32))),
            pool_timeout_seconds=float(db.get("pool_timeout_seconds", 10.0)),
        ),
        api=ApiConfig(
            cors_origins=[str(item) for item in api.get("cors_origins", ["*"])],
        ),
        dashboard=DashboardConfig(
            default_rating=int(dashboard.get("default_rating", 800)),
            default_metric=float(dashboard.get("default_metric", 0.0)),
            rating_history_limit=max(1, int(dashboard.get("rating_history_limit", 50))),
        ),
        llm=LLMConfig(
            server_default=server_default,
            request_timeout_seconds=float(llm.get("request_timeout_seconds", 60.0)),
            providers=providers,
        ),
        auth=AuthConfig(
            enabled=bool(auth.get("enabled", True)),
            provider=str(auth.get("provider", "internal")).strip() or "internal",
            signup_policy=str(
                auth.get("signup_policy", "username_password_only")
            ).strip(),
            jwt_secret_env=str(
                auth.get("jwt_secret_env", "ASCENDANY_AUTH_JWT_SECRET")
            ).strip(),
            jwt_secret=str(
                auth.get("jwt_secret", "ascendany-dev-insecure-secret")
            ).strip(),
            access_ttl_minutes=max(1, int(auth.get("access_ttl_minutes", 15))),
            refresh_ttl_days=max(1, int(auth.get("refresh_ttl_days", 30))),
            password_pepper_env=str(
                auth.get("password_pepper_env", "ASCENDANY_AUTH_PASSWORD_PEPPER")
            ).strip(),
            allow_stored_password_direct_login=bool(
                auth.get("allow_stored_password_direct_login", False)
            ),
            app01_db_config_path=(
                str(auth.get("app01_db_config_path")).strip()
                if auth.get("app01_db_config_path")
                else None
            ),
            app01_db_config_path_env=str(
                auth.get("app01_db_config_path_env", "ASCENDANY_APP01_DB_CONFIG_PATH")
            ).strip(),
        ),
    )


def _apply_env_overrides(settings: Settings) -> None:
    env_dsn = os.getenv("ASCENDANY_DB_DSN", "").strip()
    if env_dsn:
        settings.db.dsn = env_dsn
    env_host = os.getenv("ASCENDANY_DB_HOST", "").strip()
    if env_host:
        settings.db.host = env_host
    env_port = os.getenv("ASCENDANY_DB_PORT", "").strip()
    if env_port:
        settings.db.port = int(env_port)
    env_name = os.getenv("ASCENDANY_DB_NAME", "").strip()
    if env_name:
        settings.db.dbname = env_name
    env_user = os.getenv("ASCENDANY_DB_USER", "").strip()
    if env_user:
        settings.db.user = env_user
    env_server_default_mode = os.getenv("ASCENDANY_LLM_SERVER_DEFAULT_MODE", "").strip()
    if env_server_default_mode:
        settings.llm.server_default.mode = env_server_default_mode
    env_server_default_base_url = os.getenv(
        "ASCENDANY_LLM_SERVER_DEFAULT_BASE_URL", ""
    ).strip()
    if env_server_default_base_url:
        settings.llm.server_default.base_url = env_server_default_base_url
    env_server_default_model = os.getenv(
        "ASCENDANY_LLM_SERVER_DEFAULT_MODEL", ""
    ).strip()
    if env_server_default_model:
        settings.llm.server_default.model = env_server_default_model
    env_server_default_api_key_env = os.getenv(
        "ASCENDANY_LLM_SERVER_DEFAULT_API_KEY_ENV", ""
    ).strip()
    if env_server_default_api_key_env:
        settings.llm.server_default.api_key_env = env_server_default_api_key_env
    env_default_rating = os.getenv("ASCENDANY_DASHBOARD_DEFAULT_RATING", "").strip()
    if env_default_rating:
        settings.dashboard.default_rating = int(env_default_rating)
    env_cors = os.getenv("ASCENDANY_API_CORS_ORIGINS", "").strip()
    if env_cors:
        settings.api.cors_origins = [
            item.strip() for item in env_cors.split(",") if item.strip()
        ]
    env_auth_enabled = os.getenv("ASCENDANY_AUTH_ENABLED", "").strip().lower()
    if env_auth_enabled in {"0", "false", "no"}:
        settings.auth.enabled = False
    elif env_auth_enabled in {"1", "true", "yes"}:
        settings.auth.enabled = True
    env_signup_policy = os.getenv("ASCENDANY_AUTH_SIGNUP_POLICY", "").strip()
    if env_signup_policy:
        settings.auth.signup_policy = env_signup_policy
    env_provider = os.getenv("ASCENDANY_AUTH_PROVIDER", "").strip()
    if env_provider:
        settings.auth.provider = env_provider
    env_jwt_secret_env = os.getenv("ASCENDANY_AUTH_JWT_SECRET_ENV", "").strip()
    if env_jwt_secret_env:
        settings.auth.jwt_secret_env = env_jwt_secret_env
    env_access_ttl = os.getenv("ASCENDANY_AUTH_ACCESS_TTL_MINUTES", "").strip()
    if env_access_ttl:
        settings.auth.access_ttl_minutes = max(1, int(env_access_ttl))
    env_refresh_ttl = os.getenv("ASCENDANY_AUTH_REFRESH_TTL_DAYS", "").strip()
    if env_refresh_ttl:
        settings.auth.refresh_ttl_days = max(1, int(env_refresh_ttl))
    env_password_pepper_env = os.getenv(
        "ASCENDANY_AUTH_PASSWORD_PEPPER_ENV", ""
    ).strip()
    if env_password_pepper_env:
        settings.auth.password_pepper_env = env_password_pepper_env
    env_allow_stored_password_direct_login = os.getenv(
        "ASCENDANY_AUTH_ALLOW_STORED_PASSWORD_DIRECT_LOGIN", ""
    ).strip().lower()
    if env_allow_stored_password_direct_login in {"1", "true", "yes"}:
        settings.auth.allow_stored_password_direct_login = True
    elif env_allow_stored_password_direct_login in {"0", "false", "no"}:
        settings.auth.allow_stored_password_direct_login = False

    env_app01_cfg = os.getenv(settings.auth.app01_db_config_path_env, "").strip()
    if env_app01_cfg:
        settings.auth.app01_db_config_path = env_app01_cfg


def load_settings(config_path: Path | None = None) -> Settings:
    path = config_path
    if path is None:
        raw_path = os.getenv("ASCENDANY_API_CONFIG", "").strip()
        path = Path(raw_path) if raw_path else DEFAULT_CONFIG_PATH

    merged = _as_dict(Settings())
    if path.exists():
        loaded = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        merged = _merge_dict(merged, loaded)

    settings = _from_dict(merged)
    _apply_env_overrides(settings)
    return settings
