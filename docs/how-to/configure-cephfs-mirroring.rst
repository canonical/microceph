==================================
Configure CephFS replication
==================================

MicroCeph replication framework supports enabling replication for CephFS workloads to a remote MicroCeph cluster.

An operator can enable this on any subvolume or a directory path relative to the parent volume.

Prerequisites
--------------
1. A primary and a secondary MicroCeph cluster, for example named "primary_cluster" and "secondary_cluster"
2. Both primary_cluster and secondary_cluster have imported exchanged cluster tokens. refer to :doc:`import remote <./import-remote-cluster>`
3. Both clusters have an active cephfs volume called vol.
4. Both clusters have exactly one cephfs-mirror daemon enabled.


Enable CephFS replication
-------------------------------

An operator can enable replication for a given cephfs directory path as follows:

.. code-block:: none

   sudo microceph replication enable cephfs --volume vol --remote secondary --dir-path </path/to/directory>

Here, </path/to/directory> is the path relative to volume (vol) as root.

Similarly, an operator can enable replication for a given subvolume as follows:

..code-block:: none

   sudo microceph replication enable cephfs --volume vol --subvolume <subvolume> --subvolumegroup <subvolumegroup>

Here, <subvolume> is the name of the subvolume and <subvolumegroup> is the name of the parent subvolume group. If the
subvolume does not have a parent subvolume group, then <subvolumegroup> can be omitted.

Listing all RBD replication images
------------------------------------------

An operator can list all the resources enabled for replication as follows:

.. code-block:: none

  sudo microceph replication list cephfs

.. terminal::

  +--------+------------------------------------+-----------+
  | VOLUME | RESOURCE                           | TYPE      |
  +--------+------------------------------------+-----------+
  | vol    | /dir1/dir2/dir3                    | directory |
  | vol    | /volumes/subvol_group/subvol       | subvolume |
  | vol    | /volumes/_nogroup/ungrouped_subvol | subvolume |
  +--------+------------------------------------+-----------+

Check RBD replication status
------------------------------------

An operator can check the current replication status of a volume as follows:

.. code-block:: none

  sudo microceph replication status cephfs vol

.. terminal::

  +--------------------------+
  |          SUMMARY         |
  +----------------+---------+
  | Volume         | vol     |
  | Resource Count | 3       |
  | Peer Count     | 1       |
  +----------------+---------+

  +-------------+------------------------------------+--------+--------------+---------------+---------------+
  | REMOTE NAME | RESOURCE PATH                      | STATE  | SNAPS SYNCED | SNAPS DELETED | SNAPS RENAMED |
  +-------------+------------------------------------+--------+--------------+---------------+---------------+
  | primary     | /volumes/_nogroup/ungrouped_subvol | idle   |            1 |             0 |             0 |
  | primary     | /volumes/subvol_group/subvol       | idle   |            1 |             0 |             0 |
  | primary     | /path/to/directory                 | idle   |            1 |             0 |             0 |
  +-------------+------------------------------------+--------+--------------+---------------+---------------+

The status shows that there are 3 resources in the volume (vol), all with one snapshot synced to the configured remotes.


Disabling RBD replication
---------------------------------

In some use-cases (say migration), the operator may want to disable replication for a given resource.

For Subvolumes
^^^^^^^^^^^^^^

.. code-block:: none

   sudo microceph replication disable cephfs --volume vol --subvolumegroup <subvolumegroup> --subvolume <subvolume>

Similar to enablement, the subvolumegroup can be omitted if the subvolume does not belong to any group.

For Directory paths
^^^^^^^^^^^^^^^^^^^

.. code-block:: none

   sudo microceph replication disable cephfs --volume vol --dir-path </path/to/directory>

For ALL resources in a volume
^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

Disabling all resources in a volume is supported but requires the operator to pass the --force flag to avoid accidental disablement.

.. code-block:: none

   sudo microceph replication disable cephfs --volume vol --force

