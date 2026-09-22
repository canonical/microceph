.. meta::
   :description: Understand the MicroCeph SMB service, CTDB coordination, CephFS data path, networking, and failover model.

.. _smb-concepts:

SMB and CTDB concepts in MicroCeph
==================================

MicroCeph provides SMB access to CephFS by combining the Ceph SMB manager,
the MicroCeph orchestrator backend, Samba, and CTDB. This page describes only
the concepts needed to operate that integration.

SMB clusters, instances, and shares
-----------------------------------

An **SMB cluster** is the cluster-level configuration identified by a cluster
ID. It contains authentication configuration, service placement, and one or
more shares.

An **SMB instance** is the set of node-local services running on one selected
MicroCeph member. Administrators add an instance with
:command:`microceph enable smb --target` and remove it with
:command:`microceph disable smb --target`.

An **SMB share** maps a name visible to SMB clients to CephFS storage. Shares
are Ceph SMB resources and are managed separately from service instances.
Removing an instance does not remove a share while other instances remain.

Control-plane flow
------------------

The managed service and node-local service APIs have separate responsibilities:

.. code-block:: text

   microceph enable smb
       |
       v
   /1.0/managed-services/smb
       |
       v
   Ceph SMB manager and MicroCeph orchestrator
       |
       v
   /1.0/services/smb?target=<member>
       |
       v
   node-local Samba and CTDB configuration

The managed endpoint reconciles the complete member set. The internal service
endpoint materialises one member's configuration and is not a user-facing
placement interface.

CephFS data path and user identity
----------------------------------

``smbd`` authenticates a remote SMB user and maps that account to a Unix UID,
GID, and supplementary groups. Samba performs its normal process identity
transition, which requires the ``microceph:smb-identity`` snap interface.

The ``vfs_ceph_new`` module then creates an explicit libcephfs ``UserPerm``
from the same Samba Unix token. CephFS uses that UID, GID, and group list for
its POSIX permission checks. SMB file data does not pass through a kernel
CephFS mount or a proxy daemon.

CTDB coordination
-----------------

When an SMB cluster has multiple instances, the Ceph SMB manager marks it as
clustered and MicroCeph starts CTDB before Samba. CTDB coordinates the Samba
cluster metadata and recovery state; it does not store the shared file data.
The data remains in CephFS.

Each instance has a stable CTDB rank and identity. A RADOS object provides the
cluster metadata and a RADOS-backed mutex provides the recovery lock.

Network separation
------------------

CTDB uses the MicroCluster internal member address. This address needs to be
reachable only between CTDB members on TCP port 4379. It is not a client
endpoint.

``smbd`` uses a client-facing address selected from Ceph's ``public_network``
unless an explicit SMB bind address or network is configured. SMB clients
connect to the selected member address, normally on TCP port 445.

The two addresses may be the same when MicroCluster and Ceph use the same
network, but they have different roles and are resolved independently.

Availability and recovery
-------------------------

If one member fails, CTDB marks it unavailable and coordinates recovery among
the surviving members. Clients can establish new sessions through a surviving
member and continue accessing the same CephFS share.

MicroCeph does not currently manage CTDB public or floating addresses.
Therefore:

* an existing connection to the failed member is not moved to another address;
* clients or an external load balancer must select a surviving member; and
* after the failed member returns and CTDB reports it healthy, clients can
  connect to it again.

See :ref:`deploy-smb-gateway` for the operational procedure and
:ref:`smb-reference` for commands and supported configuration.
