#!/usr/bin/env bash

# The bundled MicroCeph Ceph client is 20.2.1, which predates the
# AES256KRB5 CephX key format emitted by Squid 19.2.6. Keep this fixture on
# the compatible Squid point release until the bundled client is upgraded.
readonly CEPHADM_TEST_IMAGE="quay.io/ceph/ceph:v19.2.5"

function create_cephadm_vm() {
  set -eu
  input=$1

  if [[ -z $input ]]; then
    name=$(echo "$RANDOM" | md5sum | head -c 5)
  else
    name=$input
  fi

  # Remove leftovers from an interrupted previous run. Stale block volumes
  # carry old bluestore/LVM data, which makes ceph-volume report the disks
  # as unavailable; 'orch apply osd --all-available-devices' then creates
  # zero OSDs and every later step fails far from the real cause.
  lxc delete --force $name 2>/dev/null || true
  for i in 1 2 3; do
    lxc storage volume delete default $name-$i 2>/dev/null || true
  done

  lxc --quiet launch ubuntu:24.04 --vm $name -c limits.cpu=4 -c limits.memory=4GB

  lxc storage volume create default $name-1 --type=block size=10GB
  lxc storage volume create default $name-2 --type=block size=10GB
  lxc storage volume create default $name-3 --type=block size=10GB

  lxc storage volume attach default $name-1 $name
  lxc storage volume attach default $name-2 $name
  lxc storage volume attach default $name-3 $name

  success=1
  for i in $(seq 1 10); do
    if exec_ls_on_vm $name; then
      success=0
      break
    fi
    echo "waiting."
    sleep 20s
  done

  if [[ $success -ne 0 ]]; then
    echo "Timeout waiting for machine"
    exit 1
  fi

  lxc ls
}

function exec_ls_on_vm() {
  set -u
  name=$1

  lxc exec $name -- sh -c "ls"
}

function dump_osd_diagnostics() {
  local name=$1

  echo "=== Cephadm OSD diagnostics for ${name} ==="

  echo "--- host block-device identity and mappings ---"
  lxc exec "$name" -- sh -c "lsblk --all --paths --output NAME,KNAME,TYPE,SIZE,MODEL,SERIAL,WWN,FSTYPE,MOUNTPOINTS" || true
  lxc exec "$name" -- sh -c "ls -l /dev/disk/by-id /dev/mapper 2>&1 || true" || true
  lxc exec "$name" -- sh -c "command -v dmsetup >/dev/null && dmsetup ls --tree || true" || true
  lxc exec "$name" -- sh -c "command -v multipath >/dev/null && multipath -ll || true" || true
  lxc exec "$name" -- sh -c "systemctl is-active multipathd.service multipathd.socket || true" || true
  lxc exec "$name" -- sh -c "command -v pvs >/dev/null && pvs --all -o pv_name,pv_uuid,vg_name,vg_uuid,devices || true" || true
  lxc exec "$name" -- sh -c "command -v vgs >/dev/null && vgs --all -o vg_name,vg_uuid,vg_attr,vg_size,vg_free,pv_count || true" || true
  lxc exec "$name" -- sh -c "command -v lvs >/dev/null && lvs --all -o vg_name,lv_name,lv_attr,lv_size,lv_path,devices || true" || true
  # Do not use ceph-volume inventory here. In this fixture it can report an
  # escaped /dev/mapper alias as missing after an OSD is active; the direct
  # LVM reports above distinguish that false diagnostic from a real failure.

  echo "--- Ceph cluster and orchestrator state ---"
  lxc exec "$name" -- sh -c "timeout 30s cephadm shell -- ceph -s" || true
  lxc exec "$name" -- sh -c "timeout 30s cephadm shell -- ceph health detail" || true
  lxc exec "$name" -- sh -c "timeout 30s cephadm shell -- ceph osd stat --format json-pretty" || true
  lxc exec "$name" -- sh -c "timeout 30s cephadm shell -- ceph osd tree" || true
  lxc exec "$name" -- sh -c "timeout 30s cephadm shell -- ceph orch ps --daemon_type osd --format json-pretty" || true
  lxc exec "$name" -- sh -c "timeout 30s cephadm shell -- ceph orch device ls --wide" || true
  lxc exec "$name" -- sh -c "cephadm ls" || true

  echo "--- local service and container state ---"
  lxc exec "$name" -- sh -c "systemctl --no-pager --failed" || true
  lxc exec "$name" -- sh -c "if command -v podman >/dev/null; then podman ps --all --no-trunc; elif command -v docker >/dev/null; then docker ps --all --no-trunc; else echo 'No container engine found'; fi" || true

  echo "--- OSD and kernel journal ---"
  lxc exec "$name" -- sh -c "journalctl --no-pager -b -u 'ceph-*@osd.*' -n 500" || true
  lxc exec "$name" -- sh -c "journalctl --no-pager -k -b -n 200" || true

  echo "=== End Cephadm OSD diagnostics for ${name} ==="
}

function wait_for_up_osds() {
  local name=$1
  local expected_osds=${2:-3}
  local attempts=${3:-30}
  local up_osds

  for i in $(seq 1 "$attempts"); do
    up_osds=$(lxc exec "$name" -- sh -c "cephadm shell -- ceph osd stat --format json 2>/dev/null" | jq -r '.num_up_osds // 0' 2>/dev/null || printf '0')
    if [[ ! "$up_osds" =~ ^[0-9]+$ ]]; then
      up_osds=0
    fi

    if [[ "$up_osds" -ge "$expected_osds" ]]; then
      echo "All ${expected_osds} OSDs are up on ${name}"
      return 0
    fi

    if [[ "$i" -lt "$attempts" ]]; then
      echo "Waiting for ${expected_osds} OSDs on ${name}; found ${up_osds} (attempt ${i}/${attempts})"
      sleep 10s
    fi
  done

  echo "Expected ${expected_osds} up OSDs on ${name}, found ${up_osds}; failing bootstrap"
  dump_osd_diagnostics "$name"
  return 1
}

function bootstrap_cephadm() {
  set -eux
  name=$1

  lxc exec $name -- sh -c "sudo apt update"
  lxc exec $name -- sh -c "sudo apt -y install cephadm"

  ip_info=$(lxc exec $name -- sh -c "ip -4 -j route")

  ip=$(echo "$ip_info" | jq -r '.[] | select(.dst | contains("default")) | .prefsrc' | tr -d '[:space:]')

  if ! lxc exec "$name" -- sh -c "cephadm --image ${CEPHADM_TEST_IMAGE} bootstrap --mon-ip ${ip} --single-host-defaults --skip-dashboard --skip-monitoring-stack"; then
    echo "cephadm bootstrap failed on ${name}; collecting diagnostics"
    dump_osd_diagnostics "$name"
    return 1
  fi

  if ! lxc exec "$name" -- sh -c "cephadm shell -- ceph orch apply osd --all-available-devices"; then
    echo "OSD provisioning request failed on ${name}; collecting diagnostics"
    dump_osd_diagnostics "$name"
    return 1
  fi

  # Wait for the cluster to settle before adopt can connect
  echo "Waiting for ceph cluster to become healthy..."
  healthy=0
  for i in $(seq 1 30); do
    if lxc exec $name -- sh -c "cephadm shell -- ceph health 2>/dev/null" | grep -q "HEALTH_OK"; then
      echo "Cluster is healthy"
      healthy=1
      break
    fi
    # A one-off daemon crash during bootstrap leaves a RECENT_CRASH warning
    # that never clears on its own; archive crash reports so health can
    # return to OK. A crash-looping daemon keeps regenerating the warning
    # and still fails the gate below.
    lxc exec $name -- sh -c "cephadm shell -- ceph crash archive-all 2>/dev/null" || true
    echo "Waiting for cluster health... attempt $i/30"
    sleep 10s
  done
  lxc exec "$name" -- sh -c "cephadm shell -- ceph -s" || true
  if [[ $healthy -ne 1 ]]; then
    echo "Cluster on $name did not reach HEALTH_OK; collecting diagnostics"
    dump_osd_diagnostics "$name"
    return 1
  fi

  # HEALTH_OK does not require OSDs when the cluster has no pools. OSD
  # provisioning is asynchronous, so wait for all attached block volumes to
  # become usable before later CephFS and replication stages depend on them.
  if ! wait_for_up_osds "$name" 3; then
    return 1
  fi
}

function adopt_cephadm() {
  set -eux
  # hostname
  name=$1
  # Optional: snap glob/path (defaults to /home/runner/*.snap for CI)
  local snap_glob="${2:-/home/runner/*.snap}"

  # fetch cephadm adopt data
  # FSID
  fsid=$(lxc exec $name -- sh -c "cat /etc/ceph/ceph.conf" | grep fsid | cut -d " " -f 3)

  # Mon IP
  ip_info=$(lxc exec $name -- sh -c "ip -4 -j route")
  mon_ip=$(echo "$ip_info" | jq -r '.[] | select(.dst | contains("default")) | .prefsrc' | tr -d '[:space:]')

  # Keep the admin key out of xtrace and host command arguments.
  set +x
  key=$(lxc exec "$name" -- sh -c "cat /etc/ceph/ceph.client.admin.keyring" | grep key | cut -d " " -f 3)
  set -x

  lxc --quiet file push $snap_glob $name/root/

  # install microceph snap
  lxc exec $name -- sh -c "sudo snap install --dangerous /root/microceph_*.snap"
  for feat in block-devices hardware-observe mount-observe load-rbd microceph-support network-bind process-control; do
    lxc exec $name -- sh -c "sudo snap connect microceph:$feat"
  done

  # Adopt cephadm cluster using microceph --public-network=10.230.118.167/24 --cluster-network=10.230.118.167/247/24
  set +x
  printf '%s\n' "$key" | lxc exec "$name" -- bash -c "sudo microceph cluster adopt --fsid=${fsid} --mon-hosts=${mon_ip} -"
  unset key
  set -x
}

function exchange_adopt_remote_tokens() {
  set -eux
  pri_name=$1
  sec_name=$2

  primary_token=$(lxc exec $pri_name -- sh -c "microceph cluster export $sec_name")
  secondary_token=$(lxc exec $sec_name -- sh -c "microceph cluster export $pri_name")

  # perform imports on both sites
  lxc exec $pri_name -- sh -c "microceph remote import siteb $secondary_token --local-name=$pri_name"
  lxc exec $sec_name -- sh -c "microceph remote import sitea $primary_token --local-name=$sec_name"
}

function wait_for_active_mds() {
  # Poll until the given fs has at least one active MDS rank.
  # Usage: wait_for_active_mds <container> <fs_name> [timeout_seconds]
  local container=$1 fs=$2 timeout=${3:-120}
  local deadline=$(( $(date +%s) + timeout ))
  echo "Waiting for active MDS on ${fs} in ${container}..."
  while true; do
    active=$(lxc exec "$container" -- bash -c \
      "sudo microceph.ceph fs status ${fs} --format json 2>/dev/null \
       | jq '[.mdsmap[]? | select(.state // \"\" | contains(\"active\"))] | length' 2>/dev/null || echo 0")
    if [[ "${active:-0}" -ge 1 ]]; then
      echo "MDS active on ${fs} in ${container}"
      return 0
    fi
    if [[ $(date +%s) -ge $deadline ]]; then
      echo "Timed out waiting for active MDS on ${fs} in ${container}"
      # Dump enough state to tell an MDS problem from a cluster problem:
      # an MDS stuck in 'creating' with MAX AVAIL 0 means the pools have
      # no usable OSDs, not that the MDS itself is broken.
      lxc exec "$container" -- bash -c "sudo microceph.ceph fs status ${fs}" || true
      lxc exec "$container" -- bash -c "sudo microceph.ceph -s" || true
      lxc exec "$container" -- bash -c "sudo microceph.ceph osd tree" || true
      lxc exec "$container" -- bash -c "sudo microceph.ceph df" || true
      return 1
    fi
    echo -n '.'
    sleep 5
  done
}

function remote_enable_fs_rep() {
  set -eux
  pri_name=$1
  sec_name=$2

  # Primary
  lxc exec $pri_name -- bash -c "sudo microceph enable mds"
  lxc exec $pri_name -- bash -c "sudo microceph enable cephfs-mirror"
  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs volume create vol"
  lxc exec $pri_name -- bash -c "sudo microceph.ceph mgr module enable mirroring"
  # Wait for MDS to become active before enabling mirroring; the
  # 'fs snapshot mirror enable' command hangs if no MDS rank is up yet.
  wait_for_active_mds "$pri_name" "vol"
  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs snapshot mirror enable vol"

  # Secondary
  lxc exec $sec_name -- bash -c "sudo microceph enable mds"
  lxc exec $sec_name -- bash -c "sudo microceph enable cephfs-mirror"
  lxc exec $sec_name -- bash -c "sudo microceph.ceph fs volume create vol"
  lxc exec $sec_name -- bash -c "sudo microceph.ceph mgr module enable mirroring"
  # Same wait for secondary.
  wait_for_active_mds "$sec_name" "vol"
  lxc exec $sec_name -- bash -c "sudo microceph.ceph fs snapshot mirror enable vol"
}

function bootstrap_adopt_cephfs_mirror() {
  set -eux
  pri_name=$1
  sec_name=$2

  echo "Bootstrapping FS Mirror peer"
  peer_token=$(lxc exec $sec_name -- bash -c "sudo microceph.ceph fs snapshot mirror peer_bootstrap create vol client.fsmir-vol-primary secondary" | jq '.token' | tr -d '\"')
  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs snapshot mirror peer_bootstrap import vol $peer_token"
}

function replication_adopt_check_subvolume_on_sec() {
  set -eux

  pri_name=$1
  sec_name=$2

  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs subvolume create vol test"

  subvolpath=$(lxc exec $pri_name -- bash -c "sudo microceph.ceph fs subvolume getpath vol test")
  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs snapshot mirror add vol \"$subvolpath\""

  # Give some time for the mirror to start syncing
  sleep 10s

  found="false"
  test_svn="test"
  echo "Waiting for subvolume to appear on secondary..."
  
  # Try for up to 15 minutes (90 attempts * 10s = 900s)
  for i in $(seq 1 90); do
    echo "Check attempt $i/90"
    
    # check subvolumes at secondary
    list_output=$(lxc exec $sec_name -- bash -c "sudo microceph.ceph fs subvolume ls vol 2>/dev/null | jq -r '.[].name' 2>/dev/null" || echo "")
    
    if [[ -n "$list_output" ]]; then
      echo "Current subvolumes: $list_output"
      
      for sv_name in $list_output; do
        check_name=$(echo "$sv_name" | tr -d '\"' | xargs)
        if [[ "$check_name" == "$test_svn" ]]; then
          echo "✓ Subvolume '$sv_name' found on secondary"
          found="true"
          break 2
        fi
      done
    else
      echo "No subvolumes found yet on secondary"
    fi

    if [[ $i -lt 90 ]]; then
      sleep 10s
    fi
  done

  if [[ "$found" == "false" ]]; then
    echo "✗ Timeout: subvolume did not appear on secondary after 15 minutes"
    lxc exec $sec_name -- bash -c "sudo microceph.ceph fs subvolume ls vol" || true
    exit 1
  fi
}

run="${1}"
shift

$run "$@"
