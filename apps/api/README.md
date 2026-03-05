# AscendAny FastAPI backend

## Install

```bash
uv pip install --python .venv/bin/python -r apps/api/requirements-dev.txt
```

## Run

```bash
uv run --python .venv/bin/python uvicorn apps.api.main:app --host 127.0.0.1 --port 8000 --reload
```

## Config

- Default config file: `apps/api/config/default.yaml`
- Optional override via env: `ASCENDANY_API_CONFIG=/path/to/config.yaml`
- Default provider API keys are loaded from environment variables defined by each provider entry.

### External auth provider (MySQL app01_user)

By default, the backend authenticates against PostgreSQL (`ascendany.user_accounts`).

To delegate `username/password` verification and user-related state (refresh tokens, auto-analysis cache)
to a MySQL database managed by a Django project (tables: `app01_user`, `ascendany_user_ext`,
`ascendany_user_refresh_tokens`, `ascendany_user_auto_analysis_cache`), set:

```bash
ASCENDANY_AUTH_PROVIDER=app01_mysql
ASCENDANY_APP01_DB_CONFIG_PATH=/home/xyz/config/db_config.json
```

Alternatively, configure MySQL connection via env:

```bash
ASCENDANY_APP01_DB_HOST=127.0.0.1
ASCENDANY_APP01_DB_PORT=3306
ASCENDANY_APP01_DB_USER=root
ASCENDANY_APP01_DB_PASSWORD=***
ASCENDANY_APP01_DB_NAME=xxx
```

### Server default model for desktop clients

Desktop 端选择“默认（服务器）”时，会使用后端配置，不需要客户端填写 Base URL/模型/API Key。

配置位置：
- 配置文件：`apps/api/config/default.yaml`（或 `ASCENDANY_API_CONFIG` 指向的覆盖文件）
- 默认模型配置（客户端“默认”选项实际使用）：`llm.server_default.mode`、`llm.server_default.base_url`、`llm.server_default.model`、`llm.server_default.api_key_env`
- 供应商列表（客户端手动切换 OpenAI/Anthropic/DeepSeek 时使用）：`llm.providers.*`
- 实际密钥：写在环境变量（变量名由 `api_key_env` 指定，例如 `DEFAULT_API_KEY`）
