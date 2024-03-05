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
    sudo snap connect microceph:mount-observe
    # defer dm-crypt enablement for later.

    sudo microceph cluster bootstrap
    sudo microceph.ceph version
    sudo microceph.ceph status

    # Allow ceph to notice no OSD are present
    sleep 30
    sudo microceph.ceph status
    sudo microceph.ceph health

}

function create_loop_devices() {
    for l in a b c; do
      loop_file="$(sudo mktemp -p /mnt XXXX.img)"
      sudo truncate -s 4G "${loop_file}"
      loop_dev="$(sudo losetup --show -f "${loop_file}")"
      minor="${loop_dev##/dev/loop}"
      # create well-known names
      sudo mknod -m 0660 "/dev/sdi${l}" b 7 "${minor}"
    done
}

function add_encrypted_osds() {
    # Enable dm-crypt connection and restart microceph daemon
    sudo snap connect microceph:dm-crypt
    sudo snap restart microceph.daemon
    create_loop_devices
    sudo microceph disk add /dev/sdia /dev/sdib /dev/sdic --wipe --encrypt

    # Wait for OSDs to become up
    sleep 30

    # verify disks using json output.
    res=$(sudo microceph disk list --json | jq -r '.ConfiguredDisks[].path' | grep -e "/dev/sdia" -e "/dev/sdib" -e "/dev/sdic" -c)
    if ($res -ne "3") ; then
        echo "${res} is not equal to expected disk count (3)"
        exit 1
    fi
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

function get_lxd_network() {
    local nw_name="${1?missing}"
    nw=$(lxc network list --format=csv | grep "${nw_name}" | cut -d, -f4)
    echo "$nw"
}

function create_containers() {
    set -x
    # Create public network for internal ceph cluster
    lxc network create public
    local nw=$(get_lxd_network public)
    gw=$(echo "$nw" | cut -d/ -f1)
    mask=$(echo "$nw" | cut -d/ -f2)
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
        dev_name=$(lxc exec $container -- sh -c "ip a | grep ': eth' | tail -n 1 | cut -d@ -f1 | cut -d ' ' -f2")
        lxc exec $container -- sh -c "ip a add ${gw}${i}/${mask} dev ${dev_name}"

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
        lxc exec $container -- sh -c "snap connect microceph:block-devices ; snap connect microceph:hardware-observe ; snap connect microceph:mount-observe"
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
    set -x

    # Issue cluster wide client config set.
    lxc exec node-wrk0 -- sh -c "microceph client config set rbd_cache true"
    # Issue host specific client config set for each worker node.
    for id in 1 2 ; do
        lxc exec node-wrk${id} -- sh -c "microceph client config set rbd_cache_size $((512*$id)) --target node-wrk${id}"
    done

    # Verify client configs post set on each node.
    for id in 1 2 ; do
        res1=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache = true'")
        res2=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size = $((512*$id))'")
        if (($res1 != "1")) || (($res2 != "1")) ; then
            echo "required configs not present"
            exit 1
        fi
    done

    # Reset client configs
    lxc exec node-wrk0 -- sh -c "microceph client config reset rbd_cache --yes-i-really-mean-it"
    lxc exec node-wrk0 -- sh -c "microceph client config reset rbd_cache_size --yes-i-really-mean-it"

    # Verify client configs post reset on each node.
    for id in 1 2 ; do
        res1=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache '")
        res2=$(lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size'")
        if (($res1 != "0")) || (($res2 != "0")) ; then
            echo "incorrect configs present"
            exit 1
        fi
    done
}

function verify_bootstrap_configs() {
    set -x

    local node="${1?missing}"
    local nw="${2?missing}"
    local mon_ips="${@:3}"

    # Check mon hosts entries.
    mon_hosts=$(lxc exec "${node}" -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep 'mon host'")
    for i in $mon_ips ; do
        res_host=$(echo "${mon_hosts}" | grep -c "${i}")
        if  (($res_host != "1")) ; then
            echo $res_host
            echo "mon host entry ${$i} not present in ceph.conf"
            exit 1
        fi
    done

    # Check public_network entry in ceph.conf
    local res_pub=$(lxc exec "${node}" -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'public_network = ${nw}'")
    if (($res_pub != "1")) ; then
        lxc exec "${node}" -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf"
        echo "public_network ${nw} not present in ceph.conf"
        exit 1
    fi
}

function bootstrap_head() {
    set -x

    local arg=$1
    if [ $arg = "custom" ]; then
        # Bootstrap microceph on the head node
        local nw=$(get_lxd_network public)
        local gw=$(echo "$nw" | cut -d/ -f1)
        local mask=$(echo "$nw" | cut -d/ -f2)
        local mon_ip="${gw}0"
        lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap --public-network=$nw"
        sleep 5
        # Verify ceph.conf
        verify_bootstrap_configs node-wrk0 "${nw}" "${mon_ip}"
    else
        lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap"
    fi

    lxc exec node-wrk0 -- sh -c "microceph status"
    sleep 4
    lxc exec node-wrk0 -- sh -c 'microceph.ceph -s | egrep "mon: 1 daemons, quorum node-wrk0"'
}


function cluster_nodes() {
    set -x
    local arg=$1

    # Add/join microceph nodes to the cluster
    local nw=$(get_lxd_network public)
    local gw=$(echo "$nw" | cut -d/ -f1)
    local mask=$(echo "$nw" | cut -d/ -f2)
    local mon_ips="${gw}0"
    for i in 1 2 3 ; do
        local node_mon_ip="${gw}${i}"

        # join MicroCeph cluster
        tok=$(lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk${i}" )
        lxc exec node-wrk${i} -- sh -c "microceph cluster join $tok"

        if [ $arg = "custom" ]; then
            # verify ceph.conf
            verify_bootstrap_configs "node-wrk${i}" "${nw}" $mon_ips
        fi

        # append mon_ips as a space separated entry.
        mon_ips="$mon_ips $node_mon_ip"
    done

    for i in $(seq 1 8); do
        local res=$( ( lxc exec node-wrk0 -- sh -c 'microceph status | grep -cE "^- node"' ) || true )
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
    lxc exec $container -- sh -c "microceph disk add /dev/sdia --wipe"
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
            sleep 5
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

    lxc exec node-wrk0 -- sh -c "microceph cluster migrate $src $dst"
    for i in $(seq 1 8); do
        if lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 $src | grep -E \"^  Services: osd$\"" ; then
            if lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 $dst | grep -E \"^  Services: mds, mgr, mon$\"" ; then
                echo "Services migrated"
                break
            fi
        fi
        echo "."
        sleep 10
    done
    lxc exec node-wrk0 -- sh -c "microceph status"
    lxc exec node-wrk0 -- sh -c "microceph.ceph -s"

    if lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 $src | grep -E \"^  Services: osd$\"" ; then
        if lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 $dst | grep -E \"^  Services: mds, mgr, mon$\"" ; then
            return
        fi
    fi
    echo "Never reached migration target"
    return -1
}

function test_ceph_conf() {
    set -uex
    for n in $( lxc ls -c n --format csv ); do
        echo "checking node $n"
        lxc exec $n -- sh <<'EOF'
# Test: configured rundir must be current
current=$( realpath /var/snap/microceph/current )
rundir=$( cat /var/snap/microceph/current/conf/ceph.conf | awk '/run dir/{ print $4 }' )
p=$( dirname $rundir )
if [ $p != $current ]; then
    echo "Error: snap data dir $current, configured run dir: $rundir"
    cat /var/snap/microceph/current/conf/ceph.conf
    exit -1
fi

# Test: must contain public_network
if ! grep -q public_net /var/snap/microceph/current/conf/ceph.conf ; then
    echo "Error: didn't find public_net in ceph.conf"
    cat /var/snap/microceph/current/conf/ceph.conf
    exit -1
fi
EOF
    done
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
