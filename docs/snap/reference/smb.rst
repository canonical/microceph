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
With ``--clustering never``, only ``smb-identity`` is required and the cluster
supports one member.

Initial enable accepts the cluster and credential resources, but deployment
starts only after a CephFS-backed share is applied. Later enables do not require
credentials again.

See :doc:`commands/enable` for all flags. Cluster IDs contain 1 to 18 ASCII
alphanumeric or hyphen characters, with alphanumeric first and last
characters. The bind and port options apply to the entire cluster; a new bind
list replaces the existing list.

Disable an SMB instance
-----------------------

.. code-block:: none

   microceph disable smb --cluster-id <cluster-id> [--target <member>]

Removing a non-final member updates the placement. Remove all shares before
removing the final member, which removes the SMB cluster. See
:doc:`commands/disable` for flags.

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

A member can run one SMB cluster. Clustered clusters support multiple members;
non-clustered clusters support one. Adding or removing members does not change
the clustering mode. If placement fails, existing members remain in place.

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

Requests and shares
-------------------

A successful manager response confirms request acceptance, not completion.
After a timeout or daemon crash, inspect the Ceph configuration, member
services, and logs before retrying. Credentials are not retained; provide them
again if creation must be retried.

Shares are Ceph SMB resources:

.. code-block:: none

   sudo microceph.ceph smb apply -i share.yaml
   sudo microceph.ceph smb share rm <cluster-id> <share-id>

See the :external+upstream-ceph:doc:`Ceph SMB manager documentation <mgr/smb>`
for the resource format.
