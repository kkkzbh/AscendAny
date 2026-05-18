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
- The active model API key is loaded from the environment variable named by `llm.providers.<id>.api_key_env`.

### Feedback email

Desktop feedback is submitted to `POST /api/v1/feedback`; the server sends the email so SMTP credentials never ship in the desktop installer.

```bash
EMAIL_HOST=smtp.qq.com
EMAIL_PORT=465
EMAIL_USER=uika@foxmail.com
EMAIL_PASS=QQ邮箱授权码
EMAIL_FROM=uika@foxmail.com
ASCENDANY_FEEDBACK_TO=uika@foxmail.com
```

`EMAIL_PASS` must be the QQ/Foxmail SMTP authorization code, not the mailbox login password.

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

Desktop 端统一使用后端配置，不提供客户端自定义 Base URL/模型/API Key。

配置位置：
- 配置文件：`apps/api/config/default.yaml`（或 `ASCENDANY_API_CONFIG` 指向的覆盖文件）
- 当前 Provider：`llm.active_provider`
- Provider 配置：`llm.providers.<id>.adapter`、`base_url`、`model`、`api_key_env`、`request_mode`
- 实际密钥：写在环境变量（变量名由 `api_key_env` 指定，例如 `DEFAULT_API_KEY`）
