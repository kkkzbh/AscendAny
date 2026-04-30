from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

DEFAULT_CONFIG_PATH = Path("apps/api/config/default.yaml")
PROJECT_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_LOCAL_ENV_PATH = PROJECT_ROOT / ".env.local"


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
    adapter: str
    base_url: str
    model: str
    api_key_env: str
    request_mode: str = "chat_completions"


def _default_llm_providers() -> dict[str, LLMProviderConfig]:
    return {
        "siliconflow": LLMProviderConfig(
            adapter="openai_compatible",
            base_url="https://api.siliconflow.cn/v1",
            model="Pro/moonshotai/Kimi-K2.5",
            api_key_env="ASCENDANY_LLM_SILICONFLOW_API_KEY",
        ),
        "openai": LLMProviderConfig(
            adapter="openai_compatible",
            base_url="https://shell.wyzai.top/v1",
            model="openai/gpt-5.4-medium-thinking",
            api_key_env="ASCENDANY_LLM_OPENAI_API_KEY",
        ),
        "copilot": LLMProviderConfig(
            adapter="openai_compatible",
            base_url="http://127.0.0.1:5140/api/internal/copilot/v1",
            model="openai/gpt-5-mini",
            api_key_env="ASCENDANY_LLM_COPILOT_API_KEY",
        ),
        "deepseek": LLMProviderConfig(
            adapter="openai_compatible",
            base_url="https://api.deepseek.com",
            model="deepseek-v4-flash",
            api_key_env="ASCENDANY_LLM_DEEPSEEK_API_KEY",
        ),
        "mimo": LLMProviderConfig(
            adapter="openai_compatible",
            base_url="https://token-plan-cn.xiaomimimo.com/v1",
            model="mimo-v2.5-pro",
            api_key_env="ASCENDANY_LLM_MIMO_API_KEY",
        ),
    }


@dataclass(slots=True)
class LLMConfig:
    active_provider: str = "deepseek"
    providers: dict[str, LLMProviderConfig] = field(default_factory=_default_llm_providers)
    request_timeout_seconds: float = 60.0


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
class SSOConfig:
    enabled: bool = False
    provider: str = "external_app"
    issuer: str = "external_app"
    audience: str = "ascendany_web"
    hs256_secret_env: str = "ASCENDANY_SSO_EXTERNAL_APP_SECRET"
    clock_skew_seconds: int = 15
    max_token_ttl_seconds: int = 60


@dataclass(slots=True)
class Settings:
    db: DatabaseConfig = field(default_factory=DatabaseConfig)
    api: ApiConfig = field(default_factory=ApiConfig)
    dashboard: DashboardConfig = field(default_factory=DashboardConfig)
    llm: LLMConfig = field(default_factory=LLMConfig)
    auth: AuthConfig = field(default_factory=AuthConfig)
    sso: SSOConfig = field(default_factory=SSOConfig)


def _merge_dict(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(merged.get(key), dict):
            merged[key] = _merge_dict(merged[key], value)
            continue
        merged[key] = value
    return merged


def _unquote_env_value(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1]
    return value


def _load_local_env_file(path: Path) -> None:
    if not path.exists() or not path.is_file():
        return
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        if key.startswith("export "):
            key = key.removeprefix("export ").strip()
        if not key or not key.replace("_", "").isalnum() or key[0].isdigit():
            continue
        os.environ.setdefault(key, _unquote_env_value(value))


def _load_local_env() -> None:
    raw_path = os.getenv("ASCENDANY_ADMIN_ENV_FILE", "").strip()
    env_path = Path(raw_path) if raw_path else DEFAULT_LOCAL_ENV_PATH
    if not env_path.is_absolute():
        env_path = (PROJECT_ROOT / env_path).resolve()
    _load_local_env_file(env_path)


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
            "active_provider": settings.llm.active_provider,
            "providers": {
                key: {
                    "adapter": provider.adapter,
                    "base_url": provider.base_url,
                    "model": provider.model,
                    "api_key_env": provider.api_key_env,
                    "request_mode": provider.request_mode,
                }
                for key, provider in settings.llm.providers.items()
            },
            "request_timeout_seconds": settings.llm.request_timeout_seconds,
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
        "sso": {
            "enabled": settings.sso.enabled,
            "provider": settings.sso.provider,
            "issuer": settings.sso.issuer,
            "audience": settings.sso.audience,
            "hs256_secret_env": settings.sso.hs256_secret_env,
            "clock_skew_seconds": settings.sso.clock_skew_seconds,
            "max_token_ttl_seconds": settings.sso.max_token_ttl_seconds,
        },
    }


def _build_llm_provider_configs(llm_data: dict[str, Any]) -> dict[str, LLMProviderConfig]:
    providers = _default_llm_providers()
    loaded = llm_data.get("providers", {}) or {}
    if not isinstance(loaded, dict):
        return providers

    for provider_id, baseline in list(providers.items()):
        raw_provider = loaded.get(provider_id, {}) or {}
        if not isinstance(raw_provider, dict):
            continue
        providers[provider_id] = LLMProviderConfig(
            adapter=str(raw_provider.get("adapter", baseline.adapter)),
            base_url=str(raw_provider.get("base_url", baseline.base_url)),
            model=str(raw_provider.get("model", baseline.model)),
            api_key_env=str(raw_provider.get("api_key_env", baseline.api_key_env)),
            request_mode=str(raw_provider.get("request_mode", baseline.request_mode)),
        )
    return providers


def _from_dict(raw: dict[str, Any]) -> Settings:
    db = raw.get("db", {})
    api = raw.get("api", {})
    dashboard = raw.get("dashboard", {})
    llm = raw.get("llm", {})
    auth = raw.get("auth", {})
    sso = raw.get("sso", {})

    llm_providers = _build_llm_provider_configs(llm)
    active_provider = str(llm.get("active_provider", "deepseek")).strip() or "deepseek"
    if active_provider not in llm_providers:
        active_provider = "deepseek"

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
            active_provider=active_provider,
            providers=llm_providers,
            request_timeout_seconds=float(llm.get("request_timeout_seconds", 60.0)),
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
        sso=SSOConfig(
            enabled=bool(sso.get("enabled", False)),
            provider=str(sso.get("provider", "external_app")).strip()
            or "external_app",
            issuer=str(sso.get("issuer", "external_app")).strip()
            or "external_app",
            audience=str(sso.get("audience", "ascendany_web")).strip()
            or "ascendany_web",
            hs256_secret_env=str(
                sso.get("hs256_secret_env", "ASCENDANY_SSO_EXTERNAL_APP_SECRET")
            ).strip()
            or "ASCENDANY_SSO_EXTERNAL_APP_SECRET",
            clock_skew_seconds=max(0, int(sso.get("clock_skew_seconds", 15))),
            max_token_ttl_seconds=max(1, int(sso.get("max_token_ttl_seconds", 60))),
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
    env_active_provider = os.getenv("ASCENDANY_LLM_ACTIVE_PROVIDER", "").strip()
    if env_active_provider and env_active_provider in settings.llm.providers:
        settings.llm.active_provider = env_active_provider
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
    env_sso_enabled = os.getenv("ASCENDANY_SSO_ENABLED", "").strip().lower()
    if env_sso_enabled in {"0", "false", "no"}:
        settings.sso.enabled = False
    elif env_sso_enabled in {"1", "true", "yes"}:
        settings.sso.enabled = True
    env_sso_provider = os.getenv("ASCENDANY_SSO_PROVIDER", "").strip()
    if env_sso_provider:
        settings.sso.provider = env_sso_provider
    env_sso_issuer = os.getenv("ASCENDANY_SSO_ISSUER", "").strip()
    if env_sso_issuer:
        settings.sso.issuer = env_sso_issuer
    env_sso_audience = os.getenv("ASCENDANY_SSO_AUDIENCE", "").strip()
    if env_sso_audience:
        settings.sso.audience = env_sso_audience
    env_sso_secret_env = os.getenv("ASCENDANY_SSO_HS256_SECRET_ENV", "").strip()
    if env_sso_secret_env:
        settings.sso.hs256_secret_env = env_sso_secret_env
    env_sso_clock_skew = os.getenv("ASCENDANY_SSO_CLOCK_SKEW_SECONDS", "").strip()
    if env_sso_clock_skew:
        settings.sso.clock_skew_seconds = max(0, int(env_sso_clock_skew))
    env_sso_max_ttl = os.getenv("ASCENDANY_SSO_MAX_TOKEN_TTL_SECONDS", "").strip()
    if env_sso_max_ttl:
        settings.sso.max_token_ttl_seconds = max(1, int(env_sso_max_ttl))


def load_settings(config_path: Path | None = None) -> Settings:
    _load_local_env()

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
