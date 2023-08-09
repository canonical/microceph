Add OSDs
==================================================================

.. code-block:: shell

    lxc exec microceph-1 -- microceph disk add /dev/sdb --wipe
    lxc exec microceph-2 -- microceph disk add /dev/sdb --wipe
    lxc exec microceph-3 -- microceph disk add /dev/sdb --wipe

Your ceph status should now show three OSDs as well:

.. code-block:: shell

    $ lxc exec microceph-1 ceph status
    cluster:
        id:     a95eaf13-c3fe-466a-8278-d92186effbec
        health: HEALTH_OK
    
    services:
        mon: 3 daemons, quorum microceph-1,microceph-2,microceph-3 (age 11m)
        mgr: microceph-1(active, since 15m), standbys: microceph-2, microceph-3
        osd: 3 osds: 3 up (since 8s), 3 in (since 12s)
    
    data:
        pools:   1 pools, 1 pgs
        objects: 2 objects, 577 KiB
        usage:   65 MiB used, 30 GiB / 30 GiB avail
        pgs:     1 active+clean
