#!/usr/bin/env bash

function cleaript() {
    # Docker can inject rules causing firewall conflicts
    sudo iptables -P FORWARD ACCEPT  || true
    sudo ip6tables -P FORWARD ACCEPT || true
    sudo iptables -F FORWARD  || true
    sudo ip6tables -F FORWARD || true

}

function setup_lxd() {
    sudo snap refresh
    sudo snap set lxd daemon.group=adm
    sudo lxd init --auto
}

function install_microceph() {
    # Install locally built microceph snap and connect interfaces
    sudo snap install --dangerous ~/microceph_*.snap
    sudo snap connect microceph:block-devices
    sudo snap connect microceph:hardware-observe
    # defer dm-crypt enablement for later.

    sudo microceph cluster bootstrap
    sudo microceph.ceph version
    sudo microceph.ceph status

    # Allow ceph to notice no OSD are present
    sleep 30
    sudo microceph.ceph status
    sudo microceph.ceph health

}

function add_encrypted_osds() {
    # Enable dm-crypt connection and restart microceph daemon
    sudo snap connect microceph:dm-crypt
    sudo snap restart microceph.daemon
    # Add OSDs backed by loop devices on /mnt (ephemeral "large" disk attached to GitHub action runners)
    i=0
    for l in a b c; do
      loop_file="$(sudo mktemp -p /mnt XXXX.img)"
      sudo truncate -s 1G "${loop_file}"
      loop_dev="$(sudo losetup --show -f "${loop_file}")"

      # XXX: the block-devices plug doesn't allow accessing /dev/loopX
      # devices so we make those same devices available under alternate
      # names (/dev/sdiY) that are not used inside GitHub Action runners
      minor="${loop_dev##/dev/loop}"
      sudo mknod -m 0660 "/dev/sdi${l}" b 7 "${minor}"
      sudo microceph disk add --wipe "/dev/sdi${l}" --encrypt
    done

    # Wait for OSDs to become up
    sleep 30
}

function enable_rgw() {
    # Enable rgw and wait for it to settle
    sudo microceph enable rgw
    # Wait for RGW to settle
    for i in $(seq 1 8); do
        res=$( ( sudo microceph.ceph status | grep -cF "rgw: 1 daemon" ) || true )
        if [[ $res -gt 0 ]] ; then
            echo "Found rgw daemon"
            break
        else
            echo -n '.'
            sleep 5
        fi
    done
}

function create_containers() {
    # Create head node and 3 worker containers
    for container in node-head node-wrk1 node-wrk2 node-wrk3 ; do
        # Bring up privileged containers
        lxc init ubuntu:22.04 $container
        lxc config set $container security.privileged true
        lxc config set $container security.nesting true
        # Allow access to loopback devices
        printf 'lxc.cgroup2.devices.allow = b 7:* rwm\nlxc.cgroup2.devices.allow = c 10:237 rwm' | lxc config set $container raw.lxc - 

        # Mount home and start
        lxc config device add $container homedir disk source=${HOME} path=/mnt
        lxc start $container

        # Create loopback devices on the host but access through container
        loop_file="$(sudo mktemp -p /mnt mctest-${i}-XXXX.img)"
        sudo truncate -s 1G "${loop_file}"
        sudo losetup --show -f "${loop_file}"
        loop_dev="$(sudo losetup --show -f "${loop_file}")"
        minor="${loop_dev##/dev/loop}"
        lxc exec $container -- sh -c "mknod -m 0660 /dev/sdia b 7 ${minor}"

        # Hack around bug #1712808
        lxc exec $container -- sh -c "ln -s /bin/true /usr/local/bin/udevadm"
    done

}

function install_multinode() {
    # Install and setup microceph snap
    for container in node-head node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap install --dangerous /mnt/microceph_*.snap"
        lxc exec $container -- sh -c "snap connect microceph:block-devices ; snap connect microceph:hardware-observe"
        # Hack: allow access to sysfs hardware info through lxc
        lxc exec $container -- sh -c "sed -e 's|/sys/devices/\*\*/ r,|/sys/devices/** r,|' -i.bak /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"
        lxc exec $container -- sh -c "apparmor_parser -r /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"
        lxc exec $container -- sh -c "snap restart microceph.daemon"
    done
}

function bootstrap_head() {
    # Bootstrap microceph on the head node
    lxc exec node-head -- sh -c "microceph cluster bootstrap"
    lxc exec node-head -- sh -c "microceph status"
    sleep 4
    lxc exec node-head -- sh -c 'microceph.ceph -s | egrep "mon: 1 daemons, quorum node-head"'
}

function cluster_nodes() {
    # Add/join microceph nodes to the cluster
    for i in 1 2 3 ; do
        tok=$( lxc exec node-head -- sh -c "microceph cluster add node-wrk${i}" )
        lxc exec node-wrk${i} -- sh -c "microceph cluster join $tok"
    done
    sleep 4
    lxc exec node-head -- sh -c 'microceph status'
    lxc exec node-head -- sh -c 'microceph.ceph -s'
}

function add_multinode_osds() {
    # Add disks on first 3 nodes, node-wrk3 remains empty to save resources
    for container in node-head node-wrk1 node-wrk2 ; do
        lxc exec $container -- sh -c "microceph disk add /dev/sdia"
    done
    sleep 4
    lxc exec node-head -- sh -c "microceph.ceph -s"
}

function free_runner_disk() {
    # Remove stuff we don't need to get some extra disk
    sudo rm -rf /usr/local/lib/android /usr/local/.ghcup
    sudo docker rmi $(docker images -q)
}

run="${1}"
shift

$run "$@"
