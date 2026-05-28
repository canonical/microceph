#!/bin/bash
# Integration tests for the MicroCeph orchestrator module.
#
# Can be run standalone against a pre-configured cluster:
#   ./test-orch.sh [ceph-command-prefix]
#
# Or sourced from actionutils.sh for CI.
#
# Expects a bootstrapped MicroCeph cluster with the orchestrator enabled.
# The first argument is the prefix for ceph commands (default: "microceph.ceph").

set -uo pipefail

CEPH="${1:-microceph.ceph}"
PASS=0
FAIL=0
ERRORS=()

run_ceph() { sudo "$CEPH" "$@" 2>&1; }

# --- Test helpers ---

assert_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -qE "$needle"; then
        echo "  PASS $desc"
        PASS=$((PASS + 1))
    else
        echo "  FAIL $desc (expected to contain: '$needle')"
        echo "  ----- actual output -----"
        echo "$haystack" | sed 's/^/  | /'
        echo "  -------------------------"
        FAIL=$((FAIL + 1))
        ERRORS+=("$desc")
    fi
}

assert_not_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -qE "$needle"; then
        echo "  FAIL $desc (should NOT contain: '$needle')"
        echo "  ----- actual output -----"
        echo "$haystack" | sed 's/^/  | /'
        echo "  -------------------------"
        FAIL=$((FAIL + 1))
        ERRORS+=("$desc")
    else
        echo "  PASS $desc"
        PASS=$((PASS + 1))
    fi
}

assert_exit_ok() {
    local desc="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        echo "  PASS $desc"
        PASS=$((PASS + 1))
    else
        echo "  FAIL $desc (non-zero exit)"
        FAIL=$((FAIL + 1))
        ERRORS+=("$desc")
    fi
}

# ===================================================================
echo "=== 1. Orchestrator status ==="
# ===================================================================

output=$(run_ceph orch status)
assert_contains "backend is microceph" "microceph" "$output"
assert_contains "available" "Available: Yes" "$output"

# ===================================================================
echo "=== 2. Host listing ==="
# ===================================================================

output=$(run_ceph orch host ls)
assert_contains "hosts listed" "hosts in cluster" "$output"

# ===================================================================
echo "=== 3. Service listing (baseline) ==="
# ===================================================================

output=$(run_ceph orch ls)
assert_contains "mon service" "mon" "$output"
assert_contains "mgr service" "mgr" "$output"

# ===================================================================
echo "=== 4. Daemon listing ==="
# ===================================================================

output=$(run_ceph orch ps)
assert_contains "has daemons" "mon" "$output"

# Get first hostname for filter tests
first_host=$(run_ceph orch host ls | awk 'NR==2{print $1}')
if [ -n "$first_host" ]; then
    output=$(run_ceph orch ps --hostname "$first_host")
    assert_contains "ps filtered to host" "$first_host" "$output"

    output=$(run_ceph orch ps --daemon-type mon)
    assert_contains "ps filtered to mon type" "mon" "$output"
fi

# ===================================================================
echo "=== 5. Device listing ==="
# ===================================================================

output=$(run_ceph orch device ls)
# Just verify it doesn't error out
assert_exit_ok "device ls succeeds" run_ceph orch device ls

# ===================================================================
echo "=== 6. Apply RGW ==="
# ===================================================================

# Clean up RGW if it was already enabled by prior test steps.
# `orch rm rgw` is now cluster-wide (fans out to every host running rgw).
run_ceph orch rm rgw >/dev/null 2>&1 || true
sleep 3

# Use first host for placement
placement="${first_host:-$(hostname)}"
output=$(run_ceph orch apply rgw default --placement="$placement")
assert_contains "rgw applied" "enabled|already active" "$output"
sleep 5

output=$(run_ceph orch ls)
assert_contains "rgw in service list" "rgw" "$output"

output=$(run_ceph orch ps --daemon-type rgw)
assert_contains "rgw daemon visible" "rgw" "$output"

# Per-host targeting check: the RGW daemon must end up on the
# requested host (first_host), not on whichever node served the unix
# socket. This is what the ?target= proxyTarget mechanism is for.
output=$(run_ceph orch ps --hostname "$placement" --daemon-type rgw)
assert_contains "rgw daemon on requested host $placement" "rgw" "$output"

# ===================================================================
echo "=== 6b. Apply RGW on a second host (per-host targeting) ==="
# ===================================================================

# Find a host that is NOT the first one to verify cross-node enablement
# from the local socket. Only run this leg when the cluster has more
# than one host (single-node deployments skip).
second_host=$(run_ceph orch host ls | awk 'NR>1 && NF>0 && $1 != "'"$first_host"'" {print $1; exit}')
if [ -n "$second_host" ] && [ "$second_host" != "$first_host" ]; then
    output=$(run_ceph orch apply rgw default --placement="$first_host,$second_host")
    assert_contains "rgw applied to two hosts" "enabled|already active" "$output"
    sleep 5

    # Verify both hosts now run rgw. The local node (running the orch
    # client) is the unix-socket endpoint; the second_host enablement
    # exercises the unix-socket -> proxyTarget -> remote mTLS path.
    output=$(run_ceph orch ps --hostname "$second_host" --daemon-type rgw)
    assert_contains "rgw daemon on $second_host (cross-node target)" "rgw" "$output"
else
    echo "  SKIP per-host targeting RGW test (only one host in cluster)"
fi

# ===================================================================
echo "=== 7. Apply NFS ==="
# ===================================================================

output=$(run_ceph orch apply nfs testcluster --placement="$placement")
assert_contains "nfs applied" "enabled|already active" "$output"
sleep 5

output=$(run_ceph orch ls)
# NFS may not appear in service list if the service failed to start
# on the backend (e.g. missing kernel modules on CI runners).
if echo "$output" | grep -q "nfs.testcluster"; then
    echo "  PASS nfs in service list"
    PASS=$((PASS + 1))
    NFS_RUNNING=true
else
    echo "  WARN nfs not in service list (service may have failed to start; skipping daemon check)"
    NFS_RUNNING=false
fi

if [ "$NFS_RUNNING" = true ]; then
    output=$(run_ceph orch ps --daemon-type nfs)
    assert_contains "nfs daemon visible" "nfs" "$output"
fi

# ===================================================================
echo "=== 8. Restart service ==="
# ===================================================================

output=$(run_ceph orch restart mon)
assert_contains "mon restarted" "Restarted mon on" "$output"

# Per-host fan-out: in a multi-host cluster the restart output should
# enumerate each host running mon, not a single "Restarted mon".
if [ -n "${second_host:-}" ] && [ "$second_host" != "$first_host" ]; then
    assert_contains "mon restart enumerates first host" "$first_host" "$output"
fi

# Dotted-name guard: orch restart rgw.realm1 must fail with a clear
# "does not support a service_id" error rather than silently restart
# every rgw daemon.
output=$(run_ceph orch restart rgw.realm1 2>&1 || true)
assert_contains "dotted restart rejected" "does not support a service_id" "$output"

# ===================================================================
echo "=== 9. Remove RGW ==="
# ===================================================================

output=$(run_ceph orch rm rgw)
assert_contains "rgw removed" "removed from" "$output"
sleep 3

# remove_service fans out across every host running the service, so
# RGW must be gone from BOTH first_host and second_host (when present).
local_host="${first_host:-$(hostname)}"
output=$(run_ceph orch ps --hostname "$local_host" --daemon-type rgw)
assert_not_contains "rgw gone from first host" "rgw" "$output"

if [ -n "${second_host:-}" ] && [ "$second_host" != "$first_host" ]; then
    output=$(run_ceph orch ps --hostname "$second_host" --daemon-type rgw)
    assert_not_contains "rgw gone from second host (cluster-wide remove)" "rgw" "$output"
fi

# ===================================================================
echo "=== 10. Remove NFS ==="
# ===================================================================

output=$(run_ceph orch rm nfs.testcluster)
# NFS removal may fail if the service never fully started
if echo "$output" | grep -q "Removed"; then
    echo "  PASS nfs removed"
    PASS=$((PASS + 1))
else
    echo "  WARN nfs removal returned: $output (service may not have been running)"
fi
sleep 3

output=$(run_ceph orch ls)
assert_not_contains "nfs gone from list" "nfs" "$output"

# ===================================================================
echo ""
echo "==========================================="
echo " Results: $PASS passed, $FAIL failed"
echo "==========================================="

if [ ${#ERRORS[@]} -gt 0 ]; then
    echo ""
    echo "Failed tests:"
    for e in "${ERRORS[@]}"; do
        echo "  - $e"
    done
    exit 1
fi

exit 0
