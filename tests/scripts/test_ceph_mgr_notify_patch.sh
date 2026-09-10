#!/usr/bin/env bash
# Verify the staged Ceph manager workaround against the actual staging tree.
set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "usage: $0 <staging-dir>" >&2
    exit 2
fi

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
patch_file="$repo_root/patches/0002-mgr-default-notify-types.patch"
staging_dir=$1
mgr_module="$staging_dir/share/ceph/mgr/mgr_module.py"
test -f "$mgr_module"
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# Copy the actual unpatched manager module; a handwritten fixture cannot catch
# hunk drift against the Ceph package Snapcraft staged.
mkdir -p "$tmpdir/share/ceph/mgr"
cp "$mgr_module" "$tmpdir/share/ceph/mgr/mgr_module.py"

patch --fuzz=0 -d "$tmpdir" -p1 --dry-run < "$patch_file"
patch --fuzz=0 -d "$tmpdir" -p1 < "$patch_file"
grep -Fqx '    NOTIFY_TYPES: List[NotifyType] = []' "$tmpdir/share/ceph/mgr/mgr_module.py"
