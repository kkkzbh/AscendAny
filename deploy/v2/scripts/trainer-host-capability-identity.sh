#!/usr/bin/bash -p
set +x
set -Eeuo pipefail

umask 077
export LC_ALL=C
export PATH=/usr/bin:/bin
readonly LC_ALL PATH

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

require_root_owned_ancestry() {
  local path="$1" current metadata owner group mode
  current="$(dirname -- "$path")"
  while :; do
    [[ -d "$current" && ! -L "$current" ]] ||
      fail "trusted host path ancestry is missing or linked: $current"
    metadata="$(stat -Lc '%u:%g:%a' -- "$current")"
    IFS=: read -r owner group mode <<<"$metadata"
    [[ "$owner" == 0 && "$group" == 0 && "$((8#$mode & 8#22))" == 0 ]] ||
      fail "trusted host path ancestry is non-root or writable outside root: $current"
    [[ "$current" == / ]] && break
    current="$(dirname -- "$current")"
  done
}

[[ "$#" == 2 ]] || fail "trainer host capability identity requires runtime root and Python executable"
readonly runtime_root="$1"
readonly trainer_python="$2"
readonly sandbox=/usr/bin/bwrap
readonly sandbox_runtime_root=/opt/ascendany-trainer-runtime/current
readonly driver_version_path=/sys/module/nvidia/version
[[ "$runtime_root" == /* && "$runtime_root" == "$(realpath -m -- "$runtime_root")" &&
   -d "$runtime_root" && ! -L "$runtime_root" && "$runtime_root" == "$(realpath -e -- "$runtime_root")" ]] ||
  fail "trainer runtime root must be one canonical directory"
[[ "$trainer_python" == "$runtime_root/python/bin/python3.14" &&
   -f "$trainer_python" && ! -L "$trainer_python" && -x "$trainer_python" ]] ||
  fail "trainer Python executable differs from the portable runtime contract"
[[ -f "$driver_version_path" && ! -L "$driver_version_path" ]] ||
  fail "NVIDIA kernel driver version file is unavailable"
[[ -f "$sandbox" && ! -L "$sandbox" && -x "$sandbox" &&
   "$sandbox" == "$(realpath -e -- "$sandbox")" &&
   "$(stat -Lc '%u:%g' -- "$sandbox")" == "0:0" &&
   "$((8#$(stat -Lc '%a' -- "$sandbox") & 8#22))" == 0 ]] ||
  fail "bubblewrap executable is linked, non-root, or writable outside root"
require_root_owned_ancestry "$sandbox"

readonly python_probe='import hashlib,json,os,pathlib,re,stat,sys,torch
runtime=pathlib.Path(sys.argv[1]).resolve(strict=True)
tensor=torch.zeros(1,device="cuda")
torch.cuda.synchronize()
paths=set()
for raw in pathlib.Path("/proc/self/maps").read_text(encoding="utf-8").splitlines():
 parts=raw.split(maxsplit=5)
 if len(parts)!=6 or not parts[5].startswith("/"):
  continue
 if parts[5]=="/dev/zero (deleted)":
  continue
 if parts[5].endswith(" (deleted)"):
  raise SystemExit("mapped host regular file was deleted")
 path=pathlib.Path(parts[5]).resolve(strict=True)
 if path.is_relative_to(runtime):
  continue
 mode=path.stat().st_mode
 if stat.S_ISREG(mode):
  paths.add(path)
items=[]
for path in sorted(paths,key=lambda value:os.fsencode(value)):
 before=path.stat()
 if before.st_nlink<1 or before.st_mode&0o022:
  raise SystemExit("mapped host file has unsafe links or mode")
 digest=hashlib.sha256(path.read_bytes()).hexdigest()
 after=path.stat()
 identity=lambda value:(value.st_dev,value.st_ino,value.st_mode,value.st_nlink,value.st_uid,value.st_gid,value.st_size,value.st_mtime_ns,value.st_ctime_ns)
 if identity(before)!=identity(after):
  raise SystemExit("mapped host file changed during capability calculation")
 items.append({"resolvedPath":str(path),"sha256":digest,"size":before.st_size})
mounts=[]
for line in pathlib.Path("/proc/self/mountinfo").read_text(encoding="utf-8").splitlines():
 fields=line.split()
 if len(fields)<10 or "-" not in fields:
  raise SystemExit("mountinfo is malformed")
 target=re.sub(r"\\([0-7]{3})",lambda match:chr(int(match.group(1),8)),fields[4])
 mounts.append(os.path.normpath(target))
if len(mounts)!=len(set(mounts)):
 raise SystemExit("sandbox mount targets are repeated")
mounts=sorted(mounts,key=os.fsencode)
print(json.dumps({"mappedHostFiles":items,"sandboxMountTargets":mounts},sort_keys=True,separators=(",",":")))'

probe_result="$(
  "$sandbox" \
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
    --ro-bind /lib /lib \
    --ro-bind /lib64 /lib64 \
    --ro-bind "$runtime_root" "$sandbox_runtime_root" \
    --ro-bind /sys /sys \
    --dev-bind /dev/nvidia-uvm /dev/nvidia-uvm \
    --dev-bind /dev/nvidia0 /dev/nvidia0 \
    --dev-bind /dev/nvidiactl /dev/nvidiactl \
    --clearenv \
    --setenv HOME /nonexistent \
    --setenv LANG C.UTF-8 \
    --setenv LC_ALL C.UTF-8 \
    --setenv CUBLAS_WORKSPACE_CONFIG :4096:8 \
    --setenv CUDA_VISIBLE_DEVICES 0 \
    --setenv MKL_NUM_THREADS 8 \
    --setenv OMP_NUM_THREADS 8 \
    --setenv OPENBLAS_NUM_THREADS 8 \
    --setenv PYTHONHASHSEED 0 \
    --setenv TZ UTC \
    -- \
    "$sandbox_runtime_root/python/bin/python3.14" -B -s -P -c "$python_probe" "$sandbox_runtime_root"
)" || fail "trainer CUDA host capability set cannot be enumerated"
mapped_host_files="$(jq -c '.mappedHostFiles' <<<"$probe_result")"
sandbox_mount_targets="$(jq -c '.sandboxMountTargets' <<<"$probe_result")"
jq -e '
  type == "array" and length > 0 and
  (all(.[];
    type == "object" and keys == ["resolvedPath", "sha256", "size"] and
    (.resolvedPath | type == "string") and
    ((.resolvedPath | startswith("/lib/")) or (.resolvedPath | startswith("/lib64/"))) and
    (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.size | type == "number" and floor == . and . >= 0))) and
  ([.[].resolvedPath] == ([.[].resolvedPath] | sort)) and
  (([.[].resolvedPath] | length) == ([.[].resolvedPath] | unique | length))
' <<<"$mapped_host_files" >/dev/null || fail "trainer CUDA host capability set is noncanonical"
jq -e '
  type == "array" and length > 0 and
  all(.[]; type == "string" and startswith("/")) and
  . == (sort | unique) and
  any(.[]; . == "/lib") and any(.[]; . == "/lib64") and
  any(.[]; . == "/opt/ascendany-trainer-runtime/current") and
  any(.[]; . == "/sys") and all(.[]; startswith("/usr") | not)
' <<<"$sandbox_mount_targets" >/dev/null || fail "trainer sandbox mount target set is noncanonical"

while IFS= read -r -d '' mapped_path; do
  resolved_host_path="$(realpath -e -- "$mapped_path")"
  [[ -f "$resolved_host_path" && ! -L "$resolved_host_path" &&
     "$resolved_host_path" == "$(realpath -e -- "$resolved_host_path")" ]] ||
    fail "mapped trainer host file is not one canonical regular file: $mapped_path"
  require_root_owned_ancestry "$resolved_host_path"
  expected_sha256="$(jq -er --arg path "$mapped_path" '
    [.[] | select(.resolvedPath == $path)] as $matches |
    if ($matches | length) == 1 then $matches[0].sha256 else error("missing mapped path") end
  ' <<<"$mapped_host_files")"
  expected_size="$(jq -er --arg path "$mapped_path" '
    [.[] | select(.resolvedPath == $path)] as $matches |
    if ($matches | length) == 1 then $matches[0].size else error("missing mapped path") end
  ' <<<"$mapped_host_files")"
  host_metadata_before="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$resolved_host_path")"
  IFS=: read -r _ _ host_owner host_group host_mode host_links host_size _ _ <<<"$host_metadata_before"
  [[ "$host_owner" == 0 && "$host_group" == 0 && "$host_links" -ge 1 &&
     "$host_size" == "$expected_size" && "$((8#$host_mode & 8#22))" == 0 ]] ||
    fail "mapped trainer host file has unsafe metadata: $mapped_path"
  [[ "$(sha256sum -- "$resolved_host_path" | awk '{print $1}')" == "$expected_sha256" ]] ||
    fail "mapped trainer host file content differs across the namespace boundary: $mapped_path"
  host_metadata_after="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$resolved_host_path")"
  [[ "$host_metadata_before" == "$host_metadata_after" ]] ||
    fail "mapped trainer host file changed during capability calculation: $mapped_path"
done < <(jq -j '.[] | .resolvedPath, "\u0000"' <<<"$mapped_host_files")

driver_version="$(<"$driver_version_path")"
[[ "$driver_version" =~ ^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)([.](0|[1-9][0-9]*))?$ ]] ||
  fail "NVIDIA driver version is noncanonical"
if ! jq -e --arg driverVersion "$driver_version" '
    any(.[]; .resolvedPath | endswith("/ld-linux-x86-64.so.2")) and
    any(.[]; .resolvedPath | endswith("/libc.so.6")) and
    any(.[]; .resolvedPath | endswith("/libcuda.so." + $driverVersion))
  ' <<<"$mapped_host_files" >/dev/null; then
  fail "trainer CUDA probe did not map the exact loader, glibc, and NVIDIA driver"
fi
driver_metadata_before="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$driver_version_path")"
IFS=: read -r _ _ driver_owner driver_group driver_mode driver_links driver_size _ _ <<<"$driver_metadata_before"
[[ "$driver_owner" == 0 && "$driver_group" == 0 && "$driver_links" == 1 &&
   "$((8#$driver_mode & 8#22))" == 0 ]] || fail "NVIDIA kernel driver version file has unsafe metadata"
driver_sha256="$(sha256sum -- "$driver_version_path" | awk '{print $1}')"
driver_metadata_after="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$driver_version_path")"
[[ "$driver_metadata_before" == "$driver_metadata_after" ]] ||
  fail "NVIDIA kernel driver version changed during capability calculation"

jq -cn \
  --arg driverVersion "$driver_version" \
  --arg kernelVersionPath "$driver_version_path" \
  --arg kernelVersionSha256 "$driver_sha256" \
  --argjson kernelVersionSize "$driver_size" \
  --argjson mappedHostFiles "$mapped_host_files" \
  --argjson sandboxMountTargets "$sandbox_mount_targets" '
  {
    driver:{
      kernelVersionFile:{resolvedPath:$kernelVersionPath,sha256:$kernelVersionSha256,size:$kernelVersionSize},
      version:$driverVersion
    },
    mappedHostFiles:$mappedHostFiles,
    sandboxMountTargets:$sandboxMountTargets,
    schema:"ascendany.trainer-host-capabilities.v2"
  }
'
