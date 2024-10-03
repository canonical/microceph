How-to guides
=============

Our *how-to* guides give directions on how perform key operations and processes in MicroCeph.

Installing and initialising MicroCeph cluster
---------------------------------------------

The guides in this section are helpful in the installation and initialisation
of both single-node and multi-node clusters.

.. toctree::
   :maxdepth: 1
   
   single-node
   multi-node

Configuring your cluster
------------------------

See these guides for client and network configurations, authentication service integration, and
configuration of metrics, alerts and other service instances.

.. toctree::
   :maxdepth: 1

   rbd-client-cfg
   integrate-keystone
   configure-network-keys
   enable-metrics
   enable-alerts
   enable-service-instances

Interacting with your cluster
-----------------------------

Manage your cluster: find steps on how to configure the log level,remove disks,
migrate services and more.

.. toctree::
   :maxdepth: 1

   change-log-level
   migrate-auto-services
   remove-disk
   import-remote-cluster


Upgrading your cluster
----------------------

Follow these steps carefully to ensure a smooth transition from v.17.2.x (Quincy)
to v.18.2.x (Reef)

.. toctree::
   :maxdepth: 1

   reef-upgrade

   
Consuming cluster storage
-------------------------

Follow these guides to learn how to make use of the storage provided by your cluster.

.. toctree::
   :maxdepth: 1

   mount-block-device
   mount-cephfs-share

