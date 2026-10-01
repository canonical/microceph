.. meta::
   :description: Understand how MicroCeph combines Ceph SMB, Samba, and CTDB to provide highly available SMB access to CephFS.

.. _smb-concepts:

SMB and CTDB concepts in MicroCeph
==================================

MicroCeph provides SMB access to CephFS through the Ceph SMB manager, the
MicroCeph orchestrator backend, Samba, and CTDB.

SMB clusters, instances, and shares
-----------------------------------

An **SMB cluster** is configuration identified by a cluster ID. It contains
authentication configuration and service placement. An **SMB instance** is the
node-local service set on a selected MicroCeph member. An **SMB share** maps a
client-visible name to CephFS storage.

Shares are managed separately from instances. Initial daemon deployment begins
after the first CephFS-backed share is applied.

CephFS data path and user identity
----------------------------------

``smbd`` authenticates an SMB user and maps it to a Unix UID, GID, and groups.
The ``vfs_ceph_new`` module uses that identity for CephFS POSIX permission
checks. SMB file data does not pass through a kernel CephFS mount or proxy.

CTDB coordination
-----------------

The default ``always`` mode runs CTDB, including for one instance. The
``never`` mode permits one instance without CTDB. The mode cannot be changed;
recreate the SMB cluster to change it. CTDB coordinates Samba recovery state;
CephFS stores the shared file data.

Network and recovery
--------------------

CTDB uses the MicroCluster member network on TCP port 4379. ``smbd`` uses an
explicit SMB bind address or network, or an address from Ceph's
``public_network`` on TCP port 445 by default.

MicroCeph does not manage CTDB public or floating addresses. Clients must
reconnect to a surviving member after a failure, or use an external load
balancer.

See :ref:`deploy-smb-gateway` for deployment and :ref:`smb-reference` for
configuration and commands.
