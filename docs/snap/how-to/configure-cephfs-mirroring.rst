==================================
Configure CephFS replication
==================================

CephFS is a POSIX-compliant file system over Ceph's distributed storage layer consumed by various cloud workloads.
For improved resiliency and disaster recovery, it is often desirable to replicate CephFS data to a remote cluster.

MicroCeph replication framework supports enabling replication for CephFS workloads to a remote MicroCeph cluster.

This guide describes how an operator can enable, monitor, and disable CephFS replication for any subvolume or a
directory path relative to the parent volume using MicroCeph CLI commands.

.. note::

   The CLI commands described in this guide are to be executed on the primary MicroCeph cluster.

Prerequisites
--------------
1. A primary and a secondary MicroCeph cluster, for example named ``primary_cluster`` and ``secondary_cluster``.
2. Both ``primary_cluster`` and ``secondary_cluster`` have imported exchanged cluster tokens. Refer to our :doc:`how to guide for importing a remote microceph cluster <./import-remote-cluster>`.
3. Both clusters have an active CephFS volume called ``vol``.
4. Both clusters have exactly one ``cephfs-mirror`` daemon enabled. Refer to our :ref:`how-to guide for enabling the cephfs-mirror service <enable-cephfs-mirror-daemon>`.


Enable CephFS replication
-------------------------------

An operator can enable replication for a given cephfs directory path as follows:

.. terminal:: none

   sudo microceph replication enable cephfs --volume vol --remote secondary_cluster --dir-path </path/to/directory>

Here, ``/path/to/directory`` is the desired directory path relative to volume ``vol`` as root.

Similarly, an operator can enable replication for a given subvolume as follows:

.. terminal:: none

   sudo microceph replication enable cephfs --volume vol --subvolume <subvolume> --subvolumegroup <subvolumegroup>

Here, ``<subvolume>`` is the name of the subvolume and ``<subvolumegroup>`` is the name of the parent subvolume group. If the
subvolume does not have a parent subvolume group, then ``<subvolumegroup>`` can be omitted.

Listing all CephFS replication images
------------------------------------------

An operator can list all the resources enabled for replication as follows:

.. terminal:: none

  sudo microceph replication list cephfs

.. code-block::

  +--------+------------------------------------+-----------+
  | VOLUME | RESOURCE                           | TYPE      |
  +--------+------------------------------------+-----------+
  | vol    | /dir1/dir2/dir3                    | directory |
  | vol    | /volumes/subvol_group/subvol       | subvolume |
  | vol    | /volumes/_nogroup/ungrouped_subvol | subvolume |
  +--------+------------------------------------+-----------+

Check CephFS replication status
------------------------------------

An operator can check the current replication status of a volume as follows:

.. terminal:: none

  sudo microceph replication status cephfs vol

.. code-block::

  +--------------------------+
  |          SUMMARY         |
  +----------------+---------+
  | Volume         | vol     |
  | Resource Count | 3       |
  | Peer Count     | 1       |
  +----------------+---------+

  +---------------------+----------------------------+--------+--------------+---------------+---------------+
  | REMOTE NAME         | RESOURCE PATH                      | STATE  | SNAPS SYNCED | SNAPS DELETED | SNAPS RENAMED |
  +---------------------+----------------------------+--------+--------------+---------------+---------------+
  | primary_cluster     | /volumes/_nogroup/ungrouped_subvol | idle   |            1 |             0 |             0 |
  | primary_cluster     | /volumes/subvol_group/subvol       | idle   |            1 |             0 |             0 |
  | primary_cluster     | /path/to/directory                 | idle   |            1 |             0 |             0 |
  +---------------------+---------------------------+--------+--------------+---------------+---------------+

The status shows that there are three resources in the volume (vol), all with one snapshot synced to the configured remotes.

Disabling CephFS replication
---------------------------------

In some use-cases (say migration), the operator may want to disable replication for a given resource.

For subvolumes
^^^^^^^^^^^^^^

Disable replication for a subvolume resource, here ``vol`` is the parent volume, ``<subvolumegroup>`` is the parent subvolume
group and ``<subvolume>`` is the subvolume.

.. terminal:: none

   sudo microceph replication disable cephfs --volume vol --subvolumegroup <subvolumegroup> --subvolume <subvolume>

Omit ``subvolumegroup`` if the subvolume does not belong to any group.

For directory paths
^^^^^^^^^^^^^^^^^^^

Disable replication for a directory resource, here ``</path/to/directory>`` is the directory path relative to the root of volume ``vol``.

.. terminal:: none

   sudo microceph replication disable cephfs --volume vol --dir-path </path/to/directory>

For ``all enabled resources`` in a volume
^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

Disabling all resources in a volume is supported but requires the operator to pass the ``--force`` flag to avoid accidental disablement.

.. terminal:: none

   sudo microceph replication disable cephfs --volume vol --force

