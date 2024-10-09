==================================
Configure RBD remote replication
==================================

MicroCeph supports asynchronously replicating (mirroring) RBD images to a remote cluster.

An operator can enable this on any rbd image, or a whole pool. Enabling it on a pool enables it for all the images in the pool.

Prerequisites
--------------
1. A primary and a secondary MicroCeph cluster, for example named "primary_cluster" and "secondary_cluster"
2. primary_cluster has imported configurations from secondary_cluster and vice versa. refer to :doc:`import remote <./import-remote-cluster>`
3. Both clusters have 2 rbd pools: pool_one and pool_two.
4. Both pools at cluster "primary_cluster" have 2 images each (image_one and image_two) while the pools at cluster "secondary_cluster" are empty.

Enable RBD remote replication
-------------------------------

An operator can enable replication for a given rbd pool which is present at both clusters as

.. code-block:: none

   sudo microceph remote replication rbd enable pool_one --remote secondary_cluster 

Here, pool_one is the name of the rbd pool and it is expected to be present at both the clusters.

Check RBD remote replication status
------------------------------------

The above command will enable replication for ALL the images inside pool_one, it can be checked as:

.. code-block:: none

   sudo microceph remote replication rbd status pool_one
   +------------------------+----------------------+
   |         SUMMARY        |        HEALTH        |
   +-------------+----------+-------------+--------+
   | Name        | pool_one | Replication | OK     |
   | Mode        | pool     | Daemon      | OK     |
   | Image Count | 2        | Image       | OK     |
   +-------------+----------+-------------+--------+

   +-------------------+-----------+--------------------------------------+
   |    REMOTE NAME    | DIRECTION | UUID                                 |
   +-------------------+-----------+--------------------------------------+
   | secondary_cluster | rx-tx     | f25af3c3-f405-4159-a5c4-220c01d27507 |
   +-------------------+-----------+--------------------------------------+

The status shows that there are 2 images in the pool which are enabled for mirroring.

Listing all RBD remote replication images
------------------------------------------

An operator can list all the images that have replication (mirroring) enabled as follows:

.. code-block:: none

   sudo microceph remote replication rbd list
   +-----------+------------+------------+---------------------+
   | POOL NAME | IMAGE NAME | IS PRIMARY |  LAST LOCAL UPDATE  |
   +-----------+------------+------------+---------------------+
   | pool_one  | image_one  |    true    | 2024-10-08 13:54:49 |
   | pool_one  | image_two  |    true    | 2024-10-08 13:55:19 |
   | pool_two  | image_one  |    true    | 2024-10-08 13:55:12 |
   | pool_two  | image_two  |    true    | 2024-10-08 13:55:07 |
   +-----------+------------+------------+---------------------+

Disabling RBD remote replication
---------------------------------

In some cases, it may be desired to disable replication. A single image ($pool/$image) or 
a whole pool ($pool) can be disabled in a single command as follows:

Disable Pool replication:
.. code-block:: none

   sudo microceph remote replication disable pool_one
   sudo microceph remote replication list
   +-----------+------------+------------+---------------------+
   | POOL NAME | IMAGE NAME | IS PRIMARY |  LAST LOCAL UPDATE  |
   +-----------+------------+------------+---------------------+
   | pool_two  | image_one  |    true    | 2024-10-08 13:55:12 |
   | pool_two  | image_two  |    true    | 2024-10-08 13:55:07 |
   +-----------+------------+------------+---------------------+

Disable Image replication:
.. code-block:: none

   sudo microceph remote replication disable pool_two/image_two
   sudo microceph remote replication list
   +-----------+------------+------------+---------------------+
   | POOL NAME | IMAGE NAME | IS PRIMARY |  LAST LOCAL UPDATE  |
   +-----------+------------+------------+---------------------+
   | pool_two  | image_one  |    true    | 2024-10-08 13:55:12 |
   +-----------+------------+------------+---------------------+

