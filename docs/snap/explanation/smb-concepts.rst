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

Cluster assets and cleanup
--------------------------

A logical SMB cluster is not the ``.smb`` pool. That shared RADOS pool holds
configuration and coordination objects in a separate namespace for each cluster
ID. Removing ``files`` must not remove the pool or another cluster's namespace.

Associated assets span several stores:

* **Ceph SMB resource database:** cluster settings, desired placement, shares
  and credential references.
* **RADOS:** ``.smb/<cluster-id>/`` contains ``config.smb``, ``spec.smb`` and
  ``cluster-info``; clustered deployments also use ``cluster.meta.json`` and
  ``cluster.meta.lock``.
* **Credentials:** generated sources in the monitor config-key store, the
  CephFS identity ``client.smb.fs.cluster.<cluster-id>``, and the native clustered
  configuration identity ``client.smb.config.<cluster-id>``.
* **MicroCeph database:** shared configuration snapshots and CTDB rank
  reservations in ``service_groups``; successful per-host deployment receipts
  in ``grouped_services``.
* **Gateway hosts:** Samba/CTDB services, generated configuration, local
  keyrings and identity files, persistent state and logs.
* **CephFS:** referenced directories or subvolumes, user data and SMB ownership
  earmarks. These are not stored in ``.smb``.

Removing an orchestrator service does not itself delete its logical SMB cluster
or shares. Logical cluster deletion requires no remaining shares and removes
its external configuration namespace; linked credentials are pruned, while
independent reusable credential resources are retained. Neither operation
removes the shared pool or CephFS data.

Deployment receipts record successful placement, not every failed attempt. An
empty receipt list alone must not authorize deletion of shared group state.
After interrupted cleanup, retain enough state to discover and retry unfinished
work; see :ref:`smb-reference` for uncertain request outcomes.

Current native cleanup clears generated configuration and local keyrings, but
does not wipe ``$SNAP_COMMON/data/samba`` or delete the CephFS access identity.
Deployment cleanup therefore does not mean erasing every associated asset.

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
