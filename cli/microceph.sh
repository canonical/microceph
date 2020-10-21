#!/bin/bash -eux

function init {
    CEPH_CONF=$SNAP_COMMON/etc/ceph/ceph.conf
    FSID=$(uuidgen)
    SHORTNAME=$(hostname -s)
    ADDR_LIST='[v2:127.0.0.1:3300/0,v1:127.0.0.1:6789/0]'

    cat << EOF > $CEPH_CONF
[global]
fsid = $FSID
mon initial members = $SHORTNAME
mon host = $ADDR_LIST
admin socket = $SNAP_COMMON/var/run/ceph/\$cluster-\$name.asok
pid file = $SNAP_COMMON/var/run/ceph/\$type.\$id.pid
EOF

    ceph-authtool \
        --create-keyring /tmp/ceph.mon.keyring \
        --gen-key -n mon. --cap mon 'allow *'

    ceph-authtool \
        --create-keyring /etc/ceph/ceph.client.admin.keyring \
        --gen-key -n client.admin \
        --cap mon 'allow *' \
        --cap osd 'allow *' \
        --cap mds 'allow *' \
        --cap mgr 'allow *'

    ceph-authtool \
        --create-keyring /var/lib/ceph/bootstrap-osd/ceph.keyring \
        --gen-key -n client.bootstrap-osd \
        --cap mon 'profile bootstrap-osd' \
        --cap mgr 'allow r'

    ceph-authtool \
        /tmp/ceph.mon.keyring \
        --import-keyring /etc/ceph/ceph.client.admin.keyring

    ceph-authtool \
        /tmp/ceph.mon.keyring \
        --import-keyring /var/lib/ceph/bootstrap-osd/ceph.keyring

    monmaptool \
        --create --addv $SHORTNAME $ADDR_LIST \
        --fsid $FSID \
        /tmp/monmap

    mkdir -p /var/lib/ceph/mon/ceph-$SHORTNAME

    ceph-mon \
        --mkfs -i $SHORTNAME \
        --monmap /tmp/monmap \
        --keyring /tmp/ceph.mon.keyring

    echo "You can now start the Ceph MON by typing:"
    echo "    microceph.ceph-mon -d --cluster ceph --id $SHORTNAME"
}


## MAIN ##
case $1 in
    'init')
        init
	;;
    *)
        echo "usage: $0 init"
	;;
esac
