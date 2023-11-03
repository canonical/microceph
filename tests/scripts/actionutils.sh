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
    # Create public network for internal ceph cluster
    lxc network create public
    nw=$(lxc network list --format=csv | grep "public" | cut -d, -f4)
    gw=$(echo $nw | cut -d/ -f1)
    mask=$(echo $nw | cut -d/ -f2)
    # Create head node and 3 worker containers
    for i in 0 1 2 3 ; do
        container="node-wrk${i}" # node name
        # Bring up privileged containers
        lxc init ubuntu:22.04 $container
        lxc config set $container security.privileged true
        lxc config set $container security.nesting true
        # Allow access to loopback devices
        printf 'lxc.cgroup2.devices.allow = b 7:* rwm\nlxc.cgroup2.devices.allow = c 10:237 rwm' | lxc config set $container raw.lxc - 

        # Configure and start container
        lxc config device add $container homedir disk source=${HOME} path=/mnt
        lxc network attach public $container eth2
        lxc start $container

        # allocate ip address on the attached network
        lxc exec $container -- sh -c "ip a add ${gw}${i}/${mask} dev"

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
    for container in node-wrk0 node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap install --dangerous /mnt/microceph_*.snap"
        lxc exec $container -- sh -c "snap connect microceph:block-devices ; snap connect microceph:hardware-observe"
        # Hack: allow access to sysfs hardware info through lxc
        lxc exec $container -- sh -c "sed -e 's|/sys/devices/\*\*/ r,|/sys/devices/** r,|' -i.bak /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"
        lxc exec $container -- sh -c "apparmor_parser -r /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"
        lxc exec $container -- sh -c "snap restart microceph.daemon"
    done
}

function install_store() {
    local chan="${1?missing}"
    for container in node-wrk0 node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap install microceph --channel ${chan}"
    done
}

function refresh_snap() {
    local chan="${1?missing}"
    for container in node-wrk0 node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap refresh microceph --channel ${chan}"
    done
}

function check_client_configs() {
    # Issue cluster wide client config set.
    lxc exec node-wrk0 -- sh -c "microceph client config set rbd_cache true"
    # Issue host specific client config set for each worker node.
    for id in 1 2 3 ; do
        lxc exec node-wrk${id} -- sh -c "microceph client config set rbd_cache_size $((512*$id))"
    done

    # Verify client configs post set on each node.
    for id in 1 2 3 ; do
        res1=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache = true'")
        res2=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size = $((512*$id))'")
        if (($res1 -ne "1")) || (($res2 -ne "1")) ; then
            # required configs not present.
            exit 1
        fi
    done

    # Reset client configs
    lxc exec node-wrk0 -- sh -c "microceph client config reset rbd_cache"
    lxc exec node-wrk0 -- sh -c "microceph client config reset rbd_cache_size"

    # Verify client configs post reset on each node.
    for id in 1 2 3 ; do
        res1=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache '")
        res2=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size'")
        if (($res1 -ne "0")) || (($res2 -ne "0")) ; then
            # Incorrect configs present.
            exit 1
        fi
    done
}

function bootstrap_head() {
    set -x

    # Bootstrap microceph on the head node
    nw=$(lxc network list --format=csv | grep "public" | cut -d, -f4)
    gw=$(echo $nw | cut -d/ -f1)
    mask=$(echo $nw | cut -d/ -f2)
    mon_ip="${gw}0"
    lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap --mon-ip $mon_ip"
    lxc exec node-wrk0 -- sh -c "microceph status"
    sleep 4
    lxc exec node-wrk0 -- sh -c 'microceph.ceph -s | egrep "mon: 1 daemons, quorum node-wrk0"'

    # Verify ceph.conf
    res1=$(lxc exec node-wrk0 -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'mon host = $mon_ip'")
    res2=$(lxc exec node-wrk0 -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'public_network = ${mon_ip}/${mask}'")
    if (($res1 -ne "1")) || (($res2 -ne "1")) ; then
        # required configs not present.
        exit 1
    fi
}

function cluster_nodes() {
    set -x

    # Add/join microceph nodes to the cluster
    nw=$(lxc network list --format=csv | grep "public" | cut -d, -f4)
    gw=$(echo $nw | cut -d/ -f1)
    mask=$(echo $nw | cut -d/ -f2)
    bootstrap_ip="${gw}0"
    mon_ips=${bootstrap_ip}
    for i in 1 2 3 ; do
        node_mon_ip="${gw}${i}/${mask}"

        # join MicroCeph cluster
        tok=$(lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk${i}" )
        lxc exec node-wrk${i} -- sh -c "microceph cluster join $tok"

        # verify ceph.conf
        res1=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'mon host = $mon_ips'")
        res2=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'public_network = ${bootstrap_ip}/${mask}'")
        if (($res1 -ne "0")) || (($res2 -ne "0")) ; then
            # Incorrect configs present.
            exit 1
        fi

        # append mon_ips
        mon_ips="$mon_ips,$node_mon_ip"
    done

    for i in $(seq 1 8); do
        res=$( ( lxc exec node-wrk0 -- sh -c 'microceph status | grep -cE "^- node"' ) || true )
        if [[ $res -gt 3 ]] ; then
            echo "Found >3 nodes"
            break
        else
            echo -n '.'
            sleep 2
        fi
    done

    lxc exec node-wrk0 -- sh -c 'microceph status'
    lxc exec node-wrk0 -- sh -c 'microceph.ceph -s'
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

function enable_services() {
    local node="${1?missing}"
    for s in mon mds mgr ; do
        sudo microceph enable $s --target $node
    done
    for i in $(seq 1 8); do
        if sudo microceph.ceph -s | grep -q "mon: .*daemons.*${node}" ; then
            echo "Found mon on ${node}"
            break
        else
            echo -n '.'
            sleep 2
        fi
    done
    sudo microceph.ceph -s
}

function remove_node() {
    local node="${1?missing}"
    sudo microceph cluster remove $node
    for i in $(seq 1 8); do
        if sudo microceph.ceph -s | grep -q "mon: .*daemons.*${node}" ; then
            echo -n '.'
            sleep 2
        else
            echo "No mon on ${node}"
            break
        fi
    done
    sleep 1
    sudo microceph.ceph -s
    sudo microceph status
}

function test_migration() {
    local src="${1?missing}"
    local dst="${2?missing}"

    lxc exec node-head -- sh -c "microceph cluster migrate $src $dst"
    for i in $(seq 1 8); do
        if lxc exec node-head -- sh -c "microceph status | grep -F -A 1 $src | grep -E \"^  Services: osd$\"" ; then
            if lxc exec node-head -- sh -c "microceph status | grep -F -A 1 $dst | grep -E \"^  Services: mds, mgr, mon$\"" ; then
                echo "Services migrated"
                break
            fi
        fi
        echo "."
        sleep 2
    done
    lxc exec node-head -- sh -c "microceph status"
    lxc exec node-head -- sh -c "microceph.ceph -s"

    if lxc exec node-head -- sh -c "microceph status | grep -F -A 1 $src | grep -E \"^  Services: osd$\"" ; then
        if lxc exec node-head -- sh -c "microceph status | grep -F -A 1 $dst | grep -E \"^  Services: mds, mgr, mon$\"" ; then
            return
        fi
    fi
    echo "Never reached migration target"
    return -1
}

function headexec() {
    local run="${1?missing}"
    shift
    set -x
    lxc exec node-wrk0 -- sh -c "/mnt/actionutils.sh $run $@"
}

run="${1}"
shift

$run "$@"
