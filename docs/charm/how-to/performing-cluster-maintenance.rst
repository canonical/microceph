.. meta::
   :description: How to perform cluster maintenance with charm-microceph.

Perform cluster maintenance
============================

MicroCeph provides a simple and consistent workflow to support cluster
maintenance activity.

Prerequisites
-------------

Cluster maintenance requires extra redundancy in Ceph services. Make sure you
have at least 3 units of MicroCeph.

For example, a three-node MicroCeph cluster would look similar to this:

.. code-block:: text

   Model  Controller  Cloud/Region         Version  SLA          Timestamp
   ceph   overlord    localhost/localhost  3.6.8    unsupported  10:13:41+08:00

   App        Version  Status  Scale  Charm      Channel       Rev  Exposed  Message
   microceph           active      3  microceph  squid/stable  155  no

   Unit          Workload  Agent  Machine  Public address  Ports  Message
   microceph/0   active    idle   0        10.42.75.116
   microceph/1*  active    idle   1        10.42.75.230
   microceph/2   active    idle   2        10.42.75.218

   Machine  State    Address       Inst id        Base          AZ  Message
   0        started  10.42.75.116  juju-6ae532-0  ubuntu@24.04      Running
   1        started  10.42.75.230  juju-6ae532-1  ubuntu@24.04      Running
   2        started  10.42.75.218  juju-6ae532-2  ubuntu@24.04      Running

   MicroCeph deployment summary:
   - juju-6ae532-0 (10.42.75.116)
     Services: mds, mgr, mon, osd
     Disks: 1
   - juju-6ae532-1 (10.42.75.230)
     Services: mds, mgr, mon, osd
     Disks: 1
   - juju-6ae532-2 (10.42.75.218)
     Services: mds, mgr, mon, osd
     Disks: 1

Review the action plan
-----------------------

The action plan for entering or exiting maintenance mode can be reviewed using
the dry-run option:

.. code-block:: shell

   juju run microceph/leader exit-maintenance dry-run=True
   juju run microceph/leader enter-maintenance dry-run=True

To see which steps in the action plan are optional, run:

.. code-block:: shell

   juju show-action microceph exit-maintenance
   juju show-action microceph enter-maintenance

Enter maintenance mode
-----------------------

To put unit ``microceph/2`` into maintenance mode, and optionally disable the
OSD service on that node, run:

.. code-block:: shell

   juju run microceph/2 enter-maintenance stop-osds=True

Sample output:

.. code-block:: text

   Running operation 17 with 1 task
     - task 18 on unit-microceph-2

   Waiting for task 18...
   actions:
     step-1:
       description: Check if osds.[3] in node 'juju-6ae532-2' are ok-to-stop.
       error: ""
       id: check-osd-ok-to-stop-ops
     step-2:
       description: Check if there are at least a majority of mon services, 1 mds service,
         and 1 mgr service in the cluster besides those in node 'juju-6ae532-2'
       error: ""
       id: check-non-osd-svc-enough-ops
     step-3:
       description: Run `ceph osd set noout`.
       error: ""
       id: set-noout-ops
     step-4:
       description: Assert osd has 'noout' flag set.
       error: ""
       id: assert-noout-flag-set-ops
     step-5:
       description: Stop osd service in node 'juju-6ae532-2'.
       error: ""
       id: stop-osd-ops
   errors: ""
   status: success

After entering maintenance mode, the cluster status looks like this:

.. code-block:: text

   $ juju ssh microceph/2 -- sudo snap services microceph
   Service                  Startup   Current   Notes
   microceph.cephfs-mirror  disabled  inactive  -
   microceph.daemon         enabled   active    -
   microceph.mds            enabled   active    -
   microceph.mgr            enabled   active    -
   microceph.mon            enabled   active    -
   microceph.nfs            disabled  inactive  -
   microceph.osd            disabled  inactive  -
   microceph.rbd-mirror     disabled  inactive  -
   microceph.rgw            disabled  inactive  -

   $ juju ssh microceph/2 -- sudo microceph.ceph -s
     cluster:
       id:     91da3928-adbb-4675-8dc0-52bb2a07e027
       health: HEALTH_WARN
               mons juju-6ae532-0,juju-6ae532-1,juju-6ae532-2 are low on available space
               noout flag(s) set
               1 osds down
               1 host (1 osds) down
               Degraded data redundancy: 2/6 objects degraded (33.333%), 1 pg degraded, 1 pg undersized

     services:
       mon: 3 daemons, quorum juju-6ae532-1,juju-6ae532-0,juju-6ae532-2 (age 9m)
       mgr: juju-6ae532-1(active, since 10m), standbys: juju-6ae532-0, juju-6ae532-2
       osd: 3 osds: 2 up (since 64s), 3 in (since 7m)
            flags noout

     data:
       pools:   1 pools, 1 pgs
       objects: 2 objects, 449 KiB
       usage:   81 MiB used, 12 GiB / 12 GiB avail
       pgs:     2/6 objects degraded (33.333%)
                1 active+undersized+degraded

Compare this with the cluster status before entering maintenance mode:

.. code-block:: text

   $ juju ssh microceph/2 -- sudo snap services microceph
   Service                  Startup   Current   Notes
   microceph.cephfs-mirror  disabled  inactive  -
   microceph.daemon         enabled   active    -
   microceph.mds            enabled   active    -
   microceph.mgr            enabled   active    -
   microceph.mon            enabled   active    -
   microceph.nfs            disabled  inactive  -
   microceph.osd            enabled   active    -
   microceph.rbd-mirror     disabled  inactive  -
   microceph.rgw            disabled  inactive  -

   $ juju ssh microceph/2 -- sudo microceph.ceph -s
     cluster:
       id:     91da3928-adbb-4675-8dc0-52bb2a07e027
       health: HEALTH_WARN
               mons juju-6ae532-0,juju-6ae532-1,juju-6ae532-2 are low on available space

     services:
       mon: 3 daemons, quorum juju-6ae532-1,juju-6ae532-0,juju-6ae532-2 (age 12m)
       mgr: juju-6ae532-1(active, since 12m), standbys: juju-6ae532-0, juju-6ae532-2
       osd: 3 osds: 3 up (since 50s), 3 in (since 9m)

     data:
       pools:   1 pools, 1 pgs
       objects: 2 objects, 449 KiB
       usage:   481 MiB used, 12 GiB / 12 GiB avail
       pgs:     1 active+clean

.. note::

   The ``microceph.osd`` service is disabled and inactive after entering
   maintenance mode; the cluster also has the ``noout`` flag set.

Exit maintenance mode
---------------------

To recover unit ``microceph/2`` from maintenance mode, run:

.. code-block:: shell

   juju run microceph/2 exit-maintenance

Sample output:

.. code-block:: text

   $ juju run microceph/2 exit-maintenance

   Running operation 19 with 1 task
     - task 20 on unit-microceph-2

   Waiting for task 20...
   actions:
     step-1:
       description: Run `ceph osd unset noout`.
       error: ""
       id: unset-noout-ops
     step-2:
       description: Assert osd has 'noout' flag unset.
       error: ""
       id: assert-noout-flag-unset-ops
     step-3:
       description: Start osd service in node 'juju-6ae532-2'.
       error: ""
       id: start-osd-ops
   errors: ""
   status: success

The cluster status after exiting maintenance mode for unit ``microceph/2``:

.. code-block:: text

   $ juju ssh microceph/2 -- sudo snap services microceph
   Service                  Startup   Current   Notes
   microceph.cephfs-mirror  disabled  inactive  -
   microceph.daemon         enabled   active    -
   microceph.mds            enabled   active    -
   microceph.mgr            enabled   active    -
   microceph.mon            enabled   active    -
   microceph.nfs            disabled  inactive  -
   microceph.osd            enabled   active    -
   microceph.rbd-mirror     disabled  inactive  -
   microceph.rgw            disabled  inactive  -

   $ juju ssh microceph/2 -- sudo microceph.ceph -s
     cluster:
       id:     91da3928-adbb-4675-8dc0-52bb2a07e027
       health: HEALTH_WARN
               mons juju-6ae532-0,juju-6ae532-1,juju-6ae532-2 are low on available space

     services:
       mon: 3 daemons, quorum juju-6ae532-1,juju-6ae532-0,juju-6ae532-2 (age 16m)
       mgr: juju-6ae532-1(active, since 16m), standbys: juju-6ae532-0, juju-6ae532-2
       osd: 3 osds: 3 up (since 4m), 3 in (since 13m)

     data:
       pools:   1 pools, 1 pgs
       objects: 2 objects, 449 KiB
       usage:   481 MiB used, 12 GiB / 12 GiB avail
       pgs:     1 active+clean

.. note::

   The ``microceph.osd`` service is enabled and active again after exiting
   maintenance mode; the cluster no longer has the ``noout`` flag set.
