=============================================
Perform failover for replicated RBD resources
=============================================

In case of a disaster, all replicated RBD pools can be failed over to a non-primary remote.

An operator can perform promotion on a non-primary cluster, this will in turn promote all replicated rbd
images in all rbd pools and make them primary. This enables them to be consumed by vms and other workloads.

Prerequisites
--------------
1. A primary and a secondary MicroCeph cluster, for example named "primary_cluster" and "secondary_cluster"
2. primary_cluster has imported configurations from secondary_cluster and vice versa. refer to :doc:`import remote <./import-remote-cluster>`
3. RBD replication is configured for at least 1 rbd image. refer to :doc:`configure rbd replication <./configure-rbd-mirroring>`

Failover to a non-primary remote cluster
-----------------------------------------
List all the resources on 'secondary_cluster' to check primary status.

.. code-block:: none

   sudo microceph replication list rbd
   +-----------+------------+------------+---------------------+
   | POOL NAME | IMAGE NAME | IS PRIMARY | LAST LOCAL UPDATE   |
   +-----------+------------+------------+---------------------+
   | pool_one  | image_one  | false      | 2024-10-14 09:03:17 |
   | pool_one  | image_two  | false      | 2024-10-14 09:03:17 |
   +-----------+------------+------------+---------------------+

An operator can perform cluster wide promotion as follows:

.. code-block:: none

   sudo microceph replication promote --remote primary_cluster --yes-i-really-mean-it 

Here, <remote> parameter helps microceph filter the resources to promote.
Since promotion of secondary_cluster may cause a split-brain condition in future,
it is necessary to pass --yes-i-really-mean-it flag.

Verify RBD replication primary status
---------------------------------------------

List all the resources on 'secondary_cluster' again to check primary status.

.. code-block:: none

   sudo microceph replication status rbd pool_one
   +-----------+------------+------------+---------------------+
   | POOL NAME | IMAGE NAME | IS PRIMARY | LAST LOCAL UPDATE   |
   +-----------+------------+------------+---------------------+
   | pool_one  | image_one  | true       | 2024-10-14 09:06:12 |
   | pool_one  | image_two  | true       | 2024-10-14 09:06:12 |
   +-----------+------------+------------+---------------------+

The status shows that there are 2 replicated images and both of them are now primary.

Failback to old primary
------------------------

Once the disaster struck cluster (primary_cluster) is back online the RBD resources
can be failed back to it, but, by this time the RBD images at the current primary (secondary_cluster)
would have diverged from primary_cluster. Thus, to have a clean sync, the operator must decide
which cluster would be demoted to the non-primary status. This cluster will then receive the 
RBD mirror updates from the standing primary.

Note: Demotion can cause data loss and hence can only be performed with the 'yes-i-really-mean-it' flag.

At primary_cluster (was primary before disaster), perform demotion.
.. code-block:: none

   sudo microceph replication demote --remote secondary_cluster
   failed to process demote_replication request for rbd: demotion may cause data loss on this cluster. If you
   understand the *RISK* and you're *ABSOLUTELY CERTAIN* that is what you want, pass --yes-i-really-mean-it.

Now, again at the 'primary_cluster', perform demotion with --yes-i-really-mean-it flag.
.. code-block:: none

   sudo microceph replication demote --remote secondary_cluster --yes-i-really-mean-it

Note: MicroCeph with demote the primary pools and will issue a resync for all the mirroring images, hence it may
cause data loss at the old primary cluster.
