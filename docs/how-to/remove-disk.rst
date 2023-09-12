=============
Remove a disk
=============

Overview
--------

There are valid reasons for wanting to remove a disk from a Ceph cluster. A
common use case is the need to replace one that has been identified as nearing
its shelf life. Another example is the desire to scale down the cluster through
the removal of a cluster node (machine).

The following resources provide extra context to the disk removal operation:

* the :doc:`../../explanation/scaling` page
* the :doc:`disk <../reference/commands/disk>` command reference

.. note::

   This feature is currently only supported in channel ``latest/edge`` of the
   microstack snap.

Procedure
---------

First get an overview of the cluster and its OSDs:

.. code-block:: none

   ceph status

Example output:

.. code-block:: none

     cluster:
       id:     cf16e5a8-26b2-4f9d-92be-dd3ac9602ebf
       health: HEALTH_OK

     services:
       mon: 3 daemons, quorum node-01,node-02,node-03 (age 41h)
       mgr: node-01(active, since 41h), standbys: node-02, node-03
       osd: 5 osds: 5 up (since 22h), 5 in (since 22h); 1 remapped pgs

     data:
       pools:   1 pools, 1 pgs
       objects: 2 objects, 577 KiB
       usage:   105 MiB used, 1.9 TiB / 1.9 TiB avail
       pgs:     2/6 objects misplaced (33.333%)
                1 active+clean+remapped

Then determine the ID of the OSD associated with the disk with the (native
Ceph) :command:`ceph osd tree` command:

.. code-block:: none

   ceph osd tree

Sample output:

.. code-block:: none

   ID  CLASS  WEIGHT   TYPE NAME              STATUS  REWEIGHT  PRI-AFF
   -1         1.87785  root default
   -5         1.81940      host node-mees
    3         0.90970          osd.3              up   1.00000  1.00000
    4         0.90970          osd.4              up   1.00000  1.00000
   -2         0.01949      host node-01
    0         0.01949          osd.0              up   1.00000  1.00000
   -3         0.01949      host node-02
    1         0.01949          osd.1              up   1.00000  1.00000
   -4         0.01949      host node-03
    2         0.01949          osd.2              up   1.00000  1.00000

Let's assume that our target disk is on host 'node-mees' and has an associated
OSD whose ID is 'osd.4'.

To remove the disk:

.. code-block:: none

   sudo microceph disk remove osd.4

Verify that the OSD has been removed:

.. code-block:: none

   ceph osd tree

Output:

.. code-block:: none

   ID  CLASS  WEIGHT   TYPE NAME              STATUS  REWEIGHT  PRI-AFF
   -1         0.96815  root default
   -5         0.90970      host node-mees
    3    hdd  0.90970          osd.3              up   1.00000  1.00000
   -2         0.01949      host node-01
    0    hdd  0.01949          osd.0              up   1.00000  1.00000
   -3         0.01949      host node-02
    1    hdd  0.01949          osd.1              up   1.00000  1.00000
   -4         0.01949      host node-03
    2    hdd  0.01949          osd.2              up   1.00000  1.00000

Finally, confirm cluster status and health:

.. code-block:: none

   ceph status

Output:

.. code-block:: none

     cluster:
       id:     cf16e5a8-26b2-4f9d-92be-dd3ac9602ebf
       health: HEALTH_OK

     services:
       mon: 3 daemons, quorum node-01,node-02,node-03 (age 4m)
       mgr: node-01(active, since 4m), standbys: node-02, node-03
       osd: 4 osds: 4 up (since 4m), 4 in (since 4m)

     data:
       pools:   1 pools, 1 pgs
       objects: 2 objects, 577 KiB
       usage:   68 MiB used, 991 GiB / 992 GiB avail
       pgs:     1 active+clean
