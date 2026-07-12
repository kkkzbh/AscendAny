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

[[ "$#" == 1 ]] || fail "portable Python tree identity requires exactly one root"
readonly root="$1"
[[ "$root" == /* && "$root" == "$(realpath -m -- "$root")" &&
   -d "$root" && ! -L "$root" && "$root" == "$(realpath -e -- "$root")" ]] ||
  fail "portable Python tree root must be one canonical directory"
root_metadata_before="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$root")"
IFS=: read -r root_device _ root_owner root_group root_mode root_links _ _ _ <<<"$root_metadata_before"
[[ "$root_owner" == 0 && "$root_group" == 0 && "$root_mode" =~ ^[0-7]{3}$ &&
   "$root_links" -ge 1 && "$((8#$root_mode & 8#22))" == 0 ]] ||
  fail "portable Python tree root has unsafe ownership or mode"

records="$(mktemp /tmp/ascendany-python-tree-identity.XXXXXXXX)"
entries_before="$(mktemp /tmp/ascendany-python-tree-entries-before.XXXXXXXX)"
entries_after="$(mktemp /tmp/ascendany-python-tree-entries-after.XXXXXXXX)"
mount_targets="$(mktemp /tmp/ascendany-python-tree-mounts.XXXXXXXX)"
readonly records entries_before entries_after mount_targets
trap 'rm -f -- "$records" "$entries_before" "$entries_after" "$mount_targets"' EXIT

if ! findmnt -rn -R -o TARGET --target "$root" >"$mount_targets" || [[ ! -s "$mount_targets" ]]; then
  fail "portable Python tree mount set cannot be enumerated"
fi
while IFS= read -r mount_target; do
  [[ -n "$mount_target" ]] || fail "portable Python tree mount set cannot be enumerated"
  if [[ "$mount_target" != "$root" && "$mount_target" == "$root"/* ]]; then
    fail "portable Python tree contains a descendant mount: ${mount_target#"$root"/}"
  fi
done <"$mount_targets"

if ! find -P "$root" -mindepth 1 -print0 | sort -z >"$entries_before"; then
  fail "portable Python tree entry set cannot be enumerated"
fi

printf 'ascendany.portable-python-tree.v1\0' >"$records"
printf 'D\0.\0%s\0' "$root_mode" >>"$records"
directories=1
files=0
symlinks=0
while IFS= read -r -d '' entry; do
  relative="${entry#"$root"/}"
  [[ -n "$relative" && "$relative" != "$entry" &&
     "$relative" != *$'\n'* && "$relative" != *$'\r'* ]] ||
    fail "portable Python tree contains a noncanonical path"
  if [[ -L "$entry" ]]; then
    metadata_before="$(stat -c '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$entry")"
    IFS=: read -r device _ owner group mode links link_size _ _ <<<"$metadata_before"
    [[ "$device" == "$root_device" && "$owner" == 0 && "$group" == 0 && "$mode" == 777 && "$links" == 1 ]] ||
      fail "portable Python tree contains an unsafe symbolic link: $relative"
    target="$(readlink -- "$entry")"
    [[ -n "$target" && "$target" != /* &&
       "$target" != *$'\n'* && "$target" != *$'\r'* && "${#target}" == "$link_size" ]] ||
      fail "portable Python tree contains a noncanonical symbolic-link target: $relative"
    resolved="$(realpath -e -- "$entry" 2>/dev/null || true)"
    [[ -n "$resolved" && "$resolved" == "$root"/* ]] ||
      fail "portable Python tree contains an external or dangling symbolic link: $relative"
    metadata_after="$(stat -c '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$entry")"
    [[ "$metadata_before" == "$metadata_after" ]] ||
      fail "portable Python symbolic link changed during identity calculation: $relative"
    printf 'L\0%s\0%s\0%s\0' "$relative" "$mode" "$target" >>"$records"
    symlinks=$((symlinks + 1))
  elif [[ -d "$entry" ]]; then
    metadata_before="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$entry")"
    IFS=: read -r device _ owner group mode links _ _ _ <<<"$metadata_before"
    [[ "$device" == "$root_device" && "$owner" == 0 && "$group" == 0 && "$mode" =~ ^[0-7]{3}$ &&
       "$links" -ge 1 && "$((8#$mode & 8#22))" == 0 ]] ||
      fail "portable Python tree contains an unsafe directory: $relative"
    metadata_after="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$entry")"
    [[ "$metadata_before" == "$metadata_after" ]] ||
      fail "portable Python directory changed during identity calculation: $relative"
    printf 'D\0%s\0%s\0' "$relative" "$mode" >>"$records"
    directories=$((directories + 1))
  elif [[ -f "$entry" ]]; then
    metadata_before="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$entry")"
    IFS=: read -r device _ owner group mode links size _ _ <<<"$metadata_before"
    [[ "$device" == "$root_device" && "$owner" == 0 && "$group" == 0 && "$mode" =~ ^[0-7]{3}$ &&
       "$links" == 1 && "$((8#$mode & 8#22))" == 0 ]] ||
      fail "portable Python tree contains an unsafe regular file: $relative"
    content_sha256="$(sha256sum -- "$entry" | awk '{print $1}')"
    metadata_after="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$entry")"
    [[ "$metadata_before" == "$metadata_after" ]] ||
      fail "portable Python file changed during identity calculation: $relative"
    printf 'F\0%s\0%s\0%s\0%s\0' \
      "$relative" "$mode" "$size" "$content_sha256" >>"$records"
    files=$((files + 1))
  else
    fail "portable Python tree contains a special filesystem node: $relative"
  fi
done <"$entries_before"

if ! find -P "$root" -mindepth 1 -print0 | sort -z >"$entries_after" ||
   ! cmp --silent -- "$entries_before" "$entries_after"; then
  fail "portable Python tree entry set changed during identity calculation"
fi
root_metadata_after="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' -- "$root")"
[[ "$root_metadata_before" == "$root_metadata_after" ]] ||
  fail "portable Python tree root changed during identity calculation"

(( directories > 0 && files > 0 && symlinks > 0 )) ||
  fail "portable Python tree identity cannot be empty"
tree_sha256="$(sha256sum -- "$records" | awk '{print $1}')"
printf '{"algorithm":"ascendany.portable-python-tree.v1","directories":%d,"files":%d,"sha256":"%s","symlinks":%d}\n' \
  "$directories" "$files" "$tree_sha256" "$symlinks"
