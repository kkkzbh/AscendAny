#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly MOBILE_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly BUILDER_SOURCE="${MOBILE_ROOT}/scripts/build-android-release.sh"
readonly MOBILE_PACKAGE_JSON="${MOBILE_ROOT}/package.json"
readonly ROOT_PACKAGE_JSON="${MOBILE_ROOT}/../../package.json"
readonly ENSURE_SOURCE="${MOBILE_ROOT}/scripts/ensure-android-signing.mjs"
readonly SIGNING_GRADLE_SOURCE="${MOBILE_ROOT}/android-signing.gradle"
readonly ANDROID_RELEASE_CONTRACT="${MOBILE_ROOT}/../../contracts/release-assets/android.v1.json"
readonly GRADLE_WRAPPER_PROPERTIES="${MOBILE_ROOT}/android/gradle/wrapper/gradle-wrapper.properties"
readonly GRADLE_WRAPPER_JAR="${MOBILE_ROOT}/android/gradle/wrapper/gradle-wrapper.jar"
readonly GRADLE_VERIFICATION_METADATA="${MOBILE_ROOT}/android/gradle/verification-metadata.xml"
readonly GRADLEW_SOURCE="${MOBILE_ROOT}/android/gradlew"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-android-release-fixture.XXXXXX")"
readonly FAKE_BIN="${WORK_ROOT}/fake-bin"
readonly FIXTURE_REPOSITORY="${WORK_ROOT}/repository"
readonly LIVE_TOOL_BIN="${FIXTURE_REPOSITORY}/.live-tool-bin"
readonly PATH_HIJACK_MARKER="${WORK_ROOT}/live-path-tool-executed"
readonly INTERPRETER_HIJACK_MARKER="${WORK_ROOT}/live-path-interpreter-executed"
readonly NODE_TOOL_HIJACK_MARKER="${WORK_ROOT}/live-path-node-tool-executed"
readonly APKSIGNER_PATH_HIJACK_MARKER="${WORK_ROOT}/caller-path-apksigner-executed"
readonly SYSTEM_TOOL_HIJACK_MARKER="${WORK_ROOT}/caller-path-system-tool-executed"
readonly NODE_INJECTION_MARKER="${WORK_ROOT}/ambient-node-options-executed"
readonly BASH_ENV_MARKER="${WORK_ROOT}/bash-env-executed"
readonly SOURCE_FUNCTION_HIJACK_MARKER="${WORK_ROOT}/source-function-hijack-executed"
readonly MATERIALIZER_EVAL_MARKER="${WORK_ROOT}/materializer-path-evaluated"
readonly MALICIOUS_HOME="${WORK_ROOT}/ambient-home"
readonly MALICIOUS_GRADLE_USER_HOME="${WORK_ROOT}/ambient-gradle-user-home"
readonly ACTUAL_FINGERPRINT="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
readonly TRUSTED_NODE_BINARY="$(realpath -e -- "$(command -v node)")"
readonly TRUSTED_APKSIGNER_PREFIX="${WORK_ROOT}/trusted-apksigner"
readonly FIXTURE_JAVA_HOME="${WORK_ROOT}/fixture-java-home"
readonly FIXTURE_ANDROID_HOME="${WORK_ROOT}/fixture-android-home"
readonly FIXTURE_KEYSTORE="${FIXTURE_JAVA_HOME}/fixture-release.jks"
readonly WEIRD_PROVENANCE_RELATIVE=$'provenance/name-with-tab\tand-newline\n.bin'

cleanup() {
  rm -rf -- "${WORK_ROOT}"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

[[ -x /usr/bin/bwrap && ! -L /usr/bin/bwrap ]] || fail 'fixed /usr/bin/bwrap is unavailable'
[[ "$(/usr/bin/bwrap --version)" == bubblewrap\ * ]] || fail 'fixed /usr/bin/bwrap did not report its version'

expect_failure() {
  local expected_message="$1"
  local log_path="$2"
  local command_pid
  shift 2
  "$@" >"${log_path}" 2>&1 &
  command_pid=$!
  if wait "${command_pid}" 2>/dev/null; then
    fail "command unexpectedly succeeded: $*"
  fi
  grep -F -- "${expected_message}" "${log_path}" >/dev/null ||
    fail "failure did not contain expected message: ${expected_message}"
}

assert_no_build_workspace() {
  local parent="$1"
  if find "${parent}" -mindepth 1 -maxdepth 1 -name '.ascendany-android-release.*' -print -quit | grep -q .; then
    fail "detached Android build workspace leaked under ${parent}"
  fi
}

grep -Fx 'distributionSha256Sum=ed1a8d686605fd7c23bdf62c7fc7add1c5b23b2bbc3721e661934ef4a4911d7c' \
  "${GRADLE_WRAPPER_PROPERTIES}" >/dev/null || fail 'Gradle 8.14.3 all distribution checksum is not pinned'
[[ "$(sha256sum -- "${GRADLE_WRAPPER_JAR}" | awk '{print $1}')" == \
   '7d3a4ac4de1c32b59bc6a4eb8ecb8e612ccd0cf1ae1e99f66902da64df296172' ]] ||
  fail 'Gradle 8.14.3 wrapper JAR differs from the official checksum'
grep -F "readonly PINNED_APKSIGNER_BINARY_SHA256='b47549e373b895ce6ca620d0c7887e674d9615ffa837a86ac601dcfd04adb0f0'" \
  "${BUILDER_SOURCE}" >/dev/null || fail 'Android Build Tools 36.0.0 apksigner launcher is not pinned'
grep -F "readonly PINNED_APKSIGNER_JAR_SHA256='3716d9311e55d2b0918a2fd9d54ba9e406c5f6abeea700b287f11259bc163dec'" \
  "${BUILDER_SOURCE}" >/dev/null || fail 'Android Build Tools 36.0.0 apksigner JAR is not pinned'
grep -F '/usr/bin/sync -f --' "${BUILDER_SOURCE}" >/dev/null ||
  fail 'Android release publication does not fsync staged and published output'
grep -F 'verify_android_output "${OUTPUT_DIRECTORY}" published' "${BUILDER_SOURCE}" >/dev/null ||
  fail 'Android release publication lacks final digest and structure verification'
grep -F 'cleanup_published_output' "${BUILDER_SOURCE}" >/dev/null ||
  fail 'Android release publication lacks identity-checked failure cleanup'
grep -F 'PATH="${SIGNING_TOOL_BIN}:${SYSTEM_PATH}"' "${BUILDER_SOURCE}" >/dev/null ||
  fail 'Android signer does not resolve java through the validated private tool path'
if grep -F -- '--apksigner-jar-sha256' "${BUILDER_SOURCE}" >/dev/null; then
  fail 'Android release signer identity still accepts a caller-provided JAR digest'
fi

if ! node - "${ROOT_PACKAGE_JSON}" "${MOBILE_PACKAGE_JSON}" <<'NODE'
const fs = require("node:fs");
const rootPackage = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const mobilePackage = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
const rootScripts = rootPackage.scripts ?? {};
const mobileScripts = mobilePackage.scripts ?? {};
if (Object.hasOwn(rootScripts, "build:android")) process.exit(1);
if (Object.hasOwn(mobileScripts, "dist:android:release")) process.exit(1);
for (const command of [...Object.values(rootScripts), ...Object.values(mobileScripts)]) {
  if (command.includes("build-android-release.sh") || command.includes("dist:android:release")) {
    process.exit(1);
  }
}
if (!mobileScripts["sync:android"]?.includes("test -d android")) process.exit(1);
if (mobileScripts["sync:android"].includes("cap add")) process.exit(1);
NODE
then
  fail 'package scripts expose the signing release wrapper through Node/pnpm'
fi

readonly ENSURE_FIXTURE_ROOT="${WORK_ROOT}/ensure-fixture"
install -d -m 0700 "${ENSURE_FIXTURE_ROOT}/scripts" "${ENSURE_FIXTURE_ROOT}/android/app"
install -m 0644 "${ENSURE_SOURCE}" "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs"
install -m 0644 "${SIGNING_GRADLE_SOURCE}" "${ENSURE_FIXTURE_ROOT}/android-signing.gradle"

readonly APPLY_LINE="apply from: '../../android-signing.gradle'"
readonly APP_GRADLE_FIXTURE="${ENSURE_FIXTURE_ROOT}/android/app/build.gradle"
printf '%s\n' "// ${APPLY_LINE}" >"${APP_GRADLE_FIXTURE}"
node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs" >/dev/null
[[ "$(tail -n 1 "${APP_GRADLE_FIXTURE}")" == "${APPLY_LINE}" ]] ||
  fail 'a line comment produced a false-positive signing apply match'
comment_fixture_digest="$(sha256sum -- "${APP_GRADLE_FIXTURE}" | awk '{print $1}')"
node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs" >/dev/null
[[ "$(sha256sum -- "${APP_GRADLE_FIXTURE}" | awk '{print $1}')" == "${comment_fixture_digest}" ]] ||
  fail 'an exact active signing apply line was duplicated'
unset comment_fixture_digest

printf '/*\n%s\n*/\n' "${APPLY_LINE}" >"${APP_GRADLE_FIXTURE}"
node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs" >/dev/null
[[ "$(tail -n 1 "${APP_GRADLE_FIXTURE}")" == "${APPLY_LINE}" ]] ||
  fail 'a block comment produced a false-positive signing apply match'

for multiline_literal in triple-double slashy dollar-slashy; do
  case "${multiline_literal}" in
    triple-double) printf 'def decoy = """\n%s\n"""\n' "${APPLY_LINE}" >"${APP_GRADLE_FIXTURE}" ;;
    slashy) printf 'def decoy = /\napply from: '\''..\\/..\\/android-signing.gradle'\''\n/\n' >"${APP_GRADLE_FIXTURE}" ;;
    dollar-slashy) printf 'def decoy = $/\n%s\n/$\n' "${APPLY_LINE}" >"${APP_GRADLE_FIXTURE}" ;;
  esac
  node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs" >/dev/null
  [[ "$(tail -n 1 "${APP_GRADLE_FIXTURE}")" == "${APPLY_LINE}" ]] ||
    fail "${multiline_literal} produced a false-positive signing apply match"
  multiline_fixture_digest="$(sha256sum -- "${APP_GRADLE_FIXTURE}" | awk '{print $1}')"
  node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs" >/dev/null
  [[ "$(sha256sum -- "${APP_GRADLE_FIXTURE}" | awk '{print $1}')" == "${multiline_fixture_digest}" ]] ||
    fail "${multiline_literal} fixture was not idempotent after appending the active apply"
done
unset multiline_literal multiline_fixture_digest

printf '%s\n%s\n' "${APPLY_LINE}" "${APPLY_LINE}" >"${APP_GRADLE_FIXTURE}"
expect_failure \
  'Android signing patch is applied more than once' \
  "${WORK_ROOT}/duplicate-apply.log" \
  node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs"

printf '%s\n' 'apply from: "../../android-signing.gradle"' >"${APP_GRADLE_FIXTURE}"
expect_failure \
  'Android signing apply line must exactly equal' \
  "${WORK_ROOT}/noncanonical-apply.log" \
  node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs"

printf '%s\n%s\n' "${APPLY_LINE}" 'println "statement after signing apply"' >"${APP_GRADLE_FIXTURE}"
expect_failure \
  'Android signing apply line must be the final active statement' \
  "${WORK_ROOT}/nonfinal-apply.log" \
  node "${ENSURE_FIXTURE_ROOT}/scripts/ensure-android-signing.mjs"

install -d -m 0700 \
  "${FAKE_BIN}" \
  "${FIXTURE_JAVA_HOME}/bin" \
  "${FIXTURE_ANDROID_HOME}/platforms/android-36" \
  "${FIXTURE_ANDROID_HOME}/build-tools/36.0.0/lib" \
  "${FIXTURE_ANDROID_HOME}/platform-tools" \
  "${FIXTURE_REPOSITORY}/apps/mobile/scripts" \
  "${FIXTURE_REPOSITORY}/apps/mobile/android/app" \
  "${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/wrapper" \
  "${FIXTURE_REPOSITORY}/provenance"
install -m 0755 "${BUILDER_SOURCE}" "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh"
install -m 0644 "${ENSURE_SOURCE}" "${FIXTURE_REPOSITORY}/apps/mobile/scripts/ensure-android-signing.mjs"
install -m 0644 "${SIGNING_GRADLE_SOURCE}" "${FIXTURE_REPOSITORY}/apps/mobile/android-signing.gradle"
install -m 0755 "${GRADLEW_SOURCE}" "${FIXTURE_REPOSITORY}/apps/mobile/android/gradlew"
install -m 0644 \
  "${GRADLE_WRAPPER_PROPERTIES}" \
  "${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/wrapper/gradle-wrapper.properties"
install -m 0644 \
  "${GRADLE_WRAPPER_JAR}" \
  "${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/wrapper/gradle-wrapper.jar"
install -m 0644 \
  "${GRADLE_VERIFICATION_METADATA}" \
  "${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/verification-metadata.xml"
printf '%s\n' \
  '{' \
  '  "private": true,' \
  '  "packageManager": "pnpm@9.15.4",' \
  '  "engines": { "node": ">=22.18.0" }' \
  '}' \
  >"${FIXTURE_REPOSITORY}/package.json"
printf 'lockfileVersion: 9.0\n' >"${FIXTURE_REPOSITORY}/pnpm-lock.yaml"
printf 'committed release input\n' >"${FIXTURE_REPOSITORY}/release-input.txt"
printf '%s\n' \
  'release-input.txt export-ignore' \
  'provenance/crlf.txt text eol=lf' \
  >"${FIXTURE_REPOSITORY}/.gitattributes"
printf 'first\r\nsecond\r\n' >"${FIXTURE_REPOSITORY}/provenance/crlf.txt"
printf '\x00\x01\x7f\x80\xffbinary\x00payload\n' \
  >"${FIXTURE_REPOSITORY}/provenance/binary.bin"
printf 'no-final-newline' >"${FIXTURE_REPOSITORY}/provenance/no-final-newline.txt"
printf 'preserve-two-trailing-newlines\n\n' \
  >"${FIXTURE_REPOSITORY}/provenance/trailing-newlines.txt"
printf '#!/usr/bin/bash -p\nprintf provenance-mode\n' \
  >"${FIXTURE_REPOSITORY}/provenance/executable.sh"
chmod 0755 "${FIXTURE_REPOSITORY}/provenance/executable.sh"
printf 'tab, newline, binary byte \x00 and literal path payload\n' \
  >"${FIXTURE_REPOSITORY}/${WEIRD_PROVENANCE_RELATIVE}"
printf 'literal Git path payload\n' \
  >"${FIXTURE_REPOSITORY}/\$(touch\${IFS}\$FIXTURE_MATERIALIZER_EVAL_MARKER)"

printf '%s\n' \
  '#!/usr/bin/bash -p' \
  'set -Eeuo pipefail' \
  '[[ "${ASCENDANY_ANDROID_RELEASE_WRAPPER:-}" == "1" ]] || exit 69' \
  '[[ "${ASCENDANY_ANDROID_VERSION_CODE:-}" == "10203" ]] || exit 66' \
  '[[ "${HOME:-}" == */build-home && -d "${HOME}" ]] || exit 86' \
  'fixture_root="${HOME%/build-home}"' \
  '[[ "$(</proc/1/comm)" == "bwrap" ]] || { printf "Gradle did not run below namespace init\n" >&2; exit 95; }' \
  'while IFS= read -r -d "" namespace_init_entry; do namespace_init_name="${namespace_init_entry%%=*}"; case "${namespace_init_name}" in PATH|LC_ALL) ;; *) printf "ambient variable reached Gradle namespace init: %s\n" "${namespace_init_name}" >&2; exit 101 ;; esac; done </proc/1/environ' \
  'if /usr/bin/grep -zF "fixture-ambient-secret" /proc/1/environ >/dev/null; then printf "ambient secret reached Gradle namespace init\n" >&2; exit 102; fi' \
  "fixture_keystore='${FIXTURE_KEYSTORE}'" \
  'if [[ -r "${fixture_keystore}" ]] && /usr/bin/grep -F "fixture keystore bytes" "${fixture_keystore}" >/dev/null 2>&1; then printf "keystore bytes are visible in Gradle namespace\n" >&2; exit 96; fi' \
  'keystore_path_seen=0' \
  'for process_cmdline in /proc/[0-9]*/cmdline; do if /usr/bin/grep -zF -- "${fixture_keystore}" "${process_cmdline}" >/dev/null 2>&1; then keystore_path_seen=1; fi; done' \
  '(( keystore_path_seen == 1 )) || { printf "Gradle fixture did not exercise cmdline keystore discovery\n" >&2; exit 97; }' \
  '/usr/bin/sleep 0.2' \
  '[[ ! -e "${HOME}/escaped-pnpm-descendant" ]] || { printf "pnpm descendant survived namespace exit\n" >&2; exit 98; }' \
  '[[ ! -e "${HOME}/escaped-gradle-descendant" ]] || { printf "previous Gradle descendant survived namespace exit\n" >&2; exit 99; }' \
  'ln -s "${fixture_keystore}" "${fixture_root}/app-release-signed.apk"' \
  '[[ -L "${fixture_root}/app-release-signed.apk" ]] || { printf "Gradle fixture did not create its namespace-local output symlink\n" >&2; exit 100; }' \
  'java_arguments=("$@")' \
  'java_argument_count=$#' \
  '(( java_argument_count >= 7 )) || { printf "too few Java arguments: %s\n" "$*" >&2; exit 64; }' \
  'if [[ "${java_arguments[java_argument_count-6]}" == "${fixture_root}/prefetch-source/apps/mobile/android/gradle/wrapper/gradle-wrapper.jar" ]]; then' \
  '  [[ "${java_arguments[java_argument_count-7]}" == "-jar" && "${java_arguments[java_argument_count-5]}" == "--dependency-verification=strict" && "${java_arguments[java_argument_count-4]}" == "--no-daemon" && "${java_arguments[java_argument_count-3]}" == "--max-workers" && "${java_arguments[java_argument_count-2]}" == "1" && "${java_arguments[java_argument_count-1]}" == ":app:assembleRelease" ]] || { printf "unexpected Gradle prefetch arguments: %s\n" "$*" >&2; exit 64; }' \
  '  (( $(/usr/bin/wc -l </proc/net/route) > 1 )) || { printf "Gradle prefetch has no network route\n" >&2; exit 64; }' \
  '  printf "prefetched\n" >"${GRADLE_USER_HOME}/fixture-prefetched"' \
  'elif [[ "${java_arguments[java_argument_count-8]}" == "${fixture_root}/source/apps/mobile/android/gradle/wrapper/gradle-wrapper.jar" ]]; then' \
  '  [[ "${java_arguments[java_argument_count-9]}" == "-jar" && "${java_arguments[java_argument_count-7]}" == "--dependency-verification=strict" && "${java_arguments[java_argument_count-6]}" == "--offline" && "${java_arguments[java_argument_count-5]}" == "--no-build-cache" && "${java_arguments[java_argument_count-4]}" == "--no-daemon" && "${java_arguments[java_argument_count-3]}" == "--max-workers" && "${java_arguments[java_argument_count-2]}" == "1" && "${java_arguments[java_argument_count-1]}" == ":app:assembleRelease" ]] || { printf "unexpected offline Gradle arguments: %s\n" "$*" >&2; exit 64; }' \
  '  (( $(/usr/bin/wc -l </proc/net/route) == 1 )) || { printf "formal Gradle build retained a network route\n" >&2; exit 64; }' \
  '  [[ "$(<"${GRADLE_USER_HOME}/fixture-prefetched")" == "prefetched" ]] || { printf "formal Gradle build did not consume prefetch cache\n" >&2; exit 64; }' \
  'else' \
  '  printf "unexpected wrapper JAR: %s\n" "$*" >&2; exit 64' \
  'fi' \
  '[[ "${GRADLE_USER_HOME:-}" == "${HOME%/build-home}/gradle-user-home" && -d "${GRADLE_USER_HOME}" ]] || exit 87' \
  '[[ "${TMPDIR:-}" == "${HOME%/build-home}/tmp" && -d "${TMPDIR}" ]] || exit 88' \
  '[[ -z "${ASCENDANY_ANDROID_SIGNING_STORE_FILE+x}" ]] || exit 67' \
  '[[ -z "${ASCENDANY_ANDROID_SIGNING_STORE_PASSWORD+x}" ]] || exit 68' \
  '[[ -z "${ASCENDANY_ANDROID_SIGNING_KEY_ALIAS+x}" ]] || exit 76' \
  '[[ -z "${ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD+x}" ]] || exit 77' \
  '[[ -z "${ASCENDANY_APKSIGNER_STORE_PASSWORD+x}" && -z "${ASCENDANY_APKSIGNER_KEY_PASSWORD+x}" ]] || exit 78' \
  'if /usr/bin/grep -zF -e "fixture-store-password" -e "fixture-key-password" "/proc/${PPID}/environ" >/dev/null; then exit 90; fi' \
  'while IFS= read -r -d "" entry; do' \
  '  name="${entry%%=*}"' \
  '  case "${name}" in' \
  '    ORG_GRADLE_PROJECT_*|GRADLE_OPTS|JAVA_OPTS|JAVA_TOOL_OPTIONS|JDK_JAVA_OPTIONS|_JAVA_OPTIONS|BASH_ENV|ENV|BASH_FUNC_*|SIGNING_*|ASCENDANY_*SIGNING*) exit 89 ;;' \
  '  esac' \
  'done < <(/usr/bin/env -0)' \
  'install -d -m 0700 app/build/outputs/apk/release' \
  'case "${ASCENDANY_ANDROID_VERSION_NAME:-}" in' \
  '  1.2.3) printf "unsigned apk from %s\n" "$(<../../../release-input.txt)" >app/build/outputs/apk/release/app-release-unsigned.apk ;;' \
  '  1.2.4) ln -s /dev/null app/build/outputs/apk/release/app-release-unsigned.apk ;;' \
  '  1.2.5) install -d -m 0700 app/build/outputs/apk/release/app-release-unsigned.apk ;;' \
  '  1.2.6)' \
  '    printf "unsigned apk from %s\n" "$(<../../../release-input.txt)" >app/build/outputs/apk/release/app-release-unsigned.apk' \
  '    install -d -m 0700 app/build/outputs/apk/release/nested' \
  '    printf "unexpected nested apk\n" >app/build/outputs/apk/release/nested/extra.apk' \
  '    ;;' \
  '  1.2.7) mkfifo app/build/outputs/apk/release/app-release-unsigned.apk ;;' \
  '  1.2.8) printf "unsigned apk from %s\n" "$(<../../../release-input.txt)" >app/build/outputs/apk/release/app-release-unsigned.apk ;;' \
  '  *) exit 65 ;;' \
  'esac' \
  '( /usr/bin/sleep 0.2; printf "escaped Gradle child\n" >"${HOME}/escaped-gradle-descendant" ) </dev/null >/dev/null 2>&1 &' \
  >"${FIXTURE_JAVA_HOME}/bin/java"
chmod 0755 "${FIXTURE_JAVA_HOME}/bin/java"
printf 'fixture android platform jar\n' >"${FIXTURE_ANDROID_HOME}/platforms/android-36/android.jar"
printf 'Pkg.Revision=1\nAndroidVersion.ApiLevel=36\n' \
  >"${FIXTURE_ANDROID_HOME}/platforms/android-36/source.properties"
for fixture_build_tool in aapt2 d8 zipalign; do
  printf '#!/usr/bin/bash -p\nexit 0\n' \
    >"${FIXTURE_ANDROID_HOME}/build-tools/36.0.0/${fixture_build_tool}"
  chmod 0755 "${FIXTURE_ANDROID_HOME}/build-tools/36.0.0/${fixture_build_tool}"
done
unset fixture_build_tool
printf 'Pkg.Revision=36.0.0\n' \
  >"${FIXTURE_ANDROID_HOME}/build-tools/36.0.0/source.properties"
printf '#!/usr/bin/bash -p\nexit 0\n' >"${FIXTURE_ANDROID_HOME}/platform-tools/adb"
chmod 0755 "${FIXTURE_ANDROID_HOME}/platform-tools/adb"
printf 'Pkg.Revision=37.0.0\n' >"${FIXTURE_ANDROID_HOME}/platform-tools/source.properties"

readonly FIXTURE_APKSIGNER_TEMPLATE="${WORK_ROOT}/trusted-apksigner-template"
install -d -m 0700 "${WORK_ROOT}/lib"
printf 'fixture apksigner jar\n' >"${WORK_ROOT}/lib/apksigner.jar"
chmod 0644 "${WORK_ROOT}/lib/apksigner.jar"
printf '%s\n' \
  '#!/usr/bin/bash -p' \
  'set -Eeuo pipefail' \
  'readonly FIXTURE_SIGNER_MODE="${0##*-}"' \
  "readonly FIXTURE_ACTUAL_FINGERPRINT='${ACTUAL_FINGERPRINT}'" \
  '[[ "${PATH:-}" == "${HOME%/home}/tool-bin:/usr/bin:/bin" && "${HOME:-}" == */signing.??????/home && "${TMPDIR:-}" == "${HOME%/home}/tmp" ]] || exit 80' \
  '[[ "$(/usr/bin/realpath -e -- "${HOME%/home}/tool-bin/java")" == "${JAVA_HOME}/bin/java" ]] || exit 86' \
  '[[ "$(/usr/bin/stat -c %a -- "${HOME}")" == "700" && "$(/usr/bin/stat -c %a -- "${TMPDIR}")" == "700" ]] || exit 84' \
  '[[ "$(ulimit -c)" == "0" ]] || exit 85' \
  'fixture_signing_root="$(/usr/bin/dirname -- "${HOME}")"' \
  'fixture_work_root="$(/usr/bin/dirname -- "${fixture_signing_root}")"' \
  'while IFS= read -r -d "" environment_entry; do' \
  '  environment_name="${environment_entry%%=*}"' \
  '  case "${environment_name}" in' \
  '    HOME|TMPDIR|PATH|LC_ALL|JAVA_HOME|PWD|SHLVL|_|ASCENDANY_APKSIGNER_STORE_PASSWORD|ASCENDANY_APKSIGNER_KEY_PASSWORD) ;;' \
  '    *) exit 81 ;;' \
  '  esac' \
  'done < <(/usr/bin/env -0)' \
  'unset environment_entry environment_name' \
  'case "${1:-}" in' \
  '  sign)' \
  '    [[ ! -e "${fixture_work_root}/app-release-signed.apk" && ! -L "${fixture_work_root}/app-release-signed.apk" ]] || exit 83' \
  '    [[ "${ASCENDANY_APKSIGNER_STORE_PASSWORD:-}" == " fixture-store-password " ]] || exit 70' \
  '    [[ "${ASCENDANY_APKSIGNER_KEY_PASSWORD:-}" == " fixture-key-password " ]] || exit 71' \
  '    [[ -z "${ASCENDANY_ANDROID_SIGNING_STORE_PASSWORD+x}" && -z "${ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD+x}" ]] || exit 72' \
  '    output=""; input="${!#}"; previous=""' \
  '    for argument in "$@"; do' \
  '      if [[ "${previous}" == "--out" ]]; then output="${argument}"; fi' \
  '      previous="${argument}"' \
  '    done' \
  '    [[ "$*" == *"--ks-pass env:ASCENDANY_APKSIGNER_STORE_PASSWORD"* ]] || exit 73' \
  '    [[ "$*" == *"--key-pass env:ASCENDANY_APKSIGNER_KEY_PASSWORD"* ]] || exit 74' \
  '    [[ -n "${output}" && -f "${input}" ]] || exit 75' \
  '    /usr/bin/sleep 0.3' \
  '    [[ ! -e "${fixture_work_root}/build-home/escaped-gradle-descendant" ]] || exit 82' \
  '    content="$(<"${input}")"' \
  '    printf "%s\n" "${content/#unsigned /signed }" >"${output}"' \
  '    ;;' \
  '  verify)' \
  '    [[ "${2:-}" == "-Werr" && "${3:-}" == "--verbose" && "${4:-}" == "--print-certs" && "$#" == "5" ]] || exit 64' \
  '    [[ -z "${ASCENDANY_APKSIGNER_STORE_PASSWORD+x}" && -z "${ASCENDANY_APKSIGNER_KEY_PASSWORD+x}" ]] || exit 79' \
  '    if [[ "${FIXTURE_SIGNER_MODE}" == "warning" ]]; then printf "WARNING: fixture signer warning\n" >&2; exit 3; fi' \
  '    printf "Verifies\n"' \
  '    case "${FIXTURE_SIGNER_MODE}" in' \
  '      single) printf "Signer #1 certificate SHA-256 digest: %s\n" "${FIXTURE_ACTUAL_FINGERPRINT}" ;;' \
  '      none) ;;' \
  '      multiple)' \
  '        printf "Signer #1 certificate SHA-256 digest: %s\n" "${FIXTURE_ACTUAL_FINGERPRINT}"' \
  '        printf "Signer #2 certificate SHA-256 digest: %064d\n" 0' \
  '        ;;' \
  '      *) exit 65 ;;' \
  '    esac' \
  '    ;;' \
  '  *) exit 64 ;;' \
  'esac' \
  >"${FIXTURE_APKSIGNER_TEMPLATE}"
chmod 0755 "${FIXTURE_APKSIGNER_TEMPLATE}"
fixture_apksigner_binary_sha256="$(sha256sum -- "${FIXTURE_APKSIGNER_TEMPLATE}" | awk '{print $1}')"
fixture_apksigner_jar_sha256="$(sha256sum -- "${WORK_ROOT}/lib/apksigner.jar" | awk '{print $1}')"
sed -i \
  -e "s/b47549e373b895ce6ca620d0c7887e674d9615ffa837a86ac601dcfd04adb0f0/${fixture_apksigner_binary_sha256}/" \
  -e "s/3716d9311e55d2b0918a2fd9d54ba9e406c5f6abeea700b287f11259bc163dec/${fixture_apksigner_jar_sha256}/" \
  "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh"
grep -F "readonly PINNED_APKSIGNER_BINARY_SHA256='${fixture_apksigner_binary_sha256}'" \
  "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" >/dev/null ||
  fail 'fixture did not replace the pinned apksigner launcher digest'
grep -F "readonly PINNED_APKSIGNER_JAR_SHA256='${fixture_apksigner_jar_sha256}'" \
  "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" >/dev/null ||
  fail 'fixture did not replace the pinned apksigner JAR digest'
unset fixture_apksigner_binary_sha256 fixture_apksigner_jar_sha256

git -C "${FIXTURE_REPOSITORY}" init --quiet
git -C "${FIXTURE_REPOSITORY}" config user.name 'AscendAny Android release fixture'
git -C "${FIXTURE_REPOSITORY}" config user.email 'android-release-fixture@example.invalid'
git -C "${FIXTURE_REPOSITORY}" -c core.safecrlf=false add .
raw_crlf_blob="$(
  git -C "${FIXTURE_REPOSITORY}" hash-object \
    -w --no-filters -- provenance/crlf.txt
)"
git -C "${FIXTURE_REPOSITORY}" update-index \
  --add --cacheinfo "100644,${raw_crlf_blob},provenance/crlf.txt"
unset raw_crlf_blob
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: commit Android release input'
readonly COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
readonly FIXTURE_GRADLEW="${FIXTURE_REPOSITORY}/apps/mobile/android/gradlew"
readonly FIXTURE_WRAPPER_PROPERTIES="${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/wrapper/gradle-wrapper.properties"
readonly FIXTURE_WRAPPER_JAR="${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/wrapper/gradle-wrapper.jar"
readonly FIXTURE_VERIFICATION_METADATA="${FIXTURE_REPOSITORY}/apps/mobile/android/gradle/verification-metadata.xml"
readonly FIXTURE_BUILDER="${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh"

printf '%s\n' 'distributionUrl=https\://attacker.invalid/gradle.zip' >>"${FIXTURE_WRAPPER_PROPERTIES}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_WRAPPER_PROPERTIES}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: drift wrapper properties'
readonly WRAPPER_PROPERTIES_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf 'drift\n' >>"${FIXTURE_WRAPPER_JAR}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_WRAPPER_JAR}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: drift wrapper jar'
readonly WRAPPER_JAR_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf '\n' >>"${FIXTURE_GRADLEW}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_GRADLEW}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: drift Gradle launcher'
readonly GRADLEW_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

chmod 0644 "${FIXTURE_GRADLEW}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_GRADLEW}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: remove Gradle launcher execute mode'
readonly GRADLEW_MODE_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

chmod 0755 "${FIXTURE_WRAPPER_JAR}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_WRAPPER_JAR}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: add wrapper jar execute mode'
readonly WRAPPER_JAR_MODE_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

chmod 0755 "${FIXTURE_WRAPPER_PROPERTIES}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_WRAPPER_PROPERTIES}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: add wrapper properties execute mode'
readonly WRAPPER_PROPERTIES_MODE_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf 'verification metadata drift\n' >>"${FIXTURE_VERIFICATION_METADATA}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_VERIFICATION_METADATA}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: drift verification metadata'
readonly VERIFICATION_METADATA_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

chmod 0755 "${FIXTURE_VERIFICATION_METADATA}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_VERIFICATION_METADATA}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: add verification metadata execute mode'
readonly VERIFICATION_METADATA_MODE_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

git -C "${FIXTURE_REPOSITORY}" rm --quiet "${FIXTURE_VERIFICATION_METADATA}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: remove verification metadata'
readonly VERIFICATION_METADATA_MISSING_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf '\n# reviewed builder drift\n' >>"${FIXTURE_BUILDER}"
git -C "${FIXTURE_REPOSITORY}" add "${FIXTURE_BUILDER}"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: drift reviewed release wrapper'
readonly BUILDER_DRIFT_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf 'mutate wrapper properties after initial validation\n' \
  >"${FIXTURE_REPOSITORY}/mutate-wrapper-properties-after-pin"
git -C "${FIXTURE_REPOSITORY}" add mutate-wrapper-properties-after-pin
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: mutate wrapper properties during sync'
readonly POST_PIN_PROPERTIES_MUTATION_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf 'mutate wrapper jar after initial validation\n' \
  >"${FIXTURE_REPOSITORY}/mutate-wrapper-jar-after-pin"
git -C "${FIXTURE_REPOSITORY}" add mutate-wrapper-jar-after-pin
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: mutate wrapper jar during sync'
readonly POST_PIN_JAR_MUTATION_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf 'mutate verification metadata after initial validation\n' \
  >"${FIXTURE_REPOSITORY}/mutate-verification-metadata-after-pin"
git -C "${FIXTURE_REPOSITORY}" add mutate-verification-metadata-after-pin
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: mutate verification metadata during sync'
readonly POST_PIN_METADATA_MUTATION_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

ln -s release-input.txt "${FIXTURE_REPOSITORY}/provenance-link"
git -C "${FIXTURE_REPOSITORY}" add provenance-link
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: add unsupported symlink entry'
readonly SYMLINK_ENTRY_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

git -C "${FIXTURE_REPOSITORY}" update-index \
  --add --cacheinfo "160000,${COMMIT},provenance-gitlink"
git -C "${FIXTURE_REPOSITORY}" commit --quiet --message 'fixture: add unsupported gitlink entry'
readonly GITLINK_ENTRY_COMMIT="$(git -C "${FIXTURE_REPOSITORY}" rev-parse HEAD)"
git -C "${FIXTURE_REPOSITORY}" checkout --quiet --detach "${COMMIT}"

printf 'dirty live worktree input\n' >"${FIXTURE_REPOSITORY}/release-input.txt"
printf 'release-input.txt export-ignore\n' >"${FIXTURE_REPOSITORY}/.git/info/attributes"

install -d -m 0700 "${MALICIOUS_HOME}/.gradle/init.d" "${MALICIOUS_GRADLE_USER_HOME}/init.d"
printf 'release-input.txt export-ignore\n' >"${MALICIOUS_HOME}/global-attributes"
printf '%s\n' \
  '[core]' \
  "    attributesFile = ${MALICIOUS_HOME}/global-attributes" \
  >"${MALICIOUS_HOME}/.gitconfig"
printf 'throw new GradleException("ambient HOME init script executed")\n' \
  >"${MALICIOUS_HOME}/.gradle/init.d/ambient.gradle"
printf 'throw new GradleException("ambient GRADLE_USER_HOME init script executed")\n' \
  >"${MALICIOUS_GRADLE_USER_HOME}/init.d/ambient.gradle"
printf 'printf "BASH_ENV executed\\n" >"%s"\n' "${BASH_ENV_MARKER}" >"${WORK_ROOT}/malicious-bash-env"
printf '%s\n' \
  'const fs = require("node:fs");' \
  'fs.writeFileSync(process.env.FIXTURE_NODE_INJECTION_MARKER, "NODE_OPTIONS executed\\n");' \
  >"${WORK_ROOT}/malicious-node-hook.cjs"

install -d -m 0700 "${LIVE_TOOL_BIN}"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -Eeuo pipefail' \
  'printf "live worktree tool executed\n" >"${FIXTURE_PATH_HIJACK_MARKER:?}"' \
  'exit 97' \
  >"${LIVE_TOOL_BIN}/git"
chmod 0755 "${LIVE_TOOL_BIN}/git"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -Eeuo pipefail' \
  'printf "live worktree interpreter executed\n" >"${FIXTURE_INTERPRETER_HIJACK_MARKER:?}"' \
  'exit 98' \
  >"${LIVE_TOOL_BIN}/bash"
chmod 0755 "${LIVE_TOOL_BIN}/bash"
for hijacked_tool in node pnpm; do
  printf '%s\n' \
    '#!/usr/bin/bash -p' \
    'set -Eeuo pipefail' \
    'printf "live worktree Node tool executed\n" >"${FIXTURE_NODE_TOOL_HIJACK_MARKER:?}"' \
    'exit 99' \
    >"${LIVE_TOOL_BIN}/${hijacked_tool}"
  chmod 0755 "${LIVE_TOOL_BIN}/${hijacked_tool}"
done
unset hijacked_tool

printf '%s\n' \
  '#!/usr/bin/env node' \
  'const fs = require("node:fs");' \
  'const { spawn } = require("node:child_process");' \
  'const args = process.argv.slice(2);' \
  'const env = process.env;' \
  'const fail = (code, message) => { console.error(message); process.exit(code); };' \
  'const parentEnvironment = fs.readFileSync(`/proc/${process.ppid}/environ`);' \
  'if (parentEnvironment.includes(Buffer.from("fixture-store-password")) || parentEnvironment.includes(Buffer.from("fixture-key-password"))) fail(94, "wrapper parent environment retained a signing password");' \
  'if (fs.readFileSync("/proc/1/comm", "utf8").trim() !== "bwrap" || process.ppid !== 1) fail(95, "pnpm did not run below namespace init");' \
  'for (const entry of fs.readFileSync("/proc/1/environ").toString().split("\0").filter(Boolean)) {' \
  '  const name = entry.slice(0, entry.indexOf("="));' \
  '  if (name !== "PATH" && name !== "LC_ALL") fail(101, `ambient variable reached pnpm namespace init: ${name}`);' \
  '}' \
  'if (fs.readFileSync("/proc/1/environ").includes(Buffer.from("fixture-ambient-secret"))) fail(102, "ambient secret reached pnpm namespace init");' \
  "const forbiddenKeystore = '${FIXTURE_KEYSTORE}';" \
  'try { if (fs.readFileSync(forbiddenKeystore, "utf8").includes("fixture keystore bytes")) fail(96, "keystore bytes are visible in pnpm namespace"); } catch {}' \
  'let keystorePathSeen = false;' \
  'for (const processEntry of fs.readdirSync("/proc")) {' \
  '  if (!/^[0-9]+$/.test(processEntry)) continue;' \
  '  try { if (fs.readFileSync(`/proc/${processEntry}/cmdline`).includes(Buffer.from(forbiddenKeystore))) keystorePathSeen = true; } catch {}' \
  '}' \
  'if (!keystorePathSeen) fail(97, "pnpm fixture did not exercise cmdline keystore discovery");' \
  'const forbidden = /^(?:NODE_OPTIONS|NODE_PATH|NPM_CONFIG_|npm_config_|PNPM_|COREPACK_|BASH_ENV|ENV|BASH_FUNC_|INIT_CWD|npm_command|npm_lifecycle_|npm_package_)/;' \
  'for (const name of Object.keys(env)) if (forbidden.test(name)) fail(82, `forbidden environment: ${name}`);' \
  'if (!env.HOME?.endsWith("/build-home")) fail(83, "HOME is not private");' \
  'const root = env.HOME.slice(0, -"/build-home".length);' \
  'if (env.XDG_CONFIG_HOME !== `${root}/xdg-config`) fail(84, "XDG_CONFIG_HOME is not private");' \
  'if (env.XDG_CACHE_HOME !== `${root}/xdg-cache`) fail(85, "XDG_CACHE_HOME is not private");' \
  'if (env.XDG_DATA_HOME !== `${root}/xdg-data`) fail(86, "XDG_DATA_HOME is not private");' \
  'if (env.TMPDIR !== `${root}/tmp`) fail(87, "TMPDIR is not private");' \
  'if (env.PATH !== `${root}/trusted-tool-bin:/usr/bin:/bin`) fail(88, "PATH is not closed");' \
  'if (env.LC_ALL !== "C" || env.TZ !== "UTC" || env.CI !== "1") fail(89, "fixed build environment is incomplete");' \
  'if (args.length === 1 && args[0] === "--version") {' \
  '  if (process.cwd() !== `${root}/source`) fail(93, "pnpm version check did not run from SOURCE_ROOT");' \
  '  console.log("9.15.4");' \
  '  process.exit(0);' \
  '}' \
  'if (args.length === 6 && args[0] === "--filter" && args[1] === "@ascendany/mobile..." && args[2] === "fetch" && args[3] === "--frozen-lockfile" && args[4] === "--store-dir") {' \
  '  if (args[5] !== `${root}/pnpm-store`) fail(90, "pnpm fetch store is not private");' \
  '  if (env.VITE_API_BASE_URL || env.VITE_CHAT_PROMPT_CONFIGURATION_KEY || env.VITE_CHAT_MODEL_CONFIGURATION_KEY) fail(91, "Vite config entered install");' \
  '  if (fs.readFileSync("release-input.txt", "utf8").trim() !== "committed release input") fail(70, "mutable worktree input used");' \
  '  if (!fs.existsSync("$(touch${IFS}$FIXTURE_MATERIALIZER_EVAL_MARKER)")) fail(92, "literal Git path was not materialized");' \
  '  if (!fs.readFileSync("provenance/crlf.txt").equals(Buffer.from("first\r\nsecond\r\n"))) fail(103, "CRLF provenance bytes drifted");' \
  '  if (!fs.readFileSync("provenance/binary.bin").equals(Buffer.from("00017f80ff62696e617279007061796c6f61640a", "hex"))) fail(104, "binary provenance bytes drifted");' \
  '  if (!fs.readFileSync("provenance/no-final-newline.txt").equals(Buffer.from("no-final-newline"))) fail(105, "no-final-newline provenance bytes drifted");' \
  '  if (!fs.readFileSync("provenance/trailing-newlines.txt").equals(Buffer.from("preserve-two-trailing-newlines\n\n"))) fail(106, "trailing-newline provenance bytes drifted");' \
  '  if ((fs.statSync("provenance/executable.sh").mode & 0o777) !== 0o755) fail(107, "executable provenance mode drifted");' \
  '  if (!fs.readFileSync("provenance/name-with-tab\tand-newline\n.bin").equals(Buffer.from("tab, newline, binary byte \x00 and literal path payload\n"))) fail(108, "NUL-safe special-path provenance drifted");' \
  '  fs.writeFileSync(`${root}/pnpm-store/fetched-integrity-bound-entry`, "fetched\n");' \
  '  process.exit(0);' \
  '}' \
  'if (args.length === 8 && args[0] === "--filter" && args[1] === "@ascendany/mobile..." && args[2] === "install" && args[3] === "--offline" && args[4] === "--ignore-scripts" && args[5] === "--frozen-lockfile" && args[6] === "--store-dir") {' \
  '  if (args[7] !== `${root}/pnpm-store`) fail(90, "pnpm install store is not private");' \
  '  if (fs.readFileSync(`${root}/pnpm-store/fetched-integrity-bound-entry`, "utf8").trim() !== "fetched") fail(98, "offline install did not consume fetch store");' \
  '  if (fs.readFileSync("/proc/net/route", "utf8").trim().split("\n").length !== 1) fail(99, "offline install retained a network route");' \
  '  process.exit(0);' \
  '}' \
  'if (args.length === 3 && args[0] === "--filter" && args[1] === "@ascendany/mobile" && args[2] === "sync:android") {' \
  '  if (env.VITE_API_BASE_URL !== "https://ascendany.example.invalid") fail(71, "API origin missing");' \
  '  if (env.VITE_CHAT_PROMPT_CONFIGURATION_KEY !== "agent.prompt.default") fail(72, "prompt key missing");' \
  '  if (env.VITE_CHAT_MODEL_CONFIGURATION_KEY !== "agent.model.default") fail(73, "model key missing");' \
  '  if (fs.readFileSync("/proc/net/route", "utf8").trim().split("\n").length !== 1) fail(99, "sync retained a network route");' \
  '  const escapedChild = spawn("/usr/bin/bash", ["-p", "-c", `sleep 0.2; printf "escaped pnpm child\\n" >"${env.HOME}/escaped-pnpm-descendant"`], { detached: false, stdio: "ignore" });' \
  '  escapedChild.unref();' \
  '  fs.symlinkSync(forbiddenKeystore, `${root}/app-release-signed.apk`);' \
  '  if (!fs.lstatSync(`${root}/app-release-signed.apk`).isSymbolicLink()) fail(100, "pnpm fixture did not create its namespace-local output symlink");' \
  '  if (fs.existsSync("mutate-wrapper-properties-after-pin")) fs.appendFileSync("apps/mobile/android/gradle/wrapper/gradle-wrapper.properties", "post-pin drift\\n");' \
  '  if (fs.existsSync("mutate-wrapper-jar-after-pin")) fs.appendFileSync("apps/mobile/android/gradle/wrapper/gradle-wrapper.jar", "post-pin drift\\n");' \
  '  if (fs.existsSync("mutate-verification-metadata-after-pin")) fs.appendFileSync("apps/mobile/android/gradle/verification-metadata.xml", "post-pin drift\\n");' \
  '  process.exit(0);' \
  '}' \
  'fail(64, `unexpected fake pnpm invocation: ${args.join(" ")}`);' \
  >"${FAKE_BIN}/pnpm"
chmod 0755 "${FAKE_BIN}/pnpm"
/usr/bin/sed 's/console[.]log("9[.]15[.]4")/console.log("9.15.3")/' \
  "${FAKE_BIN}/pnpm" >"${FAKE_BIN}/pnpm-wrong-version"
chmod 0755 "${FAKE_BIN}/pnpm-wrong-version"

install -d -m 0700 "${WORK_ROOT}/lib"
printf 'fixture apksigner jar\n' >"${WORK_ROOT}/lib/apksigner.jar"
chmod 0644 "${WORK_ROOT}/lib/apksigner.jar"

for trusted_signer_mode in single none multiple warning; do
  trusted_apksigner="${TRUSTED_APKSIGNER_PREFIX}-${trusted_signer_mode}"
  printf '%s\n' \
    '#!/usr/bin/bash -p' \
    'set -Eeuo pipefail' \
    "readonly FIXTURE_SIGNER_MODE='${trusted_signer_mode}'" \
    "readonly FIXTURE_ACTUAL_FINGERPRINT='${ACTUAL_FINGERPRINT}'" \
    '[[ "${PATH:-}" == "${HOME%/home}/tool-bin:/usr/bin:/bin" && "${HOME:-}" == */signing.??????/home && "${TMPDIR:-}" == "${HOME%/home}/tmp" ]] || exit 80' \
    '[[ "$(/usr/bin/realpath -e -- "${HOME%/home}/tool-bin/java")" == "${JAVA_HOME}/bin/java" ]] || exit 86' \
    '[[ "$(/usr/bin/stat -c %a -- "${HOME}")" == "700" && "$(/usr/bin/stat -c %a -- "${TMPDIR}")" == "700" ]] || exit 84' \
    '[[ "$(ulimit -c)" == "0" ]] || exit 85' \
    'fixture_signing_root="$(/usr/bin/dirname -- "${HOME}")"' \
    'fixture_work_root="$(/usr/bin/dirname -- "${fixture_signing_root}")"' \
    'while IFS= read -r -d "" environment_entry; do' \
    '  environment_name="${environment_entry%%=*}"' \
    '  case "${environment_name}" in' \
    '    HOME|TMPDIR|PATH|LC_ALL|JAVA_HOME|PWD|SHLVL|_|ASCENDANY_APKSIGNER_STORE_PASSWORD|ASCENDANY_APKSIGNER_KEY_PASSWORD) ;;' \
    '    *) exit 81 ;;' \
    '  esac' \
    'done < <(/usr/bin/env -0)' \
    'unset environment_entry environment_name' \
    'case "${1:-}" in' \
    '  sign)' \
    '    [[ ! -e "${fixture_work_root}/app-release-signed.apk" && ! -L "${fixture_work_root}/app-release-signed.apk" ]] || exit 83' \
    '    [[ "${ASCENDANY_APKSIGNER_STORE_PASSWORD:-}" == " fixture-store-password " ]] || exit 70' \
    '    [[ "${ASCENDANY_APKSIGNER_KEY_PASSWORD:-}" == " fixture-key-password " ]] || exit 71' \
    '    [[ -z "${ASCENDANY_ANDROID_SIGNING_STORE_PASSWORD+x}" && -z "${ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD+x}" ]] || exit 72' \
    '    output=""; input="${!#}"; previous=""' \
    '    for argument in "$@"; do' \
    '      if [[ "${previous}" == "--out" ]]; then output="${argument}"; fi' \
    '      previous="${argument}"' \
    '    done' \
    '    [[ "$*" == *"--ks-pass env:ASCENDANY_APKSIGNER_STORE_PASSWORD"* ]] || exit 73' \
    '    [[ "$*" == *"--key-pass env:ASCENDANY_APKSIGNER_KEY_PASSWORD"* ]] || exit 74' \
    '    [[ -n "${output}" && -f "${input}" ]] || exit 75' \
    '    /usr/bin/sleep 0.3' \
    '    [[ ! -e "${fixture_work_root}/build-home/escaped-gradle-descendant" ]] || exit 82' \
    '    content="$(<"${input}")"' \
    '    printf "%s\n" "${content/#unsigned /signed }" >"${output}"' \
    '    ;;' \
  '  verify)' \
    '    [[ "${2:-}" == "-Werr" && "${3:-}" == "--verbose" && "${4:-}" == "--print-certs" && "$#" == "5" ]] || exit 64' \
    '    [[ -z "${ASCENDANY_APKSIGNER_STORE_PASSWORD+x}" && -z "${ASCENDANY_APKSIGNER_KEY_PASSWORD+x}" ]] || exit 79' \
    '    if [[ "${FIXTURE_SIGNER_MODE}" == "warning" ]]; then printf "WARNING: fixture signer warning\n" >&2; exit 3; fi' \
    '    printf "Verifies\n"' \
    '    case "${FIXTURE_SIGNER_MODE}" in' \
    '      single) printf "Signer #1 certificate SHA-256 digest: %s\n" "${FIXTURE_ACTUAL_FINGERPRINT}" ;;' \
    '      none) ;;' \
    '      multiple)' \
    '        printf "Signer #1 certificate SHA-256 digest: %s\n" "${FIXTURE_ACTUAL_FINGERPRINT}"' \
    '        printf "Signer #2 certificate SHA-256 digest: %064d\n" 0' \
    '        ;;' \
    '      *) exit 65 ;;' \
    '    esac' \
    '    ;;' \
    '  *) exit 64 ;;' \
    'esac' \
    >"${trusted_apksigner}"
  install -m 0755 "${FIXTURE_APKSIGNER_TEMPLATE}" "${trusted_apksigner}"
done
unset trusted_signer_mode trusted_apksigner

readonly TAMPERED_APKSIGNER="${TRUSTED_APKSIGNER_PREFIX}-tampered"
install -m 0755 "${FIXTURE_APKSIGNER_TEMPLATE}" "${TAMPERED_APKSIGNER}"
printf 'tampered launcher bytes\n' >>"${TAMPERED_APKSIGNER}"
readonly WRONG_APKSIGNER_ROOT="${WORK_ROOT}/wrong-apksigner-bundle"
readonly WRONG_APKSIGNER="${WRONG_APKSIGNER_ROOT}/trusted-apksigner-single"
install -d -m 0700 "${WRONG_APKSIGNER_ROOT}/lib"
install -m 0755 "${FIXTURE_APKSIGNER_TEMPLATE}" "${WRONG_APKSIGNER}"
printf 'wrong apksigner jar bytes\n' >"${WRONG_APKSIGNER_ROOT}/lib/apksigner.jar"
chmod 0644 "${WRONG_APKSIGNER_ROOT}/lib/apksigner.jar"

printf '%s\n' \
  '#!/usr/bin/bash -p' \
  'set -Eeuo pipefail' \
  'printf "caller PATH apksigner executed\n" >"${FIXTURE_APKSIGNER_PATH_HIJACK_MARKER:?}"' \
  'exit 100' \
  >"${FAKE_BIN}/apksigner"
chmod 0755 "${FAKE_BIN}/apksigner"

for hijacked_system_tool in \
  awk basename chmod diff dirname find git grep id install ln mkdir mktemp mv realpath rm sed sha512sum sort stat; do
  printf '%s\n' \
    '#!/usr/bin/bash -p' \
    'set -Eeuo pipefail' \
    'printf "caller PATH system tool executed\n" >"${FIXTURE_SYSTEM_TOOL_HIJACK_MARKER:?}"' \
    'exit 101' \
    >"${FAKE_BIN}/${hijacked_system_tool}"
  chmod 0755 "${FAKE_BIN}/${hijacked_system_tool}"
done
unset hijacked_system_tool

printf 'fixture keystore bytes\n' >"${FIXTURE_KEYSTORE}"
chmod 0600 "${FIXTURE_KEYSTORE}"

readonly BASH_ARGV0_SPOOF_WRAPPER="${WORK_ROOT}/bash-argv0-spoof-wrapper.sh"
printf '%s\n' \
  '#!/usr/bin/bash -p' \
  'set -Eeuo pipefail' \
  'target="$1"' \
  'marker="$2"' \
  'BASH_ARGV0="${target}"' \
  'builtin() { /usr/bin/printf "builtin function executed\n" >"${marker}"; return 0; }' \
  'exit() { /usr/bin/printf "exit function executed\n" >"${marker}"; return 0; }' \
  'printf() { /usr/bin/printf "printf function executed\n" >"${marker}"; return 0; }' \
  'export() { /usr/bin/printf "export function executed\n" >"${marker}"; return 0; }' \
  'source "${target}" --help' \
  '/usr/bin/printf "BASH_ARGV0-spoofed wrapper continued\n" >"${marker}"' \
  >"${BASH_ARGV0_SPOOF_WRAPPER}"
chmod 0755 "${BASH_ARGV0_SPOOF_WRAPPER}"

readonly XTRACE_STORE_MARKER='xtrace-store-secret-marker'
readonly XTRACE_KEY_MARKER='xtrace-key-secret-marker'
/usr/bin/bash -p -x "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" --help \
    3< <(printf '%s\0' "${XTRACE_STORE_MARKER}") \
    4< <(printf '%s\0' "${XTRACE_KEY_MARKER}") \
    >"${WORK_ROOT}/xtrace-help.log" 2>&1 || fail 'release wrapper --help failed under xtrace'
if grep -F -e "${XTRACE_STORE_MARKER}" -e "${XTRACE_KEY_MARKER}" "${WORK_ROOT}/xtrace-help.log" >/dev/null; then
  fail 'release wrapper exposed a signing password under xtrace'
fi
expect_failure \
  'wrapper must run under /usr/bin/bash -p' \
  "${WORK_ROOT}/nonprivileged-interpreter.log" \
  /usr/bin/bash "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" --help
expect_failure \
  'wrapper must run under /usr/bin/bash -p' \
  "${WORK_ROOT}/sourced-wrapper.log" \
  /usr/bin/bash -p -c 'source "$1" --help' fixture-shell \
    "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh"
expect_failure \
  'wrapper must run under /usr/bin/bash -p' \
  "${WORK_ROOT}/sourced-local-function-wrapper.log" \
  /usr/bin/bash -p -c '
    marker="$2"
    builtin() { /usr/bin/printf "builtin function executed\n" >"${marker}"; return 0; }
    exit() { /usr/bin/printf "exit function executed\n" >"${marker}"; return 0; }
    printf() { /usr/bin/printf "printf function executed\n" >"${marker}"; return 0; }
    export() { /usr/bin/printf "export function executed\n" >"${marker}"; return 0; }
    source "$1" --help
    /usr/bin/printf "sourced wrapper continued\n" >"${marker}"
  ' fixture-shell \
    "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" \
    "${SOURCE_FUNCTION_HIJACK_MARKER}"
[[ ! -e "${SOURCE_FUNCTION_HIJACK_MARKER}" ]] ||
  fail 'a sourced release wrapper continued through locally defined shell functions'
expect_failure \
  'wrapper must run under /usr/bin/bash -p' \
  "${WORK_ROOT}/sourced-spoofed-zero-wrapper.log" \
  /usr/bin/bash -p -c '
    marker="$1"
    builtin() { /usr/bin/printf "builtin function executed\n" >"${marker}"; return 0; }
    exit() { /usr/bin/printf "exit function executed\n" >"${marker}"; return 0; }
    printf() { /usr/bin/printf "printf function executed\n" >"${marker}"; return 0; }
    export() { /usr/bin/printf "export function executed\n" >"${marker}"; return 0; }
    source "$0" --help
    /usr/bin/printf "spoofed sourced wrapper continued\n" >"${marker}"
  ' "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" \
    "${SOURCE_FUNCTION_HIJACK_MARKER}"
[[ ! -e "${SOURCE_FUNCTION_HIJACK_MARKER}" ]] ||
  fail 'a sourced release wrapper accepted a spoofed process name'
expect_failure \
  'wrapper must run under /usr/bin/bash -p' \
  "${WORK_ROOT}/sourced-bash-argv0-spoof-wrapper.log" \
  "${BASH_ARGV0_SPOOF_WRAPPER}" \
    "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" \
    "${SOURCE_FUNCTION_HIJACK_MARKER}"
[[ ! -e "${SOURCE_FUNCTION_HIJACK_MARKER}" ]] ||
  fail 'a sourced release wrapper accepted a BASH_ARGV0-spoofed file invocation'

run_builder() {
  local output_directory="$1"
  local version="$2"
  local commit="$3"
  local keystore="$4"
  local expected_fingerprint="$5"
  local signer_mode="${6:-single}"
  local pnpm_entry="${7:-${FAKE_BIN}/pnpm}"
  local apksigner_binary="${8:-${TRUSTED_APKSIGNER_PREFIX}-${signer_mode}}"
  local store_password_fd="${9:-3}"
  local key_password_fd="${10:-4}"
  local gradle_max_workers="${FIXTURE_GRADLE_MAX_WORKERS:-1}"
  local -a ambient_injection=()
  if [[ -n "${FIXTURE_AMBIENT_INJECTION_NAME:-}" ]]; then
    ambient_injection+=("${FIXTURE_AMBIENT_INJECTION_NAME}=${FIXTURE_AMBIENT_INJECTION_VALUE:-fixture-injection}")
  fi
  env \
    "${ambient_injection[@]}" \
    PATH="${LIVE_TOOL_BIN}:${FAKE_BIN}:${PATH}" \
    FIXTURE_PATH_HIJACK_MARKER="${PATH_HIJACK_MARKER}" \
    FIXTURE_INTERPRETER_HIJACK_MARKER="${INTERPRETER_HIJACK_MARKER}" \
    FIXTURE_NODE_TOOL_HIJACK_MARKER="${NODE_TOOL_HIJACK_MARKER}" \
    FIXTURE_APKSIGNER_PATH_HIJACK_MARKER="${APKSIGNER_PATH_HIJACK_MARKER}" \
    FIXTURE_SYSTEM_TOOL_HIJACK_MARKER="${SYSTEM_TOOL_HIJACK_MARKER}" \
    FIXTURE_MATERIALIZER_EVAL_MARKER="${MATERIALIZER_EVAL_MARKER}" \
    FIXTURE_NODE_INJECTION_MARKER="${NODE_INJECTION_MARKER}" \
    FIXTURE_AMBIENT_SECRET='fixture-ambient-secret' \
    HOME="${MALICIOUS_HOME}" \
    GRADLE_USER_HOME="${MALICIOUS_GRADLE_USER_HOME}" \
    XDG_CONFIG_HOME="${MALICIOUS_HOME}/xdg-config" \
    XDG_CACHE_HOME="${MALICIOUS_HOME}/xdg-cache" \
    XDG_DATA_HOME="${MALICIOUS_HOME}/xdg-data" \
    NODE_OPTIONS="--require=${WORK_ROOT}/malicious-node-hook.cjs" \
    NODE_PATH="${MALICIOUS_HOME}/node-path" \
    NPM_CONFIG_USERCONFIG="${MALICIOUS_HOME}/npmrc" \
    npm_config_registry='https://registry.attacker.invalid' \
    PNPM_HOME="${MALICIOUS_HOME}/pnpm-home" \
    COREPACK_HOME="${MALICIOUS_HOME}/corepack-home" \
    INIT_CWD="${FIXTURE_REPOSITORY}" \
    npm_config_local_prefix="${FIXTURE_REPOSITORY}" \
    npm_lifecycle_event='forbidden-outer-release-lifecycle' \
    JAVA_HOME="${FIXTURE_JAVA_HOME}" \
    ANDROID_HOME="${FIXTURE_ANDROID_HOME}" \
    "${FIXTURE_REPOSITORY}/apps/mobile/scripts/build-android-release.sh" \
      --version "${version}" \
      --version-code 10203 \
      --gradle-max-workers "${gradle_max_workers}" \
      --commit "${commit}" \
      --output-dir "${output_directory}" \
      --api-origin https://ascendany.example.invalid \
      --prompt-key agent.prompt.default \
      --model-key agent.model.default \
      --node-bin "${TRUSTED_NODE_BINARY}" \
      --pnpm-entry "${pnpm_entry}" \
      --apksigner-bin "${apksigner_binary}" \
      --keystore "${keystore}" \
      --key-alias ascendany \
      --store-password-fd "${store_password_fd}" \
      --key-password-fd "${key_password_fd}" \
      --signer-sha256 "${expected_fingerprint}" \
      3< <(printf '%s\0' ' fixture-store-password ') \
      4< <(printf '%s\0' ' fixture-key-password ')
}

run_racing_builder() {
  local output_directory="$1"
  local output_parent="${output_directory%/*}"
  local watcher_pid builder_status=0
  (
    local attempt
    for ((attempt = 0; attempt < 2000; attempt += 1)); do
      if /usr/bin/find "${output_parent}" \
        -mindepth 1 -maxdepth 1 -type d -name '.ascendany-android-release.*' -print -quit |
        /usr/bin/grep -q .; then
        /usr/bin/install -d -m 0700 -- "${output_directory}"
        printf 'racing owner\n' >"${output_directory}/marker"
        exit 0
      fi
      /usr/bin/sleep 0.005
    done
    exit 90
  ) &
  watcher_pid=$!
  run_builder "${output_directory}" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}" ||
    builder_status=$?
  if ! wait "${watcher_pid}"; then
    fail 'real publication race watcher did not observe the detached build workspace'
  fi
  return "${builder_status}"
}

readonly HAPPY_PARENT="${WORK_ROOT}/happy-parent"
readonly HAPPY_OUTPUT="${HAPPY_PARENT}/release"
readonly ARTIFACT_NAME='AscendAny-Android-1.2.3.apk'
install -d -m 0700 "${HAPPY_PARENT}"
run_builder "${HAPPY_OUTPUT}" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}" >"${WORK_ROOT}/happy.log"
[[ "$(<"${HAPPY_OUTPUT}/${ARTIFACT_NAME}")" == 'signed apk from committed release input' ]] ||
  fail 'Android artifact read from the mutable live worktree'
[[ -f "${HAPPY_OUTPUT}/${ARTIFACT_NAME}.sha512" ]] || fail 'portable SHA-512 sidecar is missing'
grep -E "^[0-9a-f]{128}  ${ARTIFACT_NAME//./[.]}$" "${HAPPY_OUTPUT}/${ARTIFACT_NAME}.sha512" >/dev/null ||
  fail 'SHA-512 sidecar does not use the portable digest-two-spaces-basename format'
(cd "${HAPPY_OUTPUT}" && sha512sum --check "${ARTIFACT_NAME}.sha512" >/dev/null) || fail 'SHA-512 sidecar did not verify'
if ! node - "${ANDROID_RELEASE_CONTRACT}" "${ARTIFACT_NAME}" <<'NODE'
const fs = require("node:fs");
const contract = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const artifactName = process.argv[3];
if (Object.keys(contract).sort().join(",") !== "fileNamePattern,maximumVersionLength,schema") process.exit(1);
if (contract.schema !== "ascendany.release-assets.android.v1") process.exit(1);
if (typeof contract.fileNamePattern !== "string" || contract.fileNamePattern.length === 0) process.exit(1);
if (!Number.isSafeInteger(contract.maximumVersionLength) || contract.maximumVersionLength < 1) process.exit(1);
const pattern = new RegExp(contract.fileNamePattern, "u");
const matches = (name) => name.length <= "AscendAny-Android-".length + contract.maximumVersionLength + ".apk".length && pattern.test(name);
if (!matches(artifactName)) process.exit(1);
if (!matches("AscendAny-Android-1.2.3-rc.1+build.5.apk")) process.exit(1);
for (const invalidName of [
  "AscendAny-Android.apk",
  "AscendAny-Mobile-1.2.3.apk",
  "AscendAny-Android-01.2.3.apk",
  "AscendAny-Android-1.2.3-01.apk",
  "unrelated-android-1.2.3.apk",
  "AscendAny-Android-1.2.3.apk.sha512",
  `AscendAny-Android-1.2.3+${"a".repeat(123)}.apk`,
]) {
  if (matches(invalidName)) process.exit(1);
}
NODE
then
  fail 'Android artifact basename and site download matcher drifted'
fi
assert_no_build_workspace "${HAPPY_PARENT}"

readonly RACE_PARENT="${WORK_ROOT}/race-parent"
readonly RACE_OUTPUT="${RACE_PARENT}/release"
install -d -m 0700 "${RACE_PARENT}"
expect_failure \
  'Android release publication target appeared during the no-replace rename' \
  "${WORK_ROOT}/publication-race.log" \
  run_racing_builder "${RACE_OUTPUT}"
[[ -f "${RACE_OUTPUT}/marker" ]] || fail 'publication race owner marker was replaced'
find "${RACE_OUTPUT}" -mindepth 1 -printf '%P\n' | sort >"${WORK_ROOT}/race-output-paths"
[[ "$(<"${WORK_ROOT}/race-output-paths")" == marker ]] || fail 'release payload was nested into the racing target'
assert_no_build_workspace "${RACE_PARENT}"

readonly NEGATIVE_PARENT="${WORK_ROOT}/negative-parent"
install -d -m 0700 "${NEGATIVE_PARENT}"
expect_failure \
  '--version must be canonical SemVer' \
  "${WORK_ROOT}/invalid-version.log" \
  run_builder "${NEGATIVE_PARENT}/invalid-version" 01.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  '--version must be canonical SemVer' \
  "${WORK_ROOT}/invalid-prerelease-version.log" \
  run_builder "${NEGATIVE_PARENT}/invalid-prerelease-version" 1.2.3-01 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
FIXTURE_GRADLE_MAX_WORKERS=01 \
  expect_failure \
    '--gradle-max-workers must be a canonical integer from 1 through 256' \
    "${WORK_ROOT}/invalid-gradle-max-workers.log" \
    run_builder "${NEGATIVE_PARENT}/invalid-gradle-max-workers" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
readonly LONG_VERSION="1.2.3+$(printf 'a%.0s' {1..123})"
expect_failure \
  '--version must be at most 128 ASCII bytes' \
  "${WORK_ROOT}/long-version.log" \
  run_builder "${NEGATIVE_PARENT}/long-version" "${LONG_VERSION}" "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  '--commit must be a reviewed lowercase 40-hex commit ID' \
  "${WORK_ROOT}/invalid-commit.log" \
  run_builder "${NEGATIVE_PARENT}/invalid-commit" 1.2.3 deadbeef "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  '--commit contains a symlink, submodule, or unsupported file mode' \
  "${WORK_ROOT}/symlink-tree-entry.log" \
  run_builder "${NEGATIVE_PARENT}/symlink-tree-entry" 1.2.3 "${SYMLINK_ENTRY_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'reviewed commit contains a non-blob recursive tree entry' \
  "${WORK_ROOT}/gitlink-tree-entry.log" \
  run_builder "${NEGATIVE_PARENT}/gitlink-tree-entry" 1.2.3 "${GITLINK_ENTRY_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle wrapper properties differ from the exact 8.14.3 release contract' \
  "${WORK_ROOT}/wrapper-properties-drift.log" \
  run_builder "${NEGATIVE_PARENT}/wrapper-properties-drift" 1.2.3 "${WRAPPER_PROPERTIES_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle wrapper JAR differs from the official 8.14.3 wrapper JAR' \
  "${WORK_ROOT}/wrapper-jar-drift.log" \
  run_builder "${NEGATIVE_PARENT}/wrapper-jar-drift" 1.2.3 "${WRAPPER_JAR_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle launcher differs from the pinned release launcher' \
  "${WORK_ROOT}/gradlew-drift.log" \
  run_builder "${NEGATIVE_PARENT}/gradlew-drift" 1.2.3 "${GRADLEW_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle launcher must be one regular mode 0755 file' \
  "${WORK_ROOT}/gradlew-mode-drift.log" \
  run_builder "${NEGATIVE_PARENT}/gradlew-mode-drift" 1.2.3 "${GRADLEW_MODE_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle wrapper JAR must be one regular mode 0644 file' \
  "${WORK_ROOT}/wrapper-jar-mode-drift.log" \
  run_builder "${NEGATIVE_PARENT}/wrapper-jar-mode-drift" 1.2.3 "${WRAPPER_JAR_MODE_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle wrapper properties must be one regular mode 0644 file' \
  "${WORK_ROOT}/wrapper-properties-mode-drift.log" \
  run_builder "${NEGATIVE_PARENT}/wrapper-properties-mode-drift" 1.2.3 "${WRAPPER_PROPERTIES_MODE_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle verification metadata differs from the pinned dependency contract' \
  "${WORK_ROOT}/verification-metadata-drift.log" \
  run_builder "${NEGATIVE_PARENT}/verification-metadata-drift" 1.2.3 "${VERIFICATION_METADATA_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle verification metadata must be one regular mode 0644 file' \
  "${WORK_ROOT}/verification-metadata-mode-drift.log" \
  run_builder "${NEGATIVE_PARENT}/verification-metadata-mode-drift" 1.2.3 "${VERIFICATION_METADATA_MODE_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle verification metadata must be one regular mode 0644 file' \
  "${WORK_ROOT}/verification-metadata-missing.log" \
  run_builder "${NEGATIVE_PARENT}/verification-metadata-missing" 1.2.3 "${VERIFICATION_METADATA_MISSING_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'executing Android release wrapper differs from reviewed commit' \
  "${WORK_ROOT}/reviewed-builder-drift.log" \
  run_builder "${NEGATIVE_PARENT}/reviewed-builder-drift" 1.2.3 "${BUILDER_DRIFT_COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle wrapper properties differ from the exact 8.14.3 release contract' \
  "${WORK_ROOT}/post-pin-wrapper-properties-mutation.log" \
  run_builder \
    "${NEGATIVE_PARENT}/post-pin-wrapper-properties-mutation" \
    1.2.3 \
    "${POST_PIN_PROPERTIES_MUTATION_COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle wrapper JAR differs from the official 8.14.3 wrapper JAR' \
  "${WORK_ROOT}/post-pin-wrapper-jar-mutation.log" \
  run_builder \
    "${NEGATIVE_PARENT}/post-pin-wrapper-jar-mutation" \
    1.2.3 \
    "${POST_PIN_JAR_MUTATION_COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}"
expect_failure \
  'materialized Gradle verification metadata differs from the pinned dependency contract' \
  "${WORK_ROOT}/post-pin-verification-metadata-mutation.log" \
  run_builder \
    "${NEGATIVE_PARENT}/post-pin-verification-metadata-mutation" \
    1.2.3 \
    "${POST_PIN_METADATA_MUTATION_COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}"
expect_failure \
  '--pnpm-entry must report exactly pnpm 9.15.4' \
  "${WORK_ROOT}/wrong-pnpm-version.log" \
  run_builder \
    "${NEGATIVE_PARENT}/wrong-pnpm-version" \
    1.2.3 \
    "${COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}" \
    single \
    "${FAKE_BIN}/pnpm-wrong-version"
expect_failure \
  '--apksigner-bin differs from the pinned Android Build Tools 36.0.0 launcher' \
  "${WORK_ROOT}/wrong-apksigner-launcher.log" \
  run_builder \
    "${NEGATIVE_PARENT}/wrong-apksigner-launcher" \
    1.2.3 \
    "${COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}" \
    single \
    "${FAKE_BIN}/pnpm" \
    "${TAMPERED_APKSIGNER}"
expect_failure \
  'apksigner sibling lib/apksigner.jar differs from the pinned Android Build Tools 36.0.0 JAR' \
  "${WORK_ROOT}/wrong-apksigner-jar.log" \
  run_builder \
    "${NEGATIVE_PARENT}/wrong-apksigner-jar" \
    1.2.3 \
    "${COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}" \
    single \
    "${FAKE_BIN}/pnpm" \
    "${WRONG_APKSIGNER}"

for invalid_apk_fixture in symlink:1.2.4 directory:1.2.5 nested:1.2.6 fifo:1.2.7; do
  invalid_apk_kind="${invalid_apk_fixture%%:*}"
  invalid_apk_version="${invalid_apk_fixture#*:}"
  expect_failure \
    'Gradle must produce exactly one app-release-unsigned.apk' \
    "${WORK_ROOT}/${invalid_apk_kind}-unsigned-apk.log" \
    run_builder \
      "${NEGATIVE_PARENT}/${invalid_apk_kind}-unsigned-apk" \
      "${invalid_apk_version}" \
      "${COMMIT}" \
      "${FIXTURE_KEYSTORE}" \
      "${ACTUAL_FINGERPRINT}"
done
unset invalid_apk_fixture invalid_apk_kind invalid_apk_version

for ambient_injection_fixture in \
  'SIGNING_STORE_PASSWORD:ambient Android signing credential variable is forbidden' \
  'ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD:ambient Android signing credential variable is forbidden' \
  'ORG_GRADLE_PROJECT_android.injected.signing.store.file:ambient Gradle project property is forbidden' \
  'GRADLE_OPTS:ambient Gradle/JVM option injection is forbidden' \
  'JAVA_TOOL_OPTIONS:ambient Gradle/JVM option injection is forbidden' \
  'BASH_ENV:ambient shell injection variable is forbidden' \
  'BASH_FUNC_git%%:ambient shell injection variable is forbidden'; do
  injection_name="${ambient_injection_fixture%%:*}"
  expected_injection_failure="${ambient_injection_fixture#*:}"
  injection_value='fixture-injection'
  [[ "${injection_name}" != "BASH_ENV" ]] || injection_value="${WORK_ROOT}/malicious-bash-env"
  [[ "${injection_name}" != "BASH_FUNC_git%%" ]] ||
    injection_value='() { printf "imported git function executed\\n" >"${FIXTURE_PATH_HIJACK_MARKER:?}"; }'
  FIXTURE_AMBIENT_INJECTION_NAME="${injection_name}" \
  FIXTURE_AMBIENT_INJECTION_VALUE="${injection_value}" \
    expect_failure \
      "${expected_injection_failure}" \
      "${WORK_ROOT}/ambient-${injection_name//[^A-Za-z0-9]/-}.log" \
      run_builder \
        "${NEGATIVE_PARENT}/ambient-${injection_name//[^A-Za-z0-9]/-}" \
        1.2.3 \
        "${COMMIT}" \
        "${FIXTURE_KEYSTORE}" \
        "${ACTUAL_FINGERPRINT}"
done
unset ambient_injection_fixture injection_name expected_injection_failure injection_value
[[ ! -e "${BASH_ENV_MARKER}" ]] || fail 'BASH_ENV executed before the privileged wrapper rejected it'

expect_failure \
  '--store-password-fd must be an integer from 3 through 1023' \
  "${WORK_ROOT}/leading-zero-password-fd.log" \
  run_builder \
    "${NEGATIVE_PARENT}/leading-zero-password-fd" \
    1.2.3 \
    "${COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}" \
    single \
    "${FAKE_BIN}/pnpm" \
    "${TRUSTED_APKSIGNER_PREFIX}-single" \
    03 \
    4
expect_failure \
  '--store-password-fd and --key-password-fd must be different' \
  "${WORK_ROOT}/duplicate-password-fd.log" \
  run_builder \
    "${NEGATIVE_PARENT}/duplicate-password-fd" \
    1.2.3 \
    "${COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}" \
    single \
    "${FAKE_BIN}/pnpm" \
    "${TRUSTED_APKSIGNER_PREFIX}-single" \
    3 \
    3

readonly SYMLINK_KEYSTORE="${WORK_ROOT}/symlink-keystore.jks"
ln -s "${FIXTURE_KEYSTORE}" "${SYMLINK_KEYSTORE}"
expect_failure \
  '--keystore must name an absolute readable regular file and may not be a symlink' \
  "${WORK_ROOT}/symlink-keystore.log" \
  run_builder "${NEGATIVE_PARENT}/symlink-keystore" 1.2.3 "${COMMIT}" "${SYMLINK_KEYSTORE}" "${ACTUAL_FINGERPRINT}"

readonly HARDLINK_KEYSTORE="${FIXTURE_JAVA_HOME}/fixture-release-hardlink.jks"
ln "${FIXTURE_KEYSTORE}" "${HARDLINK_KEYSTORE}"
expect_failure \
  '--keystore must have exactly one hard link' \
  "${WORK_ROOT}/hardlink-keystore.log" \
  run_builder "${NEGATIVE_PARENT}/hardlink-keystore" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
rm -- "${HARDLINK_KEYSTORE}"

chmod 0775 "${FIXTURE_ANDROID_HOME}/platforms/android-36/source.properties"
expect_failure \
  'ANDROID_HOME/platforms/android-36 descendants must not be group- or other-writable' \
  "${WORK_ROOT}/writable-android-descendant.log" \
  run_builder "${NEGATIVE_PARENT}/writable-android-descendant" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
chmod 0644 "${FIXTURE_ANDROID_HOME}/platforms/android-36/source.properties"

install -d -m 0700 "${WORK_ROOT}/symlinked-java-directory-target"
ln -s "${WORK_ROOT}/symlinked-java-directory-target" "${FIXTURE_JAVA_HOME}/symlinked-directory"
expect_failure \
  'JAVA_HOME may contain only regular-file symlinks' \
  "${WORK_ROOT}/symlinked-java-directory.log" \
  run_builder "${NEGATIVE_PARENT}/symlinked-java-directory" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"
rm -- "${FIXTURE_JAVA_HOME}/symlinked-directory"

readonly KEYSTORE_REAL_PARENT="${WORK_ROOT}/keystore-real-parent"
readonly KEYSTORE_SYMLINK_PARENT="${WORK_ROOT}/keystore-symlink-parent"
install -d -m 0700 "${KEYSTORE_REAL_PARENT}"
printf 'ancestor symlink fixture keystore\n' >"${KEYSTORE_REAL_PARENT}/release.jks"
chmod 0600 "${KEYSTORE_REAL_PARENT}/release.jks"
ln -s "${KEYSTORE_REAL_PARENT}" "${KEYSTORE_SYMLINK_PARENT}"
expect_failure \
  '--keystore must use its canonical path and may not traverse symlinked ancestors' \
  "${WORK_ROOT}/symlink-keystore-ancestor.log" \
  run_builder "${NEGATIVE_PARENT}/symlink-keystore-ancestor" 1.2.3 "${COMMIT}" "${KEYSTORE_SYMLINK_PARENT}/release.jks" "${ACTUAL_FINGERPRINT}"

readonly INSECURE_KEYSTORE="${WORK_ROOT}/insecure-keystore.jks"
printf 'insecure fixture keystore\n' >"${INSECURE_KEYSTORE}"
chmod 0640 "${INSECURE_KEYSTORE}"
expect_failure \
  '--keystore must be mode 0600 or stricter' \
  "${WORK_ROOT}/insecure-keystore.log" \
  run_builder "${NEGATIVE_PARENT}/insecure-keystore" 1.2.3 "${COMMIT}" "${INSECURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"

for unsafe_mode in 0700 4600; do
  mode_keystore="${WORK_ROOT}/mode-${unsafe_mode}-keystore.jks"
  printf 'unsafe mode fixture keystore\n' >"${mode_keystore}"
  chmod "${unsafe_mode}" "${mode_keystore}"
  expect_failure \
    '--keystore must be mode 0600 or stricter' \
    "${WORK_ROOT}/mode-${unsafe_mode}-keystore.log" \
    run_builder "${NEGATIVE_PARENT}/mode-${unsafe_mode}-keystore" 1.2.3 "${COMMIT}" "${mode_keystore}" "${ACTUAL_FINGERPRINT}"
done
unset unsafe_mode mode_keystore

for invalid_signer_mode in none multiple; do
  expect_failure \
    'the release APK must contain exactly one signer SHA-256 certificate digest' \
    "${WORK_ROOT}/${invalid_signer_mode}-signer.log" \
    run_builder \
      "${NEGATIVE_PARENT}/${invalid_signer_mode}-signer" \
      1.2.3 \
      "${COMMIT}" \
      "${FIXTURE_KEYSTORE}" \
      "${ACTUAL_FINGERPRINT}" \
      "${invalid_signer_mode}"
  [[ ! -e "${NEGATIVE_PARENT}/${invalid_signer_mode}-signer" ]] ||
    fail "${invalid_signer_mode} signer verification published an output directory"
done
unset invalid_signer_mode

expect_failure \
  'apksigner rejected the release APK' \
  "${WORK_ROOT}/warning-signer.log" \
  run_builder \
    "${NEGATIVE_PARENT}/warning-signer" \
    1.2.3 \
    "${COMMIT}" \
    "${FIXTURE_KEYSTORE}" \
    "${ACTUAL_FINGERPRINT}" \
    warning
[[ ! -e "${NEGATIVE_PARENT}/warning-signer" ]] ||
  fail 'apksigner warning published an output directory'

readonly WRONG_FINGERPRINT="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
expect_failure \
  'the release APK signer SHA-256 fingerprint does not match --signer-sha256' \
  "${WORK_ROOT}/wrong-signer.log" \
  run_builder "${NEGATIVE_PARENT}/wrong-signer" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${WRONG_FINGERPRINT}"
[[ ! -e "${NEGATIVE_PARENT}/wrong-signer" ]] || fail 'signer mismatch published an output directory'
assert_no_build_workspace "${NEGATIVE_PARENT}"
[[ ! -e "${PATH_HIJACK_MARKER}" ]] || fail 'release wrapper executed a tool from the mutable live worktree PATH'
[[ ! -e "${INTERPRETER_HIJACK_MARKER}" ]] || fail 'release wrapper used a PATH-selected Bash interpreter'
[[ ! -e "${NODE_TOOL_HIJACK_MARKER}" ]] || fail 'release wrapper used a PATH-selected Node or pnpm tool'
[[ ! -e "${APKSIGNER_PATH_HIJACK_MARKER}" ]] || fail 'release wrapper used a caller PATH-selected apksigner'
[[ ! -e "${SYSTEM_TOOL_HIJACK_MARKER}" ]] || fail 'release wrapper used a caller PATH-selected system tool'
[[ ! -e "${NODE_INJECTION_MARKER}" ]] || fail 'ambient NODE_OPTIONS executed in the release build'
[[ ! -e "${MATERIALIZER_EVAL_MARKER}" ]] || fail 'Git tree path was evaluated as shell code during materialization'

printf '\n# live wrapper drift\n' >>"${FIXTURE_BUILDER}"
expect_failure \
  'executing Android release wrapper differs from reviewed commit' \
  "${WORK_ROOT}/live-builder-drift.log" \
  run_builder "${NEGATIVE_PARENT}/live-builder-drift" 1.2.3 "${COMMIT}" "${FIXTURE_KEYSTORE}" "${ACTUAL_FINGERPRINT}"

printf 'Android release contract fixture passed.\n'
