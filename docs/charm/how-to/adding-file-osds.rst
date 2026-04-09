.. meta::
   :description: How to add virtual OSDs for test clusters using the MicroCeph charm.

Add virtual OSDs for test clusters
===================================

MicroCeph supports creating and using files as backing devices for OSDs. This is
a handy feature for testing MicroCeph without committing physical block devices.

OSDs are added by triggering the ``add-osd`` action on a microceph unit.

Procedure
---------

#. Add file-based OSDs to the microceph cluster.

   Add a single 4G file-based OSD:

   .. code-block:: text

      juju run microceph/0 add-osd loop-spec="4G,1"

   Multiple OSDs (count N) can be added in the same action:

   .. code-block:: text

      juju run microceph/0 add-osd loop-spec="4G,<N>"

   Sample output:

   .. code-block:: text

      $ juju run microceph/0 add-osd loop-spec="4G,3"
      Running operation 3 with 1 task
        - task 4 on unit-microceph-0
      Waiting for task 4...
      status: success

#. Run ceph cluster status to check if OSDs are up:

   .. code-block:: text

      juju ssh microceph/leader sudo ceph status

   Sample output:

   .. code-block:: text

        cluster:
          id:     edd914f5-fdf8-4b56-bdd7-95d6c5e10d81
          health: HEALTH_OK

        services:
          mon: 1 daemons, quorum juju-ae397f-mcu-3 (age 20m)
          mgr: juju-ae397f-mcu-3(active, since 20m)
          osd: 3 osds: 3 up (since 7s), 3 in (since 10s)

        data:
          pools:   1 pools, 1 pgs
          objects: 2 objects, 449 KiB
          usage:   79 MiB used, 12 GiB / 12 GiB avail
          pgs:     1 active+clean

        io:
          client:   938 B/s rd, 43 KiB/s wr, 0 op/s rd, 1 op/s wr

The Ceph cluster is now healthy and ready to use.
