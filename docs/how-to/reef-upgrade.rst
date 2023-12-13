===============
Upgrade to Reef
===============


Overview
--------

This guide provides step-by-step instructions on how to upgrade your MicroCeph cluster from the Quincy release to the Reef release. Follow these steps carefully to prevent to ensure a smooth transition.


Procedure
---------


Optional but Recommended: Preparation Steps
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Carry out these precautionary steps before initiating the upgrade:

1. **Back up your data**: to avoid any potential data loss, it is recommended to back up all your important data before starting the upgrade.

2. **Prevent OSDs from dropping out of the cluster**: Run the following command to avoid OSDs from unintentionally dropping out of the cluster during the upgrade process:

.. code-block:: none

   sudo ceph osd set noout

Checking Ceph Health
~~~~~~~~~~~~~~~~~~~~

Before initiating the upgrade, ensure that the cluster is healthy. Use the below command to check the cluster health:

.. code-block:: none

    sudo ceph -s

**Note**: Do not start the upgrade if the cluster is unhealthy.

Upgrading Each Cluster Node
~~~~~~~~~~~~~~~~~~~~~~~~~~~

If your cluster is healthy, proceed with the upgrade by refreshing the snap on each node using the following command:

.. code-block:: none
   
   sudo snap refresh microceph --channel reef/stable

Be sure to perform the refresh on every node in the cluster.

Verifying the Upgrade
~~~~~~~~~~~~~~~~~~~~~

Once the upgrade process is done, verify that all components have been upgraded correctly. Use the following command to check:

.. code-block:: none
   
   sudo ceph versions


Unsetting Noout
~~~~~~~~~~~~~~~

If you had previously set noout, unset it with this command:

.. code-block:: none
   
   sudo ceph osd unset noout


You have now successfully upgraded to the Reef Release.

