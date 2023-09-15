===========
Single-node
===========

This tutorial will show how to install MicroCeph on a single machine, thereby
creating a single-node "cluster".

Ensure storage requirements
---------------------------

Three OSDs will be required to form a minimal Ceph cluster. This means that
three entire disks are required to be available on the machine.

.. note::

   Upstream Ceph development is underway to allow for loopback device support.
   This will filter down to MicroCeph which will allow for easier
   proof-of-concept and developer deployments.

The disk subsystem can be inspected with the :command:`lsblk` command. In this
tutorial, the command's output is shown below. Any output related to possible
loopback devices has been suppressed for the purpose of clarity:

.. code-block:: none

   lsblk | grep -v loop

   NAME        MAJ:MIN RM   SIZE RO TYPE MOUNTPOINTS
   sda           8:0    0 931.5G  0 disk
   sdb           8:16   0 931.5G  0 disk
   sdc           8:32   0 931.5G  0 disk
   sdd           8:48   0 931.5G  0 disk
   nvme0n1     259:0    0 372.6G  0 disk
   ├─nvme0n1p1 259:1    0   512M  0 part /boot/efi
   └─nvme0n1p2 259:2    0 372.1G  0 part /

There are four disks available, here we will use disks ``/dev/sda``,
``/dev/sdb``, and ``/dev/sdc``.

Install the software
--------------------

Install the most recent stable release of MicroCeph:

.. code-block:: none

   sudo snap install microceph

Next, prevent the software from being auto-updated:

.. code-block:: none

   sudo snap refresh --hold microceph

.. caution::

   Allowing the snap to be auto-updated can lead to unintended consequences. In
   enterprise environments especially, it is better to research the
   ramifications of software changes before those changes are implemented.

Initialise the cluster
----------------------

Begin by initialising the cluster with the :command:`cluster bootstrap`
command:

.. code-block:: none

   sudo microceph cluster bootstrap

Then look at the status of the cluster with the :command:`status` command:

.. code-block:: none

   sudo microceph status

It should look similar to the following:

.. code-block:: none

   MicroCeph deployment summary:
   - node-mees (10.246.114.49)
       Services: mds, mgr, mon
         Disks: 0

Here, the machine's hostname of 'node-mees' is given along with its IP address
of '10.246.114.49'. The MDS, MGR, and MON services are running but there is not
yet any storage available.

Add storage
-----------

.. warning::

   This step will remove the data found on the target storage disks. Make sure
   you don't lose data unintentionally.

Add the three disks to the cluster by using the :command:`disk add` command:

.. code-block:: none

   sudo microceph disk add /dev/sda --wipe
   sudo microceph disk add /dev/sdb --wipe
   sudo microceph disk add /dev/sdc --wipe

Adjust the above commands according to the storage disks at your disposal.

Recheck status:

.. code-block:: none

   sudo microceph status

The output should now show three disks and the additional presence of the OSD
service:

.. code-block:: none

   MicroCeph deployment summary:
   - node-mees (10.246.114.49)
       Services: mds, mgr, mon, osd
         Disks: 3

Manage the cluster
------------------

Your Ceph cluster is now deployed and can be managed by following the resources
found in the :doc:`Howto <../how-to/index>` section.

The cluster can also be managed using native Ceph tooling if snap-level
commands are not yet available for a desired task:

.. code-block:: none

   ceph status

The cluster built during this tutorial gives the following output:

.. code-block:: none

     cluster:
       id:     4c2190cd-9a31-4949-a3e6-8d8f60408278
       health: HEALTH_OK

     services:
       mon: 1 daemons, quorum node-mees (age 7d)
       mgr: node-mees(active, since 7d)
       osd: 3 osds: 3 up (since 7d), 3 in (since 7d)

     data:
       pools:   1 pools, 1 pgs
       objects: 2 objects, 577 KiB
       usage:   96 MiB used, 2.7 TiB / 2.7 TiB avail
       pgs:     1 active+clean
