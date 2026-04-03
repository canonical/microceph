.. meta::
   :description: How to enable and disable the Ceph RADOS Gateway in charm-microceph.

Enable RGW
==========

Enable or disable the Ceph RADOS Gateway (RGW) by setting the ``enable-rgw``
configuration option.

#. Enable the RGW service:

   .. code-block:: text

      juju config microceph enable-rgw="*"

#. Check MicroCeph status to confirm that RGW is running on each node:

   .. code-block:: text

      juju ssh microceph/leader sudo microceph status

   Sample output:

   .. code-block:: text

      MicroCeph deployment summary:
      - microceph2 (10.121.193.184)
        Services: mds, mgr, mon, rgw, osd
        Disks: 1
      - microceph3 (10.121.193.185)
        Services: mds, mgr, mon, rgw, osd
        Disks: 1
      - microceph4 (10.121.193.186)
        Services: mds, mgr, mon, rgw, osd
        Disks: 1

#. Run ``ceph cluster status`` to confirm the RGW daemon is running:

   .. code-block:: text

      juju ssh microceph/leader sudo microceph.ceph status

   The output should list ``rgw`` under services. Sample output:

   .. code-block:: text

        cluster:
          id:     edd914f5-fdf8-4b56-bdd7-95d6c5e10d81
          health: HEALTH_OK

        services:
          mon: 3 daemons, quorum microceph2,microceph3,microceph4 (age 12m)
          mgr: microceph2(active, since 13m), standbys: microceph3, microceph4
          osd: 3 osds: 3 up (since 34s), 3 in (since 56s)
          rgw: 3 daemons, quorum microceph2,microceph3,microceph4 (age 30s)

        data:
          pools:   5 pools, 5 pgs
          objects: 2 objects, 577 KiB
          usage:   66 MiB used, 30 GiB / 30 GiB avail
          pgs:     5 active+clean

        io:
          client:   938 B/s rd, 43 KiB/s wr, 0 op/s rd, 1 op/s wr

   The Ceph cluster is healthy and ready to use.

#. To disable the RGW service:

   .. code-block:: text

      juju config microceph enable-rgw=""
