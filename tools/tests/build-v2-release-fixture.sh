#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly TEST_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly BUILDER_SOURCE="${TEST_ROOT}/tools/build-v2-release.sh"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-release-builder-fixture.XXXXXX")"
readonly FIXTURE_REPOSITORY="${WORK_ROOT}/repository"
readonly FAKE_BIN="${WORK_ROOT}/fake-bin"
readonly ENTRY_ATTACK_BIN="${WORK_ROOT}/entry-attack-bin"
readonly LIVE_TOOL_BIN="${FIXTURE_REPOSITORY}/.live-tool-bin"
readonly PATH_HIJACK_MARKER="${WORK_ROOT}/live-path-tool-executed"
readonly FAKE_BASH_MARKER="${WORK_ROOT}/fake-bash-executed-release-builder"
readonly BASH_ENV_MARKER="${WORK_ROOT}/bash-env-executed-release-builder"
readonly EXPORTED_FUNCTION_MARKER="${WORK_ROOT}/exported-function-executed-release-builder"
readonly SOURCED_FUNCTION_MARKER="${WORK_ROOT}/sourced-function-executed-release-builder"
readonly SOURCED_FILE_WRAPPER="${WORK_ROOT}/source-release-builder-attack.sh"
readonly GIT_PATH_EVAL_MARKER="${WORK_ROOT}/git-path-command-substitution-executed"
readonly OUTPUT_PARENT_SWAP="${WORK_ROOT}/output-parent-swap"
readonly BASH_ENV_ATTACK="${WORK_ROOT}/release-builder-bash-env"
readonly GLOBAL_ATTRIBUTES="${WORK_ROOT}/global-attributes"
readonly GLOBAL_GIT_CONFIG="${WORK_ROOT}/global-git-config"
readonly EXPECTED_PROVENANCE="${WORK_ROOT}/expected-provenance"
readonly WEIRD_PROVENANCE_RELATIVE=$'provenance/name-with-tab\tand-newline\n.bin'
readonly SOURCE_DATE_EPOCH="1700000000"
readonly RECOMMENDATION_MODEL="${WORK_ROOT}/recommendation-model.json"
readonly KNOWLEDGE_CATALOG="${WORK_ROOT}/recommendation-knowledge-catalog.json"

cleanup() {
  if [[ "${ASCENDANY_FIXTURE_KEEP_WORK_ROOT:-0}" == 1 ]]; then
    printf 'fixture work root retained: %s\n' "${WORK_ROOT}" >&2
    return
  fi
  rm -rf -- "${WORK_ROOT}"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

expect_failure() {
  local log_path="$1"
  shift
  if "$@" >"${log_path}" 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

assert_no_private_workspace() {
  local parent="$1"
  if find "${parent}" -mindepth 1 -maxdepth 1 -name '.ascendany-v2-build.*' -print -quit | grep -q .; then
    fail "private release workspace was not cleaned under ${parent}"
  fi
}

install -d -m 0700 \
  "${FIXTURE_REPOSITORY}/tools" \
  "${FIXTURE_REPOSITORY}/backend" \
  "${FIXTURE_REPOSITORY}/packages/sdk/src" \
  "${FIXTURE_REPOSITORY}/deploy/v2/config" \
  "${FIXTURE_REPOSITORY}/deploy/v2/systemd" \
  "${FIXTURE_REPOSITORY}/deploy/v2/systemd/ascendanyd.service.d" \
  "${FIXTURE_REPOSITORY}/deploy/v2/polkit-1/rules.d" \
  "${FIXTURE_REPOSITORY}/deploy/v2/sysusers.d" \
  "${FIXTURE_REPOSITORY}/deploy/v2/tmpfiles.d" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts" \
  "${FIXTURE_REPOSITORY}/contracts/openapi" \
  "${FIXTURE_REPOSITORY}/contracts/pintia" \
  "${FIXTURE_REPOSITORY}/db/roles" \
  "${FIXTURE_REPOSITORY}/provenance" \
  "${EXPECTED_PROVENANCE}/provenance" \
  "${FAKE_BIN}" \
  "${ENTRY_ATTACK_BIN}"
install -m 0755 "${BUILDER_SOURCE}" "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"

printf 'module fixture.invalid/ascendany\n\ngo 1.26\n' >"${FIXTURE_REPOSITORY}/backend/go.mod"
printf '%s\n' \
  'import { productionInitializationFixture } from "../packages/sdk/src/index.ts";' \
  'console.log(productionInitializationFixture);' \
  >"${FIXTURE_REPOSITORY}/tools/v2-production-initialization-client.ts"
printf '%s\n' '{"devDependencies":{"esbuild":"0.25.12"}}' \
  >"${FIXTURE_REPOSITORY}/packages/sdk/package.json"
printf '%s\n' \
  'export const productionInitializationFixture = "reviewed SDK dependency";' \
  >"${FIXTURE_REPOSITORY}/packages/sdk/src/index.ts"
printf 'fixture release readme\n' >"${FIXTURE_REPOSITORY}/deploy/v2/README.md"
printf 'fixture judge contract\n' >"${FIXTURE_REPOSITORY}/deploy/v2/OJ_JUDGE_CONTRACT.md"
printf 'fixture lsp contract\n' >"${FIXTURE_REPOSITORY}/deploy/v2/LSP_CONTROL_CONTRACT.md"
printf 'openapi: 3.1.0\n' >"${FIXTURE_REPOSITORY}/contracts/openapi/ascendany-v2.yaml"
printf '{"fixture":true}\n' >"${FIXTURE_REPOSITORY}/contracts/pintia/ascendany.pintia.snapshot.v2.schema.json"
printf 'fixture role contract\n' >"${FIXTURE_REPOSITORY}/db/roles/README.md"
printf 'fixture role bootstrap\n' >"${FIXTURE_REPOSITORY}/db/roles/001_v2_roles.sql"
printf 'fixture role verification\n' >"${FIXTURE_REPOSITORY}/db/roles/verify_v2_roles.sql"
for config in analytics.json.example ascendanyd-read-only-smoke.env.example backup.env.example judge.env.example migrate.env.example restore.env.example; do
  printf 'fixture configuration %s\n' "${config}" >"${FIXTURE_REPOSITORY}/deploy/v2/config/${config}"
done
printf '%s\n' \
  'ASCENDANY_RECOMMENDATION_MODEL_PATH=/opt/ascendany/v2/models/recommendation-model.json' \
  'ASCENDANY_RECOMMENDATION_MODEL_SHA256=__ASCENDANY_RECOMMENDATION_MODEL_SHA256__' \
  'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=__ASCENDANY_RECOMMENDATION_MODEL_PURPOSE__' \
  'ASCENDANY_KNOWLEDGE_CATALOG_PATH=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json' \
  'ASCENDANY_KNOWLEDGE_CATALOG_SHA256=__ASCENDANY_KNOWLEDGE_CATALOG_SHA256__' \
  >"${FIXTURE_REPOSITORY}/deploy/v2/config/ascendanyd.env.example"
cp -- "${FIXTURE_REPOSITORY}/deploy/v2/config/ascendanyd.env.example" \
  "${FIXTURE_REPOSITORY}/deploy/v2/config/catalog-publish.env.example"
printf 'fixture judge image lock\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/judge-image-lock.json"
printf 'fixture judge compiler inventory\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/judge-compiler-rootfs.inventory"
printf 'fixture judge image Containerfile\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/judge-images.Containerfile"
printf 'fixture cloudflared configuration\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/cloudflared.yaml"
printf 'fixture Fedora runtime package lock\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/fedora-runtime-packages.json"
printf 'fixture pgbouncer hba\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/pgbouncer-hba.conf"
printf 'fixture pgbouncer configuration\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/pgbouncer.ini"
printf 'fixture PostgreSQL hba\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/postgresql-hba.conf"
printf 'fixture PostgreSQL ident\n' >"${FIXTURE_REPOSITORY}/deploy/v2/config/postgresql-ident.conf"
for unit in ascendanyd.service ascendany-model-register.service ascendany-model-activate.service ascendany-catalog-publish.service ascendany-admin-bootstrap.service ascendany-backup.service ascendany-backup.timer ascendany-cloudflared.service 'ascendany-judge@.service' 'ascendany-lsp@.service' ascendany-migrate.service ascendany-pgbouncer.service 'ascendany-restore-verify@.service'; do
  printf 'fixture unit %s\n' "${unit}" >"${FIXTURE_REPOSITORY}/deploy/v2/systemd/${unit}"
done
printf '%s\n' \
  '[Service]' \
  'EnvironmentFile=' \
  'EnvironmentFile=/etc/ascendany/v2/ascendanyd.env' \
  'EnvironmentFile=/etc/ascendany/v2/ascendanyd-read-only-smoke.env' \
  >"${FIXTURE_REPOSITORY}/deploy/v2/systemd/ascendanyd.service.d/40-read-only-smoke.conf"
printf 'fixture judge policy\n' >"${FIXTURE_REPOSITORY}/deploy/v2/polkit-1/rules.d/60-ascendany-judge.rules"
printf 'fixture lsp policy\n' >"${FIXTURE_REPOSITORY}/deploy/v2/polkit-1/rules.d/61-ascendany-lsp.rules"
printf 'fixture sysusers\n' >"${FIXTURE_REPOSITORY}/deploy/v2/sysusers.d/ascendany-v2.conf"
printf 'fixture tmpfiles\n' >"${FIXTURE_REPOSITORY}/deploy/v2/tmpfiles.d/ascendany-v2.conf"
printf '#!/usr/bin/env bash\nprintf cloudflared-validator\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/validate-cloudflared.sh"
printf '#!/usr/bin/env bash\nprintf production-validator\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/validate-production.sh"
printf '#!/usr/bin/env bash\nprintf server-release-installer\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/install-v2-release.sh"
printf '#!/usr/bin/env bash\nprintf judge-image-acquirer\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/acquire-judge-image.sh"
printf '#!/usr/bin/env bash\nprintf judge-image-attester\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/attest-judge-image.sh"
printf '#!/usr/bin/env bash\nprintf judge-image-contract\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/judge-image-contract.sh"
printf '#!/usr/bin/env bash\nprintf judge-image-preloader\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/preload-judge-image.sh"
printf '#!/usr/bin/env bash\nprintf pgbouncer-rpm-acquirer\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/acquire-pgbouncer-rpm.sh"
printf '#!/usr/bin/env bash\nprintf pgbouncer-rpm-attester\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/attest-pgbouncer-rpm.sh"
printf '#!/usr/bin/env bash\nprintf postgres-pgbouncer-provisioner\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/provision-postgres-pgbouncer.sh"
printf '#!/usr/bin/env bash\nprintf postgres-schema-fingerprint\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/postgres-schema-fingerprint.sh"
printf '#!/usr/bin/env bash\nprintf restore-operator\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/restore-verify-operator.sh"
printf '#!/usr/bin/env bash\nprintf restore-publisher\n' >"${FIXTURE_REPOSITORY}/deploy/v2/scripts/publish-restore-evidence.sh"
printf 'literal hostile Git path\n' > \
  "${FIXTURE_REPOSITORY}/\$(printf injected >\$FIXTURE_GIT_PATH_EVAL_MARKER)"
printf '%s\n' \
  'deploy/v2/README.md export-ignore' \
  'provenance/crlf.txt text eol=lf' > \
  "${FIXTURE_REPOSITORY}/.gitattributes"
printf 'first\r\nsecond\r\n' >"${FIXTURE_REPOSITORY}/provenance/crlf.txt"
printf '\x00\x01\x7f\x80\xffbinary\x00payload\n' >"${FIXTURE_REPOSITORY}/provenance/binary.bin"
printf 'no-final-newline' >"${FIXTURE_REPOSITORY}/provenance/no-final-newline.txt"
printf 'preserve-two-trailing-newlines\n\n' >"${FIXTURE_REPOSITORY}/provenance/trailing-newlines.txt"
printf '#!/usr/bin/env bash\nprintf provenance-mode\n' >"${FIXTURE_REPOSITORY}/provenance/executable.sh"
printf 'tab, newline, and NUL-safe index path\x00\n' > \
  "${FIXTURE_REPOSITORY}/${WEIRD_PROVENANCE_RELATIVE}"
install -m 0644 \
  "${FIXTURE_REPOSITORY}/provenance/crlf.txt" \
  "${EXPECTED_PROVENANCE}/provenance/crlf.txt"
install -m 0644 \
  "${FIXTURE_REPOSITORY}/provenance/binary.bin" \
  "${EXPECTED_PROVENANCE}/provenance/binary.bin"
install -m 0644 \
  "${FIXTURE_REPOSITORY}/provenance/no-final-newline.txt" \
  "${EXPECTED_PROVENANCE}/provenance/no-final-newline.txt"
install -m 0644 \
  "${FIXTURE_REPOSITORY}/provenance/trailing-newlines.txt" \
  "${EXPECTED_PROVENANCE}/provenance/trailing-newlines.txt"
install -m 0644 \
  "${FIXTURE_REPOSITORY}/${WEIRD_PROVENANCE_RELATIVE}" \
  "${EXPECTED_PROVENANCE}/provenance/weird-path-content.bin"
chmod 0755 \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/publish-restore-evidence.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/restore-verify-operator.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/install-v2-release.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/acquire-judge-image.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/attest-judge-image.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/judge-image-contract.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/preload-judge-image.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/acquire-pgbouncer-rpm.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/attest-pgbouncer-rpm.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/provision-postgres-pgbouncer.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/postgres-schema-fingerprint.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/validate-cloudflared.sh" \
  "${FIXTURE_REPOSITORY}/deploy/v2/scripts/validate-production.sh" \
  "${FIXTURE_REPOSITORY}/provenance/executable.sh"

git -C "${FIXTURE_REPOSITORY}" init --quiet
git -C "${FIXTURE_REPOSITORY}" config user.name 'AscendAny release fixture'
git -C "${FIXTURE_REPOSITORY}" config user.email 'release-fixture@example.invalid'
git -C "${FIXTURE_REPOSITORY}" -c core.safecrlf=false add .
raw_crlf_blob="$(
  git -C "${FIXTURE_REPOSITORY}" hash-object \
    -w --no-filters -- provenance/crlf.txt
)"
git -C "${FIXTURE_REPOSITORY}" update-index \
  --add --cacheinfo "100644,${raw_crlf_blob},provenance/crlf.txt"
unset raw_crlf_blob
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: committed release source'
readonly COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"

readonly FIXTURE_ESBUILD="${FIXTURE_REPOSITORY}/node_modules/.pnpm/esbuild@0.25.12/node_modules/esbuild/bin/esbuild"
install -d -m 0700 -- "${FIXTURE_ESBUILD%/*}"
cat >"${FIXTURE_ESBUILD}" <<'FAKE_ESBUILD'
#!/usr/bin/bash -p
set -Eeuo pipefail
if [[ "$#" == 1 && "$1" == --version ]]; then
  printf '%s\n' '0.25.12'
  exit 0
fi
[[ -z "${BASH_ENV+x}" && -z "${ENV+x}" && "${LC_ALL:-}" == C && "${HOME:-}" == /* ]] || exit 65
[[ "$#" == 11 && "$1" == tools/v2-production-initialization-client.ts ]] || exit 65
[[ "$PWD" == */source ]] || exit 65
readonly expected_client=$'import { productionInitializationFixture } from "../packages/sdk/src/index.ts";\nconsole.log(productionInitializationFixture);'
[[ "$(<"$1")" == "$expected_client" ]] || exit 65
[[ "$(<packages/sdk/src/index.ts)" == 'export const productionInitializationFixture = "reviewed SDK dependency";' ]] || exit 65
readonly expected_flags=$'--bundle\n--platform=node\n--format=esm\n--target=node22.22\n--packages=bundle\n--tree-shaking=true\n--charset=utf8\n--legal-comments=none\n--log-level=error'
actual_flags="$(printf '%s\n' "${@:2:9}")"
[[ "$actual_flags" == "$expected_flags" ]] || exit 65
[[ "${11}" == --outfile=/*/release/operators/ascendany-production-initialize.mjs ]] || exit 65
printf '%s\n' 'console.log("bundled reviewed production initialization client");' >"${11#--outfile=}"
FAKE_ESBUILD
chmod 0755 "${FIXTURE_ESBUILD}"

printf 'deploy/v2/README.md export-ignore\n' > \
  "${FIXTURE_REPOSITORY}/.git/info/attributes"
printf 'deploy/v2/README.md export-ignore\n' > \
  "${GLOBAL_ATTRIBUTES}"
printf '[core]\n\tattributesFile = %s\n' "${GLOBAL_ATTRIBUTES}" >"${GLOBAL_GIT_CONFIG}"

cat >"${ENTRY_ATTACK_BIN}/bash" <<'FAKE_ENTRY_BASH'
#!/usr/bin/bash -p
if [[ -n "${FIXTURE_RELEASE_BUILDER_PATH:-}" &&
      "${1:-}" == "${FIXTURE_RELEASE_BUILDER_PATH}" ]]; then
  builtin printf 'fake bash reached release builder\n' >"${FIXTURE_FAKE_BASH_MARKER:?}"
fi
exec /usr/bin/bash -p "$@"
FAKE_ENTRY_BASH
chmod 0755 "${ENTRY_ATTACK_BIN}/bash"

cat >"${BASH_ENV_ATTACK}" <<'BASH_ENV_ATTACK_BODY'
if [[ "$0" == "${FIXTURE_RELEASE_BUILDER_PATH:-}" ]]; then
  builtin printf 'BASH_ENV reached release builder\n' >"${FIXTURE_BASH_ENV_MARKER:?}"
fi
BASH_ENV_ATTACK_BODY
chmod 0600 "${BASH_ENV_ATTACK}"

install -d -m 0700 "${LIVE_TOOL_BIN}"
cat >"${LIVE_TOOL_BIN}/go" <<'LIVE_GO'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'live worktree Go executed\n' >"${FIXTURE_PATH_HIJACK_MARKER:?}"
exit 97
LIVE_GO
chmod 0755 "${LIVE_TOOL_BIN}/go"
for attacked_tool in git jq; do
  cat >"${LIVE_TOOL_BIN}/${attacked_tool}" <<'LIVE_PATH_TOOL'
#!/usr/bin/bash -p
set -Eeuo pipefail
printf 'live worktree release tool executed\n' >"${FIXTURE_PATH_HIJACK_MARKER:?}"
exit 97
LIVE_PATH_TOOL
  chmod 0755 "${LIVE_TOOL_BIN}/${attacked_tool}"
done
unset attacked_tool

cat >"${FAKE_BIN}/go" <<'FAKE_GO'
#!/usr/bin/bash -p
set -Eeuo pipefail

readonly fixture_work_root="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
readonly fixture_repository="${fixture_work_root}/repository"

while IFS= read -r -d '' inherited_environment_entry; do
  case "${inherited_environment_entry%%=*}" in
    BASH_FUNC_*%%)
      printf 'fake Go child inherited a caller shell function\n' >&2
      exit 65
      ;;
  esac
done < <(/usr/bin/env -0)
[[ -z "${BASH_ENV+x}" && -z "${ENV+x}" ]] || {
  printf 'fake Go child inherited a shell startup hook\n' >&2
  exit 65
}
[[ -z "${GOROOT+x}" && -z "${GOTOOLDIR+x}" && -z "${GOCACHEPROG+x}" ]] || {
  printf 'fake Go child inherited an ambient toolchain redirect\n' >&2
  exit 65
}

if [[ "${1:-}" == "env" && "${2:-}" == "GOVERSION" && "$#" == "2" ]]; then
  [[ "${GOTOOLCHAIN:-}" == "local" && "${GOENV:-}" == "off" ]] || exit 65
  if [[ -f "${fixture_work_root}/fake-go-version" ]]; then
    printf '%s\n' "$(<"${fixture_work_root}/fake-go-version")"
  else
    printf '%s\n' 'go1.26.0'
  fi
  exit 0
fi
if [[ "${1:-}" == "env" && "${2:-}" == "GOEXPERIMENT" && "$#" == "2" ]]; then
  [[ "${GOTOOLCHAIN:-}" == "local" && "${GOENV:-}" == "off" &&
     "${GOEXPERIMENT+x}" == "x" && -z "${GOEXPERIMENT}" ]] || exit 65
  printf '%s' ''
  exit 0
fi
if [[ "${1:-}" == "mod" && "${2:-}" == "verify" && "$#" == "2" ]]; then
  [[ "${GOTOOLCHAIN:-}" == "local" && "${GOENV:-}" == "off" &&
     "${GOWORK:-}" == "off" && "${GOFLAGS+x}" == "x" && -z "${GOFLAGS}" &&
     "${GOEXPERIMENT+x}" == "x" && -z "${GOEXPERIMENT}" &&
     "${GOPROXY:-}" == "off" ]] || exit 65
  exit 0
fi
if [[ "${1:-}" != "build" ]]; then
  printf 'unexpected fake go invocation: %s\n' "$*" >&2
  exit 64
fi
readonly detached_source_root="${PWD%/backend}"
readonly weird_provenance_relative=$'provenance/name-with-tab\tand-newline\n.bin'
for provenance_file in crlf.txt binary.bin no-final-newline.txt trailing-newlines.txt; do
  /usr/bin/cmp -s \
    "${detached_source_root}/provenance/${provenance_file}" \
    "${fixture_work_root}/expected-provenance/provenance/${provenance_file}" || {
      printf 'detached provenance bytes drifted: %s\n' "${provenance_file}" >&2
      exit 65
    }
done
/usr/bin/cmp -s \
  "${detached_source_root}/${weird_provenance_relative}" \
  "${fixture_work_root}/expected-provenance/provenance/weird-path-content.bin" || {
    printf 'detached provenance path with control characters drifted\n' >&2
    exit 65
  }
[[ "$(/usr/bin/stat -Lc '%a' -- "${detached_source_root}/provenance/executable.sh")" == 755 ]] || {
  printf 'detached provenance executable mode drifted\n' >&2
  exit 65
}
[[ "${GOTOOLCHAIN:-}" == "local" && "${GOENV:-}" == "off" &&
   "${GOWORK:-}" == "off" && "${GOFLAGS+x}" == "x" && -z "${GOFLAGS}" &&
   "${GOEXPERIMENT+x}" == "x" && -z "${GOEXPERIMENT}" &&
   "${GOPROXY:-}" == "off" && "${CGO_ENABLED:-}" == "0" &&
   "${GOOS:-}" == "linux" && "${GOARCH:-}" == "amd64" &&
   "${GOAMD64:-}" == "v1" && "${GOFIPS140:-}" == "off" &&
   " $* " == *" -mod=readonly "* ]] || exit 65

output=""
previous=""
for argument in "$@"; do
  if [[ "${previous}" == "-o" ]]; then
    output="${argument}"
    break
  fi
  previous="${argument}"
done
if [[ -z "${output}" ]]; then
  printf 'fake go build received no output path\n' >&2
  exit 64
fi

stat -Lc '%a' "${PWD}/.." >"${fixture_work_root}/source-mode"
printf 'dirty during build\n' > \
  "${fixture_repository}/deploy/v2/README.md"

if [[ "${*: -1}" == './cmd/ascendany-model' ]]; then
  cat >"${output}" <<'MODEL_VERIFIER'
#!/usr/bin/bash -p
set -Eeuo pipefail
case "$1" in
  verify)
    [[ "$#" == 7 && "$2" == --model && "$4" == --sha256 && "$6" == --expected-purpose ]]
    [[ "$(/usr/bin/sha256sum -- "$3" | /usr/bin/awk '{print $1}')" == "$5" ]]
    [[ "$(/usr/bin/jq -er '.manifest.purpose' "$3")" == "$7" ]] || {
      /usr/bin/printf 'model purpose differs from expected purpose\n' >&2
      exit 1
    }
    ;;
  verify-catalog)
    [[ "$#" == 11 && "$2" == --catalog && "$4" == --catalog-sha256 && "$6" == --model && "$8" == --model-sha256 && "${10}" == --expected-purpose ]]
    [[ "$(/usr/bin/sha256sum -- "$3" | /usr/bin/awk '{print $1}')" == "$5" ]]
    [[ "$(/usr/bin/sha256sum -- "$7" | /usr/bin/awk '{print $1}')" == "$9" ]]
    [[ "$(/usr/bin/jq -er '.manifest.purpose' "$7")" == "${11}" ]]
    ;;
  *) exit 64 ;;
esac
MODEL_VERIFIER
else
  printf 'fixture binary for %s\n' "${*: -1}" >"${output}"
fi
chmod 0755 "${output}"
if [[ -f "${fixture_work_root}/extra-payload" ]]; then
  printf 'unexpected payload\n' >"$(dirname -- "${output}")/unexpected"
fi
if [[ "${*: -1}" == './cmd/ascendany-release-ops' &&
      -f "${fixture_work_root}/race-target" ]]; then
  race_target="$(<"${fixture_work_root}/race-target")"
  mkdir -- "${race_target}"
  printf 'racing owner marker\n' >"${race_target}/marker"
fi
if [[ "${*: -1}" == './cmd/ascendany-release-ops' &&
      -f "${fixture_work_root}/output-parent-swap" ]]; then
  swap_parent="$(<"${fixture_work_root}/output-parent-swap")"
  mv -- "${swap_parent}" "${swap_parent}.moved"
  install -d -m 0700 -- "${swap_parent}"
fi
FAKE_GO
chmod 0755 "${FAKE_BIN}/go"

printf '%s' '{"manifest":{"purpose":"production"}}' >"${RECOMMENDATION_MODEL}"
chmod 0400 "${RECOMMENDATION_MODEL}"
readonly RECOMMENDATION_MODEL_SHA256="$(sha256sum -- "${RECOMMENDATION_MODEL}" | awk '{print $1}')"
printf '%s' '{"fixture":"catalog"}' >"${KNOWLEDGE_CATALOG}"
chmod 0400 "${KNOWLEDGE_CATALOG}"
readonly KNOWLEDGE_CATALOG_SHA256="$(sha256sum -- "${KNOWLEDGE_CATALOG}" | awk '{print $1}')"

readonly -a PAYLOAD_PATHS=(
  bin/ascendanyd
  bin/ascendany-admin-bootstrap
  bin/ascendany-backup
  bin/ascendany-catalog-publish
  bin/ascendany-judge
  bin/ascendany-lsp
  bin/ascendany-migrate
  bin/ascendany-model
  bin/ascendany-release-ops
  models/recommendation-model.json
  models/recommendation-knowledge-catalog.json
  operators/ascendany-production-initialize.mjs
  README.md
  OJ_JUDGE_CONTRACT.md
  LSP_CONTROL_CONTRACT.md
  contracts/openapi/ascendany-v2.yaml
  contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
  db/roles/README.md
  db/roles/001_v2_roles.sql
  db/roles/verify_v2_roles.sql
  config/analytics.json
  config/ascendanyd.env
  config/ascendanyd-read-only-smoke.env
  config/backup.env
  config/catalog-publish.env
  config/cloudflared.yaml
  config/fedora-runtime-packages.json
  config/judge.env
  config/judge-compiler-rootfs.inventory
  config/judge-image-lock.json
  config/judge-images.Containerfile
  config/migrate.env
  config/pgbouncer-hba.conf
  config/pgbouncer.ini
  config/postgresql-hba.conf
  config/postgresql-ident.conf
  config/restore.env
  systemd/ascendanyd.service
  systemd/ascendany-model-register.service
  systemd/ascendany-model-activate.service
  systemd/ascendany-catalog-publish.service
  systemd/ascendanyd.service.d/40-read-only-smoke.conf
  systemd/ascendany-admin-bootstrap.service
  systemd/ascendany-backup.service
  systemd/ascendany-backup.timer
  systemd/ascendany-cloudflared.service
  systemd/ascendany-judge@.service
  systemd/ascendany-lsp@.service
  systemd/ascendany-migrate.service
  systemd/ascendany-pgbouncer.service
  systemd/ascendany-restore-verify@.service
  polkit-1/rules.d/60-ascendany-judge.rules
  polkit-1/rules.d/61-ascendany-lsp.rules
  sysusers.d/ascendany-v2.conf
  tmpfiles.d/ascendany-v2.conf
  scripts/publish-restore-evidence.sh
  scripts/restore-verify-operator.sh
  scripts/install-v2-release.sh
  scripts/acquire-judge-image.sh
  scripts/attest-judge-image.sh
  scripts/judge-image-contract.sh
  scripts/preload-judge-image.sh
  scripts/acquire-pgbouncer-rpm.sh
  scripts/attest-pgbouncer-rpm.sh
  scripts/provision-postgres-pgbouncer.sh
  scripts/postgres-schema-fingerprint.sh
  scripts/validate-cloudflared.sh
  scripts/validate-production.sh
)

run_builder() {
  local output="$1"
  local requested_commit="${FIXTURE_REQUESTED_COMMIT:-${COMMIT}}"
  local builder_path="${FIXTURE_BUILDER_PATH:-${FIXTURE_REPOSITORY}/tools/build-v2-release.sh}"
  shift
  env \
    PATH="${ENTRY_ATTACK_BIN}:${LIVE_TOOL_BIN}:${FAKE_BIN}:${PATH}" \
    BASH_ENV="${BASH_ENV_ATTACK}" \
    GOROOT="${WORK_ROOT}/ambient-goroot-must-be-cleared" \
    GOCACHEPROG="${WORK_ROOT}/ambient-gocacheprog-must-be-cleared" \
    GIT_CONFIG_GLOBAL="${GLOBAL_GIT_CONFIG}" \
    FIXTURE_RELEASE_BUILDER_PATH="${builder_path}" \
    FIXTURE_FAKE_BASH_MARKER="${FAKE_BASH_MARKER}" \
    FIXTURE_BASH_ENV_MARKER="${BASH_ENV_MARKER}" \
    FIXTURE_EXPORTED_FUNCTION_MARKER="${EXPORTED_FUNCTION_MARKER}" \
    FIXTURE_GIT_PATH_EVAL_MARKER="${GIT_PATH_EVAL_MARKER}" \
    'BASH_FUNC_cd%%=() { builtin printf "exported cd function reached release builder\n" >"${FIXTURE_EXPORTED_FUNCTION_MARKER:?}"; builtin cd "$@"; }' \
    FIXTURE_PATH_HIJACK_MARKER="${PATH_HIJACK_MARKER}" \
    "$@" \
    "${builder_path}" \
      --version 1.2.3 \
      --commit "${requested_commit}" \
      --source-date-epoch "${SOURCE_DATE_EPOCH}" \
      --go-path "${FAKE_BIN}/go" \
      --go-version go1.26.0 \
      --goos linux \
      --goarch amd64 \
      --goamd64 v1 \
      --release-purpose "${FIXTURE_RELEASE_PURPOSE:-production}" \
      --recommendation-model "${RECOMMENDATION_MODEL}" \
      --recommendation-model-sha256 "${RECOMMENDATION_MODEL_SHA256}" \
      --knowledge-catalog "${KNOWLEDGE_CATALOG}" \
      --knowledge-catalog-sha256 "${KNOWLEDGE_CATALOG_SHA256}" \
      --output "${output}"
}

git -C "${FIXTURE_REPOSITORY}" update-index --chmod=-x tools/build-v2-release.sh
git -C "${FIXTURE_REPOSITORY}" commit --quiet \
  --message 'fixture: drift server release builder mode'
readonly MODE_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" update-index --chmod=+x tools/build-v2-release.sh

readonly BUILDER_TEST_PARENT="${WORK_ROOT}/builder-test-parent"
install -d -m 0700 "${BUILDER_TEST_PARENT}"
FIXTURE_REQUESTED_COMMIT="${MODE_DRIFT_COMMIT}" \
  expect_failure "${WORK_ROOT}/commit-builder-mode.log" \
    run_builder "${BUILDER_TEST_PARENT}/commit-builder-mode"
rg --quiet 'exactly one mode 100755 blob' "${WORK_ROOT}/commit-builder-mode.log" ||
  {
    sed -n '1,80p' "${WORK_ROOT}/commit-builder-mode.log" >&2
    fail 'server release builder accepted a non-executable reviewed builder entry'
  }

/usr/bin/printf '\n# dirty live server builder\n' >> \
  "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"
expect_failure "${WORK_ROOT}/dirty-builder.log" \
  run_builder "${BUILDER_TEST_PARENT}/dirty-builder"
rg --quiet 'builder bytes differ from the reviewed commit' "${WORK_ROOT}/dirty-builder.log" ||
  fail 'server release builder accepted dirty live builder bytes'
install -m 0755 "${BUILDER_SOURCE}" "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"

chmod 0700 "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"
expect_failure "${WORK_ROOT}/live-builder-mode.log" \
  run_builder "${BUILDER_TEST_PARENT}/live-builder-mode"
rg --quiet 'root/release-user owned mode 0755' "${WORK_ROOT}/live-builder-mode.log" ||
  fail 'server release builder accepted a live builder outside exact mode 0755'
chmod 0755 "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"

mv -- "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh" \
  "${FIXTURE_REPOSITORY}/tools/build-v2-release.real"
ln -s build-v2-release.real "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"
expect_failure "${WORK_ROOT}/symlink-builder.log" \
  run_builder "${BUILDER_TEST_PARENT}/symlink-builder"
rg --quiet 'canonical repository tools/build-v2-release.sh file' "${WORK_ROOT}/symlink-builder.log" ||
  fail 'server release builder accepted a symlink at its fixed live path'
rm -- "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"
mv -- "${FIXTURE_REPOSITORY}/tools/build-v2-release.real" \
  "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"

readonly HAPPY_PARENT="${WORK_ROOT}/happy-parent"
readonly HAPPY_OUTPUT="${HAPPY_PARENT}/release"
readonly SOURCE_MODE_FILE="${WORK_ROOT}/source-mode"
install -d -m 0700 "${HAPPY_PARENT}"
printf 'dirty before build\n' >"${FIXTURE_REPOSITORY}/deploy/v2/README.md"
printf '%s\n' 'console.log("dirty live production initialization client");' \
  >"${FIXTURE_REPOSITORY}/tools/v2-production-initialization-client.ts"
printf '%s\n' \
  'export const productionInitializationFixture = "dirty live SDK dependency";' \
  >"${FIXTURE_REPOSITORY}/packages/sdk/src/index.ts"
run_builder \
  "${HAPPY_OUTPUT}" \
  >"${WORK_ROOT}/happy.log"

[[ "$(<"${SOURCE_MODE_FILE}")" == "700" ]] || fail 'detached source root was not mode 0700'
[[ "$(<"${HAPPY_OUTPUT}/README.md")" == "fixture release readme" ]] ||
  fail 'release payload read from the mutable live worktree'
[[ "$(<"${HAPPY_OUTPUT}/operators/ascendany-production-initialize.mjs")" == 'console.log("bundled reviewed production initialization client");' ]] ||
  fail 'release initialization bundle was not built from the reviewed TypeScript source'
[[ "$(stat -Lc '%a' -- "${HAPPY_OUTPUT}/operators/ascendany-production-initialize.mjs")" == 555 ]] ||
  fail 'release initialization bundle mode differs from 0555'
[[ "$(<"${FIXTURE_REPOSITORY}/deploy/v2/README.md")" == "dirty during build" ]] ||
  fail 'fixture did not mutate the live worktree during the build'
[[ "$(sha256sum -- "${HAPPY_OUTPUT}/models/recommendation-model.json" | awk '{print $1}')" == "${RECOMMENDATION_MODEL_SHA256}" ]] ||
  fail 'release model differs from the externally anchored artifact'
[[ "$(sha256sum -- "${HAPPY_OUTPUT}/models/recommendation-knowledge-catalog.json" | awk '{print $1}')" == "${KNOWLEDGE_CATALOG_SHA256}" ]] ||
  fail 'release catalog differs from the externally anchored artifact'
grep -Fx "ASCENDANY_RECOMMENDATION_MODEL_SHA256=${RECOMMENDATION_MODEL_SHA256}" \
  "${HAPPY_OUTPUT}/config/ascendanyd.env" >/dev/null ||
  fail 'release runtime configuration is not bound to the model digest'
grep -Fx 'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production' \
  "${HAPPY_OUTPUT}/config/ascendanyd.env" >/dev/null ||
  fail 'release runtime configuration is not bound to the production model purpose'
for config in ascendanyd.env catalog-publish.env; do
  grep -Fx "ASCENDANY_KNOWLEDGE_CATALOG_SHA256=${KNOWLEDGE_CATALOG_SHA256}" \
    "${HAPPY_OUTPUT}/config/${config}" >/dev/null ||
    fail "release configuration is not bound to the catalog digest: ${config}"
done
[[ ! -e "${PATH_HIJACK_MARKER}" ]] || fail 'release builder executed Go from the mutable live worktree PATH'
[[ ! -e "${FAKE_BASH_MARKER}" ]] || fail 'release builder resolved Bash through caller PATH'
[[ ! -e "${BASH_ENV_MARKER}" ]] || fail 'release builder evaluated caller BASH_ENV'
[[ ! -e "${EXPORTED_FUNCTION_MARKER}" ]] || fail 'release builder imported a caller shell function'
[[ ! -e "${GIT_PATH_EVAL_MARKER}" ]] || fail 'release builder evaluated a reviewed Git path as shell code'

expect_failure \
  "${WORK_ROOT}/unprivileged-shell.log" \
  /usr/bin/bash "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"
rg --quiet 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/unprivileged-shell.log" ||
  fail 'release builder accepted a shell without privileged mode'
expect_failure \
  "${WORK_ROOT}/sourced-privileged-shell.log" \
  /usr/bin/bash -p -c \
    'attack_marker="$1"; builtin() { /usr/bin/touch -- "$attack_marker"; }; exit() { /usr/bin/touch -- "$attack_marker"; return 0; }; printf() { /usr/bin/touch -- "$attack_marker"; }; export() { /usr/bin/touch -- "$attack_marker"; }; source "$0" --help' \
    "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh" "${SOURCED_FUNCTION_MARKER}"
rg --quiet 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/sourced-privileged-shell.log" ||
  fail 'release builder accepted a sourced privileged shell'
[[ ! -e "${SOURCED_FUNCTION_MARKER}" ]] ||
  fail 'release builder ran a local function before rejecting a sourced privileged shell'
/usr/bin/printf '%s\n' \
  '#!/usr/bin/bash -p' \
  'attack_marker="$1"' \
  'target="$2"' \
  'builtin() { /usr/bin/touch -- "$attack_marker"; }' \
  'exit() { /usr/bin/touch -- "$attack_marker"; return 0; }' \
  'printf() { /usr/bin/touch -- "$attack_marker"; }' \
  'export() { /usr/bin/touch -- "$attack_marker"; }' \
  'BASH_ARGV0="$target"' \
  'source "$target" --help' \
  >"${SOURCED_FILE_WRAPPER}"
/usr/bin/chmod 0700 "${SOURCED_FILE_WRAPPER}"
rm -f -- "${SOURCED_FUNCTION_MARKER}"
expect_failure \
  "${WORK_ROOT}/sourced-file-shell.log" \
  /usr/bin/bash -p "${SOURCED_FILE_WRAPPER}" \
    "${SOURCED_FUNCTION_MARKER}" "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh"
rg --quiet 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/sourced-file-shell.log" ||
  fail 'release builder accepted a sourced file with a forged argv zero'
[[ ! -e "${SOURCED_FUNCTION_MARKER}" ]] ||
  fail 'release builder ran a local function before rejecting a sourced file'

jq -e \
  --arg commit "${COMMIT}" \
  --argjson epoch "${SOURCE_DATE_EPOCH}" \
  '.schema == "ascendany.release.v2"
   and .version == "1.2.3"
   and .commit == $commit
   and .purpose == "production"
   and .sourceDateEpoch == $epoch
   and .build == {"goVersion":"go1.26.0","goos":"linux","goarch":"amd64","goamd64":"v1","goExperiment":"none","gofips140":"off","cgoEnabled":false}
   and (.files | length) == 68' \
  "${HAPPY_OUTPUT}/release-manifest.json" >/dev/null || fail 'release manifest metadata is invalid'

printf '%s\n' "${PAYLOAD_PATHS[@]}" >"${WORK_ROOT}/expected-manifest-paths"
jq -r '.files[].path' "${HAPPY_OUTPUT}/release-manifest.json" >"${WORK_ROOT}/actual-manifest-paths"
diff -u "${WORK_ROOT}/expected-manifest-paths" "${WORK_ROOT}/actual-manifest-paths" ||
  fail 'manifest path order or closed set changed'

printf '%s\n' "${PAYLOAD_PATHS[@]}" release-manifest.json | sort >"${WORK_ROOT}/expected-release-paths"
find "${HAPPY_OUTPUT}" -mindepth 1 ! -type d -printf '%P\n' | sort >"${WORK_ROOT}/actual-release-paths"
diff -u "${WORK_ROOT}/expected-release-paths" "${WORK_ROOT}/actual-release-paths" ||
  fail 'published release contains an unexpected path'

while IFS=$'\t' read -r relative expected_hash expected_size expected_mode; do
  actual_hash="$(sha256sum -- "${HAPPY_OUTPUT}/${relative}" | awk '{print $1}')"
  actual_size="$(stat -Lc '%s' "${HAPPY_OUTPUT}/${relative}")"
  actual_mode="0$(stat -Lc '%a' "${HAPPY_OUTPUT}/${relative}")"
  [[ "${actual_hash}:${actual_size}:${actual_mode}" == "${expected_hash}:${expected_size}:${expected_mode}" ]] ||
    fail "manifest metadata drifted for ${relative}"
done < <(jq -r '.files[] | [.path, .sha256, (.size | tostring), .mode] | @tsv' "${HAPPY_OUTPUT}/release-manifest.json")
assert_no_private_workspace "${HAPPY_PARENT}"

readonly PURPOSE_MISMATCH_PARENT="${WORK_ROOT}/purpose-mismatch-parent"
install -d -m 0700 "${PURPOSE_MISMATCH_PARENT}"
FIXTURE_RELEASE_PURPOSE=acceptance_test \
  expect_failure "${WORK_ROOT}/purpose-mismatch.log" \
    run_builder "${PURPOSE_MISMATCH_PARENT}/release"
rg --quiet 'model purpose differs from expected purpose' "${WORK_ROOT}/purpose-mismatch.log" ||
  fail 'release builder accepted a model whose purpose differs from the release purpose'
[[ ! -e "${PURPOSE_MISMATCH_PARENT}/release" ]] || fail 'purpose mismatch published a release'
assert_no_private_workspace "${PURPOSE_MISMATCH_PARENT}"

for unsafe_mode in 0720 0702; do
  unsafe_parent="${WORK_ROOT}/unsafe-${unsafe_mode}"
  install -d -m "${unsafe_mode}" "${unsafe_parent}"
  expect_failure \
    "${WORK_ROOT}/unsafe-${unsafe_mode}.log" \
    run_builder "${unsafe_parent}/release"
  rg --quiet 'must not be group- or other-writable' "${WORK_ROOT}/unsafe-${unsafe_mode}.log" ||
    fail "mode ${unsafe_mode} did not fail at the output-parent trust boundary"
  [[ ! -e "${unsafe_parent}/release" ]] || fail "unsafe parent ${unsafe_mode} received a release"
done

readonly UNSAFE_ANCESTRY_ROOT="${WORK_ROOT}/unsafe-ancestry-root"
readonly UNSAFE_ANCESTRY_PARENT="${UNSAFE_ANCESTRY_ROOT}/safe-output-parent"
install -d -m 0770 "${UNSAFE_ANCESTRY_ROOT}"
install -d -m 0700 "${UNSAFE_ANCESTRY_PARENT}"
expect_failure \
  "${WORK_ROOT}/unsafe-ancestry.log" \
  run_builder "${UNSAFE_ANCESTRY_PARENT}/release"
rg --quiet 'output parent has an unprotected writable ancestor' \
  "${WORK_ROOT}/unsafe-ancestry.log" ||
  fail 'safe leaf output parent under writable ancestry was accepted'
[[ ! -e "${UNSAFE_ANCESTRY_PARENT}/release" ]] ||
  fail 'unsafe output ancestry received a release'

readonly UNSAFE_HOME_ROOT="${WORK_ROOT}/unsafe-home-root"
readonly UNSAFE_HOME="${UNSAFE_HOME_ROOT}/release-home"
readonly UNSAFE_HOME_OUTPUT_PARENT="${WORK_ROOT}/unsafe-home-output"
install -d -m 0770 "${UNSAFE_HOME_ROOT}"
install -d -m 0700 "${UNSAFE_HOME}" "${UNSAFE_HOME_OUTPUT_PARENT}"
expect_failure \
  "${WORK_ROOT}/unsafe-home.log" \
  run_builder "${UNSAFE_HOME_OUTPUT_PARENT}/release" HOME="${UNSAFE_HOME}"
rg --quiet 'HOME has an unprotected writable ancestor' "${WORK_ROOT}/unsafe-home.log" ||
  fail 'release HOME under writable ancestry was accepted'
[[ ! -e "${UNSAFE_HOME_OUTPUT_PARENT}/release" ]] ||
  fail 'unsafe release HOME produced a release'

readonly IDENTITY_PARENT="${WORK_ROOT}/identity-parent"
install -d -m 0700 "${IDENTITY_PARENT}"
printf '%s\n' "${IDENTITY_PARENT}" >"${OUTPUT_PARENT_SWAP}"
expect_failure \
  "${WORK_ROOT}/output-parent-identity.log" \
  run_builder "${IDENTITY_PARENT}/release"
rm -f -- "${OUTPUT_PARENT_SWAP}"
rg --quiet 'output parent identity changed before publishing ascendany-release-ops build output' \
  "${WORK_ROOT}/output-parent-identity.log" ||
  fail 'output parent inode replacement was not rejected at the child-tool boundary'
[[ ! -e "${IDENTITY_PARENT}/release" ]] ||
  fail 'replacement output parent received a release'
assert_no_private_workspace "${IDENTITY_PARENT}"
assert_no_private_workspace "${IDENTITY_PARENT}.moved"
rm -rf -- "${IDENTITY_PARENT}" "${IDENTITY_PARENT}.moved"

readonly EXTRA_PARENT="${WORK_ROOT}/extra-parent"
install -d -m 0700 "${EXTRA_PARENT}"
touch "${WORK_ROOT}/extra-payload"
expect_failure \
  "${WORK_ROOT}/extra.log" \
  run_builder "${EXTRA_PARENT}/release"
rm -f "${WORK_ROOT}/extra-payload"
rg --quiet 'exact 68-path contract' "${WORK_ROOT}/extra.log" ||
  fail 'unexpected payload did not fail the closed-set gate'
[[ ! -e "${EXTRA_PARENT}/release" ]] || fail 'closed-set failure published a release'
assert_no_private_workspace "${EXTRA_PARENT}"

readonly RACE_PARENT="${WORK_ROOT}/race-parent"
readonly RACE_OUTPUT="${RACE_PARENT}/release"
install -d -m 0700 "${RACE_PARENT}"
printf '%s\n' "${RACE_OUTPUT}" >"${WORK_ROOT}/race-target"
expect_failure \
  "${WORK_ROOT}/race.log" \
  run_builder "${RACE_OUTPUT}"
rm -f "${WORK_ROOT}/race-target"
rg --quiet 'target appeared during publication' "${WORK_ROOT}/race.log" ||
  fail 'publication race did not produce the no-replace failure'
[[ -f "${RACE_OUTPUT}/marker" ]] || fail 'racing target ownership marker was replaced'
find "${RACE_OUTPUT}" -mindepth 1 -printf '%P\n' | sort >"${WORK_ROOT}/race-paths"
[[ "$(<"${WORK_ROOT}/race-paths")" == "marker" ]] || fail 'release payload nested into the racing target'
assert_no_private_workspace "${RACE_PARENT}"

readonly INVALID_PARENT="${WORK_ROOT}/invalid-parent"
install -d -m 0700 "${INVALID_PARENT}"
expect_failure \
  "${WORK_ROOT}/invalid-commit.log" \
  env PATH="${FAKE_BIN}:${PATH}" \
    "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh" \
      --version 1.2.3 \
      --commit 0000000000000000000000000000000000000000 \
      --source-date-epoch "${SOURCE_DATE_EPOCH}" \
      --go-path "${FAKE_BIN}/go" \
      --go-version go1.26.0 \
      --goos linux \
      --goarch amd64 \
      --goamd64 v1 \
      --release-purpose production \
      --recommendation-model "${RECOMMENDATION_MODEL}" \
      --recommendation-model-sha256 "${RECOMMENDATION_MODEL_SHA256}" \
      --knowledge-catalog "${KNOWLEDGE_CATALOG}" \
      --knowledge-catalog-sha256 "${KNOWLEDGE_CATALOG_SHA256}" \
      --output "${INVALID_PARENT}/release"
rg --quiet 'commit payload could not be captured' "${WORK_ROOT}/invalid-commit.log" ||
  fail 'unavailable explicit commit did not fail before building'

readonly SHA256_SHAPED_COMMIT="$(printf '0%.0s' {1..64})"
expect_failure \
  "${WORK_ROOT}/sha256-shaped-commit.log" \
  env PATH="${FAKE_BIN}:${PATH}" \
    "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh" \
      --version 1.2.3 \
      --commit "${SHA256_SHAPED_COMMIT}" \
      --source-date-epoch "${SOURCE_DATE_EPOCH}" \
      --go-path "${FAKE_BIN}/go" \
      --go-version go1.26.0 \
      --goos linux \
      --goarch amd64 \
      --goamd64 v1 \
      --release-purpose production \
      --recommendation-model "${RECOMMENDATION_MODEL}" \
      --recommendation-model-sha256 "${RECOMMENDATION_MODEL_SHA256}" \
      --knowledge-catalog "${KNOWLEDGE_CATALOG}" \
      --knowledge-catalog-sha256 "${KNOWLEDGE_CATALOG_SHA256}" \
      --output "${INVALID_PARENT}/sha256-release"
rg --quiet 'canonical 40-character Git object ID' \
  "${WORK_ROOT}/sha256-shaped-commit.log" ||
  fail 'SHA-256-shaped release identity bypassed the SHA-1-only boundary'

readonly INVALID_VERSION_PARENT="${WORK_ROOT}/invalid-version-parent"
install -d -m 0700 "${INVALID_VERSION_PARENT}"
expect_failure \
  "${WORK_ROOT}/invalid-version.log" \
  env PATH="${FAKE_BIN}:${PATH}" \
    "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh" \
      --version 1.2.3-01 \
      --commit "${COMMIT}" \
      --source-date-epoch "${SOURCE_DATE_EPOCH}" \
      --go-path "${FAKE_BIN}/go" \
      --go-version go1.26.0 \
      --goos linux \
      --goarch amd64 \
      --goamd64 v1 \
      --release-purpose production \
      --recommendation-model "${RECOMMENDATION_MODEL}" \
      --recommendation-model-sha256 "${RECOMMENDATION_MODEL_SHA256}" \
      --knowledge-catalog "${KNOWLEDGE_CATALOG}" \
      --knowledge-catalog-sha256 "${KNOWLEDGE_CATALOG_SHA256}" \
      --output "${INVALID_VERSION_PARENT}/release"
rg --quiet 'canonical semantic version' "${WORK_ROOT}/invalid-version.log" ||
  fail 'noncanonical numeric prerelease identifier was accepted'

readonly LONG_VERSION="1.2.3+$(printf 'a%.0s' {1..123})"
expect_failure \
  "${WORK_ROOT}/long-version.log" \
  env PATH="${FAKE_BIN}:${PATH}" \
    "${FIXTURE_REPOSITORY}/tools/build-v2-release.sh" \
      --version "${LONG_VERSION}" \
      --commit "${COMMIT}" \
      --source-date-epoch "${SOURCE_DATE_EPOCH}" \
      --go-path "${FAKE_BIN}/go" \
      --go-version go1.26.0 \
      --goos linux \
      --goarch amd64 \
      --goamd64 v1 \
      --release-purpose production \
      --recommendation-model "${RECOMMENDATION_MODEL}" \
      --recommendation-model-sha256 "${RECOMMENDATION_MODEL_SHA256}" \
      --knowledge-catalog "${KNOWLEDGE_CATALOG}" \
      --knowledge-catalog-sha256 "${KNOWLEDGE_CATALOG_SHA256}" \
      --output "${INVALID_VERSION_PARENT}/long-release"
rg --quiet 'at most 128 ASCII bytes' "${WORK_ROOT}/long-version.log" ||
  fail 'oversized canonical release version was accepted'

readonly TOOLCHAIN_PARENT="${WORK_ROOT}/toolchain-parent"
install -d -m 0700 "${TOOLCHAIN_PARENT}"
printf '%s\n' go1.26.1 >"${WORK_ROOT}/fake-go-version"
expect_failure \
  "${WORK_ROOT}/toolchain.log" \
  run_builder "${TOOLCHAIN_PARENT}/release"
rm -f "${WORK_ROOT}/fake-go-version"
rg --quiet 'local Go toolchain is go1.26.1' "${WORK_ROOT}/toolchain.log" ||
  fail 'Go toolchain mismatch was not rejected before building'

readonly OBJECT_INTEGRITY_PARENT="${WORK_ROOT}/object-integrity-parent"
install -d -m 0700 "${OBJECT_INTEGRITY_PARENT}"
commit_tree="$(git -C "${FIXTURE_REPOSITORY}" rev-parse "${COMMIT}^{tree}")"
forged_commit="$(
  printf 'fixture: forged commit payload\n' |
    git -C "${FIXTURE_REPOSITORY}" commit-tree "${commit_tree}"
)"
commit_object="${FIXTURE_REPOSITORY}/.git/objects/${COMMIT:0:2}/${COMMIT:2}"
forged_commit_object="${FIXTURE_REPOSITORY}/.git/objects/${forged_commit:0:2}/${forged_commit:2}"
commit_object_backup="${WORK_ROOT}/reviewed-commit-object.backup"
install -m 0444 "${commit_object}" "${commit_object_backup}"
rm -- "${commit_object}"
install -m 0444 "${forged_commit_object}" "${commit_object}"
FIXTURE_REQUESTED_COMMIT="${COMMIT}" \
  expect_failure "${WORK_ROOT}/forged-commit-object.log" \
    run_builder "${OBJECT_INTEGRITY_PARENT}/forged-commit"
rg --quiet 'commit payload failed isolated SHA-1 identity verification' \
  "${WORK_ROOT}/forged-commit-object.log" ||
  fail 'valid commit bytes stored under the requested object name bypassed isolated identity verification'
[[ ! -e "${OBJECT_INTEGRITY_PARENT}/forged-commit" ]] ||
  fail 'forged commit object produced a release'
assert_no_private_workspace "${OBJECT_INTEGRITY_PARENT}"
rm -- "${commit_object}"
install -m 0444 "${commit_object_backup}" "${commit_object}"

binary_blob="$(
  git -C "${FIXTURE_REPOSITORY}" rev-parse "${COMMIT}:provenance/binary.bin"
)"
binary_blob_object="${FIXTURE_REPOSITORY}/.git/objects/${binary_blob:0:2}/${binary_blob:2}"
binary_blob_backup="${WORK_ROOT}/reviewed-binary-blob.backup"
install -m 0444 "${binary_blob_object}" "${binary_blob_backup}"
rm -- "${binary_blob_object}"
printf 'corrupted loose Git object\n' >"${binary_blob_object}"
FIXTURE_REQUESTED_COMMIT="${COMMIT}" \
  expect_failure "${WORK_ROOT}/corrupted-blob-object.log" \
    run_builder "${OBJECT_INTEGRITY_PARENT}/corrupted-blob"
rg --quiet 'blob failed integrity verification during materialization' \
  "${WORK_ROOT}/corrupted-blob-object.log" ||
  fail 'corrupted reviewed blob object was accepted'
[[ ! -e "${OBJECT_INTEGRITY_PARENT}/corrupted-blob" ]] ||
  fail 'corrupted reviewed blob produced a release'
assert_no_private_workspace "${OBJECT_INTEGRITY_PARENT}"
rm -- "${binary_blob_object}"
install -m 0444 "${binary_blob_backup}" "${binary_blob_object}"

forged_tree="$(
  printf '100644 blob %s\tforged-only.bin\n' "${binary_blob}" |
    git -C "${FIXTURE_REPOSITORY}" mktree
)"
tree_object="${FIXTURE_REPOSITORY}/.git/objects/${commit_tree:0:2}/${commit_tree:2}"
forged_tree_object="${FIXTURE_REPOSITORY}/.git/objects/${forged_tree:0:2}/${forged_tree:2}"
tree_object_backup="${WORK_ROOT}/reviewed-root-tree.backup"
install -m 0444 "${tree_object}" "${tree_object_backup}"
rm -- "${tree_object}"
install -m 0444 "${forged_tree_object}" "${tree_object}"
FIXTURE_REQUESTED_COMMIT="${COMMIT}" \
  expect_failure "${WORK_ROOT}/forged-tree-object.log" \
    run_builder "${OBJECT_INTEGRITY_PARENT}/forged-tree"
rg --quiet 'reviewed commit tree could not be enumerated|reconstructed reviewed commit root tree differs from the verified commit payload' \
  "${WORK_ROOT}/forged-tree-object.log" ||
  fail 'valid tree bytes stored under the verified root object name bypassed reconstruction'
[[ ! -e "${OBJECT_INTEGRITY_PARENT}/forged-tree" ]] ||
  fail 'forged reviewed root tree produced a release'
assert_no_private_workspace "${OBJECT_INTEGRITY_PARENT}"
rm -- "${tree_object}"
install -m 0444 "${tree_object_backup}" "${tree_object}"
unset commit_tree forged_commit commit_object forged_commit_object commit_object_backup
unset binary_blob binary_blob_object binary_blob_backup
unset forged_tree tree_object forged_tree_object tree_object_backup

printf 'PASS: release builder verifies isolated SHA-1 commit/tree/blob provenance, privileged entry, offline pinning, and durable private atomic publication\n'
