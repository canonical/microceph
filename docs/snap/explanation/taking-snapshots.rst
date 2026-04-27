.. _taking-snapshots:

Taking backups for your workload
================================

The MicroCeph deployed Ceph cluster supports snapshot based backups
for Block and File based workloads.

This document is an index of upstream documentation available for snapshots
along with some bridging commentary to help understand it better.

RBD snapshots
--------------

Ceph supports creating point in time read-only logical copies. This allows
an operator to create a checkpoint for their workload backup. The snapshots
can be exported for external backup or kept in Ceph for rollback to older version.

Prerequisites
+++++++++++++

Refer to our :ref:`guide on mounting MicroCeph-backed block devices <mount-block-devices>`
for getting started with RBD.

Once you have a the block device mounted and in use, you can jump to `Ceph RBD Snapshots`_.

CephFs snapshots
----------------

Similar to RBD snapshots, CephFs snapshots are read-only logical copies of **any chosen sub-directory**
of the corresponding filesystem.

Prerequisites
+++++++++++++

Refer to our :ref:`guide on mounting MicroCeph-backed CephFs shares <mount-cephfs-shares>`.

Once you have a the filesystem mounted and in use, you can jump to `CephFs Snapshots`_.

.. LINKS

.. _Ceph RBD Snapshots: https://docs.ceph.com/en/latest/rbd/rbd-snapshot/
.. _CephFs Snapshots: https://docs.ceph.com/en/latest/dev/cephfs-snapshots/