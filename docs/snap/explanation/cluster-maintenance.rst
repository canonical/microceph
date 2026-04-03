================
Maintenance Mode
================

Overview
--------

Cluster maintenance is important for keeping the Ceph Storage Cluster at a healthy state.

MicroCeph provides a simple and consistent workflow to support maintenance activity. Before
executing any high-risk maintenance operations on a node, operators are strongly recommended to
enable maintenance mode to minimise the impact and ensure system stability. For more information on how
to enable maintenance mode in MicroCeph, please refer to :doc:`Perform cluster
maintenance</how-to/perform-cluster-maintenance>`.

Strategy
--------

Bringing a node into and out of maintenance mode generally follows check-and-apply pattern. We
first verify if the node is ready for maintenance operations, then run the steps to bring the node
into or out of maintenance mode if the verification passes. The strategy is idempotent, you can
repeatedly run the steps without any issue.

The strategy is defined as follows:

Enabling maintenance mode
~~~~~~~~~~~~~~~~~~~~~~~~~

- Check if OSDs on the node are ``ok-to-stop`` to ensure sufficient redundancy to tolerate the loss
  of OSDs on the node.
- Check if the number of running services is greater than the minimum (majority of MON, 1 MDS, 1 MGR)
  required for quorum.
- *(Optional)* Apply noout flag to prevent data migration from triggering during the planned
  maintenance slot. (default=True)
- *(Optional)* Bring the OSDs down and disable the service (Default=False)

Disabling maintenance mode
~~~~~~~~~~~~~~~~~~~~~~~~~~

- Remove noout flag to allow data migration from triggering after the planned maintenance slot.
- Bring the OSDs up and enable the service


