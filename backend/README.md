# AscendAny Go backend

This directory contains the Go 1.26 backend foundation. `ascendanyd` exposes:

- `GET /livez`: process liveness.
- `GET /readyz`: PostgreSQL connectivity and exact migration-version readiness.
- `GET /version`: binary, commit, build-time, and Go version metadata.

## Required configuration

The process fails at startup unless all required values are explicitly configured. Secrets are read from credential files and are never accepted in an environment variable or database URL:

```sh
export ASCENDANY_DATABASE_URL='postgresql://ascendanyd_login@127.0.0.1:6432/ascendany_v2?sslmode=require'
export ASCENDANY_DATABASE_POOL_MODE='transaction'
export ASCENDANY_DATABASE_PASSWORD_FILE='/run/credentials/ascendany/db_password'
export ASCENDANY_DATABASE_SCHEMA_VERSION='10'
export ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE='/run/credentials/ascendany/jwt_signing_private_key'
export ASCENDANY_PASSWORD_PEPPER_FILE='/run/credentials/ascendany/password_pepper'
export ASCENDANY_AUTH_ISSUER='ascendany'
export ASCENDANY_AUTH_AUDIENCE='ascendany-v2'
export ASCENDANY_AUTH_ALLOWED_ORIGINS='https://ascendany.kkkzbh.cn,ascendany-app://bundle,https://localhost,capacitor://localhost,http://127.0.0.1:5173,http://localhost:5173'
export ASCENDANY_AUTH_ACCESS_TTL='15m'
export ASCENDANY_AUTH_REFRESH_TTL='720h'
export ASCENDANY_HTTP_LISTEN='127.0.0.1:18000'
export ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS='127.0.0.1/32'
export ASCENDANY_HTTP_CLIENT_IP_HEADER='CF-Connecting-IP'
export ASCENDANY_HTTP_READ_HEADER_TIMEOUT='5s'
export ASCENDANY_HTTP_READ_TIMEOUT='10m30s'
export ASCENDANY_HTTP_AUTH_BODY_TIMEOUT='15s'
export ASCENDANY_HTTP_UPLOAD_BODY_TIMEOUT='10m'
export ASCENDANY_HTTP_SSE_MAX_DURATION='15m'
export ASCENDANY_HTTP_SSE_REAUTH_INTERVAL='15s'
export ASCENDANY_HTTP_SSE_WRITE_TIMEOUT='5s'
export ASCENDANY_HTTP_MAX_ACTIVE_SSE='64'
export ASCENDANY_HTTP_IDLE_TIMEOUT='1m'
export ASCENDANY_HTTP_SHUTDOWN_TIMEOUT='10s'
export ASCENDANY_ARTIFACT_ROOT='/var/lib/ascendany/artifacts'
export ASCENDANY_ARTIFACT_MAX_BYTES='134217728'
export ASCENDANY_ARTIFACT_ORPHAN_MIN_AGE='24h'
export ASCENDANY_ARTIFACT_RECONCILE_INTERVAL='1h'
export ASCENDANY_PINTIA_MAX_TOTAL_NODES='2000000'
export ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES='33554432'
export ASCENDANY_PINTIA_MAX_JSON_DEPTH='32'
export ASCENDANY_PINTIA_MAX_STRING_BYTES='8388608'
export ASCENDANY_PINTIA_MAX_PROBLEMS='1000'
export ASCENDANY_PINTIA_MAX_PARTICIPANTS='20000'
export ASCENDANY_PINTIA_MAX_PROBLEM_RESULTS_PER_RANKING='1000'
export ASCENDANY_PINTIA_MAX_SUBMISSIONS='20000'
export ASCENDANY_PINTIA_MAX_CASE_RESULTS_PER_SUBMISSION='1000'
export ASCENDANY_PINTIA_MAX_CODE_BYTES='1048576'
export ASCENDANY_IMPORT_WORKER_OWNER='km6-import'
export ASCENDANY_IMPORT_LEASE_DURATION='5m'
export ASCENDANY_IMPORT_RETRY_DELAY='30s'
export ASCENDANY_IMPORT_POLL_INTERVAL='1s'
export ASCENDANY_ANALYTICS_CONFIG='/etc/ascendany/v2/analytics.json'
export ASCENDANY_ANALYTICS_WORKER_OWNER='km6-analytics'
export ASCENDANY_ANALYTICS_LEASE_DURATION='5m'
export ASCENDANY_ANALYTICS_POLL_INTERVAL='1s'
export ASCENDANY_RECOMMENDATION_MODEL_PATH='/absolute/path/to/recommendation-model.json'
export ASCENDANY_RECOMMENDATION_MODEL_SHA256='64_lowercase_hex'
export ASCENDANY_RECOMMENDATION_MODEL_PURPOSE='production'
export ASCENDANY_KNOWLEDGE_CATALOG_PATH='/absolute/path/to/recommendation-knowledge-catalog.json'
export ASCENDANY_KNOWLEDGE_CATALOG_SHA256='64_lowercase_hex'
export ASCENDANY_FEEDBACK_RATE_WINDOW='1h'
export ASCENDANY_FEEDBACK_RATE_MAXIMUM='5'
export ASCENDANY_FEEDBACK_DELIVERY_CONFIGURATION_KEY='feedback.delivery.default'
export ASCENDANY_FEEDBACK_WORKER_OWNER='km6-feedback'
export ASCENDANY_FEEDBACK_LEASE_DURATION='5m'
export ASCENDANY_FEEDBACK_RETRY_DELAY='30s'
export ASCENDANY_FEEDBACK_POLL_INTERVAL='1s'
export ASCENDANY_WRITE_MODE='enabled'
```

The production contract listens on the dedicated v2 loopback port `18000` and
enables the complete writer runtime. Deployment rehearsal uses the reviewed
systemd smoke drop-in to load a final one-line environment file containing
`ASCENDANY_WRITE_MODE=disabled`; that mode leaves read APIs available, rejects
every mutation, and does not construct
background writers or LSP/OJ managers. The verified recommendation model remains
loaded so read-only recommendation behavior and release identity are unchanged.

The recommendation model path must be absolute and canonical. The file must be
a single-link regular file with mode `0644`, at most 16 MiB, and match the
configured lowercase SHA-256. Startup verifies the closed inference artifact,
its internal parameter and golden-vector digests, the fixed online feature
schema, and all golden-vector results before binding its immutable model release
to PostgreSQL. Production contains no model-training process or Python runtime.

Knowledge-catalog publication begins in `ascendanyd`. Its administrator route
validates the complete catalog/release intent and creates one short-lived,
single-use authorization bound to the canonical publication request and the
creating access token's expiry. The stopped-runtime
`ascendany-catalog-publish` binary receives that exact request and token,
verifies the token with the public JWT key, and consumes the authorization only
through
`ascendany.publish_authorized_knowledge_catalog(uuid, text, text)`. Its database
role has no direct catalog DML. A successful publication emits the strict
26-field `ascendany.knowledge_catalog.publication-receipt.v1`, including
`authorizationId`.

The database URL targets PgBouncer and must not contain a password. The database credential file must contain at least 16 bytes. The online JWT credential is one canonical PKCS#8 Ed25519 private-key PEM; the stopped publisher receives only the matching canonical PKIX public-key PEM. Access JWTs use EdDSA exclusively. The password pepper is a separate credential file of at least 32 bytes, without surrounding whitespace. Feedback webhook and model-provider bearer credentials also come from credential files: the environment contains only the path, and its variable name binds one `credentialRef` to one canonical HTTPS authority. Use `credential.FileEnvironmentVariable(reference, authority)` to derive that name. Browser origins use an exact comma-separated allowlist: canonical HTTPS, canonical loopback HTTP for development, `ascendany-app://bundle`, and `capacitor://localhost`; wildcards and padded entries are rejected. The access lifetime must be shorter than the refresh lifetime. The global HTTP read ceiling must exceed both route body deadlines; the SSE write timeout must not exceed its periodic reauthorization interval. The pool uses pgx `QueryExecModeExec` with statement and description caches disabled, so it does not rely on session-scoped prepared statements in PgBouncer transaction-pooling mode.

Readiness compares every row in `ascendany.schema_migrations_v2` with the exact version, name, and SHA-256 digest compiled into the binary. `/readyz` returns HTTP 503 when PostgreSQL cannot be pinged or the migration history has a missing, extra, renamed, or digest-drifted entry. Startup also rejects an `ASCENDANY_DATABASE_SCHEMA_VERSION` value that differs from the embedded manifest.

Optional settings are environment variables prefixed with `ASCENDANY_HTTP_`, `ASCENDANY_DATABASE_`, and `ASCENDANY_LOG_LEVEL`. See `internal/config/config.go` for names and defaults.

## Build and verify

```sh
go build -ldflags "-X github.com/kkkzbh/AscendAny/backend/internal/version.Version=0.1.0 -X github.com/kkkzbh/AscendAny/backend/internal/version.Commit=$(git rev-parse HEAD) -X github.com/kkkzbh/AscendAny/backend/internal/version.BuildTime=$(date -u +%FT%TZ)" ./cmd/ascendanyd
./ascendanyd serve
go run ./cmd/ascendany-migrate up
go test ./...
go vet ./...
```
