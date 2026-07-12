#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly TEST_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
readonly WINDOWS_BUILDER_SOURCE="${TEST_ROOT}/apps/desktop/scripts/build-windows-release.sh"
readonly RPM_BUILDER_SOURCE="${TEST_ROOT}/apps/desktop/scripts/build-linux-rpm-release.sh"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-desktop-release-fixture.XXXXXXXX")"
readonly FIXTURE_REPOSITORY="${WORK_ROOT}/repository"
readonly FIXTURE_DESKTOP="${FIXTURE_REPOSITORY}/apps/desktop"
readonly FAKE_BIN="${WORK_ROOT}/fake-bin"
readonly ENTRY_ATTACK_BIN="${WORK_ROOT}/entry-attack-bin"
readonly PATH_ATTACK_BIN="${WORK_ROOT}/path-attack-bin"
readonly LIVE_TOOL_BIN="${FIXTURE_DESKTOP}/node_modules/.bin"
readonly PATH_HIJACK_MARKER="${WORK_ROOT}/live-path-tool-executed"
readonly PATH_TOOL_MARKER="${WORK_ROOT}/caller-path-tool-executed"
readonly FAKE_BASH_MARKER="${WORK_ROOT}/fake-bash-executed-desktop-builder"
readonly BASH_ENV_MARKER="${WORK_ROOT}/bash-env-executed-desktop-builder"
readonly EXPORTED_FUNCTION_MARKER="${WORK_ROOT}/exported-function-executed-desktop-builder"
readonly SOURCED_FUNCTION_MARKER="${WORK_ROOT}/sourced-function-executed-desktop-builder"
readonly SOURCED_FILE_WRAPPER="${WORK_ROOT}/source-desktop-builder-attack.sh"
readonly GIT_PATH_EVAL_MARKER="${WORK_ROOT}/git-path-command-substitution-executed"
readonly OUTPUT_PARENT_SWAP="${WORK_ROOT}/desktop-output-parent-swap"
readonly WINDOWS_SIGN_PATH_ATTACK="${WORK_ROOT}/windows-sign-path-attack"
readonly WINDOWS_SIGN_SYMLINK_ATTACK="${WORK_ROOT}/windows-sign-symlink-attack"
readonly WINDOWS_SIGN_VICTIM="${WORK_ROOT}/windows-sign-victim"
readonly BASH_ENV_ATTACK="${WORK_ROOT}/desktop-builder-bash-env"
readonly GLOBAL_ATTRIBUTES="${WORK_ROOT}/global-attributes"
readonly GLOBAL_GIT_CONFIG="${WORK_ROOT}/global-git-config"
readonly P12_FINGERPRINT_FILE="${WORK_ROOT}/p12-fingerprint"
readonly INSTALLER_FINGERPRINT_FILE="${WORK_ROOT}/installer-fingerprint"
readonly RPM_SECRET_FINGERPRINT_FILE="${WORK_ROOT}/rpm-secret-fingerprint"
readonly RPM_ARTIFACT_FINGERPRINT_FILE="${WORK_ROOT}/rpm-artifact-fingerprint"
readonly RELEASE_HOME="${WORK_ROOT}/release-home"
readonly GNUPG_HOME="${RELEASE_HOME}/.gnupg"
readonly PNPM_STORE_SEED="${WORK_ROOT}/pnpm-store-seed"
readonly BUILD_CACHE_SEED="${WORK_ROOT}/build-cache-seed"
readonly CERTIFICATE_FILE="${WORK_ROOT}/windows-signing.p12"
readonly WINDOWS_PASSWORD_FILE="${WORK_ROOT}/windows-signing-password"
readonly EMPTY_WINDOWS_PASSWORD_FILE="${WORK_ROOT}/empty-windows-signing-password"
readonly NUL_WINDOWS_PASSWORD_FILE="${WORK_ROOT}/nul-windows-signing-password"
readonly LONG_WINDOWS_PASSWORD_FILE="${WORK_ROOT}/long-windows-signing-password"
readonly AMBIENT_FD_VICTIM="${WORK_ROOT}/ambient-fd-victim"
readonly RPM_MACRO_ATTACK_MARKER="${WORK_ROOT}/rpm-user-macro-executed"
readonly SOURCE_MODE_FILE="${WORK_ROOT}/source-mode"
readonly WINDOWS_FINGERPRINT='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
readonly OTHER_WINDOWS_FINGERPRINT='BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB'
readonly RPM_FINGERPRINT='0123456789ABCDEF0123456789ABCDEF01234567'
readonly OTHER_RPM_FINGERPRINT='89ABCDEF0123456789ABCDEF0123456789ABCDEF'

cleanup() {
  if [[ -n "${HOST_SOCKET_PID:-}" ]]; then
    kill "${HOST_SOCKET_PID}" 2>/dev/null || true
    wait "${HOST_SOCKET_PID}" 2>/dev/null || true
  fi
  rm -rf -- "${WORK_ROOT}"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

if jq -e '.scripts | has("dist:win:x64") or has("dist:linux:rpm:x64")' \
  "${TEST_ROOT}/apps/desktop/package.json" >/dev/null; then
  fail 'package.json exposes a release entry that can inherit signing material before the builder'
fi
grep -q '/usr/bin/sync -f' "${WINDOWS_BUILDER_SOURCE}" ||
  fail 'Windows release builder does not fsync staged and published artifacts'
grep -q '/usr/bin/sync -f' "${RPM_BUILDER_SOURCE}" ||
  fail 'RPM release builder does not fsync staged and published artifacts'

expect_failure() {
  local log_path="$1"
  shift
  if "$@" >"${log_path}" 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

assert_private_workspace_cleaned() {
  local parent="$1"
  if find "${parent}" -mindepth 1 -maxdepth 1 \
    \( -name '.ascendany-desktop-windows.*' -o -name '.ascendany-desktop-rpm.*' \) \
    -print -quit | grep -q .; then
    fail "private desktop release workspace remains under ${parent}"
  fi
}

assert_release_contract() {
  local output="$1"
  local artifact_name="$2"
  local expected_content="$3"
  local checksum_name="${artifact_name}.sha512"

  [[ "$(<"${output}/${artifact_name}")" == "${expected_content}" ]] ||
    fail "${artifact_name} read mutable worktree content"
  printf '%s\n' "${artifact_name}" "${checksum_name}" | sort >"${WORK_ROOT}/expected-paths"
  find "${output}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort > \
    "${WORK_ROOT}/actual-paths"
  diff -u "${WORK_ROOT}/expected-paths" "${WORK_ROOT}/actual-paths" ||
    fail "${artifact_name} release output is not the exact two-file set"
  checksum_line="$(<"${output}/${checksum_name}")"
  checksum_hash="${checksum_line%% *}"
  checksum_basename="${checksum_line#"${checksum_hash}  "}"
  [[ "${checksum_hash}" =~ ^[0-9a-f]{128}$ && "${checksum_basename}" == "${artifact_name}" ]] ||
    fail "${checksum_name} is not a basename-only portable SHA-512 record"
  [[ "${checksum_line}" != */* ]] || fail "${checksum_name} contains a path"
  (
    cd -- "${output}"
    sha512sum --check "${checksum_name}" >/dev/null
  ) || fail "${checksum_name} is not verifiable from the artifact directory"
  unset checksum_basename checksum_hash checksum_line
}

install -d -m 0700 \
  "${FIXTURE_DESKTOP}/scripts" \
  "${FAKE_BIN}" \
  "${ENTRY_ATTACK_BIN}" \
  "${PATH_ATTACK_BIN}" \
  "${RELEASE_HOME}" \
  "${GNUPG_HOME}" \
  "${PNPM_STORE_SEED}" \
  "${BUILD_CACHE_SEED}"
install -m 0600 /dev/null "${AMBIENT_FD_VICTIM}"
install -d -m 0700 "${RELEASE_HOME}/.config/rpm"
printf '%%__gpg_sign_cmd /usr/bin/touch %s\n' "${RPM_MACRO_ATTACK_MARKER}" > \
  "${RELEASE_HOME}/.config/rpm/macros"
chmod 0600 "${RELEASE_HOME}/.config/rpm/macros"
printf 'host HOME sentinel\n' >"${RELEASE_HOME}/sandbox-home-sentinel"
readonly HOST_SOCKET="${RELEASE_HOME}/sandbox-host.sock"
readonly HOST_SOCKET_READY="${WORK_ROOT}/sandbox-host-socket-ready"
/usr/bin/node - "${HOST_SOCKET}" "${HOST_SOCKET_READY}" <<'NODE' &
const fs = require("node:fs");
const net = require("node:net");
const [socketPath, readyPath] = process.argv.slice(2);
const server = net.createServer((connection) => connection.end("host socket reached"));
server.listen(socketPath, () => fs.writeFileSync(readyPath, "ready\n"));
NODE
readonly HOST_SOCKET_PID=$!
host_socket_deadline=$((SECONDS + 10))
while [[ ! -s "${HOST_SOCKET_READY}" ]]; do
  kill -0 "${HOST_SOCKET_PID}" 2>/dev/null || fail 'real bwrap socket probe server failed'
  (( SECONDS < host_socket_deadline )) || fail 'real bwrap socket probe server timed out'
done
unset host_socket_deadline
/usr/bin/bwrap \
  --die-with-parent \
  --new-session \
  --unshare-pid \
  --unshare-net \
  --unshare-ipc \
  --unshare-uts \
  --tmpfs / \
  --ro-bind /usr /usr \
  --symlink usr/bin /bin \
  --symlink usr/lib /lib \
  --symlink usr/lib64 /lib64 \
  --proc /proc \
  --dev /dev \
  --tmpfs /tmp \
  --tmpfs /run \
  --tmpfs "${RELEASE_HOME}" \
  --clearenv \
  --setenv PATH /usr/bin:/bin \
  /usr/bin/node -e '
    const fs = require("node:fs");
    const net = require("node:net");
    const [sentinel, socketPath] = process.argv.slice(1);
    if (fs.existsSync(sentinel)) process.exit(70);
    const socket = net.connect(socketPath);
    socket.on("connect", () => process.exit(71));
    socket.on("error", () => process.exit(0));
  ' "${RELEASE_HOME}/sandbox-home-sentinel" "${HOST_SOCKET}"
kill "${HOST_SOCKET_PID}" 2>/dev/null || true
wait "${HOST_SOCKET_PID}" 2>/dev/null || true
install -m 0755 "${WINDOWS_BUILDER_SOURCE}" \
  "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
install -m 0755 "${RPM_BUILDER_SOURCE}" \
  "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"
printf '{"name":"fixture-root","private":true}\n' >"${FIXTURE_REPOSITORY}/package.json"
printf 'packages:\n  - apps/*\n' >"${FIXTURE_REPOSITORY}/pnpm-workspace.yaml"
printf 'lockfileVersion: "9.0"\n' >"${FIXTURE_REPOSITORY}/pnpm-lock.yaml"
printf '{"name":"@ascendany/desktop","private":true}\n' >"${FIXTURE_DESKTOP}/package.json"
printf 'committed desktop source\n' >"${FIXTURE_DESKTOP}/fixture-source.txt"
printf 'literal hostile Git path\n' > \
  "${FIXTURE_REPOSITORY}/\$(printf injected >\$FIXTURE_GIT_PATH_EVAL_MARKER)"
printf 'apps/desktop/fixture-source.txt export-ignore\n' >"${FIXTURE_REPOSITORY}/.gitattributes"
printf 'fixture PKCS#12 bytes\n' >"${CERTIFICATE_FILE}"
chmod 0600 "${CERTIFICATE_FILE}"
printf %s 'fixture-password' >"${WINDOWS_PASSWORD_FILE}"
chmod 0600 "${WINDOWS_PASSWORD_FILE}"
printf %s '' >"${EMPTY_WINDOWS_PASSWORD_FILE}"
printf 'prefix\0suffix' >"${NUL_WINDOWS_PASSWORD_FILE}"
printf -v long_windows_password '%*s' 4097 ''
printf %s "${long_windows_password// /x}" >"${LONG_WINDOWS_PASSWORD_FILE}"
unset long_windows_password
chmod 0600 \
  "${EMPTY_WINDOWS_PASSWORD_FILE}" \
  "${NUL_WINDOWS_PASSWORD_FILE}" \
  "${LONG_WINDOWS_PASSWORD_FILE}"

git -C "${FIXTURE_REPOSITORY}" init --quiet
git -C "${FIXTURE_REPOSITORY}" config user.name 'AscendAny desktop release fixture'
git -C "${FIXTURE_REPOSITORY}" config user.email 'desktop-release-fixture@example.invalid'
git -C "${FIXTURE_REPOSITORY}" add .
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: committed desktop release source'
readonly COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
printf 'apps/desktop/fixture-source.txt export-ignore\n' > \
  "${FIXTURE_REPOSITORY}/.git/info/attributes"
printf 'apps/desktop/fixture-source.txt export-ignore\n' >"${GLOBAL_ATTRIBUTES}"
printf '[core]\n\tattributesFile = %s\n' "${GLOBAL_ATTRIBUTES}" >"${GLOBAL_GIT_CONFIG}"
printf 'dirty before desktop release\n' >"${FIXTURE_DESKTOP}/fixture-source.txt"
install -d -m 0700 "${LIVE_TOOL_BIN}"
cat >"${LIVE_TOOL_BIN}/git" <<'LIVE_PATH_TOOL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'live worktree PATH tool executed\n' >"${FIXTURE_PATH_HIJACK_MARKER:?}"
exit 97
LIVE_PATH_TOOL
chmod 0755 "${LIVE_TOOL_BIN}/git"

cat >"${ENTRY_ATTACK_BIN}/bash" <<'FAKE_ENTRY_BASH'
#!/usr/bin/bash -p
case "${1:-}" in
  */build-windows-release.sh|*/build-linux-rpm-release.sh)
    builtin printf 'fake bash reached desktop release builder\n' >"${FIXTURE_FAKE_BASH_MARKER:?}"
    ;;
esac
exec /usr/bin/bash -p "$@"
FAKE_ENTRY_BASH
chmod 0755 "${ENTRY_ATTACK_BIN}/bash"

cat >"${BASH_ENV_ATTACK}" <<'BASH_ENV_ATTACK_BODY'
case "$0" in
  */build-windows-release.sh|*/build-linux-rpm-release.sh)
    builtin printf 'BASH_ENV reached desktop release builder\n' >"${FIXTURE_BASH_ENV_MARKER:?}"
    ;;
esac
BASH_ENV_ATTACK_BODY
chmod 0600 "${BASH_ENV_ATTACK}"

cat >"${FAKE_BIN}/node" <<'FAKE_NODE'
#!/usr/bin/bash -p
set -Eeuo pipefail

readonly trusted_tool_directory="$(cd -- "${BASH_SOURCE[0]%/*}" && pwd -P)"
if [[ "${1:-}" == '-' || "${1:-}" == '-e' ]]; then
  exec /usr/bin/node "$@"
fi
[[ "${1:-}" == "${trusted_tool_directory}/pnpm" ]] || {
  printf 'trusted fixture Node received an unknown CLI path\n' >&2
  exit 64
}
cli="$1"
shift
exec /usr/bin/bash -p "${cli}" "$@"
FAKE_NODE

cat >"${FAKE_BIN}/pnpm" <<'FAKE_PNPM'
#!/usr/bin/bash -p
set -Eeuo pipefail

readonly fixture_work_root="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
readonly fixture_repository="${fixture_work_root}/repository"
readonly fixture_desktop="${fixture_repository}/apps/desktop"
readonly source_mode_file="${fixture_work_root}/source-mode"

[[ -r "/proc/${PPID}/environ" ]] || {
  printf 'fake pnpm cannot inspect its parent environment\n' >&2
  exit 65
}
while IFS= read -r -d '' parent_environment_entry; do
  parent_environment_name="${parent_environment_entry%%=*}"
  parent_environment_value="${parent_environment_entry#*=}"
  case "${parent_environment_name}" in
    CSC_KEY_PASSWORD|WIN_CSC_KEY_PASSWORD|CERTIFICATE_PASSWORD)
      printf 'fake pnpm parent process environment contains a signing password variable\n' >&2
      exit 65
      ;;
  esac
  [[ "${parent_environment_value}" != *'fixture-password'* ]] || {
    printf 'fake pnpm parent process environment exposes the signing password bytes\n' >&2
    exit 65
  }
done <"/proc/${PPID}/environ"
unset parent_environment_entry parent_environment_name parent_environment_value
while IFS= read -r -d '' process_argument; do
  [[ "${process_argument}" != *'fixture-password'* ]] || {
    printf 'fake pnpm process command line exposes the signing password bytes\n' >&2
    exit 65
  }
done <"/proc/self/cmdline"
while IFS= read -r -d '' parent_process_argument; do
  [[ "${parent_process_argument}" != *'fixture-password'* ]] || {
    printf 'fake pnpm parent command line exposes the signing password bytes\n' >&2
    exit 65
  }
done <"/proc/${PPID}/cmdline"
unset process_argument parent_process_argument

while IFS= read -r -d '' inherited_environment_entry; do
  inherited_environment_name="${inherited_environment_entry%%=*}"
  inherited_environment_value="${inherited_environment_entry#*=}"
  case "${inherited_environment_name}" in
    BASH_FUNC_*%%)
      printf 'fake pnpm child inherited a caller shell function\n' >&2
      exit 65
      ;;
    NPM_CONFIG_USERCONFIG|NPM_CONFIG_GLOBALCONFIG)
      [[ "${inherited_environment_value}" == /dev/null ]] || exit 65
      ;;
    npm_config_store_dir)
      [[ "${inherited_environment_value}" == */pnpm-store ]] || exit 65
      ;;
    ELECTRON_BUILDER_OFFLINE)
      [[ "${inherited_environment_value}" == true ]] || exit 65
      ;;
    npm_*|NPM_*|pnpm_*|PNPM_*|corepack_*|COREPACK_*|ELECTRON_*|WIN_CSC_*)
      printf 'fake pnpm child inherited an ambient package/runtime setting\n' >&2
      exit 65
      ;;
  esac
done < <(/usr/bin/env -0)
[[ -z "${BASH_ENV+x}" && -z "${ENV+x}" && -z "${NODE_OPTIONS+x}" &&
  -z "${NODE_PATH+x}" && -z "${GNUPGHOME+x}" &&
  "${HOME:-}" == */sandbox-home && "${TMPDIR:-}" == */sandbox-tmp &&
  "${XDG_CACHE_HOME:-}" == */cache ]] || {
  printf 'fake pnpm child inherited a shell or Node startup hook\n' >&2
  exit 65
}
if [[ "$*" == 'install --frozen-lockfile --offline' ]]; then
  [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
    -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" ]] || {
    printf 'desktop install inherited Windows signing material\n' >&2
    exit 65
  }
  exit 0
fi
if [[ "$*" == '--filter @ascendany/desktop build' ]]; then
  [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
    -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" ]] || {
    printf 'desktop build inherited Windows signing material\n' >&2
    exit 65
  }
  exit 0
fi
if [[ "$*" != *'--filter @ascendany/desktop exec electron-builder'* ]]; then
  printf 'unexpected fake pnpm invocation: %s\n' "$*" >&2
  exit 64
fi
if [[ "$*" == *'--win nsis'* ]]; then
  [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
    -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" &&
    "${CSC_IDENTITY_AUTO_DISCOVERY:-}" == false ]] || {
      printf 'electron-builder inherited Windows signing material or enabled builtin discovery\n' >&2
      exit 65
    }
elif [[ "$*" == *'--linux rpm'* ]]; then
  [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
    -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" ]] || {
    printf 'RPM electron-builder inherited Windows signing material\n' >&2
    exit 65
  }
else
  printf 'fake pnpm received an unknown electron-builder target: %s\n' "$*" >&2
  exit 64
fi

output=''
artifact=''
for argument in "$@"; do
  case "${argument}" in
    --config.directories.output=*) output="${argument#*=}" ;;
    --config.artifactName=*) artifact="${argument#*=}" ;;
  esac
done
[[ "${output}" = /* && -n "${artifact}" ]] || exit 64
install -d -m 0700 -- "${output}"
stat -Lc '%a' "${PWD}" >"${source_mode_file}"
source_content="$(<"${PWD}/apps/desktop/fixture-source.txt")"
if [[ "$*" == *'--win nsis'* ]]; then
  hook=''
  for argument in "$@"; do
    case "${argument}" in
      --config.win.signtoolOptions.sign=*) hook="${argument#*=}" ;;
    esac
  done
  [[ -f "${hook}" ]] || exit 64
  install -d -m 0700 -- "${output}/win-unpacked/resources"
  printf '%s\n' "${source_content}" >"${output}/win-unpacked/AscendAny.exe"
  printf '%s\n' "${source_content}" >"${output}/win-unpacked/resources/elevate.exe"
  printf '%s\n' "${source_content}" >"${output}/__uninstaller-nsis-AscendAny.exe"
  printf '%s\n' "${source_content}" >"${output}/${artifact}"
  if [[ -f "${fixture_work_root}/windows-sign-symlink-attack" ]]; then
    rm -f -- "${output}/win-unpacked/AscendAny.exe"
    ln -s -- "${fixture_work_root}/windows-sign-victim" "${output}/win-unpacked/AscendAny.exe"
  fi
  /usr/bin/node - "${hook}" "${output}" "${artifact}" "${fixture_work_root}" <<'NODE'
const [hookPath, output, artifact, fixtureRoot] = process.argv.slice(2);
const sign = require(hookPath).default;
(async () => {
  const fs = require("node:fs");
  if (fs.existsSync(`${fixtureRoot}/windows-sign-path-attack`)) {
    const rejectedPath = `${output}/not-allowed.exe`;
    fs.writeFileSync(rejectedPath, "must remain unsigned\n");
    await sign({ cscInfo: null, hash: "sha256", isNest: false, path: rejectedPath });
  }
  for (const path of [
    `${output}/win-unpacked/AscendAny.exe`,
    `${output}/win-unpacked/resources/elevate.exe`,
    `${output}/__uninstaller-nsis-AscendAny.exe`,
    `${output}/${artifact}`,
  ]) {
    await sign({ cscInfo: null, hash: "sha256", isNest: false, path });
  }
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
NODE
  rm -f -- "${output}/__uninstaller-nsis-AscendAny.exe"
else
  printf '%s\n' "${source_content}" >"${output}/${artifact}"
fi
printf 'dirty during desktop release\n' >"${fixture_desktop}/fixture-source.txt"
if [[ -f "${fixture_work_root}/desktop-output-parent-swap" ]]; then
  swap_parent="$(<"${fixture_work_root}/desktop-output-parent-swap")"
  mv -- "${swap_parent}" "${swap_parent}.moved"
  install -d -m 0700 -- "${swap_parent}"
fi
FAKE_PNPM

cat >"${FAKE_BIN}/openssl" <<'FAKE_OPENSSL'
#!/usr/bin/bash -p
set -Eeuo pipefail

readonly fixture_work_root="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"

while IFS= read -r -d '' parent_environment_entry; do
  parent_environment_name="${parent_environment_entry%%=*}"
  parent_environment_value="${parent_environment_entry#*=}"
  case "${parent_environment_name}" in
    CSC_KEY_PASSWORD|WIN_CSC_KEY_PASSWORD|CERTIFICATE_PASSWORD)
      printf 'fake OpenSSL parent process environment contains a signing password variable\n' >&2
      exit 65
      ;;
  esac
  [[ "${parent_environment_value}" != *'fixture-password'* ]] || {
    printf 'fake OpenSSL parent process environment exposes the signing password bytes\n' >&2
    exit 65
  }
done <"/proc/${PPID}/environ"
unset parent_environment_entry parent_environment_name parent_environment_value
while IFS= read -r -d '' process_argument; do
  [[ "${process_argument}" != *'fixture-password'* ]] || {
    printf 'fake OpenSSL process command line exposes the signing password bytes\n' >&2
    exit 65
  }
done <"/proc/self/cmdline"
while IFS= read -r -d '' parent_process_argument; do
  [[ "${parent_process_argument}" != *'fixture-password'* ]] || {
    printf 'fake OpenSSL parent command line exposes the signing password bytes\n' >&2
    exit 65
  }
done <"/proc/${PPID}/cmdline"
unset process_argument parent_process_argument

case "${1:-}" in
  pkcs12)
    [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
      -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" ]] || {
      printf 'PKCS#12 inspection received the wrong signing environment\n' >&2
      exit 65
    }
    output=''
    input=''
    passin=''
    previous=''
    for argument in "$@"; do
      if [[ "${previous}" == '-out' ]]; then output="${argument}"; fi
      if [[ "${previous}" == '-in' ]]; then input="${argument}"; fi
      if [[ "${previous}" == '-passin' ]]; then passin="${argument}"; fi
      previous="${argument}"
    done
    [[ "${input}" == /proc/self/fd/* && -n "${output}" &&
      "${passin}" =~ ^fd:([0-9]+)$ ]] || exit 64
    pass_fd="${BASH_REMATCH[1]}"
    [[ "$(</proc/self/fd/${pass_fd})" == 'fixture-password' ]] || {
      printf 'OpenSSL did not receive the PKCS#12 password through the inherited descriptor\n' >&2
      exit 65
    }
    printf '%s\n' \
      '-----BEGIN CERTIFICATE-----' 'fixture' '-----END CERTIFICATE-----' >"${output}"
    printf '%s%s\n%s\n%s%s\n' \
      '-----BEGIN PRIVATE ' 'KEY-----' 'fixture' '-----END PRIVATE ' 'KEY-----' >>"${output}"
    ;;
  cms)
    [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
      -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" ]] || {
      printf 'CMS verification inherited Windows signing material\n' >&2
      exit 65
    }
    signer=''
    previous=''
    for argument in "$@"; do
      if [[ "${previous}" == '-signer' ]]; then signer="${argument}"; fi
      previous="${argument}"
    done
    [[ -n "${signer}" ]] || exit 64
    printf '%s\n' '-----BEGIN CERTIFICATE-----' 'fixture' '-----END CERTIFICATE-----' >"${signer}"
    ;;
  x509)
    [[ -z "${CSC_LINK+x}" && -z "${CSC_KEY_PASSWORD+x}" &&
      -z "${CERTIFICATE_FILE+x}" && -z "${CERTIFICATE_PASSWORD+x}" ]] || {
      printf 'X.509 inspection inherited Windows signing material\n' >&2
      exit 65
    }
    input=''
    previous=''
    for argument in "$@"; do
      if [[ "${previous}" == '-in' ]]; then input="${argument}"; fi
      previous="${argument}"
    done
    if [[ "${input}" == /proc/self/fd/* ]]; then
      printf 'sha256 Fingerprint=%s\n' "$(<"${fixture_work_root}/p12-fingerprint")"
    else
      printf 'sha256 Fingerprint=%s\n' "$(<"${fixture_work_root}/installer-fingerprint")"
    fi
    ;;
  *)
    printf 'unexpected fake openssl invocation: %s\n' "$*" >&2
    exit 64
    ;;
esac
FAKE_OPENSSL

cat >"${FAKE_BIN}/osslsigncode" <<'FAKE_OSSLSIGNCODE'
#!/usr/bin/bash -p
set -Eeuo pipefail

while IFS= read -r -d '' value; do
  [[ "${value}" != *'fixture-password'* ]] || {
    printf 'osslsigncode process boundary exposed the signing password\n' >&2
    exit 65
  }
done < <(/usr/bin/env -0; /usr/bin/cat /proc/self/cmdline; /usr/bin/cat "/proc/${PPID}/environ"; /usr/bin/cat "/proc/${PPID}/cmdline")

case "${1:-}" in
  sign)
    input=''
    output=''
    certs=''
    key=''
    previous=''
    for argument in "$@"; do
      [[ "${argument}" != *'fixture-password'* ]] || exit 65
      if [[ "${previous}" == '-in' ]]; then input="${argument}"; fi
      if [[ "${previous}" == '-out' ]]; then output="${argument}"; fi
      if [[ "${previous}" == '-certs' ]]; then certs="${argument}"; fi
      if [[ "${previous}" == '-key' ]]; then key="${argument}"; fi
      previous="${argument}"
    done
    [[ -f "${input}" && -n "${output}" && "${certs}" == /proc/self/fd/* &&
      "${key}" == "${certs}" ]] || exit 64
    /usr/bin/cp -- "${input}" "${output}"
    ;;
  verify)
    exit 0
    ;;
  extract-signature)
    output=''
    previous=''
    for argument in "$@"; do
      if [[ "${previous}" == '-out' ]]; then output="${argument}"; fi
      previous="${argument}"
    done
    [[ -n "${output}" ]] || exit 64
    printf '%s\n' '-----BEGIN PKCS7-----' 'fixture' '-----END PKCS7-----' >"${output}"
    ;;
  *)
    exit 64
    ;;
esac
FAKE_OSSLSIGNCODE

cat >"${FAKE_BIN}/gpg" <<'FAKE_GPG'
#!/usr/bin/bash -p
set -Eeuo pipefail

readonly fixture_work_root="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"

if [[ "$*" == *'--list-secret-keys'* ]]; then
  fingerprint="$(<"${fixture_work_root}/rpm-secret-fingerprint")"
  awk -v fingerprint="${fingerprint}" 'BEGIN {
    OFS = ":"
    print "sec", "u", "2048", "1", substr(fingerprint, 25), "0", "0", "", "", "", "", "s", "", ""
    print "fpr", "", "", "", "", "", "", "", "", fingerprint, "", "", "", ""
  }'
  exit 0
fi
if [[ "$*" == *'--list-packets'* ]]; then
  printf 'hashed subpkt 33 len 21 (issuer fpr v4 %s)\n' \
    "$(<"${fixture_work_root}/rpm-artifact-fingerprint")"
  exit 0
fi
if [[ "$*" == *'--armor --export'* ]]; then
  printf '%s\n' '-----BEGIN PGP PUBLIC KEY BLOCK-----' 'fixture' '-----END PGP PUBLIC KEY BLOCK-----'
  exit 0
fi
printf 'unexpected fake gpg invocation: %s\n' "$*" >&2
exit 64
FAKE_GPG

cat >"${FAKE_BIN}/rpm" <<'FAKE_RPM'
#!/usr/bin/bash -p
set -Eeuo pipefail

if [[ "$*" == *'--queryformat'* ]]; then
  printf 'c2ln'
  exit 0
fi
exit 64
FAKE_RPM

cat >"${FAKE_BIN}/rpmsign" <<'FAKE_RPMSIGN'
#!/usr/bin/bash -p
set -Eeuo pipefail
readonly fixture_work_root="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
readonly expected_fingerprint="$(<"${fixture_work_root}/rpm-secret-fingerprint")"
[[ "$*" == *'--addsign'* && "$*" == *"--key-id=${expected_fingerprint}"* &&
  "$*" == *"--define __gpg ${fixture_work_root}/fake-bin/gpg"* &&
  "$*" == *'--rcfile=/usr/lib/rpm/rpmrc:/usr/lib/rpm/redhat/rpmrc'* &&
  "$*" == *'--macros=/usr/lib/rpm/macros:'* &&
  "${HOME}" == */tool-home && "${HOME}" != */release-home ]]
[[ ! -e "${fixture_work_root}/rpm-user-macro-executed" ]]
FAKE_RPMSIGN

cat >"${FAKE_BIN}/rpmkeys" <<'FAKE_RPMKEYS'
#!/usr/bin/bash -p
set -Eeuo pipefail
if [[ "$*" == *'--import'* || "$*" == *'--checksig --verbose'* ]]; then
  exit 0
fi
exit 64
FAKE_RPMKEYS

cat >"${FAKE_BIN}/bwrap" <<'FAKE_BWRAP'
#!/usr/bin/bash -p
set -Eeuo pipefail

readonly fixture_work_root="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
for inherited_fd_path in /proc/self/fd/[0-9]*; do
  inherited_target="$(readlink -- "${inherited_fd_path}" 2>/dev/null || true)"
  case "${inherited_target}" in
    "${fixture_work_root}/ambient-fd-victim"|*windows-signing.p12|*windows-signing-password)
      printf 'desktop build sandbox inherited an ambient or signing descriptor\n' >&2
      exit 65
      ;;
  esac
done
unset inherited_fd_path inherited_target

declare -a child_environment=()
declare -a command=()
chdir=''
seen_clearenv=0
seen_pid=0
seen_net=0
seen_root=0
seen_proc=0
seen_dev=0
seen_private_home=0
seen_private_store=0
seen_private_cache=0
seen_secret_mask=0
seen_repository_mask=0
seen_release_home_mask=0
seen_run_mask=0
while (( $# > 0 )); do
  case "$1" in
    --die-with-parent|--new-session|--unshare-ipc|--unshare-uts)
      shift
      ;;
    --unshare-pid)
      seen_pid=1
      shift
      ;;
    --unshare-net)
      seen_net=1
      shift
      ;;
    --clearenv)
      seen_clearenv=1
      shift
      ;;
    --proc)
      [[ "${2:-}" == /proc ]] || exit 64
      seen_proc=1
      shift 2
      ;;
    --dev)
      [[ "${2:-}" == /dev ]] || exit 64
      seen_dev=1
      shift 2
      ;;
    --tmpfs)
      [[ -n "${2:-}" ]] || exit 64
      [[ "$2" != / ]] || seen_root=1
      [[ "$2" == /tmp || "$2" == */.gnupg ]] && seen_secret_mask=1
      [[ "$2" != */repository ]] || seen_repository_mask=1
      [[ "$2" != */release-home ]] || seen_release_home_mask=1
      [[ "$2" != /run ]] || seen_run_mask=1
      shift 2
      ;;
    --dir)
      [[ -n "${2:-}" ]] || exit 64
      shift 2
      ;;
    --symlink)
      [[ -n "${2:-}" && -n "${3:-}" ]] || exit 64
      shift 3
      ;;
    --ro-bind|--bind)
      [[ -n "${2:-}" && -n "${3:-}" ]] || exit 64
      [[ "$1" != --ro-bind || "$2" != / || "$3" != / ]] || seen_root=1
      [[ "$1" != --ro-bind || "$2" != /dev/null ]] || seen_secret_mask=1
      [[ "$3" != */sandbox-home ]] || seen_private_home=1
      [[ "$3" != */pnpm-store ]] || seen_private_store=1
      [[ "$3" != */cache ]] || seen_private_cache=1
      shift 3
      ;;
    --setenv)
      [[ -n "${2:-}" ]] || exit 64
      child_environment+=( "$2=${3:-}" )
      shift 3
      ;;
    --chdir)
      chdir="${2:-}"
      shift 2
      ;;
    --)
      shift
      command=( "$@" )
      break
      ;;
    -* )
      printf 'fake bwrap received unknown option: %s\n' "$1" >&2
      exit 64
      ;;
    *)
      command=( "$@" )
      break
      ;;
  esac
done

(( seen_clearenv == 1 && seen_pid == 1 && seen_net == 1 && seen_root == 1 &&
   seen_proc == 1 && seen_dev == 1 && seen_private_home == 1 &&
   seen_private_store == 1 && seen_private_cache == 1 && seen_secret_mask == 1 &&
   seen_repository_mask == 1 && seen_release_home_mask == 1 &&
   seen_run_mask == 1 )) || {
  printf 'desktop build sandbox omitted a required namespace, private path, or secret mask\n' >&2
  exit 65
}
[[ -n "${chdir}" && ${#command[@]} -gt 0 ]] || exit 64
(
  cd -- "${chdir}"
  exec /usr/bin/env -i "${child_environment[@]}" "${command[@]}"
)
FAKE_BWRAP

chmod 0755 "${FAKE_BIN}"/*

for attacked_tool in node pnpm bwrap openssl osslsigncode gpg rpm rpmkeys rpmsign; do
  cat >"${PATH_ATTACK_BIN}/${attacked_tool}" <<'PATH_ATTACK_TOOL'
#!/usr/bin/bash -p
set -Eeuo pipefail
printf 'caller PATH tool executed\n' >"${FIXTURE_PATH_TOOL_MARKER:?}"
exit 97
PATH_ATTACK_TOOL
  chmod 0755 "${PATH_ATTACK_BIN}/${attacked_tool}"
done
unset attacked_tool

run_windows_builder() {
  local version="$1"
  local output="$2"
  local p12_fingerprint="$3"
  local installer_fingerprint="$4"
  local api_origin="${5:-https://ascendany.example.invalid}"
  local certificate_file="${6:-${CERTIFICATE_FILE}}"
  local release_home="${7:-${RELEASE_HOME}}"
  local node_binary="${8:-${FAKE_BIN}/node}"
  local requested_commit="${FIXTURE_WINDOWS_REQUESTED_COMMIT:-${COMMIT}}"
  local builder_path="${FIXTURE_WINDOWS_BUILDER_PATH:-${FIXTURE_DESKTOP}/scripts/build-windows-release.sh}"
  local password_fd ambient_fd
  local builder_status=0
  printf '%s\n' "${p12_fingerprint}" >"${P12_FINGERPRINT_FILE}"
  printf '%s\n' "${installer_fingerprint}" >"${INSTALLER_FINGERPRINT_FILE}"
  exec {password_fd}<"${FIXTURE_WINDOWS_PASSWORD_FILE:-${WINDOWS_PASSWORD_FILE}}"
  exec {ambient_fd}<"${AMBIENT_FD_VICTIM}"
  env \
    HOME="${release_home}" \
    PATH="${ENTRY_ATTACK_BIN}:${PATH_ATTACK_BIN}:${LIVE_TOOL_BIN}:${FAKE_BIN}:/usr/local/bin:/usr/bin:/bin" \
    BASH_ENV="${BASH_ENV_ATTACK}" \
    NODE_OPTIONS="--require=${WORK_ROOT}/ambient-node-options-must-be-cleared.cjs" \
    NODE_PATH="${WORK_ROOT}/ambient-node-path-must-be-cleared" \
    npm_config_script_shell="${WORK_ROOT}/ambient-script-shell-must-be-cleared" \
    ELECTRON_MIRROR="${WORK_ROOT}/ambient-electron-mirror-must-be-cleared" \
    GIT_CONFIG_GLOBAL="${GLOBAL_GIT_CONFIG}" \
    FIXTURE_FAKE_BASH_MARKER="${FAKE_BASH_MARKER}" \
    FIXTURE_BASH_ENV_MARKER="${BASH_ENV_MARKER}" \
    FIXTURE_EXPORTED_FUNCTION_MARKER="${EXPORTED_FUNCTION_MARKER}" \
    FIXTURE_GIT_PATH_EVAL_MARKER="${GIT_PATH_EVAL_MARKER}" \
    FIXTURE_PATH_TOOL_MARKER="${PATH_TOOL_MARKER}" \
    ASCENDANY_DESKTOP_VERSION="${version}" \
    ASCENDANY_DESKTOP_RELEASE_COMMIT="${requested_commit}" \
    ASCENDANY_DESKTOP_OUTPUT_DIRECTORY="${output}" \
    ASCENDANY_DESKTOP_NODE_PATH="${node_binary}" \
    ASCENDANY_DESKTOP_PNPM_CLI_PATH="${FAKE_BIN}/pnpm" \
    ASCENDANY_DESKTOP_BWRAP_PATH="${FAKE_BIN}/bwrap" \
    ASCENDANY_DESKTOP_BUILD_TOOL_ROOT="${FAKE_BIN}" \
    ASCENDANY_DESKTOP_PNPM_STORE_PATH="${PNPM_STORE_SEED}" \
    ASCENDANY_DESKTOP_BUILD_CACHE_PATH="${BUILD_CACHE_SEED}" \
    ASCENDANY_DESKTOP_OPENSSL_PATH="${FAKE_BIN}/openssl" \
    ASCENDANY_DESKTOP_OSSLSIGNCODE_PATH="${FAKE_BIN}/osslsigncode" \
    ASCENDANY_DESKTOP_CSC_PASSWORD_FD="${password_fd}" \
    CSC_LINK="${certificate_file}" \
    CERTIFICATE_FILE='inherited-certificate-file-must-be-cleared' \
    VITE_API_BASE_URL="${api_origin}" \
    VITE_CHAT_PROMPT_CONFIGURATION_KEY='agent.prompt.default' \
    VITE_CHAT_MODEL_CONFIGURATION_KEY='agent.model.default' \
    FIXTURE_PATH_HIJACK_MARKER="${PATH_HIJACK_MARKER}" \
    "${builder_path}" || builder_status=$?
  exec {password_fd}<&-
  exec {ambient_fd}<&-
  return "${builder_status}"
}

run_windows_builder_with_descriptor() {
  local descriptor="$1"
  /usr/bin/env -i \
    HOME="${RELEASE_HOME}" \
    ASCENDANY_DESKTOP_CSC_PASSWORD_FD="${descriptor}" \
    CSC_LINK="${CERTIFICATE_FILE}" \
    "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
}

run_windows_builder_with_password_file() {
  local password_file="$1"
  FIXTURE_WINDOWS_PASSWORD_FILE="${password_file}" \
    run_windows_builder '2.3.4' "${INVALID_PARENT}/password-contract-${RANDOM}" \
      "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
}

run_windows_builder_with_invalid_environment_name() {
  local password_fd
  local builder_status=0

  exec {password_fd}<"${WINDOWS_PASSWORD_FILE}"
  /usr/bin/env -i \
    HOME="${RELEASE_HOME}" \
    FIXTURE_EXPORTED_FUNCTION_MARKER="${EXPORTED_FUNCTION_MARKER}" \
    'BASH_FUNC_cd%%=() { builtin printf "exported cd function reached desktop release builder\n" >"${FIXTURE_EXPORTED_FUNCTION_MARKER:?}"; builtin cd "$@"; }' \
    ASCENDANY_DESKTOP_CSC_PASSWORD_FD="${password_fd}" \
    CSC_LINK="${CERTIFICATE_FILE}" \
    "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh" || builder_status=$?
  exec {password_fd}<&-
  return "${builder_status}"
}

run_windows_builder_xtrace() {
  local password_fd
  local builder_status=0

  exec {password_fd}<"${WINDOWS_PASSWORD_FILE}"
  /usr/bin/env -i \
    HOME="${RELEASE_HOME}" \
    ASCENDANY_DESKTOP_VERSION='2.3.4' \
    ASCENDANY_DESKTOP_RELEASE_COMMIT="${COMMIT}" \
    ASCENDANY_DESKTOP_OUTPUT_DIRECTORY="${INVALID_PARENT}/xtrace-windows" \
    ASCENDANY_DESKTOP_NODE_PATH="${FAKE_BIN}/node" \
    ASCENDANY_DESKTOP_PNPM_CLI_PATH="${FAKE_BIN}/pnpm" \
    ASCENDANY_DESKTOP_BWRAP_PATH="${FAKE_BIN}/bwrap" \
    ASCENDANY_DESKTOP_BUILD_TOOL_ROOT="${FAKE_BIN}" \
    ASCENDANY_DESKTOP_PNPM_STORE_PATH="${PNPM_STORE_SEED}" \
    ASCENDANY_DESKTOP_BUILD_CACHE_PATH="${BUILD_CACHE_SEED}" \
    ASCENDANY_DESKTOP_OPENSSL_PATH="${FAKE_BIN}/openssl" \
    ASCENDANY_DESKTOP_OSSLSIGNCODE_PATH="${FAKE_BIN}/osslsigncode" \
    ASCENDANY_DESKTOP_CSC_PASSWORD_FD="${password_fd}" \
    CSC_LINK="${XTRACE_CERTIFICATE_FILE}" \
    VITE_API_BASE_URL='https://EXAMPLE.invalid' \
    VITE_CHAT_PROMPT_CONFIGURATION_KEY='agent.prompt.default' \
    VITE_CHAT_MODEL_CONFIGURATION_KEY='agent.model.default' \
    /usr/bin/bash -p -x "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh" || builder_status=$?
  exec {password_fd}<&-
  return "${builder_status}"
}

run_rpm_builder() {
  local version="$1"
  local output="$2"
  local artifact_fingerprint="$3"
  local api_origin="${4:-https://ascendany.example.invalid}"
  local gnupg_home="${5:-${GNUPG_HOME}}"
  local release_home="${6:-${RELEASE_HOME}}"
  local node_binary="${7:-${FAKE_BIN}/node}"
  local requested_commit="${FIXTURE_RPM_REQUESTED_COMMIT:-${COMMIT}}"
  local builder_path="${FIXTURE_RPM_BUILDER_PATH:-${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh}"
  local ambient_fd builder_status=0
  printf '%s\n' "${RPM_FINGERPRINT}" >"${RPM_SECRET_FINGERPRINT_FILE}"
  printf '%s\n' "${artifact_fingerprint}" >"${RPM_ARTIFACT_FINGERPRINT_FILE}"
  exec {ambient_fd}<"${AMBIENT_FD_VICTIM}"
  env \
    HOME="${release_home}" \
    PATH="${ENTRY_ATTACK_BIN}:${PATH_ATTACK_BIN}:${LIVE_TOOL_BIN}:${FAKE_BIN}:/usr/local/bin:/usr/bin:/bin" \
    BASH_ENV="${BASH_ENV_ATTACK}" \
    NODE_OPTIONS="--require=${WORK_ROOT}/ambient-node-options-must-be-cleared.cjs" \
    NODE_PATH="${WORK_ROOT}/ambient-node-path-must-be-cleared" \
    npm_config_script_shell="${WORK_ROOT}/ambient-script-shell-must-be-cleared" \
    ELECTRON_MIRROR="${WORK_ROOT}/ambient-electron-mirror-must-be-cleared" \
    GIT_CONFIG_GLOBAL="${GLOBAL_GIT_CONFIG}" \
    FIXTURE_FAKE_BASH_MARKER="${FAKE_BASH_MARKER}" \
    FIXTURE_BASH_ENV_MARKER="${BASH_ENV_MARKER}" \
    FIXTURE_EXPORTED_FUNCTION_MARKER="${EXPORTED_FUNCTION_MARKER}" \
    FIXTURE_GIT_PATH_EVAL_MARKER="${GIT_PATH_EVAL_MARKER}" \
    FIXTURE_PATH_TOOL_MARKER="${PATH_TOOL_MARKER}" \
    'BASH_FUNC_cd%%=() { builtin printf "exported cd function reached desktop release builder\n" >"${FIXTURE_EXPORTED_FUNCTION_MARKER:?}"; builtin cd "$@"; }' \
    ASCENDANY_DESKTOP_VERSION="${version}" \
    ASCENDANY_DESKTOP_RELEASE_COMMIT="${requested_commit}" \
    ASCENDANY_DESKTOP_OUTPUT_DIRECTORY="${output}" \
    ASCENDANY_RPM_SIGNING_FINGERPRINT="${RPM_FINGERPRINT}" \
    ASCENDANY_DESKTOP_NODE_PATH="${node_binary}" \
    ASCENDANY_DESKTOP_PNPM_CLI_PATH="${FAKE_BIN}/pnpm" \
    ASCENDANY_DESKTOP_BWRAP_PATH="${FAKE_BIN}/bwrap" \
    ASCENDANY_DESKTOP_BUILD_TOOL_ROOT="${FAKE_BIN}" \
    ASCENDANY_DESKTOP_PNPM_STORE_PATH="${PNPM_STORE_SEED}" \
    ASCENDANY_DESKTOP_BUILD_CACHE_PATH="${BUILD_CACHE_SEED}" \
    ASCENDANY_DESKTOP_GPG_PATH="${FAKE_BIN}/gpg" \
    ASCENDANY_DESKTOP_RPM_PATH="${FAKE_BIN}/rpm" \
    ASCENDANY_DESKTOP_RPMKEYS_PATH="${FAKE_BIN}/rpmkeys" \
    ASCENDANY_DESKTOP_RPMSIGN_PATH="${FAKE_BIN}/rpmsign" \
    GNUPGHOME="${gnupg_home}" \
    VITE_API_BASE_URL="${api_origin}" \
    VITE_CHAT_PROMPT_CONFIGURATION_KEY='agent.prompt.default' \
    VITE_CHAT_MODEL_CONFIGURATION_KEY='agent.model.default' \
    FIXTURE_PATH_HIJACK_MARKER="${PATH_HIJACK_MARKER}" \
    "${builder_path}" || builder_status=$?
  exec {ambient_fd}<&-
  return "${builder_status}"
}

git -C "${FIXTURE_REPOSITORY}" update-index --chmod=-x \
  apps/desktop/scripts/build-windows-release.sh \
  apps/desktop/scripts/build-linux-rpm-release.sh
git -C "${FIXTURE_REPOSITORY}" commit --quiet \
  --message 'fixture: drift desktop release builder modes'
readonly MODE_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" update-index --chmod=+x \
  apps/desktop/scripts/build-windows-release.sh \
  apps/desktop/scripts/build-linux-rpm-release.sh

readonly INVALID_PARENT="${WORK_ROOT}/invalid"
install -d -m 0700 "${INVALID_PARENT}"

expect_failure "${WORK_ROOT}/windows-password-environment.log" \
  /usr/bin/env -i \
    CSC_KEY_PASSWORD='rejected-environment-password' \
    ASCENDANY_DESKTOP_CSC_PASSWORD_FD=1023 \
    "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
grep -q 'rejects signing passwords in process environment' \
  "${WORK_ROOT}/windows-password-environment.log" ||
  fail 'Windows release builder accepted the legacy password environment contract'
expect_failure "${WORK_ROOT}/windows-leading-zero-fd.log" \
  run_windows_builder_with_descriptor 03
grep -q 'canonical decimal descriptor from 3 through 1023' \
  "${WORK_ROOT}/windows-leading-zero-fd.log" ||
  fail 'Windows release builder accepted a leading-zero password descriptor'
expect_failure "${WORK_ROOT}/windows-huge-fd.log" \
  run_windows_builder_with_descriptor 999999999999999999999999999999999999999999
grep -q 'canonical decimal descriptor from 3 through 1023' \
  "${WORK_ROOT}/windows-huge-fd.log" ||
  fail 'Windows release builder evaluated an unbounded password descriptor'
expect_failure "${WORK_ROOT}/windows-closed-fd.log" \
  run_windows_builder_with_descriptor 1023
grep -q 'inherited readable file or pipe descriptor' "${WORK_ROOT}/windows-closed-fd.log" ||
  fail 'Windows release builder accepted a closed password descriptor'
expect_failure "${WORK_ROOT}/windows-invalid-environment-name.log" \
  run_windows_builder_with_invalid_environment_name
grep -q 'rejects environment names outside the shell variable contract' \
  "${WORK_ROOT}/windows-invalid-environment-name.log" ||
  fail 'Windows release builder accepted an environment entry it cannot clear with shell builtins'
[[ ! -e "${EXPORTED_FUNCTION_MARKER}" ]] ||
  fail 'Windows release builder evaluated an exported function before rejecting its environment name'
expect_failure "${WORK_ROOT}/windows-empty-password-fd.log" \
  run_windows_builder_with_password_file "${EMPTY_WINDOWS_PASSWORD_FILE}"
grep -q 'must contain 1..4096 bytes' "${WORK_ROOT}/windows-empty-password-fd.log" ||
  {
    cat "${WORK_ROOT}/windows-empty-password-fd.log" >&2
    fail 'Windows release builder accepted an empty EOF-terminated password descriptor'
  }
expect_failure "${WORK_ROOT}/windows-nul-password-fd.log" \
  run_windows_builder_with_password_file "${NUL_WINDOWS_PASSWORD_FILE}"
grep -q 'reach EOF within 4096 bytes and contain no NUL byte' \
  "${WORK_ROOT}/windows-nul-password-fd.log" ||
  fail 'Windows release builder accepted NUL in its password descriptor payload'
expect_failure "${WORK_ROOT}/windows-long-password-fd.log" \
  run_windows_builder_with_password_file "${LONG_WINDOWS_PASSWORD_FILE}"
grep -q 'reach EOF within 4096 bytes and contain no NUL byte' \
  "${WORK_ROOT}/windows-long-password-fd.log" ||
  fail 'Windows release builder read beyond its bounded password descriptor contract'

FIXTURE_WINDOWS_REQUESTED_COMMIT="${MODE_DRIFT_COMMIT}" \
  expect_failure "${WORK_ROOT}/windows-commit-builder-mode.log" \
    run_windows_builder '2.3.4' "${INVALID_PARENT}/windows-commit-builder-mode" \
      "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q 'captured reviewed commit Windows release builder must be one mode 100755 regular file' "${WORK_ROOT}/windows-commit-builder-mode.log" ||
  fail 'Windows release builder accepted a non-executable reviewed builder entry'
FIXTURE_RPM_REQUESTED_COMMIT="${MODE_DRIFT_COMMIT}" \
  expect_failure "${WORK_ROOT}/rpm-commit-builder-mode.log" \
    run_rpm_builder '2.3.4' "${INVALID_PARENT}/rpm-commit-builder-mode" "${RPM_FINGERPRINT}"
grep -q 'captured reviewed commit RPM release builder must be one mode 100755 regular file' "${WORK_ROOT}/rpm-commit-builder-mode.log" ||
  fail 'RPM release builder accepted a non-executable reviewed builder entry'

/usr/bin/printf '\n# dirty live Windows builder\n' >> \
  "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
expect_failure "${WORK_ROOT}/windows-dirty-builder.log" \
  run_windows_builder '2.3.4' "${INVALID_PARENT}/windows-dirty-builder" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q 'builder bytes differ from the reviewed commit' "${WORK_ROOT}/windows-dirty-builder.log" ||
  fail 'Windows release builder accepted dirty live builder bytes'
install -m 0755 "${WINDOWS_BUILDER_SOURCE}" \
  "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"

/usr/bin/printf '\n# dirty live RPM builder\n' >> \
  "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"
expect_failure "${WORK_ROOT}/rpm-dirty-builder.log" \
  run_rpm_builder '2.3.4' "${INVALID_PARENT}/rpm-dirty-builder" "${RPM_FINGERPRINT}"
grep -q 'builder bytes differ from the reviewed commit' "${WORK_ROOT}/rpm-dirty-builder.log" ||
  fail 'RPM release builder accepted dirty live builder bytes'
install -m 0755 "${RPM_BUILDER_SOURCE}" \
  "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"

chmod 0700 "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
expect_failure "${WORK_ROOT}/windows-live-builder-mode.log" \
  run_windows_builder '2.3.4' "${INVALID_PARENT}/windows-live-builder-mode" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q 'must be mode 0755' "${WORK_ROOT}/windows-live-builder-mode.log" ||
  fail 'Windows release builder accepted a live builder outside exact mode 0755'
chmod 0755 "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"

chmod 0700 "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"
expect_failure "${WORK_ROOT}/rpm-live-builder-mode.log" \
  run_rpm_builder '2.3.4' "${INVALID_PARENT}/rpm-live-builder-mode" "${RPM_FINGERPRINT}"
grep -q 'must be mode 0755' "${WORK_ROOT}/rpm-live-builder-mode.log" ||
  fail 'RPM release builder accepted a live builder outside exact mode 0755'
chmod 0755 "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"

mv -- "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh" \
  "${FIXTURE_DESKTOP}/scripts/build-windows-release.real"
ln -s build-windows-release.real "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
expect_failure "${WORK_ROOT}/windows-symlink-builder.log" \
  run_windows_builder '2.3.4' "${INVALID_PARENT}/windows-symlink-builder" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q 'canonical fixed repository file' "${WORK_ROOT}/windows-symlink-builder.log" ||
  fail 'Windows release builder accepted a symlink at its fixed live path'
rm -- "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
mv -- "${FIXTURE_DESKTOP}/scripts/build-windows-release.real" \
  "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"

mv -- "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh" \
  "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.real"
ln -s build-linux-rpm-release.real "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"
expect_failure "${WORK_ROOT}/rpm-symlink-builder.log" \
  run_rpm_builder '2.3.4' "${INVALID_PARENT}/rpm-symlink-builder" "${RPM_FINGERPRINT}"
grep -q 'canonical fixed repository file' "${WORK_ROOT}/rpm-symlink-builder.log" ||
  fail 'RPM release builder accepted a symlink at its fixed live path'
rm -- "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"
mv -- "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.real" \
  "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"

expect_failure "${WORK_ROOT}/unprivileged-windows-shell.log" \
  /usr/bin/bash "${FIXTURE_DESKTOP}/scripts/build-windows-release.sh"
grep -q 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/unprivileged-windows-shell.log" ||
  fail 'Windows release builder accepted a shell without privileged mode'
expect_failure "${WORK_ROOT}/unprivileged-rpm-shell.log" \
  /usr/bin/bash "${FIXTURE_DESKTOP}/scripts/build-linux-rpm-release.sh"
grep -q 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/unprivileged-rpm-shell.log" ||
  fail 'RPM release builder accepted a shell without privileged mode'
/usr/bin/printf '%s\n' \
  '#!/usr/bin/bash -p' \
  'attack_marker="$1"' \
  'target="$2"' \
  'builtin() { /usr/bin/touch -- "$attack_marker"; }' \
  'exit() { /usr/bin/touch -- "$attack_marker"; return 0; }' \
  'printf() { /usr/bin/touch -- "$attack_marker"; }' \
  'export() { /usr/bin/touch -- "$attack_marker"; }' \
  'BASH_ARGV0="$target"' \
  'source "$target"' \
  >"${SOURCED_FILE_WRAPPER}"
/usr/bin/chmod 0700 "${SOURCED_FILE_WRAPPER}"
for builder in build-windows-release.sh build-linux-rpm-release.sh; do
  rm -f -- "${SOURCED_FUNCTION_MARKER}"
  expect_failure "${WORK_ROOT}/sourced-${builder}.log" \
    /usr/bin/bash -p -c \
      'attack_marker="$1"; builtin() { /usr/bin/touch -- "$attack_marker"; }; exit() { /usr/bin/touch -- "$attack_marker"; return 0; }; printf() { /usr/bin/touch -- "$attack_marker"; }; export() { /usr/bin/touch -- "$attack_marker"; }; source "$0"' \
      "${FIXTURE_DESKTOP}/scripts/${builder}" "${SOURCED_FUNCTION_MARKER}"
  grep -q 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/sourced-${builder}.log" ||
    fail "${builder} accepted a sourced privileged shell"
  [[ ! -e "${SOURCED_FUNCTION_MARKER}" ]] ||
    fail "${builder} ran a local function before rejecting a sourced privileged shell"
  rm -f -- "${SOURCED_FUNCTION_MARKER}"
  expect_failure "${WORK_ROOT}/sourced-file-${builder}.log" \
    /usr/bin/bash -p "${SOURCED_FILE_WRAPPER}" \
      "${SOURCED_FUNCTION_MARKER}" "${FIXTURE_DESKTOP}/scripts/${builder}"
  grep -q 'must run directly under /usr/bin/bash -p' "${WORK_ROOT}/sourced-file-${builder}.log" ||
    fail "${builder} accepted a sourced file with a forged argv zero"
  [[ ! -e "${SOURCED_FUNCTION_MARKER}" ]] ||
    fail "${builder} ran a local function before rejecting a sourced file"
done
expect_failure "${WORK_ROOT}/invalid-windows.log" \
  run_windows_builder '01.2.3' "${INVALID_PARENT}/windows" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q 'canonical SemVer' "${WORK_ROOT}/invalid-windows.log" ||
  fail 'Windows release accepted a non-canonical SemVer'
expect_failure "${WORK_ROOT}/invalid-rpm.log" \
  run_rpm_builder '1.2.3-01' "${INVALID_PARENT}/rpm" "${RPM_FINGERPRINT}"
grep -q 'canonical SemVer' "${WORK_ROOT}/invalid-rpm.log" ||
  fail 'RPM release accepted a non-canonical SemVer'
expect_failure "${WORK_ROOT}/invalid-windows-origin.log" \
  run_windows_builder '1.2.3' "${INVALID_PARENT}/invalid-windows-origin" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}" 'https://EXAMPLE.invalid'
grep -q 'canonical HTTPS origin' "${WORK_ROOT}/invalid-windows-origin.log" ||
  fail 'Windows release accepted a noncanonical uppercase HTTPS origin'
expect_failure "${WORK_ROOT}/invalid-rpm-origin.log" \
  run_rpm_builder '1.2.3' "${INVALID_PARENT}/invalid-rpm-origin" \
    "${RPM_FINGERPRINT}" 'https://example.invalid:99999'
grep -q 'canonical HTTPS origin' "${WORK_ROOT}/invalid-rpm-origin.log" ||
  fail 'RPM release accepted an out-of-range HTTPS port'

printf -v long_build_identifier '%*s' 123 ''
long_build_identifier="${long_build_identifier// /a}"
readonly TOO_LONG_VERSION="1.2.3+${long_build_identifier}"
unset long_build_identifier
[[ "${#TOO_LONG_VERSION}" == 129 ]] || fail 'long SemVer fixture has the wrong length'
expect_failure "${WORK_ROOT}/long-windows.log" \
  run_windows_builder "${TOO_LONG_VERSION}" "${INVALID_PARENT}/long-windows" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q '128 ASCII bytes' "${WORK_ROOT}/long-windows.log" ||
  fail 'Windows release accepted an overlong canonical SemVer'
expect_failure "${WORK_ROOT}/long-rpm.log" \
  run_rpm_builder "${TOO_LONG_VERSION}" "${INVALID_PARENT}/long-rpm" "${RPM_FINGERPRINT}"
grep -q '128 ASCII bytes' "${WORK_ROOT}/long-rpm.log" ||
  fail 'RPM release accepted an overlong canonical SemVer'

readonly UNSAFE_ANCESTRY_ROOT="${WORK_ROOT}/unsafe-ancestry-root"
readonly UNSAFE_OUTPUT_PARENT="${UNSAFE_ANCESTRY_ROOT}/safe-output-parent"
install -d -m 0770 "${UNSAFE_ANCESTRY_ROOT}"
install -d -m 0700 "${UNSAFE_OUTPUT_PARENT}"
expect_failure "${WORK_ROOT}/unsafe-output-ancestry.log" \
  run_windows_builder '2.3.4' "${UNSAFE_OUTPUT_PARENT}/windows" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
grep -q 'output parent has an unprotected writable ancestor' \
  "${WORK_ROOT}/unsafe-output-ancestry.log" ||
  fail 'desktop output parent under writable ancestry was accepted'

readonly UNSAFE_CERTIFICATE_DIRECTORY="${UNSAFE_ANCESTRY_ROOT}/safe-certificate-directory"
readonly UNSAFE_ANCESTRY_CERTIFICATE="${UNSAFE_CERTIFICATE_DIRECTORY}/windows-signing.p12"
install -d -m 0700 "${UNSAFE_CERTIFICATE_DIRECTORY}"
install -m 0600 "${CERTIFICATE_FILE}" "${UNSAFE_ANCESTRY_CERTIFICATE}"
expect_failure "${WORK_ROOT}/unsafe-certificate-ancestry.log" \
  run_windows_builder '2.3.4' "${INVALID_PARENT}/unsafe-certificate" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}" \
    'https://ascendany.example.invalid' "${UNSAFE_ANCESTRY_CERTIFICATE}"
grep -q 'CSC_LINK has an unprotected writable ancestor' \
  "${WORK_ROOT}/unsafe-certificate-ancestry.log" ||
  fail 'Windows signing material under writable ancestry was accepted'

readonly UNSAFE_TOOL_DIRECTORY="${UNSAFE_ANCESTRY_ROOT}/safe-tool-directory"
readonly UNSAFE_ANCESTRY_NODE="${UNSAFE_TOOL_DIRECTORY}/node"
install -d -m 0700 "${UNSAFE_TOOL_DIRECTORY}"
install -m 0755 "${FAKE_BIN}/node" "${UNSAFE_ANCESTRY_NODE}"
expect_failure "${WORK_ROOT}/unsafe-tool-ancestry.log" \
  run_windows_builder '2.3.4' "${INVALID_PARENT}/unsafe-tool" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}" \
    'https://ascendany.example.invalid' "${CERTIFICATE_FILE}" \
    "${RELEASE_HOME}" "${UNSAFE_ANCESTRY_NODE}"
grep -q 'ASCENDANY_DESKTOP_NODE_PATH has an unprotected writable ancestor' \
  "${WORK_ROOT}/unsafe-tool-ancestry.log" ||
  fail 'desktop release tool under writable ancestry was accepted'

readonly UNSAFE_HOME_ROOT="${WORK_ROOT}/unsafe-home-root"
readonly UNSAFE_RELEASE_HOME="${UNSAFE_HOME_ROOT}/release-home"
readonly UNSAFE_GNUPG_HOME="${UNSAFE_RELEASE_HOME}/.gnupg"
install -d -m 0770 "${UNSAFE_HOME_ROOT}"
install -d -m 0700 "${UNSAFE_RELEASE_HOME}" "${UNSAFE_GNUPG_HOME}"
expect_failure "${WORK_ROOT}/unsafe-home-ancestry.log" \
  run_rpm_builder '2.3.4' "${INVALID_PARENT}/unsafe-home" "${RPM_FINGERPRINT}" \
    'https://ascendany.example.invalid' "${UNSAFE_GNUPG_HOME}" "${UNSAFE_RELEASE_HOME}"
grep -q 'HOME has an unprotected writable ancestor' "${WORK_ROOT}/unsafe-home-ancestry.log" ||
  fail 'desktop release HOME under writable ancestry was accepted'

readonly ALTERNATE_GNUPG_HOME="${RELEASE_HOME}/alternate-gnupg"
install -d -m 0700 "${ALTERNATE_GNUPG_HOME}"
expect_failure "${WORK_ROOT}/alternate-gnupg.log" \
  run_rpm_builder '2.3.4' "${INVALID_PARENT}/alternate-gnupg" "${RPM_FINGERPRINT}" \
    'https://ascendany.example.invalid' "${ALTERNATE_GNUPG_HOME}" "${RELEASE_HOME}"
grep -q 'GNUPGHOME must equal the canonical HOME/.gnupg' "${WORK_ROOT}/alternate-gnupg.log" ||
  fail 'RPM release accepted a keyring outside canonical HOME/.gnupg'

readonly XTRACE_PASSWORD='fixture-password'
readonly XTRACE_CERTIFICATE_FILE="${WORK_ROOT}/xtrace-signing-material-must-not-leak.p12"
install -m 0600 "${CERTIFICATE_FILE}" "${XTRACE_CERTIFICATE_FILE}"
expect_failure "${WORK_ROOT}/windows-xtrace.log" \
  run_windows_builder_xtrace
grep -q 'canonical HTTPS origin' "${WORK_ROOT}/windows-xtrace.log" ||
  fail 'Windows xtrace fixture did not reach a post-credential validation boundary'
if grep -Fq "${XTRACE_PASSWORD}" "${WORK_ROOT}/windows-xtrace.log" ||
  grep -Fq "${XTRACE_CERTIFICATE_FILE}" "${WORK_ROOT}/windows-xtrace.log"; then
  fail 'Windows release exposed signing material through xtrace'
fi

readonly CORRUPT_OBJECT_PARENT="${WORK_ROOT}/corrupt-object"
install -d -m 0700 "${CORRUPT_OBJECT_PARENT}"
corrupt_blob="$(git -C "${FIXTURE_REPOSITORY}" rev-parse "${COMMIT}:apps/desktop/fixture-source.txt")"
corrupt_object="${FIXTURE_REPOSITORY}/.git/objects/${corrupt_blob:0:2}/${corrupt_blob:2}"
install -m 0444 -- "${corrupt_object}" "${WORK_ROOT}/corrupt-object-backup"
chmod 0644 -- "${corrupt_object}"
printf 'corrupt reviewed blob\n' >"${corrupt_object}"
expect_failure "${WORK_ROOT}/corrupt-object.log" \
  run_rpm_builder '2.3.4' "${CORRUPT_OBJECT_PARENT}/release" "${RPM_FINGERPRINT}"
install -m 0444 -- "${WORK_ROOT}/corrupt-object-backup" "${corrupt_object}"
grep -Eq 'blob could not be materialized|corrupt|inflate|incorrect header' \
  "${WORK_ROOT}/corrupt-object.log" ||
  fail 'desktop release did not reject a corrupt reviewed Git blob'
[[ ! -e "${CORRUPT_OBJECT_PARENT}/release" ]] ||
  fail 'corrupt reviewed Git object produced a release'
assert_private_workspace_cleaned "${CORRUPT_OBJECT_PARENT}"
unset corrupt_blob corrupt_object

readonly SIGN_PATH_PARENT="${WORK_ROOT}/sign-path-parent"
install -d -m 0700 "${SIGN_PATH_PARENT}"
install -m 0600 /dev/null "${WINDOWS_SIGN_PATH_ATTACK}"
expect_failure "${WORK_ROOT}/sign-path.log" \
  run_windows_builder '2.3.4' "${SIGN_PATH_PARENT}/release" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
rm -f -- "${WINDOWS_SIGN_PATH_ATTACK}"
grep -q 'signing hook rejected path' "${WORK_ROOT}/sign-path.log" ||
  fail 'Windows signing hook accepted a path outside its closed allowlist'
[[ ! -e "${SIGN_PATH_PARENT}/release" ]] ||
  fail 'out-of-contract Windows signing request produced a release'
assert_private_workspace_cleaned "${SIGN_PATH_PARENT}"

readonly SIGN_SYMLINK_PARENT="${WORK_ROOT}/sign-symlink-parent"
install -d -m 0700 "${SIGN_SYMLINK_PARENT}"
printf 'protected victim\n' >"${WINDOWS_SIGN_VICTIM}"
chmod 0600 "${WINDOWS_SIGN_VICTIM}"
install -m 0600 /dev/null "${WINDOWS_SIGN_SYMLINK_ATTACK}"
expect_failure "${WORK_ROOT}/sign-symlink.log" \
  run_windows_builder '2.3.4' "${SIGN_SYMLINK_PARENT}/release" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
rm -f -- "${WINDOWS_SIGN_SYMLINK_ATTACK}"
[[ "$(<"${WINDOWS_SIGN_VICTIM}")" == 'protected victim' ]] ||
  fail 'Windows signing broker followed a symlink to an external victim'
[[ ! -e "${SIGN_SYMLINK_PARENT}/release" ]] ||
  fail 'symlink Windows signing request produced a release'
assert_private_workspace_cleaned "${SIGN_SYMLINK_PARENT}"

readonly WINDOWS_PARENT="${WORK_ROOT}/windows-happy"
readonly WINDOWS_OUTPUT="${WINDOWS_PARENT}/release"
install -d -m 0700 "${WINDOWS_PARENT}"
run_windows_builder '2.3.4-rc.1+build.5' "${WINDOWS_OUTPUT}" \
  "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}" >"${WORK_ROOT}/windows-happy.log"
if grep -Fq 'fixture-password' "${WORK_ROOT}/windows-happy.log"; then
  fail 'Windows release exposed its signing password through process output'
fi
[[ "$(<"${SOURCE_MODE_FILE}")" == '700' ]] ||
  fail 'Windows detached source root is not mode 0700'
assert_release_contract \
  "${WINDOWS_OUTPUT}" \
  'AscendAny-win-x64-2.3.4-rc.1+build.5.exe' \
  'committed desktop source'
assert_private_workspace_cleaned "${WINDOWS_PARENT}"

readonly RPM_PARENT="${WORK_ROOT}/rpm-happy"
readonly RPM_OUTPUT="${RPM_PARENT}/release"
install -d -m 0700 "${RPM_PARENT}"
run_rpm_builder '2.3.4' "${RPM_OUTPUT}" "${RPM_FINGERPRINT}" >"${WORK_ROOT}/rpm-happy.log"
[[ ! -e "${RPM_MACRO_ATTACK_MARKER}" ]] ||
  fail 'RPM external signing evaluated a release-HOME user macro'
[[ "$(<"${SOURCE_MODE_FILE}")" == '700' ]] ||
  fail 'RPM detached source root is not mode 0700'
assert_release_contract \
  "${RPM_OUTPUT}" \
  'AscendAny-linux-x64-2.3.4.rpm' \
  'committed desktop source'
[[ "$(<"${FIXTURE_DESKTOP}/fixture-source.txt")" == 'dirty during desktop release' ]] ||
  fail 'fixture did not mutate the live worktree during detached builds'
assert_private_workspace_cleaned "${RPM_PARENT}"

readonly WINDOWS_IDENTITY_PARENT="${WORK_ROOT}/windows-identity-parent"
install -d -m 0700 "${WINDOWS_IDENTITY_PARENT}"
printf '%s\n' "${WINDOWS_IDENTITY_PARENT}" >"${OUTPUT_PARENT_SWAP}"
expect_failure "${WORK_ROOT}/windows-output-parent-identity.log" \
  run_windows_builder '2.3.4' "${WINDOWS_IDENTITY_PARENT}/release" \
    "${WINDOWS_FINGERPRINT}" "${WINDOWS_FINGERPRINT}"
rm -f -- "${OUTPUT_PARENT_SWAP}"
grep -q 'output parent identity changed before installer verification' \
  "${WORK_ROOT}/windows-output-parent-identity.log" ||
  fail 'Windows output parent inode replacement was not rejected'
[[ ! -e "${WINDOWS_IDENTITY_PARENT}/release" ]] ||
  fail 'replacement Windows output parent received a release'
assert_private_workspace_cleaned "${WINDOWS_IDENTITY_PARENT}.moved"
rm -rf -- "${WINDOWS_IDENTITY_PARENT}" "${WINDOWS_IDENTITY_PARENT}.moved"

readonly RPM_IDENTITY_PARENT="${WORK_ROOT}/rpm-identity-parent"
install -d -m 0700 "${RPM_IDENTITY_PARENT}"
printf '%s\n' "${RPM_IDENTITY_PARENT}" >"${OUTPUT_PARENT_SWAP}"
expect_failure "${WORK_ROOT}/rpm-output-parent-identity.log" \
  run_rpm_builder '2.3.4' "${RPM_IDENTITY_PARENT}/release" "${RPM_FINGERPRINT}"
rm -f -- "${OUTPUT_PARENT_SWAP}"
grep -q 'output parent identity changed before RPM verification' \
  "${WORK_ROOT}/rpm-output-parent-identity.log" ||
  fail 'RPM output parent inode replacement was not rejected'
[[ ! -e "${RPM_IDENTITY_PARENT}/release" ]] ||
  fail 'replacement RPM output parent received a release'
assert_private_workspace_cleaned "${RPM_IDENTITY_PARENT}.moved"
rm -rf -- "${RPM_IDENTITY_PARENT}" "${RPM_IDENTITY_PARENT}.moved"

readonly MISMATCH_PARENT="${WORK_ROOT}/identity-mismatch"
install -d -m 0700 "${MISMATCH_PARENT}"
expect_failure "${WORK_ROOT}/windows-mismatch.log" \
  run_windows_builder '2.3.4' "${MISMATCH_PARENT}/windows" \
    "${WINDOWS_FINGERPRINT}" "${OTHER_WINDOWS_FINGERPRINT}"
grep -q 'leaf certificate fingerprint does not match' "${WORK_ROOT}/windows-mismatch.log" ||
  fail 'Windows release did not reject a signer identity mismatch'
[[ ! -e "${MISMATCH_PARENT}/windows" ]] ||
  fail 'Windows signer mismatch published a release'
assert_private_workspace_cleaned "${MISMATCH_PARENT}"

expect_failure "${WORK_ROOT}/rpm-mismatch.log" \
  run_rpm_builder '2.3.4' "${MISMATCH_PARENT}/rpm" "${OTHER_RPM_FINGERPRINT}"
grep -q 'signer fingerprint does not match' "${WORK_ROOT}/rpm-mismatch.log" ||
  fail 'RPM release did not reject a signer identity mismatch'
[[ ! -e "${MISMATCH_PARENT}/rpm" ]] || fail 'RPM signer mismatch published a release'
assert_private_workspace_cleaned "${MISMATCH_PARENT}"
[[ ! -e "${PATH_HIJACK_MARKER}" ]] ||
  fail 'desktop release executed a tool from the mutable live worktree PATH'
[[ ! -e "${PATH_TOOL_MARKER}" ]] ||
  fail 'desktop release executed a caller-PATH shadow of an explicit release tool'
[[ ! -e "${FAKE_BASH_MARKER}" ]] || fail 'desktop release builder resolved Bash through caller PATH'
[[ ! -e "${BASH_ENV_MARKER}" ]] || fail 'desktop release builder evaluated caller BASH_ENV'
[[ ! -e "${EXPORTED_FUNCTION_MARKER}" ]] ||
  fail 'desktop release builder imported a caller shell function'
[[ ! -e "${GIT_PATH_EVAL_MARKER}" ]] ||
  fail 'desktop release builder evaluated a reviewed Git path as shell code'

printf 'PASS: desktop releases re-hash reviewed trees, isolate unsigned builds, broker closed Windows signing, and publish exact fsynced outputs\n'
