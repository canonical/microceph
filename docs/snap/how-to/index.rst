.. meta::
   :description: MicroCeph how-to guides for installing, configuring, managing, upgrading and consuming Ceph cluster storage.

.. _snap-how-to:

How-to guides
=============

Our *how-to* guides walk you through the key operations you can perform with MicroCeph;
from first deployment to day-to-day cluster management.


Installation and initialisation
-------------------------------

Deploy MicroCeph on a single machine, or at scale across multiple nodes for a production cluster.
If you want to encrypt your data at rest, enable full disk encryption (FDE) when adding disks as
existing OSDs cannot be encrypted in place.

.. toctree::
   :maxdepth: 1

   single-node
   multi-node
   Enable full disk encryption <enable-fde>

Configuration
-------------

Once your cluster is running, you'll want to tune it to your environment: connect it to your network,
enable additional services, set up authentication, turn on monitoring so you can see what it's doing,
and adjust client settings for optimal performance.

.. toctree::
   :maxdepth: 1

   configure-network-keys
   enable-service-instances
   integrate-keystone
   enable-metrics
   enable-alerts
   rbd-client-cfg
   configure-native-smb

Consume cluster storage
-----------------------

With your cluster configured, connect your applications and machines to the cluster storage
using block devices or shared filesystems.

.. toctree::
   :maxdepth: 1

   mount-block-device
   mount-cephfs-share
   
Day-to-day operations
---------------------

As your cluster runs, you'll need to change things: replace aging disks, move services between nodes,
adjust logging, scale up or down, or take a node offline for maintenance without disrupting your users.
You'll also need to rotate TLS certificates periodically to keep secure connections to your object
storage running.

.. toctree::
   :maxdepth: 1

   change-log-level
   migrate-auto-services
   remove-disk
   perform-cluster-maintenance
   rotate-rgw-tls-certificate

Multi-site replication
----------------------

When a single site isn't enough, whether for disaster recovery or geographic redundancy, you can
connect a remote MicroCeph cluster, replicate data to it, and fail over when needed.

.. toctree::
   :maxdepth: 1

   import-remote-cluster
   configure-rbd-mirroring
   configure-cephfs-mirroring 
   perform-site-failover

Upgrade your cluster
--------------------

When a new major MicroCeph release comes out, you'll want to upgrade to get the latest features, performance
improvements, and security fixes. Our upgrade guide walks you through upgrading safely, one node at a time,
without losing data or availability.

.. toctree::
   :maxdepth: 1

   major-upgrade

Report security vulnerabilities
-------------------------------

Found a security issue? Report it responsibly so we can address it and protect our users.

.. toctree::
   :maxdepth: 1

   Report security issues <report-security-vuln>
