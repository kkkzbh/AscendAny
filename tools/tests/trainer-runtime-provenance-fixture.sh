#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly installer="$repo_root/deploy/v2/scripts/install-trainer-runtime.sh"
readonly validator="$repo_root/deploy/v2/scripts/validate-trainer-host.sh"
readonly production_validator="$repo_root/deploy/v2/scripts/validate-production.sh"
readonly tree_identity_tool="$repo_root/deploy/v2/scripts/trainer-runtime-tree-identity.sh"
readonly python_source="$repo_root/trainers/recommendation/runtime-python-cu130.json"
readonly source_lock="$repo_root/trainers/recommendation/runtime-requirements-cu130.lock"
readonly source_closure="$repo_root/trainers/recommendation/runtime-closure-cu130.json"
readonly source_wheels="$repo_root/trainers/recommendation/runtime-wheels-cu130.json"
readonly go_provenance="$repo_root/backend/internal/trainerprocess/runtime_provenance.go"
readonly python_attestation="$repo_root/trainers/recommendation/src/ascendany_recommendation_trainer/attestation.py"
readonly fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-trainer-runtime-provenance.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

require_literal() {
  local value="$1" literal="$2" label="$3"
  [[ "$value" == *"$literal"* ]] || fail "$label omits $literal"
}

reject_literal() {
  local value="$1" literal="$2" label="$3"
  [[ "$value" != *"$literal"* ]] || fail "$label contains forbidden $literal"
}

# The installer is intentionally sourceable only for these namespace helpers.
# shellcheck source=../../deploy/v2/scripts/install-trainer-runtime.sh
source "$installer"

empty_parent="$fixture_root/empty-runtime-parent"
mkdir "$empty_parent"
trainer_runtime_parent_is_empty "$empty_parent" || fail "empty runtime parent was rejected"
touch "$empty_parent/.stale-stage"
if trainer_runtime_parent_is_empty "$empty_parent"; then
  fail "runtime parent helper accepted an existing entry"
fi
rm "$empty_parent/.stale-stage"
touch "$empty_parent/"$'\n'
if trainer_runtime_parent_is_empty "$empty_parent"; then
  fail "runtime parent helper accepted a newline-only entry"
fi
printf 'PASS fixture runtime-parent-empty-gate\n'

tree_root="$fixture_root/tree"
mkdir -p "$tree_root/lib"
printf 'portable python bytes\n' >"$tree_root/python3.14"
printf 'stdlib bytes\n' >"$tree_root/lib/os.py"
ln -s ../python3.14 "$tree_root/lib/python"
if "$tree_identity_tool" "$tree_root" >/dev/null 2>&1; then
  fail "portable Python tree identity accepted non-root ownership"
fi
tree_identity() {
  unshare --user --map-root-user "$tree_identity_tool" "$tree_root"
}
tree_identity_one="$(tree_identity)"
tree_identity_two="$(tree_identity)"
[[ "$tree_identity_one" == "$tree_identity_two" ]] ||
  fail "portable Python tree identity was nondeterministic"
chmod 0555 "$tree_root"
[[ "$(tree_identity)" != "$tree_identity_one" ]] ||
  fail "portable Python tree identity did not bind root mode"
chmod 0775 "$tree_root"
if tree_identity >/dev/null 2>&1; then
  fail "portable Python tree identity accepted writable root"
fi
chmod 0755 "$tree_root"
ln -s /etc/passwd "$tree_root/external"
if tree_identity >/dev/null 2>&1; then
  fail "portable Python tree identity accepted external symlink"
fi
rm "$tree_root/external"
ln -s missing "$tree_root/dangling"
if tree_identity >/dev/null 2>&1; then
  fail "portable Python tree identity accepted dangling symlink"
fi
rm "$tree_root/dangling"
ln "$tree_root/python3.14" "$tree_root/hardlink"
if tree_identity >/dev/null 2>&1; then
  fail "portable Python tree identity accepted multiply linked file"
fi
rm "$tree_root/hardlink"
printf 'PASS fixture portable-python-tree-identity\n'

jq -e '
  . == {"cpython-3.14.6-linux-x86_64-gnu":{
    arch:{family:"x86_64",variant:null},libc:"gnu",major:3,minor:14,name:"cpython",
    os:"linux",patch:6,prerelease:"",
    sha256:"c172314f4a8ec137a8f605289010c3d19c8b56867d968f0095074cc68efa1d29",
    url:"https://github.com/astral-sh/python-build-standalone/releases/download/20260623/cpython-3.14.6%2B20260623-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz",
    variant:null
  }}
' "$python_source" >/dev/null || fail "portable Python source contract drifted"
printf 'PASS fixture portable-python-source-contract\n'

jq -e '
  type == "object" and keys == ["schema", "wheels"] and
  .schema == "ascendany.trainer-runtime.wheels.v1" and
  (.wheels | type == "array" and length == 30) and
  (all(.wheels[];
    type == "object" and keys == ["filename", "name", "sha256", "url"] and
    (.name | type == "string" and test("^[a-z0-9][a-z0-9-]*$")) and
    (.filename | type == "string" and test("^[0-9A-Za-z_.+]+-[0-9A-Za-z.+]+-[0-9A-Za-z_.+-]+[.]whl$")) and
    (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.url | type == "string" and test("^https://(download[.]pytorch[.]org|download-r2[.]pytorch[.]org|files[.]pythonhosted[.]org|pypi[.]nvidia[.]com)/[^?#]+[.]whl$")))) and
  ([.wheels[].name] == ([.wheels[].name] | sort)) and
  (([.wheels[].name] | length) == ([.wheels[].name] | unique | length)) and
  (([.wheels[].filename] | length) == ([.wheels[].filename] | unique | length))
' "$source_wheels" >/dev/null || fail "reviewed wheel manifest is not one exact 30-wheel set"
jq -e '
  .schema == "ascendany.trainer-runtime.closure.v1" and
  (.distributions | type == "array" and length == 30) and
  (([.distributions[].name] | length) == ([.distributions[].name] | unique | length))
' "$source_closure" >/dev/null || fail "reviewed distribution closure is not exact"
[[ "$(grep -Ec '^[A-Za-z0-9][A-Za-z0-9_.-]*==[^ ;\\]+[[:space:]]*\\$' "$source_lock")" == 30 ]] ||
  fail "reviewed hash lock does not contain exactly 30 distributions"
wheel_names="$(jq -r '.wheels[].name' "$source_wheels" | LC_ALL=C sort)"
closure_names="$(jq -r '.distributions[].name' "$source_closure" | LC_ALL=C sort)"
[[ "$wheel_names" == "$closure_names" ]] || fail "wheel names differ from the reviewed closure"
while IFS=$'\t' read -r name filename digest url; do
  grep -Eq -- "^${name//-/[-_.]}==" "$source_lock" ||
    fail "wheel $name is absent from the reviewed lock"
  grep -Fq -- "--hash=sha256:$digest" "$source_lock" ||
    fail "wheel digest for $name is absent from the reviewed lock"
  url_filename="${url##*/}"
  url_filename="${url_filename//%2B/+}"
  [[ "$url_filename" == "$filename" ]] || fail "wheel URL filename differs for $name"
done < <(jq -r '.wheels[] | [.name, .filename, .sha256, .url] | @tsv' "$source_wheels")
printf 'PASS fixture offline-wheel-closure\n'

portable_body="$(declare -f run_portable_python_readonly)"
for literal in '--unshare-all' '--die-with-parent' '--cap-drop ALL' '--ro-bind /lib /lib' \
  '--ro-bind /lib64 /lib64' '--clearenv' 'HOME /nonexistent' 'PYTHONHASHSEED 0'; do
  require_literal "$portable_body" "$literal" "portable Python namespace"
done
for literal in '--ro-bind /usr' '--bind /usr' '--share-net' '/run/credentials' '/etc/ascendany'; do
  reject_literal "$portable_body" "$literal" "portable Python namespace"
done

attestation_body="$(declare -f run_runtime_attestation)"
for literal in '--unshare-all' '--die-with-parent' '--cap-drop ALL' '--ro-bind /lib /lib' \
  '--ro-bind /lib64 /lib64' '--ro-bind /sys /sys' \
  '--dev-bind /dev/nvidia-uvm /dev/nvidia-uvm' '--dev-bind /dev/nvidia0 /dev/nvidia0' \
  '--dev-bind /dev/nvidiactl /dev/nvidiactl' '--ro-bind "$package_root" /trainer/recommendation' \
  '--clearenv' 'ASCENDANY_TRAINER_EXPECTED_HOST_CAPABILITY_SHA256' \
  'ASCENDANY_TRAINER_EXPECTED_RUNTIME_CONSTRUCTION_SHA256' \
  'ASCENDANY_TRAINER_EXPECTED_RUNTIME_PROVENANCE_SHA256' \
  'ASCENDANY_TRAINER_EXPECTED_RUNTIME_TREE_SHA256'; do
  require_literal "$attestation_body" "$literal" "runtime attestation namespace"
done
for literal in '--ro-bind /usr' '--bind /usr' '--share-net' '/run/credentials' '/etc/ascendany'; do
  reject_literal "$attestation_body" "$literal" "runtime attestation namespace"
done
printf 'PASS fixture usr-free-networkless-runtime-namespaces\n'

for source in "$installer" "$go_provenance" "$python_attestation"; do
  grep -Fq 'ascendany.trainer-runtime.provenance.v3' "$source" ||
    fail "$(basename "$source") does not require provenance.v3"
done
grep -Fq 'length == 77' "$installer" || fail "runtime installer does not bind the 77-path release"
grep -Fq 'length == 77' "$validator" || fail "trainer validator does not bind the 77-path release"
grep -Fq 'length == 77' "$production_validator" || fail "production validator does not bind the 77-path release"
for source in "$installer" "$go_provenance" "$python_attestation"; do
  grep -Fq '30ccbf0a66dc8727a02b0e245c583ee970bdafecf3a443c1686e1b30ec4939e8' "$source" ||
    fail "$(basename "$source") omits the official uv archive digest"
  grep -Fq '0650696de7f403348e9dd617e1f65dc32147c106c40129138017efd8f0f01cc8' "$source" ||
    fail "$(basename "$source") omits the official uv binary digest"
done
for key in runtimeConstructionSha256 runtimeProvenanceSha256 runtimeTreeSha256 \
  hostCapabilitySha256 runtimeAttestationSha256; do
  grep -Fq "$key" "$go_provenance" || fail "Go runtime provenance omits $key"
  grep -Fq "$key" "$python_attestation" || fail "Python runtime attestation omits $key"
  grep -Fq "$key" "$validator" || fail "trainer acceptance validation omits $key"
  grep -Fq "$key" "$production_validator" || fail "production receipt validation omits $key"
done
printf 'PASS fixture v3-five-digest-provenance-closure\n'

printf 'trainer runtime provenance fixtures passed\n'
