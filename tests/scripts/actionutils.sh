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
    local mon_ip="${1}"
    # Install locally built microceph snap and connect interfaces
    sudo snap install --dangerous ~/microceph_*.snap
    sudo snap connect microceph:block-devices
    sudo snap connect microceph:hardware-observe
    sudo snap connect microceph:mount-observe
    # defer dm-crypt enablement for later.
    sudo snap connect microceph:load-rbd
    sudo snap connect microceph:microceph-support
    sudo snap connect microceph:network-bind

    if [ -n "${mon_ip}" ]; then
        sudo microceph cluster bootstrap --mon-ip "${mon_ip}"
    else
        sudo microceph cluster bootstrap
    fi
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

function create_lvm_vol() {
    local lv_name="${1?missing}"
    [[ -e /dev/vgtst/$lv_name ]] && exit
    loop_file="$(sudo mktemp -p /mnt XXXX.img)"
    sudo truncate -s 4G "${loop_file}"
    loop_dev="$(sudo losetup --show -f "${loop_file}")"
    minor="${loop_dev##/dev/loop}"

    # Set up a lvm vol on loop file
    sudo pvcreate $loop_dev
    sudo vgcreate vgtst $loop_dev
    sudo lvcreate -l100%FREE --name $lv_name vgtst
}

function add_encrypted_osds() {
    # Enable dm-crypt connection and restart microceph daemon
    sudo snap connect microceph:dm-crypt
    sudo snap restart microceph.daemon
    create_loop_devices
    sudo microceph disk add /dev/sdia /dev/sdib --wipe --encrypt

    # Wait for OSDs to become up
    sleep 30

    # verify disks using json output.
    res=$(sudo microceph disk list --json | jq -r '.ConfiguredDisks[].path' | grep -e "/dev/sdia" -e "/dev/sdib" -c)
    if [ $res -ne 2 ] ; then
        echo "${res} is not equal to expected disk count (2)"
        exit 1
    fi
}

function add_lvm_vol() {
    set -x
    create_lvm_vol lvtest
    sudo microceph disk add /dev/vgtst/lvtest --wipe
    sleep 20
    sudo microceph.ceph -s
    res=$(sudo microceph disk list --json | jq -r '.ConfiguredDisks[].path' | grep "/dev/vgtst/lvtest" -c)
    if [ $res -ne 1 ] ; then
        echo "Didnt find lvm vol"
        exit 1
    fi
}

function disable_rgw() {
    set -x
    # Disable rgw
    sudo microceph disable rgw
}

function enable_rgw() {
    set -x
    # Enable rgw and wait for it to settle
    sudo microceph enable rgw
    wait_for_rgw 1
}

function enable_rgw_ssl() {
    set -x
    # Generate the SSL material
    sudo openssl genrsa -out /tmp/ca.key 2048
    sudo openssl req -x509 -new -nodes -key /tmp/ca.key -days 1024 -out /tmp/ca.crt -outform PEM -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com"
    sudo openssl genrsa -out /tmp/server.key 2048
    sudo openssl req -new -key /tmp/server.key -out /tmp/server.csr -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com"
    echo "subjectAltName = DNS:localhost" > /tmp/extfile.cnf
    sudo openssl x509 -req -in /tmp/server.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/server.crt -days 365 -extfile /tmp/extfile.cnf
    # Enable rgw and wait for it to settle
    sudo microceph enable rgw --ssl-certificate="$(sudo base64 -w0 /tmp/server.crt)" --ssl-private-key="$(sudo base64 -w0 /tmp/server.key)"
    wait_for_rgw 1
}

function get_lxd_network() {
    local nw_name="${1?missing}"
    nw=$(lxc network list --format=csv | grep "${nw_name}" | cut -d, -f4)
    echo "$nw"
}

function create_containers() {
    set -eux
    local net_name="${1?missing}"
    # Create public network for internal ceph cluster
    lxc network create $net_name
    local nw=$(get_lxd_network $net_name)
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
        lxc network attach $net_name $container eth2
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

# functions with remote_ prefix have following assumptions:
# sitea: node-wrk0, node-wrk1
# siteb: node-wrk2, node-wrk3
function remote_simple_bootstrap_two_sites() {
    # Bootstrap sitea
    lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap"
    lxc exec node-wrk0 -- sh -c "microceph disk add loop,2G,3"
    tok=$(lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk1" )
    lxc exec node-wrk1 -- sh -c "microceph cluster join $tok"
    sleep 10
    # Bootstrap siteb 
    lxc exec node-wrk2 -- sh -c "microceph cluster bootstrap"
    lxc exec node-wrk2 -- sh -c "microceph disk add loop,2G,3"
    tok=$(lxc exec node-wrk2 -- sh -c "microceph cluster add node-wrk3" )
    lxc exec node-wrk3 -- sh -c "microceph cluster join $tok"
    sleep 10
}

function remote_exchange_site_tokens() {
    set -eux
    sitea_token=$(lxc exec node-wrk0 -- sh -c "microceph cluster export siteb")
    siteb_token=$(lxc exec node-wrk2 -- sh -c "microceph cluster export sitea")

    # perform imports on both sites
    lxc exec node-wrk0 -- sh -c "microceph remote import siteb $siteb_token --local-name=sitea"
    lxc exec node-wrk2 -- sh -c "microceph remote import sitea $sitea_token --local-name=siteb"
}

function remote_perform_remote_ops_check() {
    # assumes the remote export/import operations are already performed on both sites.
    set -eux
    # perform ceph ops on sitea (both nodes)
    lxc exec node-wrk0 -- sh -c "microceph.ceph -s" # local ceph status
    lxc exec node-wrk0 -- sh -c "microceph.ceph -s --cluster siteb --id sitea" # remote ceph status
    lxc exec node-wrk0 -- sh -c "microceph remote list --json | grep '\"name\":\"siteb\"'"
    lxc exec node-wrk1 -- sh -c "microceph.ceph -s" # local ceph status
    lxc exec node-wrk1 -- sh -c "microceph.ceph -s --cluster siteb --id sitea" # remote ceph status
    lxc exec node-wrk1 -- sh -c "microceph remote list --json | grep '\"local_name\":\"sitea\"'"
    # perform ceph ops on siteb (both nodes)
    lxc exec node-wrk2 -- sh -c "microceph.ceph -s" # local ceph status
    lxc exec node-wrk2 -- sh -c "microceph.ceph -s --cluster sitea --id siteb" # remote ceph status
    lxc exec node-wrk2 -- sh -c "microceph remote list --json | grep '\"name\":\"sitea\"'"
    lxc exec node-wrk3 -- sh -c "microceph.ceph -s" # local ceph status
    lxc exec node-wrk3 -- sh -c "microceph.ceph -s --cluster sitea --id siteb" # remote ceph status
    lxc exec node-wrk3 -- sh -c "microceph remote list --json | grep '\"local_name\":\"siteb\"'"
}

function remote_configure_rbd_mirroring() {
    set -eux
    # create two new rbd pools on sitea
    lxc exec node-wrk0 -- sh -c "microceph.ceph osd pool create pool_one"
    lxc exec node-wrk1 -- sh -c "microceph.ceph osd pool create pool_two"
    # and siteb
    lxc exec node-wrk2 -- sh -c "microceph.ceph osd pool create pool_one"
    lxc exec node-wrk3 -- sh -c "microceph.ceph osd pool create pool_two"

    # enable both pools for rbd on site a
    lxc exec node-wrk0 -- sh -c "microceph.rbd pool init pool_one"
    lxc exec node-wrk1 -- sh -c "microceph.rbd pool init pool_two"
    # and siteb
    lxc exec node-wrk2 -- sh -c "microceph.rbd pool init pool_one"
    lxc exec node-wrk3 -- sh -c "microceph.rbd pool init pool_two"

    # create 2 images on both pools on primary site.
    lxc exec node-wrk0 -- sh -c "microceph.rbd create --size 512 pool_one/image_one"
    lxc exec node-wrk0 -- sh -c "microceph.rbd create --size 512 pool_one/image_two"
    lxc exec node-wrk1 -- sh -c "microceph.rbd create --size 512 pool_two/image_one"
    lxc exec node-wrk1 -- sh -c "microceph.rbd create --size 512 pool_two/image_two"

    # enable mirroring on pool_one
    lxc exec node-wrk0 -- sh -c "microceph replication enable rbd pool_one --remote siteb"

    # enable mirroring on pool_two images
    lxc exec node-wrk0 -- sh -c "microceph replication enable rbd pool_two/image_one --type journal --remote siteb"
    lxc exec node-wrk0 -- sh -c "microceph replication enable rbd pool_two/image_two --type snapshot --remote siteb"
}

function remote_enable_rbd_mirror_daemon() {
    lxc exec node-wrk0 -- sh -c "microceph enable rbd-mirror"
    lxc exec node-wrk2 -- sh -c "microceph enable rbd-mirror"
}

function remote_wait_for_secondary_to_sync() {
    set -eux

    # wait till $1 images are synchronised
    local threshold="${1?missing}"
    count=0
    for index in {1..100}; do
        echo "Check run #$index"
        list_output=$(lxc exec node-wrk2 -- sh -c "sudo microceph replication list rbd --json")
        echo $list_output
        echo $list_output | jq .[].Images > images.json
        jq length ./images.json > lengths
        images=$(awk '{s+=$1} END {print s}' ./lengths)
        if [[ $images -eq $threshold ]] ; then
            break
        fi

        count=$index
        echo "#################"
        sleep 30
    done

    if [$count -eq 100] ; then
        echo "replication sync check timed out"
        exit -1
    fi
}

function remote_verify_rbd_mirroring() {
    set -eux

    lxc exec node-wrk0 -- sh -c "sudo microceph replication list rbd"
    lxc exec node-wrk2 -- sh -c "sudo microceph replication list rbd"
    lxc exec node-wrk0 -- sh -c "sudo microceph replication list rbd" | grep "pool_one.*image_one"
    lxc exec node-wrk1 -- sh -c "sudo microceph replication list rbd" | grep "pool_one.*image_two"
    lxc exec node-wrk2 -- sh -c "sudo microceph replication list rbd" | grep "pool_two.*image_one"
    lxc exec node-wrk3 -- sh -c "sudo microceph replication list rbd" | grep "pool_two.*image_two"
}

function remote_failover_to_siteb() {
    set -eux

    # check images are secondary on siteb
    img_count=$(lxc exec node-wrk2 -- sh -c "sudo microceph replication list rbd --json" | grep -c "\"is_primary\":false")
    if [[ $img_count -lt 1 ]]; then
        echo "Site B has $img_count secondary images"
        exit -1
    fi

    # promote site b to primary
    lxc exec node-wrk2 -- sh -c "sudo microceph replication promote --remote sitea --yes-i-really-mean-it"

    # wait for the site images to show as primary
    is_primary_count=0
    for index in {1..100}; do
        echo "Check run #$index"
        list_output=$(lxc exec node-wrk2 -- sh -c "sudo microceph replication list rbd --json")
        echo $list_output
        images=$(echo $list_output | jq .[].Images)
        echo $images
        is_primary_count=$(echo $images | grep -c "\"is_primary\": true" || true)
        echo $is_primary_count
        if [[ $is_primary_count -gt 0 ]] ; then
            break
        fi

        echo "#################"
        sleep 30
    done
    if [[ $is_primary_count -eq 0 ]] ; then
        echo "No images promoted after 100 rounds."
        exit 1
    fi

    # resolve the split brain situation by demoting the old primary.
    lxc exec node-wrk0 -- sh -c "sudo microceph replication demote --remote siteb --yes-i-really-mean-it"

    # wait for the site images to show as non-primary
    is_primary_count=0
    for index in {1..100}; do
        echo "Check run #$index"
        list_output=$(lxc exec node-wrk0 -- sh -c "sudo microceph replication list rbd --json")
        echo $list_output
        images=$(echo $list_output | jq .[].Images)
        echo $images
        is_primary_count=$(echo $images | grep -c "\"is_primary\": false" || true)
        echo $is_primary_count
        if [[ $is_primary_count -gt 0 ]] ; then
            break
        fi

        echo "#################"
        sleep 30
    done
    if [[ $is_primary_count -eq 0 ]] ; then
        echo "No images demoted after 100 rounds."
        exit 1
    fi
}

function remote_disable_rbd_mirroring() {
    set -eux
    # check disables fail for image mirroring pools with images currently being mirrored
    lxc exec node-wrk2 -- sh -c "sudo microceph replication disable rbd pool_two 2>&1 || true"  | grep "in Image mirroring mode"
    # disable both images in pool_two and then disable pool_two
    lxc exec node-wrk2 -- sh -c "sudo microceph replication disable rbd pool_two/image_one"
    lxc exec node-wrk2 -- sh -c "sudo microceph replication disable rbd pool_two/image_two"
    lxc exec node-wrk2 -- sh -c "sudo microceph replication disable rbd pool_two"

    # disable pool one
    lxc exec node-wrk2 -- sh -c "sudo microceph replication disable rbd pool_one"
}

function remote_remove_and_verify() {
    set -eux

    # Check remote exists
    remotes=$(lxc exec node-wrk0 -- sh -c "microceph remote list --json")
    echo $remotes

    match=$(echo $remotes | grep -c '\"name\":\"siteb\"')
    if [[ $match -ne 1 ]] ; then
        echo "Expected remote record for siteb absent."
        lxc exec node-wrk0 -- sh -c "microceph remote list --json"
        exit -1
    fi

    # Remove the configured remote from MicroCeph
    lxc exec node-wrk0 -- sh -c "microceph remote remove siteb"

    # Verify remote does not exist
    remotes=$(lxc exec node-wrk0 -- sh -c "microceph remote list --json 2>&1 || true")
    echo $remotes

    match=$(echo $remotes | grep -c 'no remotes configured')
    if [[ $match -ne 1 ]] ; then
        echo "Removed remote record still present."
        lxc exec node-wrk0 -- sh -c "microceph remote list --json"
        exit -1
    fi
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

function install_hurl() {
    VERSION=5.0.1
    downloadurl="https://github.com/Orange-OpenSource/hurl/releases/download/$VERSION/hurl_${VERSION}_amd64.deb"
    curl --location --remote-name $downloadurl
    sudo dpkg -i ./hurl_${VERSION}_amd64.deb
}

function hurl() {
    echo "Running hurl $@"
    sudo hurl --unix-socket --test /var/snap/microceph/common/state/control.socket "$@"
}

function upgrade_multinode() {
    # Refresh to local version, checking health
    for container in node-wrk0 node-wrk1 node-wrk2 node-wrk3 ; do
        lxc exec $container -- sh -c "sudo snap install --dangerous /mnt/microceph_*.snap"
        lxc exec $container -- sh -c "snap connect microceph:block-devices ; snap connect microceph:hardware-observe ; snap connect microceph:mount-observe"
        sleep 5
        expect=3
        for i in $(seq 1 20); do
            res=$( ( lxc exec $container -- sh -c "microceph.ceph osd status" | fgrep -c "exists,up" ) )
            if [[ $res -eq $expect ]] ; then
                echo "Found ${expect} osd up"
                break
            else
                echo -n '.'
                sleep 10
            fi
        done
        res=$( ( lxc exec $container -- sh -c "microceph.ceph osd status" | fgrep -c "exists,up" ) )
        if [[ $res -ne $expect ]] ; then
            echo "Expected $expect OSD up, got $res"
            lxc exec $container -- sh -c "microceph.ceph -s"
            exit -1
        fi
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

function verify_health() {
    for i in {0..100}; do
        if [ "$( sudo microceph.ceph health )" = "HEALTH_OK" ] ; then
            echo "HEALTH_OK found"
            return
        fi
        sleep 3
    done
    echo "Cluster did not reach HEALTH_OK"
    sudo microceph.ceph -s
    exit 1
}

function bootstrap_head() {
    set -ex

    local arg=$1
    if [ "$arg" = "public" ]; then
        # Bootstrap microceph on the head node
        local nw=$(get_lxd_network public)
        local gw=$(echo "$nw" | cut -d/ -f1)
        local mask=$(echo "$nw" | cut -d/ -f2)
        local node_ip="${gw}0"
        lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap --public-network=$nw"
        sleep 5
        # Verify ceph.conf
        verify_bootstrap_configs node-wrk0 "${nw}" "${node_ip}"
    elif [ "$arg" = "internal" ]; then
        # Bootstrap microceph on the head node with custom microceph ip.
        local nw=$(get_lxd_network internal)
        local gw=$(echo "$nw" | cut -d/ -f1)
        local node_ip="${gw}0"
        lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap --microceph-ip=$node_ip"
        sleep 10
        # Verify microceph IP
        lxc exec node-wrk0 -- sh -c "microceph status | grep node-wrk0 | grep $node_ip"
    else
        lxc exec node-wrk0 -- sh -c "microceph cluster bootstrap"
    fi

    lxc exec node-wrk0 -- sh -c "microceph status"
    sleep 4
    lxc exec node-wrk0 -- sh -c 'microceph.ceph -s | egrep "mon: 1 daemons, quorum node-wrk0"'
}


function cluster_nodes() {
    set -ex
    local arg=$1

    # Add/join microceph nodes to the cluster
    local nw=$(get_lxd_network $arg)
    local gw=$(echo "$nw" | cut -d/ -f1)
    local mask=$(echo "$nw" | cut -d/ -f2)
    local mon_ips="${gw}0"
    for i in 1 2 3 ; do
        local node_ip="${gw}${i}"

        if [ $arg = "internal" ]; then
            # join microceph cluster using microceph IP
            tok=$(lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk${i}" )
            lxc exec node-wrk${i} -- sh -c "microceph cluster join $tok  --microceph-ip=${node_ip}"
            sleep 10
            # Verify microceph IP
            lxc exec node-wrk0 -- sh -c "microceph status | grep node-wrk${i} | grep ${node_ip}"
        else
            # join microceph cluster using default Microceph IP
            tok=$(lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk${i}" )
            lxc exec node-wrk${i} -- sh -c "microceph cluster join $tok"
        fi

        if [ $arg = "public" ]; then
            # verify ceph.conf
            verify_bootstrap_configs "node-wrk${i}" "${nw}" $mon_ips
            # append mon_ips as a space separated entry.
            mon_ips="$mon_ips $node_ip"
        fi
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

function wait_for_rgw() {
    local expect="${1?missing}"
    res=0
    for i in $(seq 1 8); do
        res=$( ( sudo microceph.ceph -s | grep -F rgw: | sed -E "s/.* ([[:digit:]]*) daemon.*/\1/" ) || true )
        if [[ $res -ge $expect ]] ; then
            echo "Found ${expect} rgw daemon(s)"
            break
        else
            echo -n '.'
            sleep 5
        fi
    done
    sudo microceph.ceph -s
    if [[ $res -lt $expect ]] ; then
        echo "Never reached ${expect} rgw daemon(s)"
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
    local default="test"
    local filename=${1:-default}
    sudo microceph.ceph status
    sudo systemctl status snap.microceph.rgw --no-pager
    if ! $(sudo microceph.radosgw-admin user list | grep -q test) ; then
        echo "Create S3 user: test"
        sudo microceph.radosgw-admin user create --uid=test --display-name=test
        sudo microceph.radosgw-admin key create --uid=test --key-type=s3 --access-key fooAccessKey --secret-key fooSecretKey
    fi
    sudo apt-get update -qq
    sudo apt-get -qq install s3cmd
    echo hello-radosgw > ~/$filename.txt
    s3cmd --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl mb s3://testbucket
    s3cmd --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl put -P ~/$filename.txt s3://testbucket
    ( curl -s http://localhost/testbucket/$filename.txt | grep -F hello-radosgw ) || return -1
}


function testrgw_ssl() {
    set -eu
    local default="test"
    local filename=${1:-default}
    sudo microceph.ceph status
    sudo systemctl status snap.microceph.rgw --no-pager
    if ! $(sudo microceph.radosgw-admin user list | grep -q test) ; then
        echo "Create S3 user: test"
        sudo microceph.radosgw-admin user create --uid=test --display-name=test
        sudo microceph.radosgw-admin key create --uid=test --key-type=s3 --access-key fooAccessKey --secret-key fooSecretKey
    fi
    sudo apt-get update -qq
    echo hello-radosgw-ssl > ~/$filename.txt
    s3cmd --ca-certs=/tmp/ca.crt --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey mb s3://testbucketssl
    s3cmd --ca-certs=/tmp/ca.crt --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey put -P ~/$filename.txt s3://testbucketssl
    ( CURL_CA_BUNDLE=/tmp/ca.crt curl -s https://localhost/testbucketssl/$filename.txt | grep -F hello-radosgw-ssl ) || return -1
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
# Test: must contain public_network
if ! grep -q public_net /var/snap/microceph/current/conf/ceph.conf ; then
    echo "Error: didn't find public_net in ceph.conf"
    cat /var/snap/microceph/current/conf/ceph.conf
    exit -1
fi
EOF
    done
}

# nodeexec <node name> <run>
function node_exec() {
    local node="${1?missing}"
    local run="${2?missing}"
    shift
    set -x
    lxc exec node-wrk0 -- sh -c "/mnt/actionutils.sh $run $@"
}

function headexec() {
    local run="${1?missing}"
    shift
    set -x
    lxc exec node-wrk0 -- sh -c "/mnt/actionutils.sh $run $@"
}

function bombard_rgw_configs() {
    set -x

    # Set random values to rgw configs
    sudo microceph cluster config set s3_auth_use_keystone "true" --skip-restart
    sudo microceph cluster config set rgw_keystone_url "example.url.com" --skip-restart
    sudo microceph cluster config set rgw_keystone_admin_user "admin" --skip-restart
    sudo microceph cluster config set rgw_keystone_admin_password "admin" --skip-restart
    sudo microceph cluster config set rgw_keystone_admin_project "project" --skip-restart
    sudo microceph cluster config set rgw_keystone_admin_domain "domain" --skip-restart
    sudo microceph cluster config set rgw_keystone_service_token_enabled "true" --skip-restart
    sudo microceph cluster config set rgw_keystone_service_token_accepted_roles "admin_role" --skip-restart
    sudo microceph cluster config set rgw_keystone_api_version "3" --skip-restart
    sudo microceph cluster config set rgw_keystone_accepted_roles "Member,member" --skip-restart
    sudo microceph cluster config set rgw_keystone_accepted_admin_roles "admin_role" --skip-restart
    sudo microceph cluster config set rgw_keystone_token_cache_size "500" --skip-restart
    # Issue last change with daemon restart.
    sudo microceph cluster config set rgw_keystone_verify_ssl "false" --wait

    # Allow ceph to notice no OSD are present
    sleep 30
    sudo microceph.ceph status
    sudo microceph.ceph health
}

run="${1}"
shift

$run "$@"
