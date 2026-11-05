#!/usr/bin/env bash
#
# cephx-aes256k-upgrade.sh
#
# Self-contained functional test of the CVE-2025-30156 cephx rework on MicroCeph:
# bring up a 4-node cluster on the OLD channel, upgrade it to a 19.2.6 snap under
# test, then rotate every cephx key to the new aes256k type and cut the cluster
# over to aes256k-only. Each step asserts its outcome and the run ends with a
# PASS/FAIL summary. This is the executable form of the "Upgrading MicroCeph to
# 19.2.6 and rotating CephX keys" how-to.
#
# It reuses the cluster helpers in tests/scripts/actionutils.sh.
#
# Environment:
#   OLD_CHANNEL      channel to start on            (default squid/stable = 19.2.3)
#   MICROCEPH_SNAP   the 19.2.6 snap under test, one of:
#                      a bare revision, e.g. 1939      -> snap refresh --revision
#                      a channel, e.g. squid/edge      -> snap refresh --channel
#                      a path to a local .snap file    -> pushed + snap install --dangerous
#                    (default: 1939)
#                    NOTE: installing by revision or channel needs that build to be
#                    reachable by the node (released, or the node logged in to the
#                    store). For an unreleased build in fresh containers, pass a
#                    local .snap path instead.
#   NET              lxd network / cluster tag         (default cephxtest)
#
# Usage:
#   sudo -E tests/scripts/cephx-aes256k-upgrade.sh          run the test
#   sudo -E tests/scripts/cephx-aes256k-upgrade.sh --clean  tear the cluster down
#
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# actionutils.sh is a command dispatcher (not a library): call it as a subprocess,
# and copy it to $HOME so it is mounted into the containers for headexec.
AU="${HOME}/actionutils.sh"
cp "${HERE}/actionutils.sh" "$AU"

OLD_CHANNEL="${OLD_CHANNEL:-squid/stable}"
MICROCEPH_SNAP="${MICROCEPH_SNAP:-1939}"
NET="${NET:-public}"
NODES=(node-wrk0 node-wrk1 node-wrk2 node-wrk3)
HEAD=node-wrk0

PASS=(); FAIL=()
ok()  { echo "PASS: $1"; PASS+=("$1"); }
bad() { echo "FAIL: $1"; FAIL+=("$1"); }
banner() { printf '\n============================================================\n== %s\n============================================================\n' "$*"; }
# run a ceph admin command on the head node
ceph_h() { lxc exec "$HEAD" -- sh -c "microceph.ceph $*"; }

if [ "${1:-}" = "--clean" ]; then
    for c in "${NODES[@]}"; do lxc delete -f "$c" 2>/dev/null || true; done
    lxc network delete "$NET" 2>/dev/null || true
    echo "cleaned"; exit 0
fi

# install the snap-under-test on one node, honouring MICROCEPH_SNAP's form
install_snap_under_test() {
    local node="$1" src="${MICROCEPH_SNAP}"
    if [ -f "$src" ] || case "$src" in */*|*.snap) true;; *) false;; esac; then
        lxc file push "$src" "${node}/root/microceph-under-test.snap"
        lxc exec "$node" -- sh -c "snap install --dangerous /root/microceph-under-test.snap"
        for i in block-devices hardware-observe mount-observe load-rbd microceph-support network-bind dm-crypt; do
            lxc exec "$node" -- sh -c "snap connect microceph:$i" || true
        done
        lxc exec "$node" -- sh -c "snap restart microceph.daemon"
    elif printf '%s' "$src" | grep -qE '^[0-9]+$'; then
        lxc exec "$node" -- sh -c "snap refresh microceph --revision ${src} --amend"
    else
        lxc exec "$node" -- sh -c "snap refresh microceph --channel ${src} --amend"
    fi
}

# wait until at least $1 OSDs are up
wait_osds() {
    local want="$1" up
    for _ in $(seq 1 40); do
        up=$(ceph_h "osd stat 2>/dev/null" | grep -oE '[0-9]+ up' | grep -oE '[0-9]+' | head -1)
        [ "${up:-0}" -ge "$want" ] && return 0
        sleep 6
    done
    ceph_h "osd stat"; return 1
}

# wait until no daemon still runs the pre-upgrade release
wait_versions_uniform() {
    for _ in $(seq 1 40); do
        ceph_h "versions -f json 2>/dev/null" | grep -q '19.2.3' || return 0
        sleep 6
    done
    return 1
}

##############################################################################
banner "Setup: 4-node MicroCeph on ${OLD_CHANNEL}"
"$AU" setup_lxd
"$AU" create_containers "$NET"
"$AU" install_store "$OLD_CHANNEL"
"$AU" bootstrap_head "$NET"
"$AU" cluster_nodes "$NET"
for c in node-wrk0 node-wrk1 node-wrk2; do "$AU" add_osd_to_node "$c"; done
"$AU" headexec wait_for_osds 3
"$AU" headexec enable_rgw
lxc exec "$HEAD" -- sh -c "microceph.ceph -s"
ceph_h "--version" | grep -q 19.2.3 && ok "cluster starts on 19.2.3" || bad "expected 19.2.3 at start"

##############################################################################
banner "Baseline: a client key and some data to carry across the upgrade"
ceph_h "osd pool create rbd 32" || true
lxc exec "$HEAD" -- sh -c "microceph.rbd pool init rbd" || true
ceph_h "auth get-or-create client.demo mon 'profile rbd' osd 'profile rbd pool=rbd' mgr 'profile rbd pool=rbd'"
lxc exec "$HEAD" -- sh -c "echo payload-baseline | microceph.rados -p rbd put obj-demo -"
lxc exec "$HEAD" -- sh -c "microceph.rados -p rbd get obj-demo -" | grep -q payload-baseline \
    && ok "baseline client I/O works on 19.2.3" || bad "baseline I/O"

##############################################################################
banner "Upgrade every node to the 19.2.6 snap under test (${MICROCEPH_SNAP})"
ceph_h "osd set noout"
for n in "${NODES[@]}"; do
    echo "--- refreshing $n"
    install_snap_under_test "$n"
    lxc exec "$n" -- sh -c "cat /var/snap/microceph/current/conf/metadata.yaml"
    wait_osds 3
done
ceph_h "osd unset noout"
if wait_versions_uniform; then ok "all daemons upgraded to 19.2.6"; else bad "some daemons still on the old release"; ceph_h "versions"; fi

##############################################################################
banner "Confirm the upgraded (mixed-cipher) state"
ceph_h "mon dump 2>/dev/null | grep -E 'auth_'"
ceph_h "mon dump 2>/dev/null | grep -q 'auth_allowed_ciphers aes, aes256k'" \
    && ok "upgraded cluster keeps aes and aes256k allowed" || bad "unexpected allowed_ciphers"
ceph_h "health detail 2>/dev/null | grep -q AUTH_INSECURE_SERVICE_KEY_TYPE" \
    && ok "AUTH_INSECURE_* health checks appear (expected)" || bad "no insecure-key warnings after upgrade"

##############################################################################
banner "Rotate: prefer aes256k for new keys"
ceph_h "mon set auth_preferred_cipher aes256k"
ceph_h "mon dump 2>/dev/null | grep -q 'auth_preferred_cipher aes256k'" \
    && ok "auth_preferred_cipher is aes256k" || bad "preferred cipher not set"

banner "Rotate the mon. key (and sync every node's mon keyring)"
KR=$(ceph_h "auth rotate --key-type=aes256k mon." | grep -vE '^exported|^[[:space:]]*$')
for n in node-wrk0 node-wrk1 node-wrk2; do
    h=$(lxc exec "$n" -- hostname)
    printf '%s\n' "$KR" | lxc exec "$n" -- sh -c "cat > /root/mon.keyring; microceph.ceph-authtool /var/snap/microceph/common/data/mon/ceph-${h}/keyring --import-keyring /root/mon.keyring; rm -f /root/mon.keyring"
    lxc exec "$n" -- sh -c "snap restart microceph.mon"
    for _ in $(seq 1 30); do [ "$(ceph_h 'quorum_status -f json 2>/dev/null' | grep -o '"rank"' | wc -l)" -ge 3 ] && break; sleep 3; done
done
[ "$(ceph_h 'quorum_status -f json' | grep -o '"rank"' | wc -l)" -ge 3 ] \
    && ok "mon quorum intact after mon. rotation" || bad "mon quorum lost"

banner "Rotate mgr / osd / mds service keys"
for n in node-wrk0 node-wrk1 node-wrk2; do
    h=$(lxc exec "$n" -- hostname)
    ceph_h "auth rotate --key-type=aes256k mgr.${h}" | lxc exec "$n" -- sh -c "cat > /var/snap/microceph/common/data/mgr/ceph-${h}/keyring"
    lxc exec "$n" -- sh -c "snap restart microceph.mgr"
done
# OSD keys rotate in place with no restart
for id in $(ceph_h "osd ls"); do
    host=$(ceph_h "osd find ${id} -f json" | sed -n 's/.*"host":[[:space:]]*"\([^"]*\)".*/\1/p')
    NEW=$(ceph_h "auth rotate --key-type=aes256k osd.${id}")
    RAW=$(printf '%s\n' "$NEW" | awk '/key =/{print $3}')
    for n in "${NODES[@]}"; do [ "$(lxc exec "$n" -- hostname)" = "$host" ] && tgt="$n"; done
    printf '[osd.%s]\n\tkey = %s\n' "$id" "$RAW" | lxc exec "$tgt" -- sh -c "cat > /var/snap/microceph/common/data/osd/ceph-${id}/keyring"
    printf '%s' "$RAW" | lxc exec "$tgt" -- sh -c "microceph.ceph tell osd.${id} rotate-key -i -" \
        && echo "  osd.${id} rotated in place" || lxc exec "$tgt" -- sh -c "snap restart microceph.osd"
done
for e in $(ceph_h "auth ls 2>/dev/null" | grep -oE 'mds\.[A-Za-z0-9_.-]+' | sort -u); do
    node="${e#mds.}"
    ceph_h "auth rotate --key-type=aes256k ${e}" | lxc exec "$node" -- sh -c "cat > /var/snap/microceph/common/data/mds/ceph-${node}/keyring" 2>/dev/null || true
    lxc exec "$node" -- sh -c "snap restart microceph.mds" 2>/dev/null || true
done
wait_osds 3
svc_clear=false
for _ in $(seq 1 24); do
    ceph_h "health detail 2>/dev/null | grep -q AUTH_INSECURE_SERVICE_KEY_TYPE" || { svc_clear=true; break; }
    sleep 5
done
if $svc_clear; then
    ok "all service daemon keys are aes256k"
else
    bad "service keys still insecure after rotation"; ceph_h "health detail | grep -A8 AUTH_INSECURE_SERVICE_KEY_TYPE"
fi

banner "Switch the service cipher"
ceph_h "mon set auth_service_cipher aes256k"
ceph_h "config set mon mon_auth_allow_insecure_key false" || true
ceph_h "mon dump 2>/dev/null | grep -q 'auth_service_cipher aes256k'" \
    && ok "auth_service_cipher is aes256k" || bad "service cipher not set"

banner "Rotate the admin key in all three places (two files + the dqlite row)"
NEW=$(ceph_h "auth rotate --key-type=aes256k client.admin")
RAW=$(printf '%s\n' "$NEW" | awk '/key =/{print $3}')
for n in "${NODES[@]}"; do
    lxc exec "$n" -- sh -c "printf '# Generated by MicroCeph, DO NOT EDIT.\n[client.admin]\n\tkey = ${RAW}\n' | tee /var/snap/microceph/current/conf/ceph.client.admin.keyring /var/snap/microceph/current/conf/ceph.keyring >/dev/null"
done
lxc exec "$HEAD" -- sh -c "microceph cluster sql \"UPDATE config SET value='${RAW}' WHERE key='keyring.client.admin'\""
lxc exec "$HEAD" -- sh -c "microceph.ceph status >/dev/null 2>&1" && ok "admin key rotated, cluster still reachable" || bad "admin rotation broke access"
# prove microcephd does not restore the old key from dqlite after a restart
lxc exec node-wrk1 -- sh -c "snap restart microceph.daemon"; sleep 40
lxc exec node-wrk1 -- sh -c "grep -q '${RAW}' /var/snap/microceph/current/conf/ceph.keyring" \
    && ok "rotated admin key survives a daemon restart (dqlite row updated)" || bad "admin key reverted after restart"

##############################################################################
banner "Cut over to aes256k-only"
ceph_h "health mute AUTH_INSECURE_CLIENT_KEY_TYPE 8w" || true
ceph_h "mon set auth_allowed_ciphers aes256k"
ceph_h "mon dump 2>/dev/null | grep -q 'auth_allowed_ciphers aes256k'" \
    && ok "auth_allowed_ciphers is aes256k-only" || bad "cutover did not take"

##############################################################################
banner "Negative + recovery: an aes-typed client is rejected, rotation restores it"
DEMOKEY=$(ceph_h "auth get-key client.demo")
if lxc exec "$HEAD" -- sh -c "microceph.rados -n client.demo --key ${DEMOKEY} -p rbd get obj-demo - >/dev/null 2>&1"; then
    bad "aes-typed client.demo still authenticated after cutover"
else
    ok "aes-typed client.demo is rejected under aes256k-only"
fi
ceph_h "auth rotate --key-type=aes256k client.demo" >/dev/null
NEWKEY=$(ceph_h "auth get-key client.demo")
lxc exec "$HEAD" -- sh -c "microceph.rados -n client.demo --key ${NEWKEY} -p rbd get obj-demo -" | grep -q payload-baseline \
    && ok "client.demo works again after rotating its key to aes256k" || bad "recovery failed"

##############################################################################
banner "Summary"
for p in "${PASS[@]:-}"; do [ -n "$p" ] && echo "  PASS  $p"; done
for f in "${FAIL[@]:-}"; do [ -n "$f" ] && echo "  FAIL  $f"; done
if [ "${#FAIL[@]}" -eq 0 ]; then echo "ALL CEPHX AES256K CHECKS PASSED"; else echo "${#FAIL[@]} FAILURE(S)"; exit 1; fi
