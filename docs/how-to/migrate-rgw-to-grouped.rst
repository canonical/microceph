==========================================
Migrating RGW to grouped service model
==========================================

MicroCeph now supports a grouped service model for RGW (RADOS Gateway) services,
similar to the NFS service. This allows multiple RGW instances to be logically
grouped and managed together, providing better organization and service
management capabilities.

Background
----------

Previously, RGW services in MicroCeph were deployed as ungrouped services,
meaning each RGW instance was tracked independently without any logical grouping.
The new grouped service model introduces the concept of a ``group-id``, allowing
multiple RGW instances to be part of the same service group.

Benefits of grouped RGW services:

* **Logical grouping**: Multiple RGW instances can be identified as part of the same service cluster
* **Better organization**: Service groups appear with descriptive names (e.g., ``rgw.my-cluster``)
* **Consistent model**: Aligns with the NFS grouped service pattern
* **Future extensibility**: Enables group-wide configuration and management capabilities

Migration process
-----------------

.. note::

   There is no automatic migration from ungrouped to grouped RGW services.
   Migration must be performed manually.

The migration process involves disabling the existing ungrouped RGW service and
re-enabling it with a group ID. This requires a brief service interruption.

**Prerequisites:**

* Ensure you have a maintenance window as the RGW service will be temporarily unavailable
* Note down the current RGW configuration (port, SSL settings, etc.)
* Identify which nodes have RGW services running

Step 1: Check current RGW services
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

View your current cluster status to identify nodes with RGW services:

.. code-block:: none

   sudo microceph status

   MicroCeph deployment summary:
   - node1 (10.111.153.78)
     Services: mds, mgr, mon, rgw, osd
     Disks: 3
   - node2 (192.168.29.152)
     Services: mds, mgr, mon, rgw
     Disks: 0

In this example, both ``node1`` and ``node2`` have ungrouped RGW services.

Step 2: Disable the ungrouped RGW service
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Disable the existing ungrouped RGW service on each node:

.. code-block:: none

   # On node1
   sudo microceph disable rgw --target node1

   # On node2
   sudo microceph disable rgw --target node2

.. caution::

   The RGW service will be unavailable during this step. Ensure clients are
   prepared for the interruption.

Step 3: Enable RGW with group ID
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Re-enable the RGW service with a group ID. Use the same configuration parameters
(port, SSL settings) as before:

.. code-block:: none

   # On node1
   sudo microceph enable rgw --target node1 --port 8080 --group-id main-gateway

   # On node2
   sudo microceph enable rgw --target node2 --port 8080 --group-id main-gateway

.. note::

   The ``--group-id`` must match the pattern: alphanumeric characters with dots
   and hyphens, between 3-63 characters long (e.g., ``main-gateway``,
   ``rgw-cluster-1``).

Step 4: Verify the migration
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Check the cluster status to verify the grouped RGW services are running:

.. code-block:: none

   sudo microceph status

   MicroCeph deployment summary:
   - node1 (10.111.153.78)
     Services: mds, mgr, mon, rgw.main-gateway, osd
     Disks: 3
   - node2 (192.168.29.152)
     Services: mds, mgr, mon, rgw.main-gateway
     Disks: 0

Notice that the RGW services now appear as ``rgw.main-gateway``, indicating
they are part of the same service group.

Managing grouped RGW services
------------------------------

Once migrated to the grouped model, you must always include the ``--group-id``
parameter when disabling RGW services:

.. code-block:: none

   # Disable grouped RGW
   sudo microceph disable rgw --group-id main-gateway --target node1

   # This will NOT work for grouped services (only for ungrouped)
   sudo microceph disable rgw --target node1

Backward compatibility
----------------------

MicroCeph maintains backward compatibility with ungrouped RGW services. You can:

* Continue to use ungrouped RGW services without migration
* Deploy new ungrouped RGW services by omitting the ``--group-id`` flag
* Mix ungrouped and grouped RGW services in the same cluster (on different nodes)

However, a single node can only run one RGW service at a time, either grouped
or ungrouped.

Troubleshooting
---------------

**Issue**: Cannot enable grouped RGW service, error about existing service

**Solution**: Ensure the node doesn't already have an RGW service (grouped or
ungrouped). Disable the existing service first.

**Issue**: Group ID validation error

**Solution**: Ensure your ``--group-id`` follows the required pattern:
alphanumeric characters with dots and hyphens, 3-63 characters long.

**Issue**: Services not showing as grouped in status

**Solution**: Verify that you used the same ``--group-id`` on all nodes that
should be part of the group.

.. LINKS

.. _RADOS Gateway service: https://docs.ceph.com/en/latest/radosgw/
