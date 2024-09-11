=========================================
How to install MicroCeph on a single node
=========================================

This guide will show how to install MicroCeph on a single machine, thereby
creating a single-node cluster.

This installation will be achieved through the use of loop files placed on the root
disk, which is a convenient way for setting up small test and development
clusters.

.. warning::

   Using dedicated block devices will result in the best IOPS performance for
   connecting clients. Basing a Ceph cluster on a single disk also necessarily
   leads to a common failure domain for all OSDs. For these reasons, loop files
   should not be used in production environments.

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

Three OSDs will be required to form a minimal Ceph cluster. In a
production system, typically we would assign a physical block device
to an OSD. However for this tutorial, we will make use of file backed
OSDs for simplicity.

Add the three file-backed OSDs to the cluster by using the
:command:`disk add` command. In the example, three 4GiB files are being
created:

.. code-block:: none

   sudo microceph disk add loop,4G,3

.. note::

   Although you can adjust the file size and file number to your needs, with a
   recommended minimum of 2GiB per OSD, there is no obvious benefit to running
   more than three OSDs via loop files. Be wary that an OSD, whether based on
   a physical device or a file, is resource intensive.

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
found in the :doc:`How-to <../how-to/index>` section.

The cluster can also be managed using native Ceph tooling if snap-level
commands are not yet available for a desired task:

.. code-block:: none

   sudo ceph status

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
