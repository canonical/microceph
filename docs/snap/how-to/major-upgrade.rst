.. _major-upgrades:

Major Upgrades
===============

This guide provides step-by-step instructions on how to upgrade your MicroCeph cluster to a new major release. 

Follow these steps carefully to prevent to ensure a smooth transition.

In the code examples below an upgrade to the Squid stable
release is shown. The procedure should apply to any major release
upgrade in a similar way however.



Procedure
---------


Prerequisites
~~~~~~~~~~~~~

Firstly, before initiating the upgrade, ensure that the cluster is healthy. Use the below command to check the cluster health:

.. code-block:: none

    sudo ceph -s

.. note::
   
   Do not start the upgrade if the cluster is unhealthy.


Then, review the :ref:`release notes <release-notes>` to check for any version-specific information.
Also consult the :external+upstream-ceph:doc:`upstream Ceph release notes <releases/index>` for the target version, and check the
:external+ubuntu-release-notes:doc:`Ubuntu release notes <index>` for any relevant information.


Optional but Recommended: Preparation Steps
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Carry out these precautionary steps before initiating the upgrade:

1. **Back up your data**: as a general precaution, it is recommended to take a backup of your data (such as stored S3 objects, RBD volumes, or CephFS filesystems).

2. **Prevent OSDs from dropping out of the cluster**: Run the following command to avoid OSDs from unintentionally dropping out of the cluster during the upgrade process:

.. code-block:: none

   sudo ceph osd set noout


Upgrading Each Cluster Node
~~~~~~~~~~~~~~~~~~~~~~~~~~~

If your cluster is healthy, proceed with the upgrade by refreshing the snap on each node using the following command:

.. code-block:: none
   
   sudo snap refresh microceph --channel squid/stable

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


You have now successfully upgraded your Ceph cluster.


