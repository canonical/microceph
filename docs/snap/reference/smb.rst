.. meta::
   :description: Reference for managed SMB services, configuration, placement, and networking in MicroCeph.

.. _smb-reference:

SMB service reference
=====================

MicroCeph manages SMB clusters through the Ceph SMB manager and MicroCeph
orchestrator backend. Instances with the same cluster ID form one SMB cluster.

Enable an SMB instance
----------------------

.. code-block:: none

   microceph enable smb --cluster-id <cluster-id> [--target <member>] [flags]

The first member creates the cluster and requires either ``--credentials-file``
or ``--user-group-ref``. CTDB is enabled by default; connect
``microceph:smb-identity`` and ``microceph:ctdb-run`` before applying a share.
With ``--clustering never``, CTDB is disabled regardless of the number of members;
the ``ctdb-run`` connection is not required.

Initial enable accepts the cluster and credential resources, but deployment
starts only after a CephFS-backed share is applied. Later enables do not require
credentials again.

See :doc:`commands/enable` for all flags. Cluster IDs contain 1 to 18 ASCII
alphanumeric or hyphen characters, with alphanumeric first and last
characters. The bind and port options apply to the entire cluster; a new bind
list replaces the existing list.

Disable a managed SMB deployment
----------------------------------

.. code-block:: none

   microceph disable smb --cluster-id <cluster-id> [--target <member>] [--force]

.. _smb-disable-scopes:

Disable scopes
~~~~~~~~~~~~~~

Remove one desired member:

.. code-block:: none

   microceph disable smb --cluster-id files --target node-a

This updates placement and cleans only ``node-a``. It retains the logical SMB
cluster, shares, public/private configuration, rank reservations, and CephFS
data. Removing the last desired member is rejected: empty placement is
unsupported and member removal never implicitly deletes the logical cluster.

Remove the whole gateway deployment while retaining its configuration:

.. code-block:: none

   microceph disable smb --cluster-id files

Without ``--target``, cleanup covers the deployment across its hosts, not only
the local member. The logical cluster, shares, public/private configuration,
and rank reservations remain. A subsequent SMB resource update can resubmit
the deployment under Ceph's normal semantics.

Delete the logical cluster and clean its deployment:

.. code-block:: none

   microceph disable smb --cluster-id files --force

Ceph validates logical-cluster deletion before deployment cleanup, so deletion
is rejected while shares remain. ``--force`` does not delete shares, the shared
``.smb`` pool, or CephFS data. All forms require ``--cluster-id``; ``--target``
and ``--force`` cannot be combined. The command displays its selected scope and
retained assets before mutation. These flags do not change ``microceph.ceph``
command meanings. See :doc:`commands/disable` for all flags.

Runtime services
----------------

Clustered deployments run ``microceph.smbd``, ``microceph.ctdbd``, and
``microceph.ctdb-nodes``. Non-clustered deployments run only
``microceph.smbd``. Inspect their status with:

.. code-block:: none

   snap services microceph.smbd microceph.ctdbd microceph.ctdb-nodes
   microceph.ctdb status

Networking and recovery
-----------------------

CTDB uses the MicroCluster member network on TCP port 4379. ``smbd`` uses an
explicit ``--bind-address`` or ``--bind-network``, or an address from Ceph's
``public_network`` on TCP port 445 by default.

MicroCeph does not manage CTDB public or floating addresses. Clients must
reconnect to a surviving member after a failure, or use an external load
balancer.

Placement and supported configuration
-------------------------------------

A member can run one SMB cluster. The clustering mode controls whether CTDB runs,
not the number of members. Adding or removing members does not change that mode.
If placement fails, existing members remain in place.

Placement checks local SMB ownership as well as successful deployment records,
so interrupted deployments are not overlooked. If a member's ownership cannot
be checked, placement reports an error before changing local deployments. This
can include an unreachable member that was not selected for the new placement.

As upstream Ceph warns, running multiple instances of the same SMB service without
CTDB can cause unexpected behaviour.

Before removing a MicroCeph member, explicitly remove it from the desired SMB
placement with the existing ``microceph disable smb --cluster-id <cluster-id>
--target <member>`` operation before removing the member. This updates the
placement even when the target has never deployed: initial SMB placement is
desired before the first share is applied. Cephadm host removal does not rewrite
SMB specs, and draining only guards specs that have already been submitted; a
shareless SMB cluster has not submitted a spec yet. MicroCeph does not
automatically clean up desired-only placement references.

MicroCeph supports local-user authentication, direct ``samba-vfs/new`` CephFS
access, client bind addresses or networks, and a custom SMB port. Active
Directory, ``cephfs-proxy``, CTDB public or floating addresses, custom CTDB
ports, and arbitrary Samba configuration are not supported.

Local user identity
-------------------

Samba uses users imported from Ceph. Without explicit upstream IDs, users and
groups receive IDs based on their list position. Reordering, inserting, or
deleting users can change the numeric ownership interpretation of existing
CephFS files. Append users to preserve existing positions.

When multiple users-and-groups references are supplied, MicroCeph combines their
entries in reference order. Duplicate user names or duplicate group names are
rejected before publishing the combined local configuration. Explicit IDs are
preserved; no existing file ownership is changed automatically.

Older deployments with multiple references may have imported only the final
source. Check numeric IDs and existing ownership before reapplying such a
configuration with this correction.

Requests and shares
-------------------

A successful manager response confirms request acceptance, not completion.
After a timeout or daemon crash, inspect the Ceph configuration, member
services, and logs before retrying. Credentials are not retained; provide them
again if creation must be retried.

Managed enable and disable requests for the same SMB cluster share a database
admission lease across MicroCeph members. A competing request receives HTTP 409
(``operation in progress``), including when requesting background execution; it
is not queued. Requests for different SMB clusters have independent leases.

The request deadline is 15 minutes and the lease lasts 20 minutes from admission.
Normal completion releases the lease. If the outcome is uncertain, or the daemon
crashes or cannot release the lease, a later request can reclaim it after expiry.
An old request cannot release a replacement request's lease.

Expiry does not cancel outstanding Ceph work or guarantee that old callbacks have
finished. Inspect state after an uncertain outcome before retrying. Direct
``microceph.ceph smb`` updates do not acquire this managed-request lease and retain
Ceph's full-resource replacement semantics.

Shares are Ceph SMB resources:

.. code-block:: none

   sudo microceph.ceph smb apply -i share.yaml
   sudo microceph.ceph smb share rm <cluster-id> <share-id>

See the :external+upstream-ceph:doc:`Ceph SMB manager documentation <mgr/smb>`
for the resource format.
