Initial Setup
==================================================================

.. code-block:: shell

    lxc exec microceph-1 microceph cluster bootstrap
    lxc exec microceph-1 microceph cluster add microceph-2
    lxc exec microceph-1 microceph cluster add microceph-3

The output of the above will look like:

.. code-block:: shell

    eyJuYW1lIjoibWljcm9jZXBoLTIiLCJzZWNyZXQiOiIwNWJkYzA3OTJkZmI4MDhjM2ZkMWJjNmIyMDFiMTUxMmI5NmY2N2QyNGEwMTdkZTFjMDNkOWIxZTBhZWFmZDI3IiwiZmluZ2VycHJpbnQiOiJkMjRkOGJiYjY0MTgyMWFlMjhkY2VlYWM2YmNkMGU4MmY1M2U2OTdmNDJjM2EyZTc0ZjhkMTk4MDhmNzZiNjgyIiwiam9pbl9hZGRyZXNzZXMiOlsiMTAuNjIuNzMuMTE0Ojc0NDMiXX0=
    eyJuYW1lIjoibWljcm9jZXBoLTMiLCJzZWNyZXQiOiIyOWJhZGExZGYzYTMyYjJiNDRiZDIwYTliM2QwNGRiNzBjMjYzMTZlZjZmYjkzYTJhOTVkYjgzMWEwMmFjNGYwIiwiZmluZ2VycHJpbnQiOiJkMjRkOGJiYjY0MTgyMWFlMjhkY2VlYWM2YmNkMGU4MmY1M2U2OTdmNDJjM2EyZTc0ZjhkMTk4MDhmNzZiNjgyIiwiam9pbl9hZGRyZXNzZXMiOlsiMTAuNjIuNzMuMTE0Ojc0NDMiXX0=
    
Each line above is a token to be used, once, to join another node to the
cluster, specifically microceph-2 and microceph-3, in order.

.. code-block:: shell

    lxc exec microceph-2 microceph cluster join eyJuYW1lIjoibWljcm9jZXBoLTIiLCJzZWNyZXQiOiIwNWJkYzA3OTJkZmI4MDhjM2ZkMWJjNmIyMDFiMTUxMmI5NmY2N2QyNGEwMTdkZTFjMDNkOWIxZTBhZWFmZDI3IiwiZmluZ2VycHJpbnQiOiJkMjRkOGJiYjY0MTgyMWFlMjhkY2VlYWM2YmNkMGU4MmY1M2U2OTdmNDJjM2EyZTc0ZjhkMTk4MDhmNzZiNjgyIiwiam9pbl9hZGRyZXNzZXMiOlsiMTAuNjIuNzMuMTE0Ojc0NDMiXX0=
    lxc exec microceph-3 microceph cluster join eyJuYW1lIjoibWljcm9jZXBoLTMiLCJzZWNyZXQiOiIyOWJhZGExZGYzYTMyYjJiNDRiZDIwYTliM2QwNGRiNzBjMjYzMTZlZjZmYjkzYTJhOTVkYjgzMWEwMmFjNGYwIiwiZmluZ2VycHJpbnQiOiJkMjRkOGJiYjY0MTgyMWFlMjhkY2VlYWM2YmNkMGU4MmY1M2U2OTdmNDJjM2EyZTc0ZjhkMTk4MDhmNzZiNjgyIiwiam9pbl9hZGRyZXNzZXMiOlsiMTAuNjIuNzMuMTE0Ojc0NDMiXX0=

You should now have a Ceph monitor cluster with quorum:

.. code-block:: shell

    $ lxc exec microceph-1 ceph status
    cluster:
        id:     a95eaf13-c3fe-466a-8278-d92186effbec
        health: HEALTH_WARN
                OSD count 0 < osd_pool_default_size 3
    
    services:
        mon: 3 daemons, quorum microceph-1,microceph-2,microceph-3 (age 6s)
        mgr: microceph-1(active, since 4m), standbys: microceph-2
        osd: 0 osds: 0 up, 0 in
    
    data:
        pools:   0 pools, 0 pgs
        objects: 0 objects, 0 B
        usage:   0 B used, 0 B / 0 B avail
        pgs

