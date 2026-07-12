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
    "$fixture_root/contracts" \
    "$fixture_root/deploy/v2/systemd" \
    "$fixture_root/trainers/recommendation/src"
  cp -- "$verifier" "$fixture_root/tools/verify-v2-boundary.sh"
  printf 'VALUE = 1\n' >"$fixture_root/trainers/recommendation/src/trainer.py"
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
  'a container runtime outside the isolated trainer uses Python' \
  bash "$python_container/tools/verify-v2-boundary.sh"

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
