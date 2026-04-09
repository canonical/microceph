.. meta::
   :description: How to add physical disk OSDs to a MicroCeph charm cluster.

Add physical disk OSDs
======================

OSDs are added by triggering the ``add-osd`` action on each microceph unit.
The unit's underlying machine is understood to house one or more storage devices.

#. List the available disks on the microceph node:

   .. code-block:: text

      juju run microceph/0 list-disks

   The output specifies the OSDs already attached to MicroCeph and any
   unpartitioned disks that can be added as OSD. Sample output:

   .. code-block:: text

      Running operation 1 with 1 task
        - task 2 on unit-microceph-0
      Waiting for task 2...
      osds: '[]'
      unpartitioned-disks: '[{''model'': '''', ''size'': ''10.00GiB'', ''type'': ''virtio'',
        ''path'': ''/dev/disk/by-id/virtio-71aa0fef-aec9-4129-9''}, {''model'': '''', ''size'':
        ''40.00GiB'', ''type'': ''virtio'', ''path'': ''/dev/disk/by-id/''}]'

#. Add the unpartitioned disks as OSDs to the microceph cluster:

   .. code-block:: text

      juju run microceph/0 add-osd device-id=<DISK PATH>

   Multiple disks can be added in the same action:

   .. code-block:: text

      juju run microceph/0 add-osd device-id=<DISK PATH>,<DISK PATH>

   Sample output:

   .. code-block:: text

      $ juju run microceph/0 add-osd device-id=/dev/disk/by-id/virtio-71aa0fef-aec9-4129-9
      Running operation 3 with 1 task
        - task 4 on unit-microceph-0
      Waiting for task 4...
      status: success

#. Verify that the disks are added as OSDs to the Ceph cluster:

   .. code-block:: text

      juju run microceph/0 list-disks

   The added disks should now be visible in the OSDs list. Sample output:

   .. code-block:: text

      Running operation 5 with 1 task
        - task 6 on unit-microceph-0
      Waiting for task 6...
      osds: '[{''osd'': ''0'', ''location'': ''microceph2'', ''path'': ''/dev/disk/by-id/virtio-71aa0fef-aec9-4129-9''}]'
      unpartitioned-disks: '[{''model'': '''', ''size'': ''40.00GiB'', ''type'': ''virtio'',
        ''path'': ''/dev/disk/by-id/''}]'

#. Run steps 1–3 on all storage nodes.

#. Run ceph cluster status to check if OSDs are up:

   .. code-block:: text

      juju ssh microceph/leader sudo microceph.ceph status

   Sample output:

   .. code-block:: text

        cluster:
          id:     edd914f5-fdf8-4b56-bdd7-95d6c5e10d81
          health: HEALTH_OK

        services:
          mon: 3 daemons, quorum microceph2,microceph3,microceph4 (age 12m)
          mgr: microceph2(active, since 13m), standbys: microceph3, microceph4
          osd: 3 osds: 3 up (since 34s), 3 in (since 56s)

        data:
          pools:   1 pools, 1 pgs
          objects: 2 objects, 577 KiB
          usage:   66 MiB used, 30 GiB / 30 GiB avail
          pgs:     1 active+clean

        io:
          client:   938 B/s rd, 43 KiB/s wr, 0 op/s rd, 1 op/s wr

The Ceph cluster is now healthy and ready to use.
