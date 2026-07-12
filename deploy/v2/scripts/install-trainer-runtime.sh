#!/usr/bin/bash -p

trainer_runtime_parent_is_empty() (
  local parent="$1"
  [[ -d "$parent" && ! -L "$parent" ]] || return 1
  shopt -s dotglob nullglob
  local -a entries=("$parent"/*)
  (( ${#entries[@]} == 0 ))
)

run_portable_python_readonly() {
  local source_root="$1" sandbox_root="$2"
  shift 2
  /usr/bin/bwrap \
    --unshare-all \
    --die-with-parent \
    --new-session \
    --cap-drop ALL \
    --hostname ascendany-trainer-build \
    --proc /proc \
    --dev /dev \
    --tmpfs /tmp \
    --dir /run \
    --dir /opt \
    --dir /opt/ascendany-trainer-runtime \
    --ro-bind /lib /lib \
    --ro-bind /lib64 /lib64 \
    --ro-bind "$source_root" "$sandbox_root" \
    --clearenv \
    --setenv HOME /nonexistent \
    --setenv LANG C.UTF-8 \
    --setenv LC_ALL C.UTF-8 \
    --setenv PYTHONHASHSEED 0 \
    --setenv TZ UTC \
    -- \
    "$sandbox_root/python/bin/python3.14" -B -s -P "$@"
}

run_runtime_attestation() {
  local source_root="$1" package_root="$2"
  local runtime_construction_sha256="$3" runtime_provenance_sha256="$4"
  local runtime_tree_sha256="$5" host_capability_sha256="$6"
  local output_source
  output_source="$(mktemp -d /tmp/ascendany-trainer-attestation-output.XXXXXXXX)"
  chmod 0700 "$output_source"
  local probe='import json,os,sys;sys.path.insert(0,"/trainer/recommendation");from ascendany_recommendation_trainer.attestation import attest_runtime;print(json.dumps(attest_runtime(dict(os.environ)),sort_keys=True,separators=(",",":")))'
  local result=0
  /usr/bin/bwrap \
    --unshare-all \
    --die-with-parent \
    --new-session \
    --cap-drop ALL \
    --hostname ascendany-trainer \
    --proc /proc \
    --dev /dev \
    --tmpfs /tmp \
    --dir /run \
    --dir /opt \
    --dir /opt/ascendany-trainer-runtime \
    --dir /trainer \
    --ro-bind /lib /lib \
    --ro-bind /lib64 /lib64 \
    --ro-bind "$source_root" /opt/ascendany-trainer-runtime/current \
    --ro-bind /sys /sys \
    --dev-bind /dev/nvidia-uvm /dev/nvidia-uvm \
    --dev-bind /dev/nvidia0 /dev/nvidia0 \
    --dev-bind /dev/nvidiactl /dev/nvidiactl \
    --ro-bind "$package_root" /trainer/recommendation \
    --bind "$output_source" /output \
    --chdir /output \
    --clearenv \
    --setenv ASCENDANY_TRAINER_EXPECTED_HOST_CAPABILITY_SHA256 "$host_capability_sha256" \
    --setenv ASCENDANY_TRAINER_EXPECTED_RUNTIME_CONSTRUCTION_SHA256 "$runtime_construction_sha256" \
    --setenv ASCENDANY_TRAINER_EXPECTED_RUNTIME_PROVENANCE_SHA256 "$runtime_provenance_sha256" \
    --setenv ASCENDANY_TRAINER_EXPECTED_RUNTIME_TREE_SHA256 "$runtime_tree_sha256" \
    --setenv ASCENDANY_TRAINER_RUNTIME_ROOT /opt/ascendany-trainer-runtime/current \
    --setenv CUBLAS_WORKSPACE_CONFIG :4096:8 \
    --setenv CUDA_VISIBLE_DEVICES 0 \
    --setenv HOME /nonexistent \
    --setenv LANG C.UTF-8 \
    --setenv LC_ALL C.UTF-8 \
    --setenv MKL_NUM_THREADS 8 \
    --setenv OMP_NUM_THREADS 8 \
    --setenv OPENBLAS_NUM_THREADS 8 \
    --setenv PYTHONHASHSEED 0 \
    --setenv PWD /output \
    --setenv TZ UTC \
    -- \
    /opt/ascendany-trainer-runtime/current/python/bin/python3.14 \
      -B -s -P -c "$probe" || result=$?
  rm -rf --one-file-system -- "$output_source"
  return "$result"
}

if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
  return 0
fi

set +x
set -Eeuo pipefail

umask 022
export PATH=/usr/bin:/bin
readonly PATH
export LC_ALL=C

readonly release_root=/opt/ascendany/v2
readonly release_manifest="$release_root/release-manifest.json"
readonly runtime_parent=/opt/ascendany-trainer-runtime
readonly runtime_selector="$runtime_parent/current"
readonly runtime_family=torch-2.13.0-cu130
readonly runtime_lock_relative=trainers/recommendation/runtime-requirements-cu130.lock
readonly runtime_closure_relative=trainers/recommendation/runtime-closure-cu130.json
readonly runtime_wheels_relative=trainers/recommendation/runtime-wheels-cu130.json
readonly runtime_python_source_relative=trainers/recommendation/runtime-python-cu130.json
readonly runtime_installer_relative=scripts/install-trainer-runtime.sh
readonly runtime_tree_identity_relative=scripts/trainer-runtime-tree-identity.sh
readonly host_capability_identity_relative=scripts/trainer-host-capability-identity.sh
readonly construction_inputs_name=.ascendany-construction-inputs
readonly runtime_marker_name=.ascendany-runtime-provenance.json
readonly captured_manifest_name=release-manifest.json
readonly captured_lock_name=runtime-requirements-cu130.lock
readonly captured_closure_name=runtime-closure-cu130.json
readonly captured_wheels_name=runtime-wheels-cu130.json
readonly captured_python_source_name=runtime-python-cu130.json
readonly captured_installer_name=install-trainer-runtime.sh
readonly captured_tree_identity_name=trainer-runtime-tree-identity.sh
readonly captured_host_capability_name=trainer-host-capability-identity.sh
readonly captured_uv_name=uv
readonly sandbox_bin=/usr/bin/bwrap
readonly trainer_unit=ascendany-trainer-agent.service
readonly portable_python_version=3.14.6
readonly portable_python_key=cpython-3.14.6-linux-x86_64-gnu
readonly torch_version=2.13.0+cu130
readonly cuda_version=13.0
readonly uv_version='uv 0.9.26'
readonly uv_archive_url='https://github.com/astral-sh/uv/releases/download/0.9.26/uv-x86_64-unknown-linux-gnu.tar.gz'
readonly uv_archive_sha256=30ccbf0a66dc8727a02b0e245c583ee970bdafecf3a443c1686e1b30ec4939e8
readonly uv_binary_sha256=0650696de7f403348e9dd617e1f65dc32147c106c40129138017efd8f0f01cc8
readonly uv_archive_root=uv-x86_64-unknown-linux-gnu

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

require_root_owned_ancestry() {
  local path="$1" current metadata owner group mode
  current="$(dirname -- "$path")"
  while :; do
    [[ -d "$current" && ! -L "$current" ]] || fail "trusted path ancestry is missing or linked: $current"
    metadata="$(stat -Lc '%u:%g:%a' -- "$current")"
    IFS=: read -r owner group mode <<<"$metadata"
    [[ "$owner" == 0 && "$group" == 0 && "$((8#$mode & 8#22))" == 0 ]] ||
      fail "trusted path ancestry is non-root or writable outside root: $current"
    [[ "$current" == / ]] && break
    current="$(dirname -- "$current")"
  done
}

stable_file_identity() {
  local path="$1"
  stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$path"
}

declare -A captured_source_identities=()
capture_stable_file() {
  local source="$1" target="$2" expected_mode="$3"
  local identity_before identity_after target_identity source_sha target_sha
  require_root_owned_ancestry "$source"
  [[ -f "$source" && ! -L "$source" && "$(realpath -e -- "$source")" == "$source" &&
     "$(stat -Lc '%u:%g:%a:%h' -- "$source")" == "0:0:$expected_mode:1" ]] ||
    fail "reviewed release file has unsafe identity: ${source#"$release_root"/}"
  identity_before="$(stable_file_identity "$source")"
  source_sha="$(sha256sum -- "$source" | awk '{print $1}')"
  [[ "$identity_before" == "$(stable_file_identity "$source")" ]] ||
    fail "reviewed release file changed while hashing: ${source#"$release_root"/}"
  install -m "0$expected_mode" -- "$source" "$target"
  identity_after="$(stable_file_identity "$source")"
  [[ "$identity_before" == "$identity_after" ]] ||
    fail "reviewed release file changed while capturing: ${source#"$release_root"/}"
  target_identity="$(stat -Lc '%u:%g:%a:%h' -- "$target")"
  target_sha="$(sha256sum -- "$target" | awk '{print $1}')"
  [[ "$target_identity" == "0:0:$expected_mode:1" && "$target_sha" == "$source_sha" ]] ||
    fail "captured release file differs from its stable source: ${source#"$release_root"/}"
  captured_source_identities["$source"]="$identity_after"
}

manifest_value() {
  local manifest="$1" relative="$2" field="$3"
  jq -er --arg path "$relative" --arg field "$field" '
    [.files[] | select(.path == $path)] as $matches |
    if ($matches | length) == 1 then $matches[0][$field] else error("missing path") end
  ' "$manifest"
}

capture_reviewed_file() {
  local manifest="$1" relative="$2" expected_mode="$3" target="$4"
  local expected_sha expected_size expected_manifest_mode
  expected_sha="$(manifest_value "$manifest" "$relative" sha256)" ||
    fail "release manifest does not bind exactly one $relative"
  expected_size="$(manifest_value "$manifest" "$relative" size)" ||
    fail "release manifest does not bind the size of $relative"
  expected_manifest_mode="$(manifest_value "$manifest" "$relative" mode)" ||
    fail "release manifest does not bind the mode of $relative"
  [[ "$expected_manifest_mode" == "0$expected_mode" ]] ||
    fail "release manifest mode differs for $relative"
  capture_stable_file "$release_root/$relative" "$target" "$expected_mode"
  [[ "$(sha256sum -- "$target" | awk '{print $1}')" == "$expected_sha" &&
     "$(stat -Lc '%s' -- "$target")" == "$expected_size" ]] ||
    fail "captured release file differs from its manifest: $relative"
}

recheck_captured_sources() {
  local source
  for source in "${!captured_source_identities[@]}"; do
    [[ "$(stable_file_identity "$source")" == "${captured_source_identities[$source]}" ]] ||
      fail "reviewed release input changed during coherent capture: ${source#"$release_root"/}"
  done
}

remove_exact_seed_packaging() {
  local python_root="$1"
  local runtime_source="$(dirname -- "$python_root")"
  local site_packages="$python_root/lib/python3.14/site-packages"
  local seed_probe seed_metadata
  seed_probe='import importlib.metadata,json; print(json.dumps(sorted((d.metadata.get("Name"),d.version) for d in importlib.metadata.distributions()),separators=(",",":")))'
  seed_metadata="$(
    run_portable_python_readonly "$runtime_source" /opt/ascendany-trainer-runtime/current \
      -c "$seed_probe"
  )" || fail "portable CPython seed distribution set cannot be read"
  [[ "$seed_metadata" == '[["pip","26.1.2"]]' ]] ||
    fail "portable CPython seed distribution set drifted"
  [[ -d "$site_packages/pip" && ! -L "$site_packages/pip" &&
     -d "$site_packages/pip-26.1.2.dist-info" && ! -L "$site_packages/pip-26.1.2.dist-info" ]] ||
    fail "portable CPython seed pip directories drifted"
  local -a seed_bins=()
  mapfile -t seed_bins < <(find "$python_root/bin" -mindepth 1 -maxdepth 1 -type f -name 'pip*' -printf '%f\n' | LC_ALL=C sort)
  [[ "$(printf '%s\n' "${seed_bins[@]}")" == $'pip\npip3\npip3.14' ]] ||
    fail "portable CPython seed pip scripts drifted"
  local forbidden
  for forbidden in \
    setuptools setuptools-78.1.0.dist-info _distutils_hack pkg_resources distutils-precedence.pth; do
    [[ ! -e "$site_packages/$forbidden" && ! -L "$site_packages/$forbidden" ]] ||
      fail "portable CPython unexpectedly carries seed setuptools content: $forbidden"
  done
  rm -rf -- "$site_packages/pip" "$site_packages/pip-26.1.2.dist-info"
  rm -- "$python_root/bin/pip" "$python_root/bin/pip3" "$python_root/bin/pip3.14"
  [[ ! -e "$site_packages/pip" && ! -e "$site_packages/pip-26.1.2.dist-info" ]] ||
    fail "portable CPython seed pip removal was incomplete"
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] || fail "trainer runtime installer must execute directly"
[[ "$EUID" == 0 ]] || fail "trainer runtime installer requires root"
[[ "$#" == 0 ]] || fail "trainer runtime installer accepts no arguments"
readonly installer_path="$release_root/$runtime_installer_relative"
[[ "$0" == "$installer_path" && -f "$installer_path" && ! -L "$installer_path" &&
   "$(realpath -e -- "$installer_path")" == "$installer_path" &&
   "$(stat -Lc '%u:%g:%a:%h' -- "$installer_path")" == "0:0:755:1" ]] ||
  fail "trainer runtime installer must be the canonical reviewed release script"
[[ -d "$release_root" && ! -L "$release_root" &&
   "$(realpath -e -- "$release_root")" == "$release_root" &&
   "$(stat -Lc '%u:%g:%a' -- "$release_root")" == "0:0:755" ]] ||
  fail "release root has unsafe identity"
require_root_owned_ancestry "$release_root"
if [[ "$(systemctl show "$trainer_unit" --property=ActiveState --value)" != inactive ||
      "$(systemctl show "$trainer_unit" --property=SubState --value)" != dead ]]; then
  fail "$trainer_unit must be inactive and dead before runtime installation"
fi
[[ -f "$sandbox_bin" && ! -L "$sandbox_bin" && -x "$sandbox_bin" &&
   "$(realpath -e -- "$sandbox_bin")" == "$sandbox_bin" &&
   "$(stat -Lc '%u:%g' -- "$sandbox_bin")" == "0:0" &&
   "$((8#$(stat -Lc '%a' -- "$sandbox_bin") & 8#22))" == 0 ]] ||
  fail "bubblewrap executable is linked, non-root, or writable outside root"
require_root_owned_ancestry "$sandbox_bin"
for required_tool in /usr/bin/curl /usr/bin/flock /usr/bin/findmnt /usr/bin/tar; do
  [[ -f "$required_tool" && ! -L "$required_tool" && -x "$required_tool" &&
     "$(realpath -e -- "$required_tool")" == "$required_tool" &&
     "$(stat -Lc '%u:%g' -- "$required_tool")" == "0:0" &&
     "$((8#$(stat -Lc '%a' -- "$required_tool") & 8#22))" == 0 ]] ||
    fail "required runtime construction tool has unsafe identity: $required_tool"
  require_root_owned_ancestry "$required_tool"
done

if [[ ! -e "$runtime_parent" && ! -L "$runtime_parent" ]]; then
  install -d -o root -g root -m 0755 "$runtime_parent"
fi
[[ -d "$runtime_parent" && ! -L "$runtime_parent" &&
   "$(realpath -e -- "$runtime_parent")" == "$runtime_parent" &&
   "$(stat -Lc '%u:%g:%a' -- "$runtime_parent")" == "0:0:755" ]] ||
  fail "trainer runtime parent has unsafe identity"
require_root_owned_ancestry "$runtime_parent"
readonly install_lock=/run/lock/ascendany-trainer-runtime.lock
exec {install_lock_fd}>"$install_lock"
chmod 0600 "$install_lock"
chown root:root "$install_lock"
[[ "$(stat -Lc '%u:%g:%a:%h' -- "$install_lock")" == "0:0:600:1" ]] ||
  fail "trainer runtime installation lock has unsafe identity"
/usr/bin/flock --exclusive --nonblock "$install_lock_fd" ||
  fail "another trainer runtime publication owns the installation lock"

readonly current_boot_id="$(</proc/sys/kernel/random/boot_id)"
[[ "$current_boot_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] ||
  fail "kernel boot identity is noncanonical"
shopt -s dotglob nullglob
for stale_stage in "$runtime_parent"/."$runtime_family".stage.*; do
  stale_marker="$stale_stage/.ascendany-stage-owner.json"
  [[ -d "$stale_stage" && ! -L "$stale_stage" &&
     "$(stat -Lc '%u:%g:%a' -- "$stale_stage")" == "0:0:700" &&
     -f "$stale_marker" && ! -L "$stale_marker" &&
     "$(stat -Lc '%u:%g:%a:%h' -- "$stale_marker")" == "0:0:600:1" ]] ||
    fail "stale trainer runtime stage has no provable installer identity: $stale_stage"
  stale_pid="$(jq -er '.pid' "$stale_marker")" || fail "stale stage PID is invalid"
  stale_boot_id="$(jq -er '.bootId' "$stale_marker")" || fail "stale stage boot identity is invalid"
  jq -e --arg path "$stale_stage" '
    type == "object" and keys == ["bootId", "path", "pid", "schema"] and
    .schema == "ascendany.trainer-runtime.stage-owner.v1" and .path == $path and
    (.bootId | type == "string" and test("^[0-9a-f-]{36}$")) and
    (.pid | type == "number" and floor == . and . > 0)
  ' "$stale_marker" >/dev/null || fail "stale stage owner marker is noncanonical"
  if [[ "$stale_boot_id" == "$current_boot_id" && -d "/proc/$stale_pid" ]]; then
    fail "a live process still owns trainer runtime stage $stale_stage"
  fi
  rm -rf --one-file-system -- "$stale_stage"
done

previous_selector=
if [[ -L "$runtime_selector" ]]; then
  previous_selector="$(readlink -- "$runtime_selector")"
  [[ "$previous_selector" =~ ^${runtime_family}-[0-9a-f]{64}$ &&
     -d "$runtime_parent/$previous_selector" && ! -L "$runtime_parent/$previous_selector" ]] ||
    fail "trainer runtime selector target is invalid"
elif [[ -e "$runtime_selector" ]]; then
  fail "trainer runtime selector must be a relative symbolic link"
fi
for runtime_entry in "$runtime_parent"/*; do
  runtime_name="${runtime_entry##*/}"
  if [[ "$runtime_name" == current ]]; then
    [[ -L "$runtime_entry" ]] || fail "trainer runtime selector is not a symbolic link"
  elif [[ "$runtime_name" =~ ^${runtime_family}-[0-9a-f]{64}$ ]]; then
    [[ -d "$runtime_entry" && ! -L "$runtime_entry" ]] || fail "versioned trainer runtime is not one directory"
  else
    fail "trainer runtime parent contains an unowned entry: $runtime_name"
  fi
done
readonly runtime_parent_identity="$(stat -Lc '%d:%i' -- "$runtime_parent")"
runtime_stage="$(mktemp -d -p "$runtime_parent" ".${runtime_family}.stage.XXXXXXXX")"
readonly runtime_stage
chmod 0700 "$runtime_stage"
stage_owner_json="$(jq -cn --arg bootId "$current_boot_id" --arg path "$runtime_stage" --argjson pid "$$" '
  {bootId:$bootId,path:$path,pid:$pid,schema:"ascendany.trainer-runtime.stage-owner.v1"}
')"
printf '%s' "$stage_owner_json" >"$runtime_stage/.ascendany-stage-owner.json"
chmod 0600 "$runtime_stage/.ascendany-stage-owner.json"
chown root:root "$runtime_stage/.ascendany-stage-owner.json"
published=0
runtime_stage_identity=
runtime_root=
selector_switched=0
cleanup() {
  if [[ -d "$runtime_stage" && ! -L "$runtime_stage" ]]; then
    rm -rf -- "$runtime_stage"
  fi
  if [[ "$selector_switched" == 1 && -n "$runtime_root" && -L "$runtime_selector" &&
        "$(readlink -- "$runtime_selector" 2>/dev/null || true)" == "${runtime_root##*/}" ]]; then
    selector_restore="$runtime_parent/.current.rollback.$$"
    rm -f -- "$selector_restore"
    if [[ -n "$previous_selector" ]]; then
      ln -s -- "$previous_selector" "$selector_restore"
      mv --no-target-directory -- "$selector_restore" "$runtime_selector"
    else
      rm -- "$runtime_selector"
    fi
    sync -f "$runtime_parent"
  fi
  if [[ "$published" == 1 && -n "$runtime_stage_identity" && -n "$runtime_root" &&
        -d "$runtime_root" && ! -L "$runtime_root" &&
        "$(stat -Lc '%d:%i' -- "$runtime_root" 2>/dev/null || true)" == "$runtime_stage_identity" ]]; then
    rm -rf -- "$runtime_root"
    sync -f "$runtime_parent"
  fi
}
trap cleanup EXIT

readonly construction_inputs="$runtime_stage/$construction_inputs_name"
install -d -o root -g root -m 0700 "$construction_inputs"
readonly captured_manifest="$construction_inputs/$captured_manifest_name"
readonly captured_lock="$construction_inputs/$captured_lock_name"
readonly captured_closure="$construction_inputs/$captured_closure_name"
readonly captured_wheels="$construction_inputs/$captured_wheels_name"
readonly captured_python_source="$construction_inputs/$captured_python_source_name"
readonly captured_installer="$construction_inputs/$captured_installer_name"
readonly captured_tree_identity="$construction_inputs/$captured_tree_identity_name"
readonly captured_host_capability="$construction_inputs/$captured_host_capability_name"
readonly captured_uv="$construction_inputs/$captured_uv_name"

capture_stable_file "$release_manifest" "$captured_manifest" 644
jq -e '
  type == "object" and keys == ["build", "commit", "files", "schema", "sourceDateEpoch", "version"] and
  .schema == "ascendany.release.v2" and
  (.commit | type == "string" and test("^[0-9a-f]{40}$")) and
  (.version | type == "string" and length > 0 and length <= 128) and
  (.sourceDateEpoch | type == "number" and floor == . and . >= 0) and
  (.build | type == "object" and keys == ["cgoEnabled", "goExperiment", "goVersion", "goamd64", "goarch", "gofips140", "goos"] and
    .cgoEnabled == false and .goos == "linux" and .goarch == "amd64" and .goamd64 == "v1" and .gofips140 == "off") and
  (.files | type == "array" and length == 77) and
  (all(.files[];
    type == "object" and keys == ["mode", "path", "sha256", "size"] and
    (.path | type == "string" and test("^[0-9A-Za-z][0-9A-Za-z._/-]*$")) and
    (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.size | type == "number" and floor == . and . > 0) and
    (.mode | type == "string" and test("^0[0-7]{3}$")))) and
  (([.files[].path] | length) == ([.files[].path] | unique | length))
' "$captured_manifest" >/dev/null || fail "captured release manifest is invalid"
capture_reviewed_file "$captured_manifest" "$runtime_lock_relative" 644 "$captured_lock"
capture_reviewed_file "$captured_manifest" "$runtime_closure_relative" 644 "$captured_closure"
capture_reviewed_file "$captured_manifest" "$runtime_wheels_relative" 644 "$captured_wheels"
capture_reviewed_file "$captured_manifest" "$runtime_python_source_relative" 644 "$captured_python_source"
capture_reviewed_file "$captured_manifest" "$runtime_installer_relative" 755 "$captured_installer"
capture_reviewed_file "$captured_manifest" "$runtime_tree_identity_relative" 755 "$captured_tree_identity"
capture_reviewed_file "$captured_manifest" "$host_capability_identity_relative" 755 "$captured_host_capability"
recheck_captured_sources

uv_download_stage="$runtime_stage/.uv-download"
install -d -o root -g root -m 0700 "$uv_download_stage"
/usr/bin/curl --fail --location --silent --show-error --proto '=https' --tlsv1.3 \
  --output "$uv_download_stage/uv.tar.gz" "$uv_archive_url"
[[ "$(sha256sum -- "$uv_download_stage/uv.tar.gz" | awk '{print $1}')" == "$uv_archive_sha256" ]] ||
  fail "official uv archive digest differs from the construction contract"
uv_archive_entries="$(/usr/bin/tar -tzf "$uv_download_stage/uv.tar.gz")"
[[ "$uv_archive_entries" == $'uv-x86_64-unknown-linux-gnu/\nuv-x86_64-unknown-linux-gnu/uv\nuv-x86_64-unknown-linux-gnu/uvx' ]] ||
  fail "official uv archive entry set differs from the construction contract"
/usr/bin/tar -xzf "$uv_download_stage/uv.tar.gz" -C "$uv_download_stage" \
  "$uv_archive_root/uv"
install -o root -g root -m 0755 "$uv_download_stage/$uv_archive_root/uv" "$captured_uv"
rm -rf --one-file-system -- "$uv_download_stage"
[[ "$(sha256sum -- "$captured_uv" | awk '{print $1}')" == "$uv_binary_sha256" &&
   "$(stat -Lc '%u:%g:%a:%h' -- "$captured_uv")" == "0:0:755:1" &&
   "$($captured_uv --version)" == "$uv_version" ]] ||
  fail "official uv binary identity differs from the construction contract"
readonly uv_bin="$captured_uv"

readonly python_archive_url='https://github.com/astral-sh/python-build-standalone/releases/download/20260623/cpython-3.14.6%2B20260623-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz'
readonly python_archive_sha256=c172314f4a8ec137a8f605289010c3d19c8b56867d968f0095074cc68efa1d29
jq -e --arg key "$portable_python_key" --arg url "$python_archive_url" --arg sha "$python_archive_sha256" '
  . == {
    ($key):{
      arch:{family:"x86_64",variant:null},libc:"gnu",major:3,minor:14,name:"cpython",
      os:"linux",patch:6,prerelease:"",sha256:$sha,url:$url,variant:null
    }
  }
' "$captured_python_source" >/dev/null || fail "portable CPython source contract drifted"

python_install_stage="$runtime_stage/.python-install"
install -d -o root -g root -m 0700 "$python_install_stage"
/usr/bin/env -i PATH=/usr/bin:/bin HOME=/nonexistent LC_ALL=C \
  "$uv_bin" python install \
  --install-dir "$python_install_stage" \
  --no-bin \
  --no-registry \
  --managed-python \
  --no-cache \
  --config-file /dev/null \
  --python-downloads-json-url "file://$captured_python_source" \
  "$portable_python_version"
readonly installed_python="$python_install_stage/$portable_python_key"
[[ -d "$installed_python" && ! -L "$installed_python" ]] ||
  fail "uv did not install the exact portable CPython source"
mv --no-target-directory -- "$installed_python" "$runtime_stage/python"
rm -rf -- "$python_install_stage"
readonly trainer_python_stage="$runtime_stage/python/bin/python3.14"
[[ -f "$trainer_python_stage" && ! -L "$trainer_python_stage" && -x "$trainer_python_stage" ]] ||
  fail "portable CPython executable is unavailable"
chown -R root:root "$runtime_stage/python"
chmod -R go-w "$runtime_stage/python"
/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
  "$captured_tree_identity" "$runtime_stage/python" >/dev/null ||
  fail "portable CPython base tree violates the closed-tree contract"
remove_exact_seed_packaging "$runtime_stage/python"

lock_closure_probe='import json,pathlib,re,sys,urllib.parse
lock_path,closure_path,wheels_path=sys.argv[1:]
packages={}
current=None
for raw in pathlib.Path(lock_path).read_text(encoding="utf-8").splitlines():
 line=raw.strip()
 if not line or line.startswith("#") or line.startswith("--index-url") or line.startswith("--extra-index-url"):
  continue
 requirement=re.fullmatch(r"([A-Za-z0-9][A-Za-z0-9_.-]*)==([^ ;\\]+)\s*\\?",line)
 if requirement:
  name=re.sub(r"[-_.]+","-",requirement.group(1)).lower()
  if name in packages:
   raise SystemExit("lock repeats a distribution")
  packages[name]={"hashes":set(),"version":requirement.group(2)}
  current=name
  continue
 digest=re.fullmatch(r"--hash=sha256:([0-9a-f]{64})\s*\\?",line)
 if digest and current is not None:
  packages[current]["hashes"].add(digest.group(1))
  continue
 raise SystemExit("lock contains an unparsed requirement or detached hash")
if len(packages)!=30 or any(not value["hashes"] for value in packages.values()):
 raise SystemExit("lock package or hash set is incomplete")
items=sorted(({"name":name,"version":value["version"]} for name,value in packages.items()),key=lambda item:item["name"])
closure_raw=pathlib.Path(closure_path).read_bytes()
closure=json.loads(closure_raw)
expected={"distributions":items,"schema":"ascendany.trainer-runtime.closure.v1"}
if closure!=expected or json.dumps(closure,sort_keys=True,separators=(",",":")).encode()+b"\n"!=closure_raw:
 raise SystemExit("reviewed runtime closure differs from the hashed lock")
wheels_raw=pathlib.Path(wheels_path).read_bytes()
wheels=json.loads(wheels_raw)
if json.dumps(wheels,sort_keys=True,separators=(",",":")).encode()+b"\n"!=wheels_raw or set(wheels)!={"schema","wheels"} or wheels.get("schema")!="ascendany.trainer-runtime.wheels.v1":
 raise SystemExit("reviewed wheel manifest is noncanonical")
entries=wheels.get("wheels")
if not isinstance(entries,list) or len(entries)!=30:
 raise SystemExit("reviewed wheel manifest count drifted")
seen_files=set()
seen_urls=set()
for index,entry in enumerate(entries):
 if not isinstance(entry,dict) or set(entry)!={"filename","name","sha256","url"}:
  raise SystemExit("wheel manifest entry shape drifted")
 name=entry["name"]
 filename=entry["filename"]
 digest=entry["sha256"]
 url=entry["url"]
 if index and entries[index-1]["name"]>=name or name not in packages:
  raise SystemExit("wheel manifest names are duplicated or unsorted")
 if not isinstance(filename,str) or not re.fullmatch(r"[0-9A-Za-z_.+]+-[0-9A-Za-z.+]+-[0-9A-Za-z_.+-]+\.whl",filename):
  raise SystemExit("wheel filename is noncanonical")
 parts=filename.split("-",2)
 if re.sub(r"[-_.]+","-",parts[0]).lower()!=name or parts[1]!=packages[name]["version"] or digest not in packages[name]["hashes"]:
  raise SystemExit("wheel filename, version, or hash differs from the lock")
 parsed=urllib.parse.urlsplit(url)
 if parsed.scheme!="https" or parsed.hostname not in {"download.pytorch.org","download-r2.pytorch.org","files.pythonhosted.org","pypi.nvidia.com"} or parsed.port is not None or parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment or urllib.parse.unquote(parsed.path.rsplit("/",1)[-1])!=filename:
  raise SystemExit("wheel URL differs from the official closed origin contract")
 if filename in seen_files or url in seen_urls:
  raise SystemExit("wheel manifest repeats a file or URL")
 seen_files.add(filename)
 seen_urls.add(url)'
run_portable_python_readonly "$runtime_stage" "$runtime_selector" \
  -c "$lock_closure_probe" \
  "$runtime_selector/$construction_inputs_name/$captured_lock_name" \
  "$runtime_selector/$construction_inputs_name/$captured_closure_name" \
  "$runtime_selector/$construction_inputs_name/$captured_wheels_name" ||
  fail "reviewed runtime lock, closure, and wheel manifest disagree"

wheelhouse="$runtime_stage/.ascendany-wheelhouse"
offline_cache="$runtime_stage/.ascendany-uv-offline-cache"
install -d -o root -g root -m 0700 "$wheelhouse" "$offline_cache"
while IFS=$'\t' read -r wheel_filename wheel_sha256 wheel_url; do
  wheel_part="$wheelhouse/.$wheel_filename.part"
  /usr/bin/curl --disable --fail --location --silent --show-error \
    --proto '=https' --tlsv1.3 --output "$wheel_part" "$wheel_url"
  [[ -f "$wheel_part" && ! -L "$wheel_part" &&
     "$(sha256sum -- "$wheel_part" | awk '{print $1}')" == "$wheel_sha256" ]] ||
    fail "downloaded wheel differs from its reviewed digest: $wheel_filename"
  chmod 0600 "$wheel_part"
  chown root:root "$wheel_part"
  mv --no-target-directory --no-clobber -- "$wheel_part" "$wheelhouse/$wheel_filename"
done < <(jq -r '.wheels[] | [.filename,.sha256,.url] | @tsv' "$captured_wheels")
expected_wheel_names="$(jq -r '.wheels[].filename' "$captured_wheels" | LC_ALL=C sort)"
actual_wheel_names="$(find "$wheelhouse" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | LC_ALL=C sort)"
[[ "$actual_wheel_names" == "$expected_wheel_names" &&
   "$(find "$wheelhouse" -mindepth 1 -maxdepth 1 ! -type f -print -quit)" == "" ]] ||
  fail "private wheelhouse has a missing, extra, or special entry"
while IFS=$'\t' read -r wheel_filename wheel_sha256; do
  [[ "$(stat -Lc '%u:%g:%a:%h' -- "$wheelhouse/$wheel_filename")" == "0:0:600:1" &&
     "$(sha256sum -- "$wheelhouse/$wheel_filename" | awk '{print $1}')" == "$wheel_sha256" ]] ||
    fail "private wheelhouse identity drifted: $wheel_filename"
done < <(jq -r '.wheels[] | [.filename,.sha256] | @tsv' "$captured_wheels")

/usr/bin/bwrap \
  --unshare-all \
  --die-with-parent \
  --new-session \
  --cap-drop ALL \
  --hostname ascendany-trainer-build \
  --proc /proc \
  --dev /dev \
  --tmpfs /tmp \
  --dir /run \
  --dir /opt \
  --dir /opt/ascendany-trainer-runtime \
  --dir /wheelhouse \
  --dir /cache \
  --ro-bind /lib /lib \
  --ro-bind /lib64 /lib64 \
  --bind "$runtime_stage" "$runtime_selector" \
  --ro-bind "$wheelhouse" /wheelhouse \
  --bind "$offline_cache" /cache \
  --clearenv \
  --setenv HOME /nonexistent \
  --setenv LANG C.UTF-8 \
  --setenv LC_ALL C.UTF-8 \
  --setenv PYTHONHASHSEED 0 \
  --setenv TZ UTC \
  -- \
  "$runtime_selector/$construction_inputs_name/$captured_uv_name" pip sync \
    --offline \
    --no-index \
    --find-links /wheelhouse \
    --cache-dir /cache \
    --config-file /dev/null \
    --python "$runtime_selector/python/bin/python3.14" \
    --require-hashes \
    --no-build \
    --break-system-packages \
    --link-mode=copy \
    --no-managed-python \
    --no-python-downloads \
    "$runtime_selector/$construction_inputs_name/$captured_lock_name"
rm -rf --one-file-system -- "$wheelhouse" "$offline_cache"
find "$runtime_stage/python/bin" -mindepth 1 ! -name python3.14 -delete
[[ "$(find "$runtime_stage/python/bin" -mindepth 1 -maxdepth 1 -printf '%f:%y\n')" == "python3.14:f" ]] ||
  fail "portable Python bin directory differs from the one-executable contract"

live_closure_probe='import importlib.metadata,json,re
items=[]
for distribution in importlib.metadata.distributions():
 name=distribution.metadata.get("Name")
 if not name:
  raise SystemExit("installed distribution has no name")
 items.append({"name":re.sub(r"[-_.]+","-",name).lower(),"version":distribution.version})
if len(items)!=30 or len(items)!=len({item["name"] for item in items}):
 raise SystemExit("installed distribution closure is not exact")
print(json.dumps({"distributions":sorted(items,key=lambda item:item["name"]),"schema":"ascendany.trainer-runtime.closure.v1"},sort_keys=True,separators=(",",":")))'
actual_closure="$runtime_stage/.ascendany-runtime-closure.actual.json"
run_portable_python_readonly "$runtime_stage" "$runtime_selector" \
  -c "$live_closure_probe" >"$actual_closure" ||
  fail "installed distribution closure cannot be enumerated"
cmp --silent -- "$captured_closure" "$actual_closure" ||
  fail "installed distribution closure differs from the reviewed lock closure"
rm -- "$actual_closure"

chown -R root:root "$runtime_stage"
chmod -R go-w "$runtime_stage"
chmod 0755 "$runtime_stage" "$runtime_stage/python" "$runtime_stage/python/bin" "$trainer_python_stage"
chmod 0755 "$construction_inputs" "$captured_installer" "$captured_tree_identity" "$captured_host_capability" "$captured_uv"
python_tree_identity="$(
  /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
    "$captured_tree_identity" "$runtime_stage/python"
)" || fail "portable Python final tree identity cannot be calculated"
host_capabilities="$(
  /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
    "$captured_host_capability" "$runtime_stage" "$trainer_python_stage"
)" || fail "trainer host capability identity cannot be calculated"
host_capabilities="$(jq -cS . <<<"$host_capabilities")" ||
  fail "trainer host capability identity cannot be canonicalized"

release_manifest_sha256="$(sha256sum -- "$captured_manifest" | awk '{print $1}')"
release_commit="$(jq -er '.commit' "$captured_manifest")"
release_version="$(jq -er '.version' "$captured_manifest")"
runtime_lock_sha256="$(sha256sum -- "$captured_lock" | awk '{print $1}')"
runtime_closure_sha256="$(sha256sum -- "$captured_closure" | awk '{print $1}')"
runtime_wheels_sha256="$(sha256sum -- "$captured_wheels" | awk '{print $1}')"
runtime_python_source_sha256="$(sha256sum -- "$captured_python_source" | awk '{print $1}')"
runtime_installer_sha256="$(sha256sum -- "$captured_installer" | awk '{print $1}')"
runtime_tree_identity_sha256="$(sha256sum -- "$captured_tree_identity" | awk '{print $1}')"
host_capability_identity_sha256="$(sha256sum -- "$captured_host_capability" | awk '{print $1}')"
host_capability_sha256="$(printf '%s' "$host_capabilities" | sha256sum | awk '{print $1}')"
construction_document="$(jq -cnS \
  --arg releaseManifestSha256 "$release_manifest_sha256" \
  --arg requirementsSha256 "$runtime_lock_sha256" \
  --arg closureSha256 "$runtime_closure_sha256" \
  --arg wheelsSha256 "$runtime_wheels_sha256" \
  --arg pythonSourceSha256 "$runtime_python_source_sha256" \
  --arg installerSha256 "$runtime_installer_sha256" \
  --arg treeIdentitySha256 "$runtime_tree_identity_sha256" \
  --arg hostCapabilityIdentitySha256 "$host_capability_identity_sha256" \
  --arg hostCapabilitySha256 "$host_capability_sha256" \
  --arg uvArchiveSha256 "$uv_archive_sha256" \
  --arg uvBinarySha256 "$uv_binary_sha256" \
  --arg uvURL "$uv_archive_url" \
  --arg uvVersion "$uv_version" '
  {
    closureSha256:$closureSha256,
    hostCapabilityIdentitySha256:$hostCapabilityIdentitySha256,
    hostCapabilitySha256:$hostCapabilitySha256,
    installerSha256:$installerSha256,
    pythonSourceSha256:$pythonSourceSha256,
    releaseManifestSha256:$releaseManifestSha256,
    requirementsSha256:$requirementsSha256,
    treeIdentitySha256:$treeIdentitySha256,
    wheelsSha256:$wheelsSha256,
    uv:{archiveSha256:$uvArchiveSha256,binarySha256:$uvBinarySha256,url:$uvURL,version:$uvVersion}
  }
')" || fail "trainer runtime construction identity cannot be encoded"
construction_digest="$(printf '%s' "$construction_document" | sha256sum | awk '{print $1}')"
[[ "$construction_digest" =~ ^[0-9a-f]{64}$ ]] || fail "trainer runtime construction digest is invalid"
runtime_root="$runtime_parent/$runtime_family-$construction_digest"
readonly runtime_root
marker_json="$(jq -cnS \
  --arg schema ascendany.trainer-runtime.provenance.v3 \
  --arg constructionDigest "$construction_digest" \
  --arg releaseCommit "$release_commit" \
  --arg releaseVersion "$release_version" \
  --arg releaseManifestPath "$construction_inputs_name/$captured_manifest_name" \
  --arg releaseManifestSha256 "$release_manifest_sha256" \
  --arg requirementsReleasePath "$runtime_lock_relative" \
  --arg requirementsCapturedPath "$construction_inputs_name/$captured_lock_name" \
  --arg requirementsSha256 "$runtime_lock_sha256" \
  --arg closureReleasePath "$runtime_closure_relative" \
  --arg closureCapturedPath "$construction_inputs_name/$captured_closure_name" \
  --arg closureSha256 "$runtime_closure_sha256" \
  --arg wheelsReleasePath "$runtime_wheels_relative" \
  --arg wheelsCapturedPath "$construction_inputs_name/$captured_wheels_name" \
  --arg wheelsSha256 "$runtime_wheels_sha256" \
  --arg pythonSourceReleasePath "$runtime_python_source_relative" \
  --arg pythonSourceCapturedPath "$construction_inputs_name/$captured_python_source_name" \
  --arg pythonSourceSha256 "$runtime_python_source_sha256" \
  --arg installerReleasePath "$runtime_installer_relative" \
  --arg installerCapturedPath "$construction_inputs_name/$captured_installer_name" \
  --arg installerSha256 "$runtime_installer_sha256" \
  --arg treeIdentityReleasePath "$runtime_tree_identity_relative" \
  --arg treeIdentityCapturedPath "$construction_inputs_name/$captured_tree_identity_name" \
  --arg treeIdentitySha256 "$runtime_tree_identity_sha256" \
  --arg hostCapabilityReleasePath "$host_capability_identity_relative" \
  --arg hostCapabilityCapturedPath "$construction_inputs_name/$captured_host_capability_name" \
  --arg hostCapabilitySha256 "$host_capability_identity_sha256" \
  --arg pythonVersion "$portable_python_version" \
  --arg torchVersion "$torch_version" \
  --arg cudaVersion "$cuda_version" \
  --arg uvVersion "$uv_version" \
  --arg uvURL "$uv_archive_url" \
  --arg uvArchiveSha256 "$uv_archive_sha256" \
  --arg uvBinarySha256 "$uv_binary_sha256" \
  --arg uvCapturedPath "$construction_inputs_name/$captured_uv_name" \
  --argjson pythonTree "$python_tree_identity" \
  --argjson hostCapabilities "$host_capabilities" '
  def input($releasePath;$capturedPath;$sha256):
    {capturedPath:$capturedPath,releasePath:$releasePath,sha256:$sha256};
  {
    constructionDigest:$constructionDigest,
    constructionInputs:{
      closure:input($closureReleasePath;$closureCapturedPath;$closureSha256),
      hostCapabilityIdentity:input($hostCapabilityReleasePath;$hostCapabilityCapturedPath;$hostCapabilitySha256),
      installer:input($installerReleasePath;$installerCapturedPath;$installerSha256),
      pythonSource:input($pythonSourceReleasePath;$pythonSourceCapturedPath;$pythonSourceSha256),
      requirements:input($requirementsReleasePath;$requirementsCapturedPath;$requirementsSha256),
      treeIdentity:input($treeIdentityReleasePath;$treeIdentityCapturedPath;$treeIdentitySha256),
      wheels:input($wheelsReleasePath;$wheelsCapturedPath;$wheelsSha256)
    },
    hostCapabilities:$hostCapabilities,
    pythonTree:$pythonTree,
    runtime:{
      cudaVersion:$cudaVersion,
      pythonVersion:$pythonVersion,
      torchVersion:$torchVersion,
      uv:{archiveSha256:$uvArchiveSha256,binarySha256:$uvBinarySha256,capturedPath:$uvCapturedPath,url:$uvURL,version:$uvVersion}
    },
    schema:$schema,
    sourceRelease:{commit:$releaseCommit,manifestPath:$releaseManifestPath,manifestSha256:$releaseManifestSha256,version:$releaseVersion}
  }
')" || fail "trainer runtime provenance marker cannot be encoded"
printf '%s' "$marker_json" >"$runtime_stage/$runtime_marker_name"
chmod 0644 "$runtime_stage/$runtime_marker_name"
chown root:root "$runtime_stage/$runtime_marker_name"
runtime_provenance_sha256="$(printf '%s' "$marker_json" | sha256sum | awk '{print $1}')"
staged_attestation="$(
  run_runtime_attestation \
    "$runtime_stage" \
    "$release_root/trainers/recommendation" \
    "$construction_digest" \
    "$runtime_provenance_sha256" \
    "$(jq -er '.sha256' <<<"$python_tree_identity")" \
    "$host_capability_sha256"
)" || fail "staged runtime failed the production child attestation"
jq -e \
  --arg construction "$construction_digest" \
  --arg provenance "$runtime_provenance_sha256" \
  --arg tree "$(jq -er '.sha256' <<<"$python_tree_identity")" \
  --arg host "$host_capability_sha256" '
  type == "object" and
  keys == ["hostCapabilitySha256", "runtimeAttestationSha256", "runtimeConstructionSha256", "runtimeProvenanceSha256", "runtimeTreeSha256"] and
  .hostCapabilitySha256 == $host and
  .runtimeConstructionSha256 == $construction and
  .runtimeProvenanceSha256 == $provenance and
  .runtimeTreeSha256 == $tree and
  (.runtimeAttestationSha256 | type == "string" and test("^[0-9a-f]{64}$"))
' <<<"$staged_attestation" >/dev/null || fail "staged runtime attestation result is noncanonical"
staged_attestation="$(jq -cS . <<<"$staged_attestation")"

rm -- "$runtime_stage/.ascendany-stage-owner.json"
stage_mount_targets="$(mktemp /tmp/ascendany-runtime-stage-mounts.XXXXXXXX)"
findmnt -rn -R -o TARGET --target "$runtime_stage" >"$stage_mount_targets" ||
  fail "staged runtime mount set cannot be enumerated"
while IFS= read -r mount_target; do
  if [[ "$mount_target" != "$runtime_stage" && "$mount_target" == "$runtime_stage"/* ]]; then
    fail "staged runtime contains a descendant mount: ${mount_target#"$runtime_stage"/}"
  fi
done <"$stage_mount_targets"
rm -- "$stage_mount_targets"
stage_device="$(stat -Lc '%d' -- "$runtime_stage")"
while IFS= read -r -d '' stage_entry; do
  [[ "$(stat -c '%d' -- "$stage_entry")" == "$stage_device" ]] ||
    fail "staged runtime crosses its publication filesystem: ${stage_entry#"$runtime_stage"/}"
done < <(find -P "$runtime_stage" -mindepth 1 -print0)
if find "$runtime_stage" \( ! -user root -o ! -group root \) -print -quit | grep -q . ||
   find "$runtime_stage" ! -type l -perm /022 -print -quit | grep -q . ||
   find "$runtime_stage" ! -type d ! -type f ! -type l -print -quit | grep -q . ||
   find "$runtime_stage" -type f -links +1 -print -quit | grep -q . ||
   find "$runtime_stage" -type l ! -path "$runtime_stage/python/*" -print -quit | grep -q .; then
  fail "staged runtime contains an unsafe entry outside the closed portable Python tree"
fi
[[ "$(find "$runtime_stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)" == \
   $'.ascendany-construction-inputs\n.ascendany-runtime-provenance.json\npython' ]] ||
  fail "staged runtime top-level entry set differs from its closed contract"
[[ "$(find "$construction_inputs" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)" == \
   $'install-trainer-runtime.sh\nrelease-manifest.json\nruntime-closure-cu130.json\nruntime-python-cu130.json\nruntime-requirements-cu130.lock\nruntime-wheels-cu130.json\ntrainer-host-capability-identity.sh\ntrainer-runtime-tree-identity.sh\nuv' ]] ||
  fail "staged runtime construction input set differs from its closed contract"
[[ "$(stat -Lc '%u:%g:%a:%h' -- "$runtime_stage/$runtime_marker_name")" == "0:0:644:1" &&
   "$(stat -Lc '%u:%g:%a:%h' -- "$trainer_python_stage")" == "0:0:755:1" ]] ||
  fail "staged runtime provenance or interpreter metadata drifted"
[[ "$(stat -Lc '%d:%i' -- "$runtime_parent")" == "$runtime_parent_identity" ]] ||
  fail "trainer runtime parent identity changed during installation"
sync -f "$runtime_stage"
runtime_stage_identity="$(stat -Lc '%d:%i' -- "$runtime_stage")"
readonly runtime_stage_identity
if [[ -e "$runtime_root" || -L "$runtime_root" ]]; then
  [[ -d "$runtime_root" && ! -L "$runtime_root" &&
     "$(stat -Lc '%u:%g:%a' -- "$runtime_root")" == "0:0:755" ]] ||
    fail "existing construction-addressed trainer runtime has unsafe identity"
  cmp --silent -- "$runtime_stage/$runtime_marker_name" "$runtime_root/$runtime_marker_name" ||
    fail "existing construction-addressed trainer runtime has different provenance"
  rm -rf --one-file-system -- "$runtime_stage"
else
  published=1
  mv --no-target-directory --no-clobber -- "$runtime_stage" "$runtime_root"
  [[ ! -e "$runtime_stage" && -d "$runtime_root" && ! -L "$runtime_root" &&
     "$(stat -Lc '%d:%i' -- "$runtime_root")" == "$runtime_stage_identity" ]] ||
    fail "trainer runtime publication did not atomically publish the staged directory"
fi
sync -f "$runtime_parent"

readonly trainer_python="$runtime_root/python/bin/python3.14"
readonly published_tree_identity="$runtime_root/$construction_inputs_name/$captured_tree_identity_name"
readonly published_host_capability="$runtime_root/$construction_inputs_name/$captured_host_capability_name"
published_python_tree_identity="$(
  /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
    "$published_tree_identity" "$runtime_root/python"
)" || fail "published portable Python tree identity cannot be recomputed"
[[ "$published_python_tree_identity" == "$python_tree_identity" ]] ||
  fail "portable Python tree identity changed during publication"
published_host_capabilities="$(
  /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
    "$published_host_capability" "$runtime_root" "$trainer_python"
)" || fail "published trainer host capability identity cannot be recomputed"
[[ "$published_host_capabilities" == "$host_capabilities" ]] ||
  fail "trainer host capability identity changed during publication"

published_attestation="$(
  run_runtime_attestation \
    "$runtime_root" \
    "$release_root/trainers/recommendation" \
    "$construction_digest" \
    "$runtime_provenance_sha256" \
    "$(jq -er '.sha256' <<<"$python_tree_identity")" \
    "$host_capability_sha256"
)" || fail "published runtime failed the production child attestation"
published_attestation="$(jq -cS . <<<"$published_attestation")" ||
  fail "published runtime attestation result cannot be canonicalized"
[[ "$published_attestation" == "$staged_attestation" ]] ||
  fail "trainer runtime attestation changed during publication"

selector_stage="$runtime_parent/.current.stage.$$"
[[ ! -e "$selector_stage" && ! -L "$selector_stage" ]] ||
  fail "trainer runtime selector stage already exists"
ln -s -- "${runtime_root##*/}" "$selector_stage"
[[ -L "$selector_stage" && "$(readlink -- "$selector_stage")" == "${runtime_root##*/}" ]] ||
  fail "trainer runtime selector stage is invalid"
mv --no-target-directory -- "$selector_stage" "$runtime_selector"
selector_switched=1
[[ -L "$runtime_selector" && "$(readlink -- "$runtime_selector")" == "${runtime_root##*/}" &&
   "$(realpath -e -- "$runtime_selector")" == "$runtime_root" ]] ||
  fail "trainer runtime selector did not atomically select the published construction"
sync -f "$runtime_parent"

published=0
selector_switched=0
trap - EXIT
printf '%s\n' "$runtime_root"
