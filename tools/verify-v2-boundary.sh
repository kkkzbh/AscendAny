#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${repo_root}"

fail() {
  printf 'v2 boundary violation: %s\n' "$1" >&2
  exit 1
}

require_no_rg_match() {
  local scan_name="$1" violation="$2" status
  shift 2
  if rg "$@" >/dev/null; then
    fail "$violation"
  else
    status=$?
  fi
  if [[ "$status" != "1" ]]; then
    fail "$scan_name failed with rg exit status $status"
  fi
}

capture_rg_file_list() {
  local output_name="$1" scan_name="$2" status
  shift 2
  local -n output="$output_name"
  local result="$scan_tmp_dir/rg-file-list"
  output=()
  if rg --null "$@" >"$result"; then
    mapfile -d '' -t output <"$result"
  else
    status=$?
    if [[ "$status" != "1" ]]; then
      fail "$scan_name failed with rg exit status $status"
    fi
  fi
}

source_path_exists() {
  [[ -e "$1" || -L "$1" ]]
}

is_production_path() {
  local candidate="$1" root
  for root in "${production_roots[@]}"; do
    if [[ "$candidate" == "$root" || "$candidate" == "$root/"* ]]; then
      return 0
    fi
  done
  return 1
}

check_production_source_path() {
  local candidate="$1" current="$1" magic
  while [[ "$current" != "." && "$current" != "/" ]]; do
    if [[ -L "$current" ]]; then
      fail "production source path has a symbolic-link component: $current"
    fi
    current="$(dirname -- "$current")"
  done
  source_path_exists "$candidate" || return 0
  if [[ ! -f "$candidate" ]]; then
    fail "production source path is not a regular file: $candidate"
  fi
  magic="$(od -An -tx1 -N4 -- "$candidate" | tr -d '[:space:]')" ||
    fail "production source magic could not be read: $candidate"
  case "$magic" in
    7f454c46|4d5a*|feedface|feedfacf|cefaedfe|cffaedfe|cafebabe|bebafeca)
      fail "compiled executable is present in the production source inventory: $candidate"
      ;;
  esac
}

for command in dirname git mktemp od realpath rg rm tr; do
  if ! command -v "$command" >/dev/null 2>&1; then
    fail "required boundary command is missing: $command"
  fi
done

if ! git_root="$(git rev-parse --show-toplevel 2>/dev/null)" ||
   [[ "$(realpath -e -- "$git_root" 2>/dev/null || true)" != "$repo_root" ]]; then
  fail 'the boundary verifier is not running at its owning Git worktree root'
fi

production_roots=(
  backend
  apps
  packages
  tools
  contracts
  deploy/v2
)
for root in "${production_roots[@]}"; do
  if [[ ! -d "$root" || -L "$root" ||
        "$(realpath -e -- "$root" 2>/dev/null || true)" != "$repo_root/$root" ]]; then
    fail "production source root is missing, linked, or outside the worktree: $root"
  fi
done

scan_tmp_dir="$(mktemp -d)" || fail 'cannot allocate a private boundary scan directory'
trap 'rm -rf -- "$scan_tmp_dir"' EXIT
source_inventory_path="$scan_tmp_dir/source-inventory"
if ! git -c core.quotepath=false ls-files -z --cached --others --exclude-standard >"$source_inventory_path"; then
  fail 'Git source inventory failed'
fi
mapfile -d '' -t source_inventory <"$source_inventory_path"

for candidate in "${source_inventory[@]}"; do
  if is_production_path "$candidate"; then
    check_production_source_path "$candidate"
  fi
done

legacy_roots=(
  apps/api
  preprocess
  db/schema
  services/recommendation
  data/legacy-web-oj
)
for root in "${legacy_roots[@]}"; do
  for candidate in "${source_inventory[@]}"; do
    source_path_exists "$candidate" || continue
    if [[ "$candidate" == "$root" || "$candidate" == "$root/"* ]]; then
      fail "legacy source remains at $candidate"
    fi
  done
done

for candidate in "${source_inventory[@]}"; do
  source_path_exists "$candidate" || continue
  [[ "$candidate" == *.py ]] || continue
  fail "Python source exists in the inference-only application repository: $candidate"
done

for candidate in "${source_inventory[@]}"; do
  source_path_exists "$candidate" || continue
  if [[ "$candidate" == tools/pintia-exporter-extension/src/*.js ]]; then
    fail 'the Pintia extension source contains a hand-written JavaScript runtime'
  fi
  case "$candidate" in
    apps/*/src/*.js|apps/*/src/*.jsx|apps/*/src/*.mjs|apps/*/src/*.cjs)
      fail "a first-party application runtime is hand-written JavaScript: $candidate"
      ;;
  esac
done

require_no_rg_match \
  'first-party JavaScript runtime import scan' \
  'a first-party application imports an untyped JavaScript runtime module' \
  -n \
  --glob '*.{ts,tsx,mts,cts}' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  'from[[:space:]]+["'\''`][^"'\''`]+[.](mjs|cjs|js|jsx)["'\''`]' \
  apps

require_no_rg_match \
  'generated SDK endpoint ownership scan' \
  'an SDK production source owns a hand-written /api/v2 endpoint outside generated output' \
  -n \
  --glob '*.{ts,tsx,mts,cts}' \
  --glob '!**/generated/**' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  '/api/v2' \
  packages/sdk/src

require_no_rg_match \
  'Pintia v1 contract scan' \
  'a production path references a Pintia v1 contract' \
  -n \
  --glob '!**/*_test.go' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  --glob '!**/dist/**' \
  'ascendany[.]pintia[.](unit|snapshot)[.]v1' \
  "${production_roots[@]}"

# The reviewed online path authorizes one immutable publication intent through
# catalog-publication-authorizations. Catalog mutation packages, shared-secret
# authorization, direct catalog write endpoints, and receipt v2 remain retired.
require_no_rg_match \
  'legacy or generic catalog mutation contract scan' \
  'production retains a legacy or generic knowledge-catalog mutation path' \
  -n \
  --glob '!tools/verify-v2-boundary.sh' \
  --glob '!tools/tests/verify-v2-boundary-fixture.sh' \
  --glob '!**/*_test.go' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  'catalogauthorization|secretSha256|knowledge_catalog_publication_authorization_id|publication-receipt[.]v2|[Cc]reateKnowledgeCatalogVersion|[Uu]pdateKnowledgeCatalog|[Mm]utateKnowledgeCatalog|/api/v2/admin/recommendation/knowledge-catalog(["'\''`]|$)' \
  backend apps packages contracts deploy/v2 tools

require_no_rg_match \
  'retired Python systemd runtime scan' \
  'a v2 systemd unit references the retired Python online runtime' \
  -n 'uvicorn|apps/api|preprocess/' deploy/v2/systemd

require_no_rg_match \
  'retired runtime shell closure scan' \
  'a production shell path references a Python, trainer, or retired application runtime' \
  -i -n \
  --glob '*.sh' \
  --glob '!tools/tests/**' \
  --glob '!tools/verify-v2-boundary.sh' \
  --glob '!deploy/v2/scripts/validate-production.sh' \
  '(python([0-9.]*)?([[:space:]/]|$)|uvicorn|apps[./]api|ascendany-trainer)' \
  deploy/v2/scripts tools

# The production validator owns absence checks for the retired generation. It
# may name the retired paths and units as data, but it must never be able to
# launch them. Every other production shell path remains under the broader
# zero-reference rule above.
require_no_rg_match \
  'retired runtime validator execution scan' \
  'the production validator contains an execution path for a retired Python or trainer runtime' \
  -i -n \
  --glob 'validate-production.sh' \
  '(^|[;&|][[:space:]]*)(exec[[:space:]]+)?([^;&|[:space:]]*/)?(python([0-9.]*)?|uvicorn|ascendany-trainer-agent)([[:space:];&|]|$)|(bash|sh|env|xargs|systemd-run)[[:space:]][^;&|]*(python([0-9.]*)?|uvicorn|apps[./]api|ascendany-trainer)|(systemctl[[:space:]]+(start|restart|try-restart|reload|reload-or-restart|enable|reenable|unmask)[^;&|]*(ascendany-api|ascendany-trainer))' \
  deploy/v2/scripts

require_no_rg_match \
  'retired API service ownership scan' \
  'a production shell path outside the production validator references the retired API service' \
  -i -n \
  --glob '*.sh' \
  --glob '!tools/tests/**' \
  --glob '!tools/verify-v2-boundary.sh' \
  --glob '!deploy/v2/scripts/validate-production.sh' \
  'ascendany-api' \
  deploy/v2/scripts tools

require_no_rg_match \
  'retired recommendation database and API scan' \
  'a production source retains a recommendation training database or API execution path' \
  -i -n \
  --glob '!**/*_test.go' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  --glob '!**/public/**' \
  --glob '!tools/tests/**' \
  '(recommendation_(training|trainer)|recommendation/(training-runs|trainer-agent)|KindTraining|configuration_kind[^\n]*training|training[^\n]*configuration_kind)' \
  backend db contracts/openapi packages/sdk/src apps deploy/v2

require_no_rg_match \
  'retired database route shell scan' \
  'a production shell path references a retired database role, database, or pool route' \
  -n \
  --glob '*.sh' \
  --glob '!tools/tests/**' \
  --glob '!tools/verify-v2-boundary.sh' \
  '("AscendAny"|dbname=AscendAny([[:space:]]|$)|--dbname=AscendAny|--username=AscendAny|ascendany[.]legacy)' \
  deploy/v2/scripts tools

for obsolete in \
  deploy/v2/config/postgresql-hba-bootstrap.conf \
  deploy/v2/config/postgresql-ident-bootstrap.conf; do
  if source_path_exists "$obsolete"; then
    fail "obsolete PostgreSQL transition configuration remains: $obsolete"
  fi
done

require_no_rg_match \
  'Python container runtime scan' \
  'a production container runtime uses Python' \
  -n \
  --glob '**/Containerfile' \
  --glob '**/Containerfile.*' \
  --glob '**/Dockerfile' \
  --glob '**/Dockerfile.*' \
  '^[[:space:]]*FROM[[:space:]]+[^[:space:]]*python([:@]|$)' \
  .

if git ls-files --error-unmatch -- .env.local >/dev/null 2>&1; then
  fail '.env.local is tracked'
else
  git_status=$?
  if [[ "$git_status" != "1" ]]; then
    fail "tracked .env.local check failed with git exit status $git_status"
  fi
fi

for candidate in "${source_inventory[@]}"; do
  source_path_exists "$candidate" || continue
  case "$candidate" in
    *.env.example|*.env.*.example) ;;
    *.pem|*.key|*.p12|*.pfx|*.jks|*.keystore|*.pgpass|*/.pgpass|*/.netrc|*.env|*.env.*)
      fail "credential-bearing filename is present in the source tree: $candidate"
      ;;
  esac
done

require_no_rg_match \
  'live credential shape scan' \
  'a source file contains a recognized live credential shape' \
  -I --hidden -n \
  --glob '!**/.git/**' \
  --glob '!**/node_modules/**' \
  --glob '!**/.venv/**' \
  --glob '!**/dist/**' \
  --glob '!apps/web/public/vendor/**' \
  --glob '!tmp/**' \
  '(-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9_]{36,}|glpat-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|sk_live_[A-Za-z0-9]{16,}|sk-[A-Za-z0-9_-]{32,})' \
  .

require_no_rg_match \
  'database password URL scan' \
  'a production source embeds a database password in a URL' \
  -I --hidden -n \
  --glob '!**/.git/**' \
  --glob '!**/node_modules/**' \
  --glob '!**/.venv/**' \
  --glob '!**/dist/**' \
  --glob '!apps/web/public/vendor/**' \
  --glob '!tmp/**' \
  --glob '!**/*_test.go' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  '(postgres(ql)?|mysql)://[^/@[:space:]]+:[^/@[:space:]]+@' \
  .

# Host-process ownership is closed to these reviewed modules. The two backup
# archive owners invoke the configured absolute zstd binary; process.go owns
# the configured PostgreSQL tools. Every additional execution site fails.
approved_go_exec_sites=(
  backend/internal/backup/archive.go
  backend/internal/backup/catalog_receipts.go
  backend/internal/backup/process.go
  backend/internal/judgeexecutor/systemd.go
  backend/internal/judgerunner/podman.go
  backend/internal/lspexecutor/systemd.go
  backend/internal/lsprunner/runner.go
)
capture_rg_file_list actual_go_exec_sites 'Go host-process execution scan' \
  -l 'exec[.]Command(Context)?[(]' backend \
  --glob '*.go' \
  --glob '!**/*_test.go'
declare -A approved_go_exec_lookup=()
for file in "${approved_go_exec_sites[@]}"; do
  approved_go_exec_lookup["$file"]=1
done
for file in "${actual_go_exec_sites[@]}"; do
  if [[ -z "${approved_go_exec_lookup[$file]:-}" ]]; then
    fail "unreviewed Go host-process execution site: $file"
  fi
done

require_no_rg_match \
  'first-party TypeScript host-process execution scan' \
  'a first-party TypeScript runtime can spawn a host process' \
  -n \
  --glob '*.{ts,tsx,mts,cts,js,jsx,mjs,cjs}' \
  --glob '!**/*.test.ts' \
  --glob '!**/*.test.tsx' \
  --glob '!**/dist/**' \
  --glob '!**/node_modules/**' \
  "(node:child_process|from [\"']child_process[\"']|require[(][\"']child_process[\"'][)]|Deno[.]Command|Bun[.]spawn)" \
  apps packages tools/pintia-exporter-extension

printf 'AscendAny v2 source boundary verified.\n'
