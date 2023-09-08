==========
Multi-node
==========

This tutorial will show how to install MicroCeph on three machines, thereby
creating a multi-node cluster.

Ensure storage requirements
---------------------------

Three OSDs will be required to form a minimal Ceph cluster. This means that, on
each of the three machines, one entire disk must be allocated for storage.

The disk subsystem can be inspected with the :command:`lsblk` command. In this
tutorial, the command's output on each machine looks very similar to what's
shown below. Any output related to possible loopback devices has been
suppressed for the purpose of clarity:

.. code-block:: none

   lsblk | grep -v loop

   NAME   MAJ:MIN RM   SIZE RO TYPE MOUNTPOINTS
   vda    252:0    0    40G  0 disk
   ├─vda1 252:1    0     1M  0 part
   └─vda2 252:2    0    40G  0 part /
   vdb    252:16   0    20G  0 disk

For the example cluster, each machine will use ``/dev/vdb`` for storage.

Prepare the three machines
--------------------------

On each of the three machines we will need to:

* install the software
* disable auto-updates of the software

Below we'll show these steps explicitly on **node-1**, which we'll call the
primary node.

Install the most recent stable release of MicroCeph:

.. code-block:: none

   sudo snap install microceph

Prevent the software from being auto-updated:

.. code-block:: none

   sudo snap refresh --hold microceph

Repeat the above three steps for node-2 and node-3.

Prepare the cluster
-------------------

On **node-1** we will now:

* initialis the cluster
* create registration tokens

Initialise the cluster with the :command:`cluster bootstrap` command:

.. code-block:: none

   sudo microceph cluster bootstrap

Tokens are needed to join the other two nodes to the cluster. Generate these
with the :command:`cluster add` command.

Token for node-2:

.. code-block:: none

   sudo microceph cluster add node-2

   eyJuYW1lIjoibm9kZS0yIiwic2VjcmV0IjoiYmRjMzZlOWJmNmIzNzhiYzMwY2ZjOWVmMzRjNDM5YzNlZTMzMTlmZDIyZjkxNmJhMTI1MzVkZmZiMjA2MTdhNCIsImZpbmdlcnByaW50IjoiMmU0MmEzYjEwYTg1MDcwYTQ1MDcyODQxZjAyNWY5NGE0OTc4NWU5MGViMzZmZGY0ZDRmODhhOGQyYjQ0MmUyMyIsImpvaW5fYWRkcmVzc2VzIjpbIjEwLjI0Ni4xMTQuMTE6NzQ0MyJdfQ==

Token for node-3:

.. code-block:: none

   sudo microceph cluster add node-3

   eyJuYW1lIjoibm9kZS0zIiwic2VjcmV0IjoiYTZjYWJjOTZiNDJkYjg0YTRkZTFiY2MzY2VkYTI1M2Y4MTU1ZTNhYjAwYWUyOWY1MDA4ZWQzY2RmOTYzMjBmMiIsImZpbmdlcnByaW50IjoiMmU0MmEzYjEwYTg1MDcwYTQ1MDcyODQxZjAyNWY5NGE0OTc4NWU5MGViMzZmZGY0ZDRmODhhOGQyYjQ0MmUyMyIsImpvaW5fYWRkcmVzc2VzIjpbIjEwLjI0Ni4xMTQuMTE6NzQ0MyJdfQ==

Keep these tokens in a safe place. They'll be needed in the next step.

Join the non-primary nodes to the cluster
-----------------------------------------

The :command:`cluster join` command is used to join nodes to a cluster.

On **node-2**, add the machine to the cluster using the token assigned to
node-2:

.. code-block:: none

   sudo microceph cluster join eyJuYW1lIjoibm9kZS0yIiwic2VjcmV0IjoiYmRjMzZlOWJmNmIzNzhiYzMwY2ZjOWVmMzRjNDM5YzNlZTMzMTlmZDIyZjkxNmJhMTI1MzVkZmZiMjA2MTdhNCIsImZpbmdlcnByaW50IjoiMmU0MmEzYjEwYTg1MDcwYTQ1MDcyODQxZjAyNWY5NGE0OTc4NWU5MGViMzZmZGY0ZDRmODhhOGQyYjQ0MmUyMyIsImpvaW5fYWRkcmVzc2VzIjpbIjEwLjI0Ni4xMTQuMTE6NzQ0MyJdfQ==

On **node-3**, add the machine to the cluster using the token assigned to
node-3:

.. code-block:: none

   sudo microceph cluster join eyJuYW1lIjoibm9kZS0zIiwic2VjcmV0IjoiYTZjYWJjOTZiNDJkYjg0YTRkZTFiY2MzY2VkYTI1M2Y4MTU1ZTNhYjAwYWUyOWY1MDA4ZWQzY2RmOTYzMjBmMiIsImZpbmdlcnByaW50IjoiMmU0MmEzYjEwYTg1MDcwYTQ1MDcyODQxZjAyNWY5NGE0OTc4NWU5MGViMzZmZGY0ZDRmODhhOGQyYjQ0MmUyMyIsImpvaW5fYWRkcmVzc2VzIjpbIjEwLjI0Ni4xMTQuMTE6NzQ0MyJdfQ==

Add storage
-----------

.. warning::

   This step will remove the data found on the target storage disks. Make sure
   you don't lose data unintentionally.

On **each** of the three machines, use the :command:`disk add` command to add
storage:

.. code-block:: none

   sudo microceph disk add /dev/vdb --wipe

Adjust the above command per machine according to the storage disks at your
disposal.

Check MicroCeph status
----------------------

On any of the three nodes, the :command:`status` command can be invoked to
check the status of MicroCeph:

.. code-block:: none

   sudo microceph status

   MicroCeph deployment summary:
   - node-01 (10.246.114.11)
     Services: mds, mgr, mon, osd
     Disks: 1
   - node-02 (10.246.114.47)
     Services: mds, mgr, mon, osd
     Disks: 1
   - node-03 (10.246.115.11)
     Services: mds, mgr, mon, osd
     Disks: 1

Machine hostnames are given along with their IP addresses. The MDS, MGR, MON,
and OSD services are running and each node is supplying a single disk, as
expected.

Manage the cluster
------------------

Your Ceph cluster is now deployed and can be managed by following the resources
found in the :doc:`Howto <../how-to/index>` section.

The cluster can also be managed using native Ceph tooling if snap-level
commands are not yet available for a desired task by appending a native command
to the :command:`microceph` command. This is the equivalent to the standard
:command:`ceph status` command for instance:

.. code-block:: none

   microceph.ceph status

This gives:

.. code-block:: none

     cluster:
       id:     cf16e5a8-26b2-4f9d-92be-dd3ac9602ebf
       health: HEALTH_OK

     services:
       mon: 3 daemons, quorum node-01,node-02,node-03 (age 14m)
       mgr: node-01(active, since 43m), standbys: node-02, node-03
       osd: 3 osds: 3 up (since 4s), 3 in (since 6s)

     data:
       pools:   1 pools, 1 pgs
       objects: 0 objects, 0 B
       usage:   336 MiB used, 60 GiB / 60 GiB avail
       pgs:     100.000% pgs unknown
                1 unknown

Naturally you are free to use Ceph commands directly: :command:`sudo ceph
status`.
