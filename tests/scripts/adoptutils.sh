#!/usr/bin/env bash

function create_cephadm_vm() {
  input=$1

  if [[ -z $input ]]; then
    name=$(echo $RANDOM | md5sum | head -c 5)
  else
    name=$input
  fi

  lxc launch ubuntu:24.04 --vm $name -c limits.cpu=4 -c limits.memory=4GB

  lxc storage volume create default $name-1 --type=block size=10GB
  lxc storage volume create default $name-2 --type=block size=10GB
  lxc storage volume create default $name-3 --type=block size=10GB

  lxc storage volume attach default $name-1 $name
  lxc storage volume attach default $name-2 $name
  lxc storage volume attach default $name-3 $name
}

function bootstrap_cephadm() {
  set -eux
  name=$1

  lxc exec $name -- sh -c "sudo apt update"
  lxc exec $name -- sh -c "sudo apt -y install cephadm"

  ip_info=$(lxc exec $name -- sh -c "ip -4 -j route")

  ip=$(echo $ip_info | jq -r '.[] | select(.dst | contains("default")) | .prefsrc' | tr -d '[:space:]')

  lxc exec $name -- sh -c "cephadm bootstrap --mon-ip $ip --single-host-defaults --skip-dashboard --skip-monitoring-stack"
  lxc exec $name -- sh -c "cephadm shell -- ceph orch apply osd --all-available-devices"
}

function adopt_cephadm() {
  set -eux
  # hostname
  name=$1

  # fetch cephadm adopt data
  # FSID
  fsid=$(lxc exec $name -- sh -c "cat /etc/ceph/ceph.conf" | grep fsid | cut -d " " -f 3)

  # Mon IP
  ip_info=$(lxc exec $name -- sh -c "ip -4 -j route")
  mon_ip=$(echo $ip_info | jq -r '.[] | select(.dst | contains("default")) | .prefsrc' | tr -d '[:space:]')

  # Admin Key
  key=$(lxc exec $name -- sh -c "cat /etc/ceph/ceph.client.admin.keyring" | grep key | cut -d " " -f 3)

  # install microceph snap
  lxc exec $name -- sh -c "sudo snap install --dangerous /mnt/microceph_*.snap"
  for feat in block-devices hardware-observe mount-observe load-rbd microceph-support network-bind process-control; do
    lxc exec $name -- sh -c "sudo snap connect microceph:$feat"
  done

  # Adopt cephadm cluster using microceph --public-network=10.230.118.167/24 --cluster-network=10.230.118.167/247/24
  lxc exec $name -- bash -c "sudo microceph cluster adopt --fsid=$fsid --admin-key=$key --mon-hosts=$mon_ip"
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

function remote_enable_fs_rep() {
  set -eux
  pri_name=$1
  sec_name=$2

  # Primary
  lxc exec $pri_name -- bash -c "sudo microceph enable mds"
  lxc exec $pri_name -- bash -c "sudo microceph enable cephfs-mirror"
  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs volume create vol"
  lxc exec $pri_name -- bash -c "sudo microceph.ceph mgr module enable mirroring"
  lxc exec $pri_name -- bash -c "sudo microceph.ceph fs snapshot mirror enable vol"

  # Secondary
  lxc exec $sec_name -- bash -c "sudo microceph enable mds"
  lxc exec $sec_name -- bash -c "sudo microceph enable cephfs-mirror"
  lxc exec $sec_name -- bash -c "sudo microceph.ceph fs volume create vol"
  lxc exec $sec_name -- bash -c "sudo microceph.ceph mgr module enable mirroring"
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

  lxc exec $pri_name -- bash -c "sudo ceph fs subvolume create vol test"

  subvolpath=$(lxc exec $pri_name -- bash -c "sudo ceph fs subvolume getpath vol test")
  lxc exec $pri_name -- bash -c "sudo ceph fs snapshot mirror add vol $subvolpath"

  found="false"
  counter=0
  while [[ "$found" == "false" ]]; do
    # check subvolumes at secondary
    list_output=$(lxc exec $sec_name -- bash -c "ceph fs subvolume ls vol | jq '.[].name'")
    counter=$((counter + 1))
    echo $list_output

    for sv_name in $list_output; do
      if [[ $sv_name == "test" ]]; then
        echo "subvolume $sv_name found"
        found="true"
        break
      fi
    done

    if [[ $counter -eq 60 ]]; then
      echo "Timedout waiting for subvolume to appear"
      exit 1
    fi

    sleep 1m
  done
}

run="${1}"
shift

$run "$@"
