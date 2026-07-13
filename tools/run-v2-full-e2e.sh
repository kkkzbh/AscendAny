#!/usr/bin/bash -p
if [[ "${BASH:-}" != "/usr/bin/bash" || "$-" != *p* || "$-" == *[cis]* ||
      -n "${BASH_EXECUTION_STRING:-}" || "${#BASH_SOURCE[@]}" -ne 1 ||
      "${BASH_SOURCE[0]}" != "$0" ]]; then
  /usr/bin/printf '%s\n' 'full E2E must run directly under /usr/bin/bash -p' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi
set +x
builtin unset BASH_ENV ENV CDPATH GLOBIGNORE
builtin export -n SHELLOPTS BASHOPTS
set -Eeuo pipefail

export LC_ALL=C
umask 077

[[ -n "${PATH:-}" ]] || {
  /usr/bin/printf '%s\n' 'caller PATH is required to locate the reviewed Node and pnpm tools' >&2
  exit 2
}
export PATH
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly REPOSITORY_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd -P)"
readonly RELEASE_BUILDER="${REPOSITORY_ROOT}/tools/build-v2-release.sh"
readonly INSTALLER_SOURCE="${REPOSITORY_ROOT}/deploy/v2/scripts/install-v2-release.sh"
readonly EXPORTER_SOURCE_FIXTURE="${REPOSITORY_ROOT}/tools/pintia-exporter-extension/tests/fixtures/sanitized-source-shape.json"
readonly FEDORA_PACKAGE_LOCK="${REPOSITORY_ROOT}/deploy/v2/config/fedora-runtime-packages.json"
readonly RESET_CONFIRMATION='run-disposable-ascendany-v2-full-e2e'
readonly POSTGRES_IMAGE='docker.io/library/postgres@sha256:030da09481c3876b71a7e49738a932e1c18c398201a1e4ccfdbff1e5a541215b'
readonly LABEL_KEY='io.ascendany.v2-full-e2e'
readonly SOURCE_DATABASE='ascendany_v2'
readonly SCRATCH_DATABASE='ascendany_v2_restore_verify'
readonly SCHEMA_OWNER='ascendany_owner'
readonly RUNTIME_LOGIN='ascendanyd_login'
readonly MIGRATOR_LOGIN='ascendany_migrator_login'
readonly BACKUP_LOGIN='ascendany_backup_login'
readonly RESTORE_LOGIN='ascendany_restore_login'
readonly RELEASE_PURPOSE='acceptance_test'
readonly EXPECTED_ARTIFACT_COUNT=3

usage() {
  /usr/bin/printf '%s\n' \
    "Usage: $0 --confirm-reset ${RESET_CONFIRMATION} --version SEMVER --commit 40_HEX --go-path /canonical/go --recommendation-model /protected/model.json --recommendation-model-sha256 64_HEX --knowledge-catalog /protected/catalog.json --knowledge-catalog-sha256 64_HEX" \
    '' \
    'The command requires a clean checkout at --commit and preloaded, digest-pinned' \
    'PostgreSQL 17 image and the release-locked native PgBouncer. It destroys only its random labeled pod,' \
    'volume, network, credentials, installation namespace, and temporary work tree.'
}

fail() {
  /usr/bin/printf '%s\n' "$1" >&2
  exit 2
}

validate_protected_file_ancestry() {
  local label="$1"
  local file="$2"
  local current=/
  local component owner mode_text mode
  local -a components=()

  IFS=/ read -r -a components <<<"$(dirname -- "${file}")"
  for component in '' "${components[@]}"; do
    if [[ -n "${component}" ]]; then
      current="${current%/}/${component}"
    fi
    [[ -d "${current}" && ! -L "${current}" ]] ||
      fail "${label} has a missing, non-directory, or symlink ancestor: ${current}"
    owner="$(stat -Lc '%u' -- "${current}")"
    mode_text="$(stat -Lc '%a' -- "${current}")"
    mode="$((8#${mode_text}))"
    [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
      fail "${label} has an ancestor outside the root/E2E-user ownership boundary: ${current}"
    (( (mode & 8#022) == 0 || (owner == 0 && (mode & 8#1000) != 0) )) ||
      fail "${label} has an unprotected writable ancestor: ${current}"
  done
}

validate_protected_json_file() {
  local label="$1"
  local file="$2"
  local maximum_size="$3"
  local metadata owner mode_text size links mode

  [[ "${file}" == /* && ! "${file}" =~ [[:cntrl:]] && -f "${file}" && ! -L "${file}" && -r "${file}" ]] ||
    fail "${label} must identify one readable absolute regular non-symlink file"
  [[ "$(realpath -e -- "${file}")" == "${file}" ]] ||
    fail "${label} must already be canonical and contain no symlink ancestry"
  metadata="$(stat -Lc '%u:%a:%s:%h' -- "${file}")"
  IFS=: read -r owner mode_text size links <<<"${metadata}"
  mode="$((8#${mode_text}))"
  [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
    fail "${label} must be owned by root or the E2E user"
  (( (mode & 8#022) == 0 )) || fail "${label} must not be group- or other-writable"
  [[ "${links}" == 1 && "${size}" =~ ^[1-9][0-9]*$ && "${size}" -le "${maximum_size}" ]] ||
    fail "${label} must be one single-link bounded nonempty file"
  validate_protected_file_ancestry "${label}" "${file}"
}

CONFIRMATION=''
VERSION=''
REQUESTED_COMMIT=''
GO_BINARY=''
RECOMMENDATION_MODEL=''
RECOMMENDATION_MODEL_SHA256=''
KNOWLEDGE_CATALOG=''
KNOWLEDGE_CATALOG_SHA256=''
while (($# > 0)); do
  case "$1" in
    --confirm-reset)
      (($# >= 2)) || fail '--confirm-reset requires a value'
      CONFIRMATION="$2"
      shift 2
      ;;
    --version)
      (($# >= 2)) || fail '--version requires a value'
      VERSION="$2"
      shift 2
      ;;
    --commit)
      (($# >= 2)) || fail '--commit requires a value'
      REQUESTED_COMMIT="$2"
      shift 2
      ;;
    --go-path)
      (($# >= 2)) || fail '--go-path requires a value'
      GO_BINARY="$2"
      shift 2
      ;;
    --recommendation-model)
      (($# >= 2)) || fail '--recommendation-model requires a value'
      [[ -z "${RECOMMENDATION_MODEL}" ]] || fail '--recommendation-model may be specified only once'
      RECOMMENDATION_MODEL="$2"
      shift 2
      ;;
    --recommendation-model-sha256)
      (($# >= 2)) || fail '--recommendation-model-sha256 requires a value'
      [[ -z "${RECOMMENDATION_MODEL_SHA256}" ]] || fail '--recommendation-model-sha256 may be specified only once'
      RECOMMENDATION_MODEL_SHA256="$2"
      shift 2
      ;;
    --knowledge-catalog)
      (($# >= 2)) || fail '--knowledge-catalog requires a value'
      [[ -z "${KNOWLEDGE_CATALOG}" ]] || fail '--knowledge-catalog may be specified only once'
      KNOWLEDGE_CATALOG="$2"
      shift 2
      ;;
    --knowledge-catalog-sha256)
      (($# >= 2)) || fail '--knowledge-catalog-sha256 requires a value'
      [[ -z "${KNOWLEDGE_CATALOG_SHA256}" ]] || fail '--knowledge-catalog-sha256 may be specified only once'
      KNOWLEDGE_CATALOG_SHA256="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[[ "${CONFIRMATION}" == "${RESET_CONFIRMATION}" ]] ||
  fail "--confirm-reset must equal ${RESET_CONFIRMATION}"
[[ "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] ||
  fail '--version must be one canonical SemVer value'
[[ "${REQUESTED_COMMIT}" =~ ^[0-9a-f]{40}$ ]] ||
  fail '--commit must be exactly 40 lowercase hexadecimal characters'
((EUID != 0)) || fail 'full E2E must run as a rootless user'
[[ -n "${HOME:-}" && "${HOME}" == /* && -d "${HOME}" && ! -L "${HOME}" &&
   "$(/usr/bin/realpath -e -- "${HOME}")" == "${HOME}" &&
   "$(/usr/bin/stat -Lc '%u' -- "${HOME}")" == "${EUID}" ]] ||
  fail 'HOME must be one canonical real directory owned by the E2E user'
[[ "${GO_BINARY}" == /* && -x "${GO_BINARY}" && ! -L "${GO_BINARY}" ]] ||
  fail '--go-path must identify an absolute executable regular file'
[[ "$(/usr/bin/realpath -e -- "${GO_BINARY}")" == "${GO_BINARY}" ]] ||
  fail '--go-path must already be canonical and contain no symlink ancestry'
validate_protected_json_file '--recommendation-model' "${RECOMMENDATION_MODEL}" 16777216
[[ "${RECOMMENDATION_MODEL_SHA256}" =~ ^[0-9a-f]{64}$ ]] ||
  fail '--recommendation-model-sha256 must be 64 lowercase hexadecimal characters'
[[ "$(sha256sum -- "${RECOMMENDATION_MODEL}" | awk '{print $1}')" == "${RECOMMENDATION_MODEL_SHA256}" ]] ||
  fail 'recommendation model bytes differ from --recommendation-model-sha256'
validate_protected_json_file '--knowledge-catalog' "${KNOWLEDGE_CATALOG}" 262144
[[ "${KNOWLEDGE_CATALOG_SHA256}" =~ ^[0-9a-f]{64}$ ]] ||
  fail '--knowledge-catalog-sha256 must be 64 lowercase hexadecimal characters'
[[ "$(sha256sum -- "${KNOWLEDGE_CATALOG}" | awk '{print $1}')" == "${KNOWLEDGE_CATALOG_SHA256}" ]] ||
  fail 'knowledge catalog bytes differ from --knowledge-catalog-sha256'
jq -e 'type == "object"' "${KNOWLEDGE_CATALOG}" >/dev/null ||
  fail 'knowledge catalog bytes must contain one JSON object'
jq -e --arg catalog_sha256 "${KNOWLEDGE_CATALOG_SHA256}" '
  .schema == "ascendany.recommendation.inference-model.v1" and
  .manifest.purpose == "acceptance_test" and
  .manifest.knowledgeCatalogSha256 == $catalog_sha256
' "${RECOMMENDATION_MODEL}" >/dev/null ||
  fail 'recommendation model must be acceptance-only and bind the supplied knowledge catalog SHA-256'

for command_name in awk bwrap cmp curl diff dirname env find git grep id install jq mktemp mv node \
  openssl pg_dump pg_isready pg_restore pgbouncer pnpm podman psql realpath sed sha256sum \
  sort ss stat tr wc zstd; do
  command -v "${command_name}" >/dev/null 2>&1 ||
    fail "required command is unavailable: ${command_name}"
done
unset command_name

readonly PGBOUNCER_BINARY=/usr/bin/pgbouncer
[[ "$(realpath -e -- "$(command -v pgbouncer)")" == "${PGBOUNCER_BINARY}" &&
   -f "${FEDORA_PACKAGE_LOCK}" && ! -L "${FEDORA_PACKAGE_LOCK}" ]] ||
  fail 'full E2E requires the release-owned Fedora package lock and native PgBouncer path'
EXPECTED_PGBOUNCER_NEVRA="$(jq -er '
  select(
    .schema == "ascendany.fedora-runtime-packages.v1" and
    .fedoraRelease == 44 and .architecture == "x86_64" and
    (.packages | keys == ["cloudflared", "pgbouncer"]) and
    (.packages.pgbouncer | keys == ["files", "nevra", "rpmSHA256", "signingFingerprint"]) and
    (.packages.pgbouncer.files | length == 1) and
    (.packages.pgbouncer.files[0] | keys == ["group", "mode", "owner", "path", "sha256", "size"]) and
    .packages.pgbouncer.files[0].path == "/usr/bin/pgbouncer"
  ) | .packages.pgbouncer.nevra
' "${FEDORA_PACKAGE_LOCK}")" || fail 'the Fedora runtime package lock violates its closed schema'
readonly EXPECTED_PGBOUNCER_NEVRA
EXPECTED_PGBOUNCER_SHA256="$(jq -er '.packages.pgbouncer.files[0].sha256' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary digest is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_SHA256
EXPECTED_PGBOUNCER_SIZE="$(jq -er '.packages.pgbouncer.files[0].size' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary size is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_SIZE
EXPECTED_PGBOUNCER_MODE="$(jq -er '.packages.pgbouncer.files[0].mode' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary mode is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_MODE
EXPECTED_PGBOUNCER_OWNER="$(jq -er '.packages.pgbouncer.files[0].owner' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary owner is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_OWNER
EXPECTED_PGBOUNCER_GROUP="$(jq -er '.packages.pgbouncer.files[0].group' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary group is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_GROUP
[[ -x /usr/bin/rpm && "$(/usr/bin/rpm -q --qf '%{NEVRA}' pgbouncer)" == "${EXPECTED_PGBOUNCER_NEVRA}" &&
   "$(sha256sum -- "${PGBOUNCER_BINARY}" | awk '{print $1}')" == "${EXPECTED_PGBOUNCER_SHA256}" &&
   "$(stat -Lc '%s' -- "${PGBOUNCER_BINARY}")" == "${EXPECTED_PGBOUNCER_SIZE}" &&
   "$(stat -Lc '%04a' -- "${PGBOUNCER_BINARY}")" == "${EXPECTED_PGBOUNCER_MODE}" &&
   "$(stat -Lc '%U:%G' -- "${PGBOUNCER_BINARY}")" == "${EXPECTED_PGBOUNCER_OWNER}:${EXPECTED_PGBOUNCER_GROUP}" &&
   "$("${PGBOUNCER_BINARY}" --version | sed -n '1p')" == 'PgBouncer 1.25.2' &&
   "$(/usr/bin/uname -m)" == x86_64 ]] ||
  fail 'full E2E requires the exact release-locked Fedora 44 PgBouncer package'
grep -Fqx 'ID=fedora' /etc/os-release && grep -Fqx 'VERSION_ID=44' /etc/os-release ||
  fail 'full E2E native PgBouncer host must be Fedora 44'

readonly NODE_BINARY="$(realpath -e -- "$(command -v node)")"
readonly PNPM_LAUNCHER="$(command -v pnpm)"
readonly PNPM_BINARY="$(realpath -e -- "${PNPM_LAUNCHER}")"
[[ -f "${NODE_BINARY}" && ! -L "${NODE_BINARY}" && -x "${NODE_BINARY}" ]] ||
  fail 'Node must resolve to one executable regular file'
[[ -f "${PNPM_BINARY}" && ! -L "${PNPM_BINARY}" && -x "${PNPM_BINARY}" ]] ||
  fail 'pnpm must resolve to one executable regular file'
[[ "${PNPM_LAUNCHER}" == /* && -x "${PNPM_LAUNCHER}" &&
   "$(realpath -e -- "${PNPM_LAUNCHER}")" == "${PNPM_BINARY}" &&
   "$(realpath -e -- "$(dirname -- "${PNPM_LAUNCHER}")")" == "$(dirname -- "${PNPM_LAUNCHER}")" ]] ||
  fail 'pnpm launcher must have one canonical directory and the reviewed target identity'
[[ "$("${NODE_BINARY}" --version)" =~ ^v22\. ]] || fail 'full E2E requires Node.js 22'
[[ "$(PATH="$(dirname -- "${NODE_BINARY}"):/usr/bin:/bin" "${PNPM_BINARY}" --version)" == 9.15.4 ]] ||
  fail 'full E2E requires pnpm 9.15.4'
readonly TOOL_PATH="$(dirname -- "${NODE_BINARY}"):$(dirname -- "${PNPM_LAUNCHER}"):$(dirname -- "${PNPM_BINARY}"):/usr/local/bin:/usr/bin:/bin"

[[ -x "${RELEASE_BUILDER}" && -x "${INSTALLER_SOURCE}" ]] ||
  fail 'release builder or installer is unavailable/executable mode is wrong'
[[ -f "${EXPORTER_SOURCE_FIXTURE}" && ! -L "${EXPORTER_SOURCE_FIXTURE}" ]] ||
  fail 'the committed sanitized exporter source-shape fixture is unavailable'
[[ "$(git -C "${REPOSITORY_ROOT}" rev-parse HEAD)" == "${REQUESTED_COMMIT}" ]] ||
  fail 'requested commit differs from checkout HEAD'
[[ -z "$(git -C "${REPOSITORY_ROOT}" status --porcelain=v1 --untracked-files=all)" ]] ||
  fail 'release full E2E requires a completely clean checkout'
git -C "${REPOSITORY_ROOT}" diff --quiet "${REQUESTED_COMMIT}" -- "${EXPORTER_SOURCE_FIXTURE#${REPOSITORY_ROOT}/}" ||
  fail 'the exporter source-shape fixture differs from the reviewed commit'
[[ "$(podman info --format '{{.Host.Security.Rootless}}')" == true ]] ||
  fail 'Podman is not operating in rootless mode'
podman image exists "${POSTGRES_IMAGE}" ||
  fail "preload the pinned PostgreSQL image: ${POSTGRES_IMAGE}"

readonly GO_VERSION="$(/usr/bin/env -i PATH=/usr/bin:/bin HOME="${HOME}" GOTOOLCHAIN=local GOENV=off "${GO_BINARY}" env GOVERSION)"
[[ "${GO_VERSION}" =~ ^go1\.26(\.[0-9]+)?([A-Za-z0-9.:_+~-]+)?$ ]] ||
  fail 'full E2E requires one canonical Go 1.26 toolchain'
readonly GO_EXPERIMENT="$(/usr/bin/env -i PATH=/usr/bin:/bin HOME="${HOME}" GOTOOLCHAIN=local GOENV=off GOEXPERIMENT='' "${GO_BINARY}" env GOEXPERIMENT)"
[[ -z "${GO_EXPERIMENT}" || "${GO_EXPERIMENT}" =~ ^[0-9A-Za-z_,.-]+$ ]] ||
  fail 'Go experiment set is noncanonical'
readonly SOURCE_DATE_EPOCH="$(git -C "${REPOSITORY_ROOT}" show -s --format=%ct "${REQUESTED_COMMIT}")"
[[ "${SOURCE_DATE_EPOCH}" =~ ^[0-9]+$ ]] || fail 'commit timestamp is noncanonical'

readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-v2-full-e2e.XXXXXX")"
readonly LOG_ROOT="${WORK_ROOT}/logs"
readonly CREDENTIAL_ROOT="${WORK_ROOT}/credentials"
readonly INPUT_ROOT="${WORK_ROOT}/inputs"
readonly CAPTURED_KNOWLEDGE_CATALOG="${INPUT_ROOT}/recommendation-knowledge-catalog.json"
readonly RELEASE_OUTPUT="${WORK_ROOT}/release/ascendany-v2"
readonly REJECT_INSTALL_OPT_ROOT="${WORK_ROOT}/rejected-opt"
readonly INSTALL_OPT_ROOT="${WORK_ROOT}/installed-opt"
readonly INSTALL_BOOTSTRAP="${WORK_ROOT}/install-v2-release.sh"
readonly ARTIFACT_ROOT="${WORK_ROOT}/artifacts"
readonly BACKUP_ROOT="${WORK_ROOT}/backups"
readonly RESTORE_PARENT="${WORK_ROOT}/restore"
readonly RESTORE_ARTIFACT_ROOT="${RESTORE_PARENT}/artifacts"
readonly RUNTIME_PARENT="${WORK_ROOT}/runtime"
readonly BACKUP_RUNTIME_ROOT="${RUNTIME_PARENT}/ascendany-backup"
readonly SOURCE_FINGERPRINT="${WORK_ROOT}/source-database.fingerprint"
readonly RESTORED_FINGERPRINT="${WORK_ROOT}/restored-database.fingerprint"
readonly SOURCE_MODEL_STATE="${WORK_ROOT}/source-model-state.json"
readonly RESTORED_MODEL_STATE="${WORK_ROOT}/restored-model-state.json"
readonly API_MODEL_PROVENANCE="${WORK_ROOT}/api-model-provenance.json"
readonly SOURCE_MODEL_PROVENANCE="${WORK_ROOT}/source-model-provenance.json"
readonly RESTORED_MODEL_PROVENANCE="${WORK_ROOT}/restored-model-provenance.json"
readonly POSTGRES_PASSWORD_FILE="${CREDENTIAL_ROOT}/postgres-password"
readonly ADMIN_PGPASS_FILE="${CREDENTIAL_ROOT}/postgres.pgpass"
readonly RUNTIME_PASSWORD_FILE="${CREDENTIAL_ROOT}/runtime-password"
readonly MIGRATOR_PASSWORD_FILE="${CREDENTIAL_ROOT}/migrator-password"
readonly BACKUP_PASSWORD_FILE="${CREDENTIAL_ROOT}/backup-password"
readonly RESTORE_PASSWORD_FILE="${CREDENTIAL_ROOT}/restore-password"
readonly PASSWORD_PEPPER_FILE="${CREDENTIAL_ROOT}/password-pepper"
readonly JWT_SIGNING_KEY_FILE="${CREDENTIAL_ROOT}/jwt-signing-key"
readonly ADMIN_BOOTSTRAP_CREDENTIAL_ROOT="${CREDENTIAL_ROOT}/admin-bootstrap"
readonly ADMIN_PASSWORD_FILE="${ADMIN_BOOTSTRAP_CREDENTIAL_ROOT}/admin_password"
readonly PGBOUNCER_CONFIG_ROOT="${WORK_ROOT}/pgbouncer"
readonly SERVER_ENV="${WORK_ROOT}/ascendanyd.env"
readonly SERVER_LOG="${LOG_ROOT}/ascendanyd.jsonl"
readonly SMOKE_SERVER_ENV="${WORK_ROOT}/ascendanyd-smoke.env"
readonly SMOKE_SERVER_LOG="${LOG_ROOT}/ascendanyd-smoke.jsonl"
readonly PRE_SMOKE_DATABASE_FINGERPRINT="${WORK_ROOT}/pre-smoke-database.fingerprint"
readonly POST_SMOKE_DATABASE_FINGERPRINT="${WORK_ROOT}/post-smoke-database.fingerprint"
readonly SMOKE_MUTATION_RESULT="${LOG_ROOT}/smoke-mutation.json"
readonly CLIENT_BUNDLE="${WORK_ROOT}/v2-full-e2e-client.mjs"
readonly EXPORTER_FIXTURE_BUNDLE="${WORK_ROOT}/v2-full-e2e-exporter-fixture.mjs"
readonly GENERATED_SNAPSHOT_PATH="${WORK_ROOT}/ascendany-pintia-exporter-snapshot-v2.json"
readonly EXPORTER_FIXTURE_RESULT="${LOG_ROOT}/exporter-fixture-result.json"
readonly CLIENT_RESULT="${LOG_ROOT}/client-result.json"
readonly APP_RESULT="${LOG_ROOT}/app-result.json"
readonly CREATE_LOG="${LOG_ROOT}/backup-create.json"
readonly VERIFY_LOG="${LOG_ROOT}/backup-verify.json"
readonly RESTORE_LOG="${LOG_ROOT}/backup-restore.json"

mkdir -m 0700 -- "${LOG_ROOT}" "${CREDENTIAL_ROOT}" "${ADMIN_BOOTSTRAP_CREDENTIAL_ROOT}" \
  "${INPUT_ROOT}" "${WORK_ROOT}/release" \
  "${REJECT_INSTALL_OPT_ROOT}" "${INSTALL_OPT_ROOT}" "${ARTIFACT_ROOT}" "${BACKUP_ROOT}" "${RESTORE_PARENT}" \
  "${RUNTIME_PARENT}" "${BACKUP_RUNTIME_ROOT}" "${PGBOUNCER_CONFIG_ROOT}"
chmod 0750 -- "${ARTIFACT_ROOT}" "${BACKUP_ROOT}"
chmod 0755 -- "${REJECT_INSTALL_OPT_ROOT}" "${INSTALL_OPT_ROOT}"
install -m 0755 -- "${INSTALLER_SOURCE}" "${INSTALL_BOOTSTRAP}"
install -m 0600 -- "${KNOWLEDGE_CATALOG}" "${CAPTURED_KNOWLEDGE_CATALOG}"
[[ "$(sha256sum -- "${CAPTURED_KNOWLEDGE_CATALOG}" | awk '{print $1}')" == "${KNOWLEDGE_CATALOG_SHA256}" ]] ||
  fail 'captured knowledge catalog bytes differ from the independently supplied SHA-256'

TOKEN=''
POD_NAME=''
INFRA_NAME=''
POSTGRES_NAME=''
NETWORK_NAME=''
VOLUME_NAME=''
DIRECT_HOST=''
POOL_HOST=''
API_HOST=''
API_PORT=''
for _attempt in {1..32}; do
  TOKEN="$(openssl rand -hex 6)"
  POD_NAME="ascendany-v2-full-${TOKEN}"
  INFRA_NAME="${POD_NAME}-infra"
  POSTGRES_NAME="${POD_NAME}-postgres"
  NETWORK_NAME="${POD_NAME}-network"
  VOLUME_NAME="${POD_NAME}-postgres-data"
  DIRECT_HOST="127.127.$((16#${TOKEN:0:2} % 254 + 1)).$((16#${TOKEN:2:2} % 254 + 1))"
  POOL_HOST="127.127.$((16#${TOKEN:4:2} % 254 + 1)).$((16#${TOKEN:6:2} % 254 + 1))"
  API_HOST='127.0.0.1'
  API_PORT="$((20000 + 16#${TOKEN:8:4} % 40000))"
  if ! podman pod exists "${POD_NAME}" && ! podman network exists "${NETWORK_NAME}" &&
     ! podman volume exists "${VOLUME_NAME}" &&
     [[ -z "$(ss -H -ltn "src ${DIRECT_HOST} and sport = :5432")" ]] &&
     [[ -z "$(ss -H -ltn "src ${POOL_HOST} and sport = :6432")" ]] &&
     [[ -z "$(ss -H -ltn "sport = :${API_PORT}")" ]]; then
    break
  fi
  TOKEN=''
done
[[ -n "${TOKEN}" ]] || fail 'could not allocate collision-free E2E resources'
readonly TOKEN POD_NAME INFRA_NAME POSTGRES_NAME NETWORK_NAME VOLUME_NAME
readonly DIRECT_HOST POOL_HOST API_HOST API_PORT
readonly LABEL_VALUE="${TOKEN}"

SERVER_PID=''
PGBOUNCER_PID=''
POD_CREATED=0
NETWORK_CREATED=0
VOLUME_CREATED=0
cleanup() {
  local original_status=$?
  local cleanup_status=0
  local leftovers=''
  local leftover_count=0
  trap - EXIT INT TERM
  set +e
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill -TERM "${SERVER_PID}"
    wait "${SERVER_PID}" 2>/dev/null
  fi
  if [[ -n "${PGBOUNCER_PID}" ]] && kill -0 "${PGBOUNCER_PID}" 2>/dev/null; then
    kill -TERM "${PGBOUNCER_PID}"
    wait "${PGBOUNCER_PID}" 2>/dev/null
  fi
  ((POD_CREATED == 0)) || podman pod rm --force "${POD_NAME}" >/dev/null || cleanup_status=1
  ((VOLUME_CREATED == 0)) || podman volume rm --force "${VOLUME_NAME}" >/dev/null || cleanup_status=1
  ((NETWORK_CREATED == 0)) || podman network rm --force "${NETWORK_NAME}" >/dev/null || cleanup_status=1
  leftovers="$(podman ps --all --filter "label=${LABEL_KEY}=${LABEL_VALUE}" --format '{{.Names}}';
    podman pod ps --filter "label=${LABEL_KEY}=${LABEL_VALUE}" --format '{{.Name}}';
    podman volume ls --filter "label=${LABEL_KEY}=${LABEL_VALUE}" --format '{{.Name}}';
    podman network ls --filter "label=${LABEL_KEY}=${LABEL_VALUE}" --format '{{.Name}}')"
  [[ -z "${leftovers}" ]] || {
    leftover_count="$(wc -l <<<"${leftovers}" | tr -d ' ')"
    /usr/bin/printf '%s\n' 'labeled full-E2E resources remain after cleanup' >&2
    cleanup_status=1
  }
  rm -rf --one-file-system -- "${WORK_ROOT}"
  /usr/bin/printf 'FULL_E2E_CLEANUP labeled_resources=%s temporary_tree_removed=%s\n' \
    "${leftover_count}" \
    "$([[ ! -e "${WORK_ROOT}" ]] && printf true || printf false)"
  ((original_status == 0)) || exit "${original_status}"
  exit "${cleanup_status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

/usr/bin/printf '%s\n' 'Building the reviewed release payload'
"${RELEASE_BUILDER}" \
  --version "${VERSION}" \
  --commit "${REQUESTED_COMMIT}" \
  --source-date-epoch "${SOURCE_DATE_EPOCH}" \
  --go-path "${GO_BINARY}" \
  --go-version "${GO_VERSION}" \
  --goos linux \
  --goarch amd64 \
  --goamd64 v1 \
  --release-purpose "${RELEASE_PURPOSE}" \
  --recommendation-model "${RECOMMENDATION_MODEL}" \
  --recommendation-model-sha256 "${RECOMMENDATION_MODEL_SHA256}" \
  --output "${RELEASE_OUTPUT}" \
  >"${LOG_ROOT}/release-builder.log"

readonly RELEASE_MANIFEST="${RELEASE_OUTPUT}/release-manifest.json"
[[ -f "${RELEASE_MANIFEST}" && ! -L "${RELEASE_MANIFEST}" &&
   "$(stat -Lc '%a:%h' -- "${RELEASE_MANIFEST}")" == 644:1 ]] ||
  fail 'release manifest is not one regular mode-0644 file'
readonly RELEASE_MANIFEST_SHA256="$(sha256sum -- "${RELEASE_MANIFEST}" | awk '{print $1}')"
jq -e \
  --arg version "${VERSION}" \
  --arg commit "${REQUESTED_COMMIT}" \
  --arg purpose "${RELEASE_PURPOSE}" \
  --arg go_version "${GO_VERSION}" \
  --arg go_experiment "${GO_EXPERIMENT:-none}" \
  --argjson source_date_epoch "${SOURCE_DATE_EPOCH}" '
  type == "object" and
  keys == ["build", "commit", "files", "purpose", "schema", "sourceDateEpoch", "version"] and
  .schema == "ascendany.release.v2" and
  .version == $version and
  .commit == $commit and
  .purpose == $purpose and
  .sourceDateEpoch == $source_date_epoch and
  .build == {
    "cgoEnabled": false,
    "goExperiment": $go_experiment,
    "goVersion": $go_version,
    "goamd64": "v1",
    "goarch": "amd64",
    "gofips140": "off",
    "goos": "linux"
  } and
  (.files | type == "array" and length > 0) and
  ([.files[].path] | length == (unique | length)) and
  all(.files[];
    type == "object" and
    (keys == ["mode", "path", "sha256", "size"]) and
    (.path | type == "string" and test("^[0-9A-Za-z_@./+-]+$") and
      length > 0 and startswith("/") == false and endswith("/") == false and
      contains("//") == false and . != "release-manifest.json" and
      test("(^|/)\\.\\.?(/|$)") == false) and
    (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.size | type == "number" and . > 0 and floor == .) and
    (.mode | type == "string" and test("^0(644|755)$")))
' "${RELEASE_MANIFEST}" >/dev/null || fail 'release manifest violates the reviewed v2 contract'
while IFS=$'\t' read -r relative expected_digest expected_size expected_mode; do
  payload="${RELEASE_OUTPUT}/${relative}"
  [[ -f "${payload}" && ! -L "${payload}" ]] || fail "release entry is not a regular file: ${relative}"
  [[ "$(stat -Lc '%h' -- "${payload}")" == 1 ]] || fail "release entry has an extra hard link: ${relative}"
  [[ "$(sha256sum -- "${payload}" | awk '{print $1}')" == "${expected_digest}" ]] ||
    fail "release entry digest differs: ${relative}"
  [[ "$(stat -Lc '%s' -- "${payload}")" == "${expected_size}" ]] ||
    fail "release entry size differs: ${relative}"
  [[ "0$(stat -Lc '%a' -- "${payload}")" == "${expected_mode}" ]] ||
    fail "release entry mode differs: ${relative}"
done < <(jq -r '.files[] | [.path, .sha256, (.size | tostring), .mode] | @tsv' "${RELEASE_MANIFEST}")
unset relative expected_digest expected_size expected_mode payload

run_release_installer() {
  local opt_root="$1"
  shift
  /usr/bin/bwrap \
    --die-with-parent \
    --unshare-user --uid 0 --gid 0 \
    --unshare-pid --unshare-ipc --unshare-uts --unshare-net \
    --proc /proc --dev /dev --tmpfs /tmp \
    --ro-bind /usr /usr \
    --symlink usr/bin /bin \
    --symlink usr/sbin /sbin \
    --symlink usr/lib /lib \
    --symlink usr/lib64 /lib64 \
    --dir /opt --bind "${opt_root}" /opt \
    --dir /release --ro-bind "${RELEASE_OUTPUT}" /release/ascendany-v2 \
    --dir /bootstrap --ro-bind "${INSTALL_BOOTSTRAP}" /bootstrap/install-v2-release.sh \
    -- /bootstrap/install-v2-release.sh \
      --source /release/ascendany-v2 \
      --manifest-sha256 "${RELEASE_MANIFEST_SHA256}" \
      "$@"
}

/usr/bin/printf '%s\n' 'Proving the default production installer rejects the acceptance-only release'
if run_release_installer "${REJECT_INSTALL_OPT_ROOT}" \
    >"${LOG_ROOT}/production-installer-rejection.out" \
    2>"${LOG_ROOT}/production-installer-rejection.err"; then
  fail 'default production installer accepted an acceptance-only release'
fi
grep -Fqx 'release installation failed: release purpose acceptance_test differs from expected production' \
  "${LOG_ROOT}/production-installer-rejection.err" ||
  fail 'default production installer rejection did not occur at the release-purpose gate'
[[ ! -e "${REJECT_INSTALL_OPT_ROOT}/ascendany/v2" && ! -L "${REJECT_INSTALL_OPT_ROOT}/ascendany/v2" ]] ||
  fail 'default production installer published an acceptance-only release'

/usr/bin/printf '%s\n' 'Installing through the explicit acceptance-only capability in an isolated root namespace'
run_release_installer "${INSTALL_OPT_ROOT}" \
  --expected-purpose "${RELEASE_PURPOSE}" \
  >"${LOG_ROOT}/release-installer.log"
unset -f run_release_installer

readonly INSTALLED_RELEASE="${INSTALL_OPT_ROOT}/ascendany/v2"
[[ -f "${INSTALLED_RELEASE}/release-manifest.json" &&
   ! -L "${INSTALLED_RELEASE}/release-manifest.json" &&
   "$(stat -Lc '%a:%h' -- "${INSTALLED_RELEASE}/release-manifest.json")" == 644:1 ]] ||
  fail 'installed release manifest is not one regular mode-0644 file'
cmp --silent -- "${RELEASE_MANIFEST}" "${INSTALLED_RELEASE}/release-manifest.json" ||
  fail 'installed manifest bytes differ from the externally anchored release manifest'
readonly EXPECTED_INSTALLED_FILES="${WORK_ROOT}/expected-installed-files"
readonly ACTUAL_INSTALLED_FILES="${WORK_ROOT}/actual-installed-files"
{
  jq -r '.files[].path' "${RELEASE_MANIFEST}"
  printf '%s\n' release-manifest.json
} | LC_ALL=C sort >"${EXPECTED_INSTALLED_FILES}"
find "${INSTALLED_RELEASE}" -mindepth 1 ! -type d -printf '%P\n' | LC_ALL=C sort >"${ACTUAL_INSTALLED_FILES}"
diff -u "${EXPECTED_INSTALLED_FILES}" "${ACTUAL_INSTALLED_FILES}" >/dev/null ||
  fail 'installed release contains a missing, non-regular, or unmanifested entry'
[[ -z "$(find "${INSTALLED_RELEASE}" -mindepth 1 \( -type l -o \( ! -type d ! -type f \) \) -print -quit)" ]] ||
  fail 'installed release contains a symbolic link or special file'
while IFS=$'\t' read -r relative expected_digest expected_size expected_mode; do
  payload="${INSTALLED_RELEASE}/${relative}"
  [[ -f "${payload}" && ! -L "${payload}" ]] || fail "installed entry is not a regular file: ${relative}"
  [[ "$(stat -Lc '%h' -- "${payload}")" == 1 ]] || fail "installed entry has an extra hard link: ${relative}"
  [[ "$(sha256sum -- "${payload}" | awk '{print $1}')" == "${expected_digest}" ]] ||
    fail "installed entry digest differs: ${relative}"
  [[ "$(stat -Lc '%s' -- "${payload}")" == "${expected_size}" ]] ||
    fail "installed entry size differs: ${relative}"
  [[ "0$(stat -Lc '%a' -- "${payload}")" == "${expected_mode}" ]] ||
    fail "installed entry mode differs: ${relative}"
done < <(jq -r '.files[] | [.path, .sha256, (.size | tostring), .mode] | @tsv' "${RELEASE_MANIFEST}")
unset relative expected_digest expected_size expected_mode payload
for binary in ascendanyd ascendany-admin-bootstrap ascendany-backup ascendany-migrate; do
  [[ -x "${INSTALLED_RELEASE}/bin/${binary}" ]] || fail "installed binary is unavailable: ${binary}"
done
unset binary

/usr/bin/printf '%s\n' 'Checking generated SDK and building all first-party TypeScript applications'
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/sdk check >"${LOG_ROOT}/sdk-check.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/pintia-exporter check >"${LOG_ROOT}/pintia-exporter-check.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" public-assets:check >"${LOG_ROOT}/public-assets-check.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/site build >"${LOG_ROOT}/site-build.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/web check >"${LOG_ROOT}/web-check.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/import-console check >"${LOG_ROOT}/import-console-check.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/mobile check >"${LOG_ROOT}/mobile-check.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/desktop test >"${LOG_ROOT}/desktop-test.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/desktop build >"${LOG_ROOT}/desktop-build.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/sdk exec tsc \
  --noEmit --strict --target ES2022 --module ESNext --moduleResolution Bundler \
  --lib ES2022,DOM --types node --allowImportingTsExtensions \
  "${REPOSITORY_ROOT}/tools/v2-full-e2e-client.ts" \
  "${REPOSITORY_ROOT}/tools/v2-full-e2e-exporter-fixture.ts" \
  >"${LOG_ROOT}/client-typecheck.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/sdk exec esbuild \
  "${REPOSITORY_ROOT}/tools/v2-full-e2e-client.ts" \
  --bundle --platform=node --format=esm --target=node22 --outfile="${CLIENT_BUNDLE}" \
  >"${LOG_ROOT}/client-build.log"
PATH="${TOOL_PATH}" "${PNPM_BINARY}" --dir "${REPOSITORY_ROOT}" --filter @ascendany/sdk exec esbuild \
  "${REPOSITORY_ROOT}/tools/v2-full-e2e-exporter-fixture.ts" \
  --bundle --platform=node --format=esm --target=node22 --outfile="${EXPORTER_FIXTURE_BUNDLE}" \
  >"${LOG_ROOT}/exporter-fixture-build.log"
"${NODE_BINARY}" "${EXPORTER_FIXTURE_BUNDLE}" \
  --source "${EXPORTER_SOURCE_FIXTURE}" --output "${GENERATED_SNAPSHOT_PATH}" \
  >"${EXPORTER_FIXTURE_RESULT}"
jq -e '
  .schema == "ascendany.full-e2e.exporter-fixture.v1" and
  .snapshotSchema == "ascendany.pintia.snapshot.v2" and
  .problemCount > 0 and .participantCount > 0 and .submissionCount > 0
' "${EXPORTER_FIXTURE_RESULT}" >/dev/null || fail 'Pintia exporter E2E fixture evidence is incomplete'
[[ -f "${GENERATED_SNAPSHOT_PATH}" && ! -L "${GENERATED_SNAPSHOT_PATH}" &&
   "$(stat -Lc '%a' "${GENERATED_SNAPSHOT_PATH}")" == 600 ]] ||
  fail 'Pintia exporter E2E snapshot file contract is invalid'
[[ "$(git -C "${REPOSITORY_ROOT}" rev-parse HEAD)" == "${REQUESTED_COMMIT}" &&
   -z "$(git -C "${REPOSITORY_ROOT}" status --porcelain=v1 --untracked-files=all)" ]] ||
  fail 'TypeScript checks or builds mutated the reviewed checkout'

readonly POSTGRES_ADMIN_PASSWORD="$(openssl rand -hex 24)"
readonly RUNTIME_PASSWORD="$(openssl rand -hex 24)"
readonly MIGRATOR_PASSWORD="$(openssl rand -hex 24)"
readonly BACKUP_PASSWORD="$(openssl rand -hex 24)"
readonly RESTORE_PASSWORD="$(openssl rand -hex 24)"
readonly PASSWORD_PEPPER="$(openssl rand -hex 32)"
readonly JWT_SIGNING_KEY="$(openssl rand -hex 48)"
readonly ADMIN_PASSWORD="$(openssl rand -hex 24)"

printf '%s' "${POSTGRES_ADMIN_PASSWORD}" >"${POSTGRES_PASSWORD_FILE}"
printf '%s' "${RUNTIME_PASSWORD}" >"${RUNTIME_PASSWORD_FILE}"
printf '%s' "${MIGRATOR_PASSWORD}" >"${MIGRATOR_PASSWORD_FILE}"
printf '%s' "${BACKUP_PASSWORD}" >"${BACKUP_PASSWORD_FILE}"
printf '%s' "${RESTORE_PASSWORD}" >"${RESTORE_PASSWORD_FILE}"
printf '%s' "${PASSWORD_PEPPER}" >"${PASSWORD_PEPPER_FILE}"
printf '%s' "${JWT_SIGNING_KEY}" >"${JWT_SIGNING_KEY_FILE}"
printf '%s' "${ADMIN_PASSWORD}" >"${ADMIN_PASSWORD_FILE}"
printf '%s:5432:*:postgres:%s\n' "${DIRECT_HOST}" "${POSTGRES_ADMIN_PASSWORD}" >"${ADMIN_PGPASS_FILE}"
chmod 0600 -- \
  "${POSTGRES_PASSWORD_FILE}" "${ADMIN_PGPASS_FILE}" "${RUNTIME_PASSWORD_FILE}" \
  "${MIGRATOR_PASSWORD_FILE}" "${BACKUP_PASSWORD_FILE}" "${RESTORE_PASSWORD_FILE}" \
  "${PASSWORD_PEPPER_FILE}" "${JWT_SIGNING_KEY_FILE}" "${ADMIN_PASSWORD_FILE}"
[[ "$(stat -Lc '%a' -- "${ADMIN_BOOTSTRAP_CREDENTIAL_ROOT}")" == 700 ]] ||
  fail 'administrator bootstrap credential directory mode drifted'

readonly PGBOUNCER_CONFIG_FILE="${PGBOUNCER_CONFIG_ROOT}/pgbouncer.ini"
readonly PGBOUNCER_HBA_FILE="${PGBOUNCER_CONFIG_ROOT}/pgbouncer-hba.conf"
readonly PGBOUNCER_USERLIST_FILE="${PGBOUNCER_CONFIG_ROOT}/userlist.txt"
install -m 0644 -- "${INSTALLED_RELEASE}/config/pgbouncer.ini" "${PGBOUNCER_CONFIG_FILE}"
install -m 0644 -- "${INSTALLED_RELEASE}/config/pgbouncer-hba.conf" "${PGBOUNCER_HBA_FILE}"
[[ "$(grep -Ec '^auth_type = hba$' "${PGBOUNCER_CONFIG_FILE}")" == 1 &&
   "$(grep -Ec '^pool_mode = transaction$' "${PGBOUNCER_CONFIG_FILE}")" == 1 &&
   "$(grep -Ec '^listen_addr = 127\.0\.0\.1$' "${PGBOUNCER_CONFIG_FILE}")" == 1 &&
   "$(grep -Ec '^listen_port = 6432$' "${PGBOUNCER_CONFIG_FILE}")" == 1 &&
   "$(grep -Ec '^auth_file = ' "${PGBOUNCER_CONFIG_FILE}")" == 1 &&
   "$(grep -Ec '^auth_hba_file = ' "${PGBOUNCER_CONFIG_FILE}")" == 1 ]] ||
  fail 'release-owned PgBouncer configuration violates the full-E2E transform contract'
sed \
  -e "s|host=127.0.0.1 port=5432|host=${DIRECT_HOST} port=5432|g" \
  -e "s|listen_addr = 127.0.0.1|listen_addr = ${POOL_HOST}|" \
  -e "s|^auth_file = .*$|auth_file = ${PGBOUNCER_USERLIST_FILE}|" \
  -e "s|^auth_hba_file = .*$|auth_hba_file = ${PGBOUNCER_HBA_FILE}|" \
  "${PGBOUNCER_CONFIG_FILE}" >"${PGBOUNCER_CONFIG_FILE}.normalized"
mv -- "${PGBOUNCER_CONFIG_FILE}.normalized" "${PGBOUNCER_CONFIG_FILE}"
chmod 0400 -- "${PGBOUNCER_CONFIG_FILE}" "${PGBOUNCER_HBA_FILE}"

admin_psql() {
  local database="$1"
  shift
  /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C \
    PGHOST="${DIRECT_HOST}" PGPORT=5432 PGDATABASE="${database}" PGUSER=postgres \
    PGCONNECT_TIMEOUT=5 PGPASSFILE="${ADMIN_PGPASS_FILE}" \
    /usr/bin/psql -X --no-password --set=ON_ERROR_STOP=1 "$@"
}

role_psql() {
  local role="$1"
  local password_file="$2"
  local database="$3"
  shift 3
  local pgpass="${WORK_ROOT}/${role}.pgpass"
  local password
  password="$(<"${password_file}")"
  printf '%s:5432:*:%s:%s\n' "${DIRECT_HOST}" "${role}" "${password}" >"${pgpass}"
  chmod 0600 "${pgpass}"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C \
    PGHOST="${DIRECT_HOST}" PGPORT=5432 PGDATABASE="${database}" PGUSER="${role}" \
    PGCONNECT_TIMEOUT=5 PGPASSFILE="${pgpass}" \
    /usr/bin/psql -X --no-password --set=ON_ERROR_STOP=1 "$@"
  rm -f -- "${pgpass}"
}

restore_owner_psql() {
  local pgpass="${WORK_ROOT}/restore-owner.pgpass"
  printf '%s:5432:*:%s:%s\n' "${DIRECT_HOST}" "${RESTORE_LOGIN}" "${RESTORE_PASSWORD}" >"${pgpass}"
  chmod 0600 "${pgpass}"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C \
    PGHOST="${DIRECT_HOST}" PGPORT=5432 PGDATABASE=postgres PGUSER="${RESTORE_LOGIN}" \
    PGCONNECT_TIMEOUT=5 PGPASSFILE="${pgpass}" PGOPTIONS="-c role=${SCHEMA_OWNER}" \
    /usr/bin/psql -X --no-password --set=ON_ERROR_STOP=1 "$@"
  rm -f -- "${pgpass}"
}

run_with_private_runtime_root() {
  local host_root="$1"
  local visible_root="$2"
  shift 2
  [[ -d "${host_root}" && ! -L "${host_root}" && "$(stat -Lc '%a:%u' "${host_root}")" == "700:$(id -u)" ]] ||
    fail 'private backup runtime root violates the owner/mode contract'
  /usr/bin/bwrap \
    --die-with-parent --ro-bind / / --dev /dev \
    --bind "${WORK_ROOT}" "${WORK_ROOT}" \
    --tmpfs /run --dir "${visible_root}" --bind "${host_root}" "${visible_root}" \
    -- "$@"
}

assert_single_backup_log() {
  local file="$1"
  local message="$2"
  [[ "$(wc -l <"${file}" | tr -d ' ')" == 1 ]] || fail "${message} must emit exactly one JSON line"
  jq -e --arg message "${message}" --argjson artifact_count "${EXPECTED_ARTIFACT_COUNT}" '
    type == "object" and .level == "INFO" and .msg == $message and
    (.backupId | type == "string" and test("^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$")) and
    (.manifestSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    .artifactCount == $artifact_count
  ' "${file}" >/dev/null || fail "${message} evidence contract failed"
}

database_fingerprint() {
  local database="$1"
  local output="$2"
  admin_psql "${database}" --tuples-only --no-align >"${output}" <<'SQL'
SELECT format(
  'SELECT %L || ''|'' || COALESCE(jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text)::text, ''[]'') FROM %I.%I AS row_value;',
  'table:' || table_name, table_schema, table_name
)
FROM information_schema.tables
WHERE table_schema = 'ascendany' AND table_type = 'BASE TABLE'
ORDER BY table_name
\gexec
SELECT format(
  'SELECT %L || ''|'' || jsonb_build_object(''lastValue'', last_value, ''isCalled'', is_called)::text FROM %I.%I;',
  'sequence:' || relation.relname, namespace.nspname, relation.relname
)
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany' AND relation.relkind = 'S'
ORDER BY relation.relname
\gexec
SQL
  LC_ALL=C sort -o "${output}" "${output}"
  chmod 0600 "${output}"
}

database_model_state() {
  local database="$1"
  local output="$2"
  admin_psql "${database}" --tuples-only --no-align >"${output}" <<'SQL'
WITH counts AS (
  SELECT
    (SELECT count(*) FROM ascendany.schema_migrations_v2) AS schema_migration_count,
    (SELECT max(version) FROM ascendany.schema_migrations_v2) AS schema_migration_max,
    (SELECT count(*) FROM ascendany.recommendation_model_releases) AS model_release_count,
    (SELECT count(*) FROM ascendany.recommendation_model_head) AS model_head_count,
    (SELECT count(*) FROM ascendany.recommendation_model_activation_events) AS activation_count,
    (
      SELECT count(*)
      FROM ascendany.recommendation_model_head AS head
      JOIN ascendany.recommendation_model_releases AS release
        ON release.recommendation_model_release_id = head.current_release_id
      JOIN ascendany.recommendation_model_activation_events AS activation
        ON activation.head_revision = head.head_revision
       AND activation.recommendation_model_release_id = head.current_release_id
       AND activation.artifact_sha256 = release.artifact_sha256
      WHERE head.singleton
    ) AS current_activation_count,
    (
      SELECT count(*)
      FROM ascendany.configuration_items AS item
      JOIN ascendany.configuration_versions AS version
        ON version.configuration_item_id = item.configuration_item_id
       AND version.configuration_version_id = item.active_version_id
      WHERE item.configuration_kind = 'knowledge_catalog'
    ) AS active_catalog_count
), active_catalog AS (
  SELECT
    item.configuration_key,
    item.configuration_kind,
    item.head_revision,
    version.version_number,
    version.schema_id,
    version.document_sha256,
    version.credential_ref
  FROM ascendany.configuration_items AS item
  JOIN ascendany.configuration_versions AS version
    ON version.configuration_item_id = item.configuration_item_id
   AND version.configuration_version_id = item.active_version_id
  WHERE item.configuration_kind = 'knowledge_catalog'
), current_model AS (
  SELECT
    release.recommendation_model_release_id AS release_id,
    head.current_release_id AS head_release_id,
    activation.recommendation_model_release_id AS activation_release_id,
    head.head_revision,
    activation.head_revision AS activation_head_revision,
    release.model_id::text AS model_id,
    release.model_purpose,
    release.artifact_sha256,
    release.artifact_size_bytes,
    release.artifact_mode,
    release.model_schema,
    release.algorithm,
    release.inference_contract,
    release.manifest ->> 'trainedAt' AS trained_at,
    release.trained_at = (release.manifest ->> 'trainedAt')::timestamptz AS trained_at_matches_manifest,
    release.training_provenance_sha256,
    release.feature_schema_sha256,
    release.knowledge_catalog_sha256,
    release.parameter_sha256,
    release.golden_vectors_sha256,
    release.manifest,
    release.manifest_sha256,
    release.created_at AS release_created_at,
    head.updated_at AS head_updated_at,
    activation.application_version,
    activation.application_commit,
    activation.application_build_time,
    activation.activated_at
  FROM ascendany.recommendation_model_head AS head
  JOIN ascendany.recommendation_model_releases AS release
    ON release.recommendation_model_release_id = head.current_release_id
  JOIN ascendany.recommendation_model_activation_events AS activation
    ON activation.head_revision = head.head_revision
   AND activation.recommendation_model_release_id = head.current_release_id
   AND activation.artifact_sha256 = release.artifact_sha256
  WHERE head.singleton
)
SELECT jsonb_build_object(
  'schemaMigrationCount', counts.schema_migration_count,
  'schemaMigrationMax', counts.schema_migration_max,
  'modelReleaseCount', counts.model_release_count,
  'modelHeadCount', counts.model_head_count,
  'activationCount', counts.activation_count,
  'currentActivationCount', counts.current_activation_count,
  'activeCatalogCount', counts.active_catalog_count,
  'catalog', (
    SELECT jsonb_build_object(
      'configurationKey', configuration_key,
      'configurationKind', configuration_kind,
      'headRevision', head_revision,
      'versionNumber', version_number,
      'schemaId', schema_id,
      'documentSha256', document_sha256,
      'credentialRef', credential_ref
    )
    FROM active_catalog
  ),
  'model', (
    SELECT jsonb_build_object(
      'modelId', model_id,
      'purpose', model_purpose,
      'artifactSha256', artifact_sha256,
      'artifactSizeBytes', artifact_size_bytes,
      'artifactMode', artifact_mode,
      'modelSchema', model_schema,
      'algorithm', algorithm,
      'inferenceContract', inference_contract,
      'trainedAt', trained_at,
      'trainingProvenanceSha256', training_provenance_sha256,
      'featureSchemaSha256', feature_schema_sha256,
      'knowledgeCatalogSha256', knowledge_catalog_sha256,
      'parameterSha256', parameter_sha256,
      'goldenVectorsSha256', golden_vectors_sha256,
      'modelHeadRevision', head_revision,
      'applicationVersion', application_version,
      'applicationCommit', application_commit,
      'applicationBuildTime', application_build_time
    )
    FROM current_model
  ),
  'binding', (
    SELECT jsonb_build_object(
      'releaseId', release_id,
      'headReleaseId', head_release_id,
      'activationReleaseId', activation_release_id,
      'headRevision', head_revision,
      'activationHeadRevision', activation_head_revision,
      'manifest', manifest,
      'manifestSha256', manifest_sha256,
      'trainedAtMatchesManifest', trained_at_matches_manifest,
      'releaseCreatedAt', release_created_at,
      'headUpdatedAt', head_updated_at,
      'activatedAt', activated_at
    )
    FROM current_model
  )
)::text
FROM counts;
SQL
  chmod 0600 "${output}"
}

assert_database_model_state() {
  local database="$1"
  local output="$2"
  local expected_build_time="$3"
  database_model_state "${database}" "${output}"
  [[ "$(wc -l <"${output}" | tr -d ' ')" == 1 ]] ||
    fail "${database} model-state query did not return exactly one row"
  jq -e \
    --arg artifact_sha256 "${RECOMMENDATION_MODEL_SHA256}" \
    --arg catalog_sha256 "${KNOWLEDGE_CATALOG_SHA256}" \
    --arg model_purpose "${RELEASE_PURPOSE}" \
    --arg application_version "${VERSION}" \
    --arg application_commit "${REQUESTED_COMMIT}" \
    --arg application_build_time "${expected_build_time}" '
      .schemaMigrationCount == 6 and .schemaMigrationMax == 6 and
      .modelReleaseCount == 1 and .modelHeadCount == 1 and
      .activationCount >= 1 and .currentActivationCount == 1 and
      .activeCatalogCount == 1 and
      .catalog == {
        "configurationKey": "recommendation.catalog.active",
        "configurationKind": "knowledge_catalog",
        "headRevision": 1,
        "versionNumber": 1,
        "schemaId": "ascendany.knowledge_catalog.recommendation.v1",
        "documentSha256": $catalog_sha256,
        "credentialRef": null
      } and
      .model.purpose == $model_purpose and
      .model.artifactSha256 == $artifact_sha256 and
      .model.artifactMode == 420 and
      .model.modelSchema == "ascendany.recommendation.inference-model.v1" and
      .model.algorithm == "knowledge_mirt_feature_v1" and
      .model.inferenceContract == "ascendany.recommendation.inference.v1" and
      .model.knowledgeCatalogSha256 == $catalog_sha256 and
      .model.applicationVersion == $application_version and
      .model.applicationCommit == $application_commit and
      .model.applicationBuildTime == $application_build_time and
      .model.modelHeadRevision == .binding.headRevision and
      .binding.headRevision == .binding.activationHeadRevision and
      .binding.releaseId == .binding.headReleaseId and
      .binding.releaseId == .binding.activationReleaseId and
      .binding.trainedAtMatchesManifest == true and
      .binding.manifest.schema == .model.modelSchema and
      .binding.manifest.modelId == .model.modelId and
      .binding.manifest.purpose == .model.purpose and
      .binding.manifest.trainedAt == .model.trainedAt and
      .binding.manifest.algorithm == .model.algorithm and
      .binding.manifest.inferenceContract == .model.inferenceContract and
      .binding.manifest.trainingProvenanceSha256 == .model.trainingProvenanceSha256 and
      .binding.manifest.featureSchemaSha256 == .model.featureSchemaSha256 and
      .binding.manifest.knowledgeCatalogSha256 == .model.knowledgeCatalogSha256 and
      .binding.manifest.knowledgeCatalogSha256 == $catalog_sha256 and
      .binding.manifest.parameterSha256 == .model.parameterSha256 and
      .binding.manifest.goldenVectorsSha256 == .model.goldenVectorsSha256
    ' "${output}" >/dev/null || fail "${database} migration/model/catalog SQL state violates the full-E2E contract"
}

artifact_fingerprint() {
  local root="$1"
  local output="$2"
  [[ -z "$(find "${root}" -mindepth 1 \( -type l -o \( ! -type d ! -type f \) \) -print -quit)" ]] ||
    fail 'artifact tree contains a symbolic link or special entry'
  (
    cd -- "${root}"
    while IFS= read -r -d '' relative; do
      printf '%s\0%s\0%s\0%s\0' \
        "${relative#./}" \
        "$(stat -Lc '%a' -- "${relative}")" \
        "$(stat -Lc '%s' -- "${relative}")" \
        "$(sha256sum -- "${relative}" | awk '{print $1}')"
    done < <(find . -type f -print0 | LC_ALL=C sort -z)
  ) >"${output}"
  chmod 0600 "${output}"
}

/usr/bin/printf '%s\n' 'Starting isolated rootless PostgreSQL 17 and PgBouncer'
podman network create --label "${LABEL_KEY}=${LABEL_VALUE}" "${NETWORK_NAME}" >/dev/null
NETWORK_CREATED=1
podman volume create --label "${LABEL_KEY}=${LABEL_VALUE}" "${VOLUME_NAME}" >/dev/null
VOLUME_CREATED=1
if podman pod create \
    --name "${POD_NAME}" \
    --infra-name "${INFRA_NAME}" \
    --label "${LABEL_KEY}=${LABEL_VALUE}" \
    --network "${NETWORK_NAME}" \
    --publish "${DIRECT_HOST}:5432:5432" \
    >/dev/null; then
  POD_CREATED=1
else
  podman pod exists "${POD_NAME}" && POD_CREATED=1
  fail 'could not create the isolated full-E2E pod'
fi

podman run --detach \
  --pod "${POD_NAME}" \
  --name "${POSTGRES_NAME}" \
  --label "${LABEL_KEY}=${LABEL_VALUE}" \
  --env POSTGRES_USER=postgres \
  --env POSTGRES_DB=postgres \
  --env POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password \
  --volume "${POSTGRES_PASSWORD_FILE}:/run/secrets/postgres-password:ro,Z" \
  --volume "${VOLUME_NAME}:/var/lib/postgresql/data:Z" \
  "${POSTGRES_IMAGE}" \
  -c password_encryption=scram-sha-256 \
  >/dev/null

postgres_ready=0
for _attempt in {1..120}; do
  if pg_isready --host="${DIRECT_HOST}" --port=5432 --username=postgres --dbname=postgres >/dev/null 2>&1; then
    postgres_ready=1
    break
  fi
  if [[ "$(podman inspect --format '{{.State.Running}}' "${POSTGRES_NAME}" 2>/dev/null)" != true ]]; then
    podman logs "${POSTGRES_NAME}" >&2
    fail 'PostgreSQL stopped before readiness'
  fi
  sleep 0.5
done
((postgres_ready == 1)) || fail 'PostgreSQL did not become ready'
readonly POSTGRES_SERVER_VERSION_NUM="$(admin_psql postgres --tuples-only --no-align --command='SHOW server_version_num')"
[[ "${POSTGRES_SERVER_VERSION_NUM}" =~ ^17[0-9]{4}$ ]] || fail 'disposable database server is not PostgreSQL 17'

admin_psql postgres --command="CREATE DATABASE ${SOURCE_DATABASE} WITH TEMPLATE template0 ENCODING 'UTF8'" >/dev/null
admin_psql "${SOURCE_DATABASE}" --file="${INSTALLED_RELEASE}/db/roles/001_v2_roles.sql" >/dev/null
admin_psql "${SOURCE_DATABASE}" >/dev/null <<SQL
ALTER ROLE ${RUNTIME_LOGIN} PASSWORD '${RUNTIME_PASSWORD}';
ALTER ROLE ${MIGRATOR_LOGIN} PASSWORD '${MIGRATOR_PASSWORD}';
ALTER ROLE ${BACKUP_LOGIN} PASSWORD '${BACKUP_PASSWORD}';
ALTER ROLE ${RESTORE_LOGIN} PASSWORD '${RESTORE_PASSWORD}';
SQL
admin_psql postgres --tuples-only --no-align >"${PGBOUNCER_USERLIST_FILE}" <<'SQL'
SELECT format('"%s" "%s"', rolname, rolpassword)
FROM pg_authid
WHERE rolname = 'ascendanyd_login'
  AND rolpassword LIKE 'SCRAM-SHA-256$%'
SQL
[[ "$(wc -l <"${PGBOUNCER_USERLIST_FILE}" | tr -d ' ')" == 1 ]] ||
  fail 'real PgBouncer E2E did not capture the sole v2 runtime SCRAM identity'
[[ "$(grep -c ' "SCRAM-SHA-256\$' "${PGBOUNCER_USERLIST_FILE}")" == 1 ]] ||
  fail 'real PgBouncer E2E userlist contains a non-SCRAM credential'
if grep -Fq -- "${RUNTIME_PASSWORD}" "${PGBOUNCER_USERLIST_FILE}"; then
  fail 'real PgBouncer E2E userlist contains plaintext credential material'
fi
chmod 0400 -- "${PGBOUNCER_USERLIST_FILE}"

/usr/bin/env -i \
  PATH=/usr/bin:/bin LC_ALL=C \
  ASCENDANY_DATABASE_URL="postgresql://${MIGRATOR_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
  ASCENDANY_DATABASE_PASSWORD_FILE="${MIGRATOR_PASSWORD_FILE}" \
  ASCENDANY_DATABASE_ROLE="${SCHEMA_OWNER}" \
  ASCENDANY_DATABASE_SCHEMA=ascendany \
  ASCENDANY_DATABASE_SCHEMA_VERSION=6 \
  ASCENDANY_MIGRATION_HISTORY_TABLE=ascendany.schema_migrations_v2 \
  ASCENDANY_MIGRATION_LOCK_TIMEOUT=30s \
  ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
  "${INSTALLED_RELEASE}/bin/ascendany-migrate" up \
  >/dev/null 2>"${LOG_ROOT}/migrate.json"

# Migrations create new objects. Reapplying the authoritative closure is a
# required part of the database ownership contract, followed by exact verify.
admin_psql "${SOURCE_DATABASE}" --file="${INSTALLED_RELEASE}/db/roles/001_v2_roles.sql" >/dev/null
admin_psql "${SOURCE_DATABASE}" --file="${INSTALLED_RELEASE}/db/roles/verify_v2_roles.sql" >/dev/null

"${PGBOUNCER_BINARY}" -q "${PGBOUNCER_CONFIG_FILE}" \
  >"${LOG_ROOT}/pgbouncer.out" 2>"${LOG_ROOT}/pgbouncer.err" &
PGBOUNCER_PID=$!

pgbouncer_ready=0
for _attempt in {1..120}; do
  if [[ "$(/usr/bin/env PGPASSWORD="${RUNTIME_PASSWORD}" psql \
      -X --no-password --tuples-only --no-align \
      --host="${POOL_HOST}" --port=6432 --username="${RUNTIME_LOGIN}" \
      --dbname="${SOURCE_DATABASE}" --command='SELECT current_user' 2>/dev/null || true)" == "${RUNTIME_LOGIN}" ]]; then
    pgbouncer_ready=1
    break
  fi
  if ! kill -0 "${PGBOUNCER_PID}" 2>/dev/null; then
    sed -n '1,200p' "${LOG_ROOT}/pgbouncer.out" "${LOG_ROOT}/pgbouncer.err" >&2
    fail 'PgBouncer stopped before readiness'
  fi
  sleep 0.5
done
((pgbouncer_ready == 1)) || fail 'PgBouncer did not become ready'
grep -Fx 'pool_mode = transaction' "${PGBOUNCER_CONFIG_FILE}" >/dev/null ||
  fail 'release-owned PgBouncer config is not transaction mode'
grep -Fx 'auth_type = hba' "${PGBOUNCER_CONFIG_FILE}" >/dev/null ||
  fail 'release-owned PgBouncer config does not enforce HBA authentication'
readonly PGBOUNCER_POOL_MODE=transaction
[[ "${PGBOUNCER_POOL_MODE}" == transaction ]] || fail 'PgBouncer is not in transaction mode'

probe_pool_hba_rejection() {
  local user="$1" database="$2" output expected
  expected="psql: error: connection to server at \"${POOL_HOST}\", port 6432 failed: FATAL:  login rejected"
  if output="$(/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
      PGHOST="${POOL_HOST}" PGPORT=6432 PGDATABASE="${database}" PGUSER="${user}" \
      PGCONNECT_TIMEOUT=5 /usr/bin/psql -X --no-password -c 'SELECT 1' 2>&1)"; then
    fail "PgBouncer E2E accepted forbidden ${user} access to ${database}"
  fi
  [[ "${output}" == "${expected}" ]] ||
    fail "PgBouncer E2E did not return the exact HBA rejection for ${user} on ${database}"
}
probe_pool_hba_rejection "${MIGRATOR_LOGIN}" "${SOURCE_DATABASE}"
probe_pool_hba_rejection postgres "${SOURCE_DATABASE}"

prepare_server_environment() {
  local target="$1"
  local write_mode="$2"
  [[ "${write_mode}" == enabled || "${write_mode}" == disabled ]] ||
    fail 'server write mode must be enabled or disabled'
  cp --no-preserve=mode,ownership -- "${INSTALLED_RELEASE}/config/ascendanyd.env" "${target}"
  sed -i \
    "s|^ASCENDANY_RECOMMENDATION_MODEL_PATH=.*$|ASCENDANY_RECOMMENDATION_MODEL_PATH=${INSTALLED_RELEASE}/models/recommendation-model.json|" \
    "${target}"
  [[ "$(grep -Fxc "ASCENDANY_RECOMMENDATION_MODEL_PATH=${INSTALLED_RELEASE}/models/recommendation-model.json" "${target}")" == 1 &&
     "$(grep -Fxc "ASCENDANY_RECOMMENDATION_MODEL_SHA256=${RECOMMENDATION_MODEL_SHA256}" "${target}")" == 1 &&
     "$(grep -Fxc "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=${RELEASE_PURPOSE}" "${target}")" == 1 ]] ||
    fail 'installed server configuration does not bind the exact acceptance model identity'
  {
    printf '\nASCENDANY_HTTP_LISTEN=%s:%s\n' "${API_HOST}" "${API_PORT}"
    printf 'ASCENDANY_DATABASE_URL=postgresql://%s@%s:6432/%s?sslmode=disable\n' "${RUNTIME_LOGIN}" "${POOL_HOST}" "${SOURCE_DATABASE}"
    printf 'ASCENDANY_DATABASE_PASSWORD_FILE=%s\n' "${RUNTIME_PASSWORD_FILE}"
    printf 'ASCENDANY_JWT_SIGNING_KEY_FILE=%s\n' "${JWT_SIGNING_KEY_FILE}"
    printf 'ASCENDANY_PASSWORD_PEPPER_FILE=%s\n' "${PASSWORD_PEPPER_FILE}"
    printf 'ASCENDANY_AUTH_ALLOWED_ORIGINS=http://%s:%s\n' "${API_HOST}" "${API_PORT}"
    printf 'ASCENDANY_ANALYTICS_CONFIG=%s\n' "${INSTALLED_RELEASE}/config/analytics.json"
    printf 'ASCENDANY_ANALYTICS_WORKER_OWNER=e2e-%s-analytics\n' "${TOKEN}"
    printf 'ASCENDANY_ANALYTICS_POLL_INTERVAL=100ms\n'
    printf 'ASCENDANY_FEEDBACK_WORKER_OWNER=e2e-%s-feedback\n' "${TOKEN}"
    printf 'ASCENDANY_FEEDBACK_POLL_INTERVAL=100ms\n'
    printf 'ASCENDANY_CHAT_AGENT_WORKER_OWNER=e2e-%s-chat\n' "${TOKEN}"
    printf 'ASCENDANY_CHAT_AGENT_POLL_INTERVAL=100ms\n'
    printf 'ASCENDANY_IMPORT_WORKER_OWNER=e2e-%s-import\n' "${TOKEN}"
    printf 'ASCENDANY_IMPORT_POLL_INTERVAL=100ms\n'
    printf 'ASCENDANY_JUDGE_SOCKET_DIRECTORY=%s/judge-sockets\n' "${RUNTIME_PARENT}"
    printf 'ASCENDANY_JUDGE_WORKER_USER=%s\n' "$(id -un)"
    printf 'ASCENDANY_JUDGE_SYSTEMCTL_PATH=/usr/bin/false\n'
    printf 'ASCENDANY_JUDGE_WORKER_OWNER=e2e-%s-judge\n' "${TOKEN}"
    printf 'ASCENDANY_JUDGE_POLL_INTERVAL=100ms\n'
    printf 'ASCENDANY_LSP_CONTROL_SOCKET=%s/lsp-control.sock\n' "${RUNTIME_PARENT}"
    printf 'ASCENDANY_LSP_WORKER_USER=%s\n' "$(id -un)"
    printf 'ASCENDANY_LSP_SYSTEMCTL_PATH=/usr/bin/false\n'
    printf 'ASCENDANY_ARTIFACT_ROOT=%s\n' "${ARTIFACT_ROOT}"
    printf 'ASCENDANY_ARTIFACT_RECONCILE_INTERVAL=1h\n'
    printf 'ASCENDANY_WRITE_MODE=%s\n' "${write_mode}"
    printf 'ASCENDANY_DATABASE_MAX_CONNECTIONS=4\n'
    printf 'ASCENDANY_DATABASE_MIN_CONNECTIONS=1\n'
    printf 'ASCENDANY_LOG_LEVEL=info\n'
  } >>"${target}"
  chmod 0600 "${target}"
}

prepare_server_environment "${SMOKE_SERVER_ENV}" disabled
database_fingerprint "${SOURCE_DATABASE}" "${PRE_SMOKE_DATABASE_FINGERPRINT}"
/usr/bin/printf '%s\n' 'Starting the installed server in read-only smoke mode'
/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/bash -c 'set -a; source "$1"; set +a; exec "$2" serve' bash \
  "${SMOKE_SERVER_ENV}" "${INSTALLED_RELEASE}/bin/ascendanyd" \
  >"${SMOKE_SERVER_LOG}" 2>&1 &
SERVER_PID=$!
readonly BASE_URL="http://${API_HOST}:${API_PORT}"
smoke_ready=0
for _attempt in {1..240}; do
  if curl --fail --silent --show-error \
      --header "Origin: ${BASE_URL}" --header 'CF-Connecting-IP: 203.0.113.19' \
      "${BASE_URL}/readyz" >/dev/null 2>&1; then
    smoke_ready=1
    break
  fi
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    jq -c '{level,msg,error,code}' "${SMOKE_SERVER_LOG}" >&2 2>/dev/null || true
    fail 'read-only smoke server stopped before readiness'
  fi
  sleep 0.25
done
((smoke_ready == 1)) || fail 'read-only smoke server did not become ready'
curl --fail --silent --show-error \
  --header "Origin: ${BASE_URL}" --header 'CF-Connecting-IP: 203.0.113.19' \
  "${BASE_URL}/api/v2/capabilities" |
  jq -e '.writesEnabled == false' >/dev/null || fail 'read-only smoke capabilities enabled writes'
smoke_status="$(curl --silent --show-error --output "${SMOKE_MUTATION_RESULT}" --write-out '%{http_code}' \
  --request POST --header 'Content-Type: application/json' \
  --header "Origin: ${BASE_URL}" --header 'CF-Connecting-IP: 203.0.113.19' \
  --data '{"username":"admin","password":"smoke-must-not-authenticate"}' \
  "${BASE_URL}/api/v2/auth/login")"
[[ "${smoke_status}" == 503 ]] || fail 'read-only smoke mutation did not return HTTP 503'
jq -e '.code == "writes_disabled"' "${SMOKE_MUTATION_RESULT}" >/dev/null ||
  fail 'read-only smoke mutation did not return writes_disabled'
kill -TERM "${SERVER_PID}"
wait "${SERVER_PID}"
SERVER_PID=''
database_fingerprint "${SOURCE_DATABASE}" "${POST_SMOKE_DATABASE_FINGERPRINT}"
cmp -s "${PRE_SMOKE_DATABASE_FINGERPRINT}" "${POST_SMOKE_DATABASE_FINGERPRINT}" ||
  fail 'read-only smoke changed an AscendAny base table or sequence'
prepare_server_environment "${SERVER_ENV}" enabled

/usr/bin/env -i \
  PATH=/usr/bin:/bin LC_ALL=C \
  ASCENDANY_DATABASE_URL="postgresql://${RUNTIME_LOGIN}@${POOL_HOST}:6432/${SOURCE_DATABASE}?sslmode=disable" \
  ASCENDANY_DATABASE_POOL_MODE=transaction \
  ASCENDANY_DATABASE_PASSWORD_FILE="${RUNTIME_PASSWORD_FILE}" \
  ASCENDANY_DATABASE_SCHEMA_VERSION=6 \
  ASCENDANY_DATABASE_MAX_CONNECTIONS=1 \
  ASCENDANY_DATABASE_MIN_CONNECTIONS=0 \
  ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
  ASCENDANY_DATABASE_MAX_CONNECTION_LIFETIME=5m \
  ASCENDANY_DATABASE_MAX_CONNECTION_IDLE_TIME=1m \
  ASCENDANY_DATABASE_HEALTH_TIMEOUT=5s \
  ASCENDANY_PASSWORD_PEPPER_FILE="${PASSWORD_PEPPER_FILE}" \
  CREDENTIALS_DIRECTORY="${ADMIN_BOOTSTRAP_CREDENTIAL_ROOT}" \
  "${INSTALLED_RELEASE}/bin/ascendany-admin-bootstrap" create \
    --username admin --display-name 'E2E Administrator' \
  >"${LOG_ROOT}/admin-bootstrap.json" 2>"${LOG_ROOT}/admin-bootstrap.error"
[[ ! -s "${LOG_ROOT}/admin-bootstrap.error" ]] || fail 'administrator bootstrap emitted unexpected stderr'
jq -e '.username == "admin" and .displayName == "E2E Administrator" and .role == "admin" and .authRevision == 1' \
  "${LOG_ROOT}/admin-bootstrap.json" >/dev/null || fail 'administrator bootstrap result is noncanonical'

/usr/bin/printf '%s\n' 'Activating the installed release model with the HTTP runtime stopped'
/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/bash -c 'set -a; source "$1"; set +a; exec "$2" activate-model' bash \
  "${SERVER_ENV}" "${INSTALLED_RELEASE}/bin/ascendanyd" \
  >"${LOG_ROOT}/model-activation.log" 2>&1 || {
    jq -c '{level,msg,error}' "${LOG_ROOT}/model-activation.log" >&2 2>/dev/null || true
    fail 'release model activation failed before the write-enabled runtime started'
  }

mkdir -m 0700 -- "${RUNTIME_PARENT}/judge-sockets"

/usr/bin/printf '%s\n' 'Starting the installed server and exercising the generated SDK business flow'
/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/bash -c 'set -a; source "$1"; set +a; exec "$2" serve' bash \
  "${SERVER_ENV}" "${INSTALLED_RELEASE}/bin/ascendanyd" \
  >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!
server_ready=0
for _attempt in {1..240}; do
  if curl --fail --silent --show-error \
      --header "Origin: ${BASE_URL}" --header 'CF-Connecting-IP: 203.0.113.19' \
      "${BASE_URL}/readyz" >/dev/null 2>&1; then
    server_ready=1
    break
  fi
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    jq -c '{level,msg,error,code}' "${SERVER_LOG}" >&2 2>/dev/null || true
    fail 'installed server stopped before readiness'
  fi
  sleep 0.25
done
((server_ready == 1)) || fail 'installed server did not become ready'

/usr/bin/env -i \
  PATH=/usr/bin:/bin LC_ALL=C \
  ASCENDANY_E2E_BASE_URL="${BASE_URL}" \
  ASCENDANY_E2E_ORIGIN="${BASE_URL}" \
  ASCENDANY_E2E_SNAPSHOT_PATH="${GENERATED_SNAPSHOT_PATH}" \
  ASCENDANY_E2E_ADMIN_PASSWORD_FILE="${ADMIN_PASSWORD_FILE}" \
  ASCENDANY_E2E_EXPECTED_COMMIT="${REQUESTED_COMMIT}" \
  ASCENDANY_E2E_EXPECTED_VERSION="${VERSION}" \
  ASCENDANY_E2E_EXPECTED_MODEL_SHA256="${RECOMMENDATION_MODEL_SHA256}" \
  ASCENDANY_E2E_EXPECTED_MODEL_PURPOSE="${RELEASE_PURPOSE}" \
  ASCENDANY_E2E_KNOWLEDGE_CATALOG_PATH="${CAPTURED_KNOWLEDGE_CATALOG}" \
  ASCENDANY_E2E_EXPECTED_CATALOG_SHA256="${KNOWLEDGE_CATALOG_SHA256}" \
  "${NODE_BINARY}" "${CLIENT_BUNDLE}" >"${CLIENT_RESULT}"
jq -e \
  --arg model_sha256 "${RECOMMENDATION_MODEL_SHA256}" \
  --arg model_purpose "${RELEASE_PURPOSE}" \
  --arg catalog_sha256 "${KNOWLEDGE_CATALOG_SHA256}" '
  .schema == "ascendany.full-e2e.client.v1" and
  .releaseVerified == true and .importStatus == "succeeded" and
  .importReplayConverged == true and .analyticsStatus == "succeeded" and
  .typedDomainDuplicateStatus == "superseded" and
  .newSnapshotStatus == "succeeded" and .snapshotSequence == 2 and
  .enrollmentSingleUse == true and .studentAnalyticsState == "ready" and
  .leaderboardState == "ready" and .examCount == 1 and
  .knowledgeCatalogPublished == true and
  .knowledgeCatalogSha256 == $catalog_sha256 and
  .recommendationState == "fresh" and
  .recommendationResultSchema == "ascendany.recommendation.inference-result.v1" and
  .recommendationOutputCount > 0 and .recommendationOrdered == true and
  .model.purpose == $model_purpose and
  .model.artifactSha256 == $model_sha256 and
  .model.knowledgeCatalogSha256 == $catalog_sha256
' "${CLIENT_RESULT}" >/dev/null || fail 'generated SDK business-flow evidence is incomplete'

"${NODE_BINARY}" "${REPOSITORY_ROOT}/tools/v2-full-e2e-app-smoke.mjs" \
  --server-base-url "${BASE_URL}/" >"${APP_RESULT}"
jq -e '
  .schema == "ascendany.full-e2e.app-smoke.v1" and
  .site == true and .web == true and .importConsole == true and
  .mobilePreview == true and .electronRenderer == true
' "${APP_RESULT}" >/dev/null || fail 'first-party application smoke evidence is incomplete'

kill -TERM "${SERVER_PID}"
wait "${SERVER_PID}"
SERVER_PID=''
EXPECTED_APPLICATION_BUILD_TIME="$(jq -er '.model.applicationBuildTime' "${CLIENT_RESULT}")" ||
  fail 'client evidence lacks the active application build-time provenance'
readonly EXPECTED_APPLICATION_BUILD_TIME
jq -S '.model' "${CLIENT_RESULT}" >"${API_MODEL_PROVENANCE}"
chmod 0600 "${API_MODEL_PROVENANCE}"
assert_database_model_state "${SOURCE_DATABASE}" "${SOURCE_MODEL_STATE}" "${EXPECTED_APPLICATION_BUILD_TIME}"
jq -S '.model' "${SOURCE_MODEL_STATE}" >"${SOURCE_MODEL_PROVENANCE}"
chmod 0600 "${SOURCE_MODEL_PROVENANCE}"
cmp --silent -- "${API_MODEL_PROVENANCE}" "${SOURCE_MODEL_PROVENANCE}" ||
  fail 'generated SDK model provenance differs from the exact source SQL state'
database_fingerprint "${SOURCE_DATABASE}" "${SOURCE_FINGERPRINT}"

/usr/bin/printf '%s\n' 'Creating, verifying, and restoring a backup of the exercised business database'
if ! run_with_private_runtime_root \
    "${BACKUP_RUNTIME_ROOT}" /run/ascendany-backup \
    /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C TZ=UTC \
    ASCENDANY_DATABASE_URL="postgresql://${BACKUP_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_DATABASE_PASSWORD_FILE="${BACKUP_PASSWORD_FILE}" \
    ASCENDANY_ARTIFACT_ROOT="${ARTIFACT_ROOT}" \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_RUNTIME_ROOT=/run/ascendany-backup \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_BACKUP_RETAIN_DAILY=1 \
    ASCENDANY_BACKUP_RETAIN_WEEKLY=0 \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT=30m \
    ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
    ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
    ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
    "${INSTALLED_RELEASE}/bin/ascendany-backup" create \
    >/dev/null 2>"${CREATE_LOG}"; then
  fail 'backup create failed'
fi
assert_single_backup_log "${CREATE_LOG}" 'backup published'
[[ -z "$(find "${BACKUP_RUNTIME_ROOT}" -mindepth 1 -maxdepth 1 -print)" ]] ||
  fail 'backup runtime retained a private credential'
rmdir -- "${BACKUP_RUNTIME_ROOT}"
readonly BACKUP_ID="$(jq -er '.backupId' "${CREATE_LOG}")"
readonly BACKUP_MANIFEST_SHA256="$(jq -er '.manifestSHA256' "${CREATE_LOG}")"

if ! /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT=30m \
    ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
    ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
    ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
    "${INSTALLED_RELEASE}/bin/ascendany-backup" verify "${BACKUP_ID}" \
    >/dev/null 2>"${VERIFY_LOG}"; then
  fail 'credential-free backup verification failed'
fi
assert_single_backup_log "${VERIFY_LOG}" 'backup verified'
[[ "$(jq -er '.manifestSHA256' "${VERIFY_LOG}")" == "${BACKUP_MANIFEST_SHA256}" ]] ||
  fail 'verified backup manifest digest changed'
[[ "$(find "${BACKUP_ROOT}/${BACKUP_ID}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort | tr '\n' ' ')" == \
  'artifacts.tar.zst database.dump manifest.json manifest.sha256 ' ]] ||
  fail 'backup bundle has an unexpected entry set'

role_psql "${RESTORE_LOGIN}" "${RESTORE_PASSWORD_FILE}" postgres \
  --command="CREATE DATABASE ${SCRATCH_DATABASE} WITH OWNER ${SCHEMA_OWNER} TEMPLATE template0 ENCODING 'UTF8' ALLOW_CONNECTIONS false" \
  >/dev/null
restore_owner_psql --command="REVOKE ALL PRIVILEGES ON DATABASE ${SCRATCH_DATABASE} FROM PUBLIC" >/dev/null
restore_owner_psql --command="GRANT CONNECT ON DATABASE ${SCRATCH_DATABASE} TO ${RESTORE_LOGIN}" >/dev/null
restore_owner_psql --command="ALTER DATABASE ${SCRATCH_DATABASE} WITH ALLOW_CONNECTIONS true" >/dev/null

readonly RESTORE_RUNTIME_VISIBLE="/run/ascendany-restore-verify-${BACKUP_ID}"
readonly RESTORE_RUNTIME_ROOT="${RUNTIME_PARENT}/ascendany-restore-verify-${BACKUP_ID}"
mkdir -m 0700 -- "${RESTORE_RUNTIME_ROOT}"
if ! run_with_private_runtime_root \
    "${RESTORE_RUNTIME_ROOT}" "${RESTORE_RUNTIME_VISIBLE}" \
    /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_RESTORE_DATABASE_URL="postgresql://${RESTORE_LOGIN}@${DIRECT_HOST}:5432/${SCRATCH_DATABASE}?sslmode=disable" \
    ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE="${RESTORE_PASSWORD_FILE}" \
    ASCENDANY_RESTORE_ARTIFACT_ROOT="${RESTORE_ARTIFACT_ROOT}" \
    ASCENDANY_RESTORE_RUNTIME_ROOT="${RESTORE_RUNTIME_VISIBLE}" \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT=30m \
    ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
    ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
    ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
    "${INSTALLED_RELEASE}/bin/ascendany-backup" restore-verify "${BACKUP_ID}" \
    >/dev/null 2>"${RESTORE_LOG}"; then
  fail 'backup restore verification failed'
fi
assert_single_backup_log "${RESTORE_LOG}" 'backup restore verified'
[[ "$(jq -er '.databaseName' "${RESTORE_LOG}")" == "${SCRATCH_DATABASE}" ]] ||
  fail 'restore verifier reported the wrong scratch database'
[[ "$(jq -er '.manifestSHA256' "${RESTORE_LOG}")" == "${BACKUP_MANIFEST_SHA256}" ]] ||
  fail 'restored backup manifest digest changed'
[[ -z "$(find "${RESTORE_RUNTIME_ROOT}" -mindepth 1 -maxdepth 1 -print)" ]] ||
  fail 'restore runtime retained a private credential'

assert_database_model_state "${SCRATCH_DATABASE}" "${RESTORED_MODEL_STATE}" "${EXPECTED_APPLICATION_BUILD_TIME}"
jq -S '.model' "${RESTORED_MODEL_STATE}" >"${RESTORED_MODEL_PROVENANCE}"
chmod 0600 "${RESTORED_MODEL_PROVENANCE}"
cmp --silent -- "${SOURCE_MODEL_STATE}" "${RESTORED_MODEL_STATE}" ||
  fail 'restored migration/model/catalog SQL state differs from the source'
cmp --silent -- "${SOURCE_MODEL_PROVENANCE}" "${RESTORED_MODEL_PROVENANCE}" ||
  fail 'restored model provenance differs from the exact source model provenance'
database_fingerprint "${SCRATCH_DATABASE}" "${RESTORED_FINGERPRINT}"
cmp --silent -- "${SOURCE_FINGERPRINT}" "${RESTORED_FINGERPRINT}" ||
  fail 'restored business database differs from the source at the complete table boundary'
readonly SOURCE_PUBLISHED_ARTIFACT_ROOT="${ARTIFACT_ROOT}/sha256"
readonly RESTORED_PUBLISHED_ARTIFACT_ROOT="${RESTORE_ARTIFACT_ROOT}/sha256"
for published_root in "${SOURCE_PUBLISHED_ARTIFACT_ROOT}" "${RESTORED_PUBLISHED_ARTIFACT_ROOT}"; do
  [[ -d "${published_root}" && ! -L "${published_root}" &&
     "$(stat -Lc '%a' -- "${published_root}")" == 750 ]] ||
    fail 'source/restored published artifact namespace is not one real mode-0750 directory'
done
unset published_root
readonly SOURCE_ARTIFACT_COUNT="$(find "${SOURCE_PUBLISHED_ARTIFACT_ROOT}" -type f | wc -l | tr -d ' ')"
readonly RESTORED_ARTIFACT_COUNT="$(find "${RESTORED_PUBLISHED_ARTIFACT_ROOT}" -type f | wc -l | tr -d ' ')"
[[ "${SOURCE_ARTIFACT_COUNT}" == "${EXPECTED_ARTIFACT_COUNT}" &&
   "${RESTORED_ARTIFACT_COUNT}" == "${EXPECTED_ARTIFACT_COUNT}" ]] ||
  fail 'source/restored artifact entry counts differ from the three import provenance artifacts'
readonly SOURCE_ARTIFACT_FINGERPRINT="${WORK_ROOT}/source-artifacts.fingerprint"
readonly RESTORED_ARTIFACT_FINGERPRINT="${WORK_ROOT}/restored-artifacts.fingerprint"
artifact_fingerprint "${SOURCE_PUBLISHED_ARTIFACT_ROOT}" "${SOURCE_ARTIFACT_FINGERPRINT}"
artifact_fingerprint "${RESTORED_PUBLISHED_ARTIFACT_ROOT}" "${RESTORED_ARTIFACT_FINGERPRINT}"
cmp --silent -- "${SOURCE_ARTIFACT_FINGERPRINT}" "${RESTORED_ARTIFACT_FINGERPRINT}" ||
  fail 'restored import provenance artifacts differ from the source'

restore_owner_psql --command="ALTER DATABASE ${SCRATCH_DATABASE} WITH ALLOW_CONNECTIONS false" >/dev/null
restore_owner_psql --command="DROP DATABASE ${SCRATCH_DATABASE} WITH (FORCE)" >/dev/null
rm -rf --one-file-system -- "${RESTORE_ARTIFACT_ROOT}"
rmdir -- "${RESTORE_RUNTIME_ROOT}"

[[ "$(git -C "${REPOSITORY_ROOT}" rev-parse HEAD)" == "${REQUESTED_COMMIT}" &&
   -z "$(git -C "${REPOSITORY_ROOT}" status --porcelain=v1 --untracked-files=all)" ]] ||
  fail 'full E2E mutated the reviewed checkout'

/usr/bin/printf \
  'FULL_E2E_RESULT release_purpose=acceptance_test production_installer_rejection=purpose_gate release_manifest_verified=true acceptance_installer_verified=true postgres_major=17 pgbouncer_package=1.25.2-1.fc44 pgbouncer_pool_mode=transaction pgbouncer_auth_type=hba pgbouncer_userlist=v2_runtime_scram_only sdk_generated=true pintia_exporter_checked=true typescript_apps=5 api_import=succeeded import_replay=converged typed_domain_duplicate=superseded new_snapshot_sequence=2 analytics=succeeded enrollment=single_use student_analytics=ready recommendation=fresh recommendation_output=ordered model_catalog_provenance=exact app_http_smoke=5 backup_commands=3 artifact_count=3 restored_database_exact=true restored_model_provenance_exact=true role_closure_reapplied=true sandbox_acceptance=separate_fail_closed_gate\n'
