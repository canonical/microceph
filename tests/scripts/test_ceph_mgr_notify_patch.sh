#!/usr/bin/env bash
# Verify the staged Ceph manager workaround applies to the packaged module.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
patch_file="$repo_root/patches/0002-mgr-default-notify-types.patch"
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

mkdir -p "$tmpdir/share/ceph/mgr"
cat > "$tmpdir/share/ceph/mgr/mgr_module.py" <<'EOF'
class MgrModule(ceph_module.BaseMgrModule, MgrModuleLoggingMixin):
    MGR_POOL_NAME = ".mgr"
EOF

patch -d "$tmpdir" -p1 --dry-run < "$patch_file"
patch -d "$tmpdir" -p1 < "$patch_file"
grep -Fqx '    NOTIFY_TYPES: List[NotifyType] = []' "$tmpdir/share/ceph/mgr/mgr_module.py"
