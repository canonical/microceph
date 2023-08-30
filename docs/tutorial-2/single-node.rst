===========
Single-node
===========

This tutorial shows how to install MicroCeph on a single machine.

Ensure storage requirements
---------------------------

Three OSDs will be required to form a minimal Ceph cluster. This means that
three individual disks are required to be available on the host machine.

.. note::

   Upstream Ceph development is underway to allow for loopback device support.
   This will filter down to MicroCeph which will allow for easier
   proof-of-concept and developer deployments.

The disk subsystem can be summarised via the :command:`lsblk` command. In this
tutorial, it looks like this (loopback devices have been suppressed in the
output for purposes of brevity and clarity:

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

To install the most recent stable release of MicroCeph:

.. code-block:: none

   sudo snap install microceph

Next, prevent the software from being auto-updated:

.. code-block:: none

   sudo snap refresh --hold microceph

.. caution::

   Allowing the snap to be auto-updated can lead to unintended consequences.
   In enterprise environments especially, it is better to research the
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

Add the three disks to the cluster by using the :command:`disk add` command:

.. code-block:: none

   sudo microceph disk add /dev/sda --wipe
   sudo microceph disk add /dev/sdb --wipe
   sudo microceph disk add /dev/sdc --wipe

Rechcek status:

.. code-block:: none

   sudo microceph status

The output should now show three disks and the additional presense of the OSD
service:

.. code-block:: none

   MicroCeph deployment summary:
   - node-mees (10.246.114.49)
       Services: mds, mgr, mon, osd
         Disks: 3

Manage the cluster
------------------

Your Ceph cluster is now deployed and can be managed by following the resources
found in the :doc:`Howto <../how-to/index>` section. The cluster can be managed
using native Ceph tooling if snap-level commands are not yet avaiable for a
desired task.
