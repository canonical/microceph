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
    sudo snap connect microceph:raw-volume
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
    set -x
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
        lxc exec $container -- sh -c "snap connect microceph:block-devices; snap connect microceph:raw-volume; snap connect microceph:hardware-observe"
        # Hack: allow access to sysfs hardware info through lxc
        lxc exec $container -- sh -c "sed -e 's|/sys/devices/\*\*/ r,|/sys/devices/** r,|' -i.bak /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"
        lxc exec $container -- sh -c "apparmor_parser -r /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"
        lxc exec $container -- sh -c "snap restart microceph.daemon"
    done
}

function install_store() {
    local chan="${1?missing}"
    for container in node-head node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap install microceph --channel ${chan}"
    done
}

function refresh_snap() {
    local chan="${1?missing}"
    for container in node-head node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap refresh microceph --channel ${chan}"
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
    for i in $(seq 1 8); do
        res=$( ( lxc exec node-head -- sh -c 'microceph status | grep -cE "^- node"' ) || true )
        if [[ $res -gt 3 ]] ; then
            echo "Found >3 nodes"
            break
        else
            echo -n '.'
            sleep 2
        fi
    done
    lxc exec node-head -- sh -c 'microceph status'
    lxc exec node-head -- sh -c 'microceph.ceph -s'
}

function add_osd_to_node() {
    local container="${1?missing}"
    lxc exec $container -- sh -c "microceph disk add /dev/sdia"
    sleep 1
}

function wait_for_osds() {
    local expect="${1?missing}"
    res=0
    for i in $(seq 1 8); do
        res=$( ( sudo microceph.ceph -s | grep -F osd: | sed -E "s/.* ([[:digit:]]*) in .*/\1/" ) || true )
        if [[ $res -ge $expect ]] ; then
            echo "Found >=${expect} OSDs"
            break
        else
            echo -n '.'
            sleep 2
        fi
    done
    sudo microceph.ceph -s
    if [[ $res -lt $expect ]] ; then
        echo "Never reached ${expect} OSDs"
        return -1
    fi    
}

function free_runner_disk() {
    # Remove stuff we don't need to get some extra disk
    sudo rm -rf /usr/local/lib/android /usr/local/.ghcup
    sudo docker rmi $(docker images -q)
}


function testrgw() {
    set -eu
    sudo microceph.ceph status
    sudo systemctl status snap.microceph.rgw
    if ! microceph.radosgw-admin user list | grep -q test ; then
        sudo microceph.radosgw-admin user create --uid=test --display-name=test
        sudo microceph.radosgw-admin key create --uid=test --key-type=s3 --access-key fooAccessKey --secret-key fooSecretKey
    fi
    sudo apt-get update -qq
    sudo apt-get -qq install s3cmd
    echo hello-radosgw > ~/test.txt
    s3cmd --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl mb s3://testbucket
    s3cmd --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl put -P ~/test.txt s3://testbucket
    ( curl -s http://localhost/testbucket/test.txt | grep -F hello-radosgw ) || return -1
}

function headexec() {
    local run="${1?missing}"
    shift
    set -x
    lxc exec node-head -- sh -c "/mnt/actionutils.sh $run $@"
}

run="${1}"
shift

$run "$@"
