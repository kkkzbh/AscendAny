#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
verifier="$repo_root/tools/verify-v2-boundary.sh"
fixture_parent="$(mktemp -d)"
trap 'rm -rf -- "$fixture_parent"' EXIT

make_fixture() {
  local fixture_root="$1"
  mkdir -p \
    "$fixture_root/backend" \
    "$fixture_root/apps" \
    "$fixture_root/packages/sdk/src" \
    "$fixture_root/tools/pintia-exporter-extension/src" \
    "$fixture_root/contracts/openapi" \
    "$fixture_root/db" \
    "$fixture_root/deploy/v2/systemd" \
    "$fixture_root/deploy/v2/scripts" \
    "$fixture_root/deploy/v2/config"
  cp -- "$verifier" "$fixture_root/tools/verify-v2-boundary.sh"
  git -C "$fixture_root" init -q
}

expect_failure() {
  local label="$1" expected="$2" output
  shift 2
  if output="$("$@" 2>&1)"; then
    printf 'fixture %s unexpectedly passed\n' "$label" >&2
    return 1
  fi
  if [[ "$output" != *"$expected"* ]]; then
    printf 'fixture %s failed for the wrong reason:\n%s\n' "$label" "$output" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

baseline="$fixture_parent/baseline"
make_fixture "$baseline"
bash "$baseline/tools/verify-v2-boundary.sh" >/dev/null
printf 'PASS fixture baseline\n'

rg_error="$fixture_parent/rg-error"
make_fixture "$rg_error"
mkdir "$rg_error/fake-bin"
printf '#!/usr/bin/env bash\nexit 2\n' >"$rg_error/fake-bin/rg"
chmod 0755 "$rg_error/fake-bin/rg"
expect_failure \
  rg-error \
  'failed with rg exit status 2' \
  env PATH="$rg_error/fake-bin:$PATH" bash "$rg_error/tools/verify-v2-boundary.sh"

rg_list_error="$fixture_parent/rg-list-error"
make_fixture "$rg_list_error"
mkdir "$rg_list_error/fake-bin"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'for argument in "$@"; do' \
  '  if [[ "$argument" == "exec[.]Command(Context)?[(]" ]]; then' \
  '    exit 2' \
  '  fi' \
  'done' \
  'exit 1' >"$rg_list_error/fake-bin/rg"
chmod 0755 "$rg_list_error/fake-bin/rg"
expect_failure \
  rg-file-list-error \
  'Go host-process execution scan failed with rg exit status 2' \
  env PATH="$rg_list_error/fake-bin:$PATH" bash "$rg_list_error/tools/verify-v2-boundary.sh"

linked_root="$fixture_parent/linked-root"
make_fixture "$linked_root"
rmdir "$linked_root/backend"
mkdir "$fixture_parent/external-backend"
ln -s "$fixture_parent/external-backend" "$linked_root/backend"
expect_failure \
  linked-production-root \
  'production source root is missing, linked, or outside the worktree: backend' \
  bash "$linked_root/tools/verify-v2-boundary.sh"

linked_python="$fixture_parent/linked-python"
make_fixture "$linked_python"
printf 'VALUE = 2\n' >"$fixture_parent/external-runtime.py"
ln -s "$fixture_parent/external-runtime.py" "$linked_python/apps/linked_runtime.py"
expect_failure \
  linked-python-source \
  'production source path has a symbolic-link component: apps/linked_runtime.py' \
  bash "$linked_python/tools/verify-v2-boundary.sh"

python_source="$fixture_parent/python-source"
make_fixture "$python_source"
printf 'VALUE = 1\n' >"$python_source/apps/runtime.py"
expect_failure \
  python-source \
  'Python source exists in the inference-only application repository: apps/runtime.py' \
  bash "$python_source/tools/verify-v2-boundary.sh"

linked_ancestor="$fixture_parent/linked-ancestor"
make_fixture "$linked_ancestor"
mkdir "$linked_ancestor/apps/web"
printf 'export const value = 1;\n' >"$linked_ancestor/apps/web/main.ts"
git -C "$linked_ancestor" add apps/web/main.ts
rm -rf -- "$linked_ancestor/apps/web"
mkdir "$fixture_parent/external-web"
ln -s "$fixture_parent/external-web" "$linked_ancestor/apps/web"
expect_failure \
  linked-production-ancestor \
  'production source path has a symbolic-link component: apps/web' \
  bash "$linked_ancestor/tools/verify-v2-boundary.sh"

compiled_executable="$fixture_parent/compiled-executable"
make_fixture "$compiled_executable"
printf '\x7fELFfixture executable bytes\n' >"$compiled_executable/backend/ascendany-judge"
chmod 0755 "$compiled_executable/backend/ascendany-judge"
expect_failure \
  compiled-executable \
  'compiled executable is present in the production source inventory: backend/ascendany-judge' \
  bash "$compiled_executable/tools/verify-v2-boundary.sh"

for compiled_kind in pe macho32 macho64 macho32-reversed macho64-reversed java-class fat-reversed; do
  compiled_fixture="$fixture_parent/compiled-${compiled_kind}"
  make_fixture "$compiled_fixture"
  case "$compiled_kind" in
    pe) printf '\x4d\x5a\x90\x00fixture\n' >"$compiled_fixture/backend/generated.bin" ;;
    macho32) printf '\xfe\xed\xfa\xcefixture\n' >"$compiled_fixture/backend/generated.bin" ;;
    macho64) printf '\xfe\xed\xfa\xcffixture\n' >"$compiled_fixture/backend/generated.bin" ;;
    macho32-reversed) printf '\xce\xfa\xed\xfefixtures\n' >"$compiled_fixture/backend/generated.bin" ;;
    macho64-reversed) printf '\xcf\xfa\xed\xfefixtures\n' >"$compiled_fixture/backend/generated.bin" ;;
    java-class) printf '\xca\xfe\xba\xbefixture\n' >"$compiled_fixture/backend/generated.bin" ;;
    fat-reversed) printf '\xbe\xba\xfe\xcafixture\n' >"$compiled_fixture/backend/generated.bin" ;;
  esac
  expect_failure \
    "compiled-${compiled_kind}" \
    'compiled executable is present in the production source inventory: backend/generated.bin' \
    bash "$compiled_fixture/tools/verify-v2-boundary.sh"
done
unset compiled_kind compiled_fixture

python_container="$fixture_parent/python-container"
make_fixture "$python_container"
mkdir -p "$python_container/ops/oj"
printf 'FROM docker.io/library/python:3.12-slim\n' >"$python_container/ops/oj/Containerfile"
expect_failure \
  python-container-runtime \
  'a production container runtime uses Python' \
  bash "$python_container/tools/verify-v2-boundary.sh"

retired_shell_runtime="$fixture_parent/retired-shell-runtime"
make_fixture "$retired_shell_runtime"
printf '%s\n' '#!/usr/bin/env bash' 'exec python3 -m uvicorn apps.api.main:app' \
  >"$retired_shell_runtime/deploy/v2/scripts/runtime.sh"
expect_failure \
  retired-shell-runtime \
  'a production shell path references a Python, trainer, API-v1, or retired application runtime' \
  bash "$retired_shell_runtime/tools/verify-v2-boundary.sh"

retired_validator_absence="$fixture_parent/retired-validator-absence"
make_fixture "$retired_validator_absence"
cat >"$retired_validator_absence/deploy/v2/scripts/validate-production.sh" <<'VALIDATOR'
#!/usr/bin/env bash
retired_trainer_unit='ascendany-trainer-agent.service'
retired_runtime_root='/opt/ascendany-trainer-runtime'
retired_python_root='/opt/ascendany/.venv'
test ! -e "$retired_runtime_root"
test ! -e "$retired_python_root"
systemctl is-active "$retired_trainer_unit" >/dev/null 2>&1 || true
VALIDATOR
bash "$retired_validator_absence/tools/verify-v2-boundary.sh" >/dev/null
printf 'PASS fixture retired-validator-absence-reference\n'

retired_validator_exec="$fixture_parent/retired-validator-exec"
make_fixture "$retired_validator_exec"
printf '%s\n' '#!/usr/bin/env bash' \
  'exec /opt/ascendany-trainer-runtime/current/python/bin/python3 /tmp/retired.py' \
  >"$retired_validator_exec/deploy/v2/scripts/validate-production.sh"
expect_failure \
  retired-validator-exec \
  'the production validator contains an execution path for a retired Python or trainer runtime' \
  bash "$retired_validator_exec/tools/verify-v2-boundary.sh"

retired_validator_unit_start="$fixture_parent/retired-validator-unit-start"
make_fixture "$retired_validator_unit_start"
printf '%s\n' '#!/usr/bin/env bash' \
  'systemctl start ascendany-trainer-agent.service' \
  >"$retired_validator_unit_start/deploy/v2/scripts/validate-production.sh"
expect_failure \
  retired-validator-unit-start \
  'the production validator contains an execution path for a retired Python or trainer runtime' \
  bash "$retired_validator_unit_start/tools/verify-v2-boundary.sh"

retired_absence_outside_validator="$fixture_parent/retired-absence-outside-validator"
make_fixture "$retired_absence_outside_validator"
printf '%s\n' '#!/usr/bin/env bash' \
  'test ! -e /opt/ascendany-trainer-runtime' \
  >"$retired_absence_outside_validator/deploy/v2/scripts/retired-absence.sh"
expect_failure \
  retired-absence-outside-validator \
  'a production shell path references a Python, trainer, API-v1, or retired application runtime' \
  bash "$retired_absence_outside_validator/tools/verify-v2-boundary.sh"

retired_api_service="$fixture_parent/retired-api-service"
make_fixture "$retired_api_service"
printf '%s\n' '#!/usr/bin/env bash' 'systemctl start ascendany-api.service' \
  >"$retired_api_service/deploy/v2/scripts/runtime.sh"
expect_failure \
  retired-api-service-ownership \
  'a production shell path outside the production validator references the retired API service' \
  bash "$retired_api_service/tools/verify-v2-boundary.sh"

retired_api_validator="$fixture_parent/retired-api-validator"
make_fixture "$retired_api_validator"
printf '%s\n' '#!/usr/bin/env bash' 'retired_api_unit=ascendany-api.service' \
  >"$retired_api_validator/deploy/v2/scripts/validate-production.sh"
bash "$retired_api_validator/tools/verify-v2-boundary.sh" >/dev/null
printf 'PASS fixture retired-api-validator-ownership\n'

retired_recommendation_database="$fixture_parent/retired-recommendation-database"
make_fixture "$retired_recommendation_database"
mkdir -p "$retired_recommendation_database/backend/internal/migrate/migrations"
printf '%s\n' 'CREATE TABLE recommendation_training_runs (id bigint);' \
  >"$retired_recommendation_database/backend/internal/migrate/migrations/0002.sql"
expect_failure \
  retired-recommendation-database \
  'a production source retains a recommendation training database or API execution path' \
  bash "$retired_recommendation_database/tools/verify-v2-boundary.sh"

retired_database_route="$fixture_parent/retired-database-route"
make_fixture "$retired_database_route"
printf '%s\n' '#!/usr/bin/env bash' 'psql --username=AscendAny --dbname=AscendAny' \
  >"$retired_database_route/tools/retired-database.sh"
expect_failure \
  retired-database-route \
  'a production shell path references a retired database role, database, or pool route' \
  bash "$retired_database_route/tools/verify-v2-boundary.sh"

obsolete_postgres_transition="$fixture_parent/obsolete-postgres-transition"
make_fixture "$obsolete_postgres_transition"
printf '%s\n' 'obsolete bootstrap route' \
  >"$obsolete_postgres_transition/deploy/v2/config/postgresql-hba-bootstrap.conf"
expect_failure \
  obsolete-postgres-transition \
  'obsolete PostgreSQL transition configuration remains: deploy/v2/config/postgresql-hba-bootstrap.conf' \
  bash "$obsolete_postgres_transition/tools/verify-v2-boundary.sh"

approved_catalog_receipt_exec="$fixture_parent/approved-catalog-receipt-exec"
make_fixture "$approved_catalog_receipt_exec"
mkdir -p "$approved_catalog_receipt_exec/backend/internal/backup"
printf '%s\n' 'package backup' 'func archive() { exec.CommandContext(ctx, zstdPath) }' \
  >"$approved_catalog_receipt_exec/backend/internal/backup/catalog_receipts.go"
bash "$approved_catalog_receipt_exec/tools/verify-v2-boundary.sh" >/dev/null
printf 'PASS fixture approved-catalog-receipt-exec\n'

unreviewed_catalog_receipt_exec="$fixture_parent/unreviewed-catalog-receipt-exec"
make_fixture "$unreviewed_catalog_receipt_exec"
mkdir -p "$unreviewed_catalog_receipt_exec/backend/internal/backup"
printf '%s\n' 'package backup' 'func archive() { exec.CommandContext(ctx, zstdPath) }' \
  >"$unreviewed_catalog_receipt_exec/backend/internal/backup/catalog_receipt_helper.go"
expect_failure \
  unreviewed-catalog-receipt-exec \
  'unreviewed Go host-process execution site: backend/internal/backup/catalog_receipt_helper.go' \
  bash "$unreviewed_catalog_receipt_exec/tools/verify-v2-boundary.sh"

shell_test_literal="$fixture_parent/shell-test-literal"
make_fixture "$shell_test_literal"
mkdir -p "$shell_test_literal/apps/mobile/scripts"
printf '%s\n' 'node -e '\''require("node:child_process")'\''' >"$shell_test_literal/apps/mobile/scripts/fixture.sh"
bash "$shell_test_literal/tools/verify-v2-boundary.sh" >/dev/null
printf 'PASS fixture shell-test-literal\n'

typescript_process="$fixture_parent/typescript-process"
make_fixture "$typescript_process"
mkdir -p "$typescript_process/apps/web/src"
printf '%s\n' 'import { spawn } from "node:child_process";' >"$typescript_process/apps/web/src/runtime.ts"
expect_failure \
  typescript-host-process \
  'a first-party TypeScript runtime can spawn a host process' \
  bash "$typescript_process/tools/verify-v2-boundary.sh"

javascript_runtime="$fixture_parent/javascript-runtime"
make_fixture "$javascript_runtime"
mkdir -p "$javascript_runtime/apps/web/src"
printf '%s\n' 'export const runtime = true;' >"$javascript_runtime/apps/web/src/runtime.mjs"
expect_failure \
  first-party-javascript-runtime \
  'a first-party application runtime is hand-written JavaScript: apps/web/src/runtime.mjs' \
  bash "$javascript_runtime/tools/verify-v2-boundary.sh"

controlled_catalog_authorization="$fixture_parent/controlled-catalog-authorization"
make_fixture "$controlled_catalog_authorization"
mkdir -p "$controlled_catalog_authorization/backend/internal/migrate/migrations"
printf '%s\n' \
  'CREATE TABLE ascendany.knowledge_catalog_publication_authorizations (public_id uuid PRIMARY KEY);' \
  'ALTER TABLE ascendany.knowledge_catalog_publications ADD COLUMN publication_authorization_id uuid;' \
  >"$controlled_catalog_authorization/backend/internal/migrate/migrations/0007_catalog.sql"
printf '%s\n' \
  'paths:' \
  '  /api/v2/admin/recommendation/catalog-publication-authorizations:' \
  '    post: {operationId: authorizeKnowledgeCatalogPublication}' \
  'components: {schemas: {AuthorizedRequest: {properties: {authorizationId: {type: string}}}}}' \
  >"$controlled_catalog_authorization/contracts/openapi/ascendany-v2.yaml"
bash "$controlled_catalog_authorization/tools/verify-v2-boundary.sh" >/dev/null
printf 'PASS fixture controlled-catalog-authorization\n'

obsolete_catalog_authorization="$fixture_parent/obsolete-catalog-authorization"
make_fixture "$obsolete_catalog_authorization"
printf '%s\n' 'package catalogauthorization' 'const field = "secretSha256"' \
  >"$obsolete_catalog_authorization/backend/obsolete.go"
expect_failure \
  obsolete-catalog-authorization \
  'production retains a legacy or generic knowledge-catalog mutation path' \
  bash "$obsolete_catalog_authorization/tools/verify-v2-boundary.sh"

generic_catalog_mutation="$fixture_parent/generic-catalog-mutation"
make_fixture "$generic_catalog_mutation"
printf '%s\n' 'package httpapi' 'func createKnowledgeCatalogVersion() {}' \
  >"$generic_catalog_mutation/backend/generic_catalog.go"
expect_failure \
  generic-catalog-mutation \
  'production retains a legacy or generic knowledge-catalog mutation path' \
  bash "$generic_catalog_mutation/tools/verify-v2-boundary.sh"

javascript_import="$fixture_parent/javascript-import"
make_fixture "$javascript_import"
mkdir -p "$javascript_import/apps/web/src"
printf '%s\n' 'import { runtime } from "./runtime.mjs";' >"$javascript_import/apps/web/src/main.ts"
expect_failure \
  first-party-javascript-import \
  'a first-party application imports an untyped JavaScript runtime module' \
  bash "$javascript_import/tools/verify-v2-boundary.sh"

handwritten_endpoint="$fixture_parent/handwritten-endpoint"
make_fixture "$handwritten_endpoint"
mkdir -p "$handwritten_endpoint/packages/sdk/src"
printf '%s\n' 'export const path = "/api/v2/handwritten";' >"$handwritten_endpoint/packages/sdk/src/runtime.ts"
expect_failure \
  handwritten-sdk-endpoint \
  'an SDK production source owns a hand-written /api/v2 endpoint outside generated output' \
  bash "$handwritten_endpoint/tools/verify-v2-boundary.sh"

printf 'verify-v2-boundary fixtures passed.\n'
