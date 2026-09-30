.. meta::
   :description: Reference for managed SMB services, commands, networking, placement, and supported configuration in MicroCeph.

.. _smb-reference:

SMB service reference
=====================

MicroCeph manages SMB clusters through the Ceph SMB manager and the MicroCeph
orchestrator backend. Administrators add and remove an SMB instance on one
MicroCeph member at a time. Instances using the same cluster ID form one
managed SMB cluster.

Enable an SMB instance
----------------------

.. code-block:: none

   microceph enable smb --cluster-id <cluster-id> [--target <member>] [flags]

The first member creates the SMB cluster and requires a local-user
configuration source. The default clustering mode uses CTDB even with one
member: connect ``microceph:smb-identity`` and ``microceph:ctdb-run`` before
applying a share. With explicit ``--clustering never``, only ``smb-identity``
is required and only one member is supported.

Initial enable completes when Ceph accepts the cluster and credential resources;
it does not mean that SMB daemons are running. Ceph defers initial deployment
until a CephFS-backed share exists. Apply the first share before adding another
member. Later enables do not require credentials again.

Options
~~~~~~~

``--cluster-id``
   Required. Identifies the managed SMB cluster. It must contain 1 to 18 ASCII
   alphanumeric or hyphen characters, with alphanumeric first and last characters,
   matching the upstream Ceph SMB resource rules.

``--target``
   The MicroCeph member on which to enable an SMB instance. The local member is
   used when omitted.

``--credentials-file``
   Read creation-only credentials from a JSON file, or from stdin with ``-``.
   The format is ``{"users":[{"name":"alice","password":"..."}]}``.
   Protect files with mode ``0600``. Password arguments and interactive prompts
   are not supported. This option cannot be combined with ``--user-group-ref``.

``--clustering``
   ``always`` enables CTDB; ``never`` runs a single instance without CTDB.
   Omission defaults to ``always`` on creation and preserves the existing mode
   on updates. Explicit mode changes are rejected; remove and recreate the
   cluster to change its mode.

``--user-group-ref``
   Reference an existing Ceph SMB users-and-groups resource when creating the
   cluster. The option is repeatable.

``--bind-address``
   Add a client-facing address from which each selected member chooses its
   local ``smbd`` address. Repeat the option to supply addresses for multiple
   members.

``--bind-network``
   Add a client-facing CIDR from which each member selects its local ``smbd``
   address. Repeat the option for multiple client networks. It is mutually
   exclusive with ``--bind-address``.

``--port``
   Set the SMB listening port. The default is 445. Valid explicit values are
   from 1 through 65535. Clients using a custom port must specify it too (for
   example, ``smbclient -p 1445 //member/share``). CTDB continues to use 4379.

``--wait``
   Wait for the managed request to complete (default true). Initial creation
   still requires a first share before deployment. With ``false``, a successful
   response means acceptance, not completion; subsequent errors appear in the
   daemon log. Desired resources and placement ordering are owned by the Ceph
   SMB manager.

The bind and port options are cluster-wide. When they are supplied while
adding another member, MicroCeph updates the complete SMB cluster
configuration rather than only the target member. A new bind list replaces
rather than extends the existing list, so repeat every address or network that
other members still require.

Disable an SMB instance
-----------------------

.. code-block:: none

   microceph disable smb --cluster-id <cluster-id> [--target <member>]

Removing a non-final member updates the cluster placement and keeps the other
instances running. Removing the final member removes the Ceph SMB cluster. A
cluster with shares must have those shares removed before its final member can
be removed.

Runtime services
----------------

A clustered deployment runs these snap services on every selected member,
including when only one member remains. Non-clustered deployments run only
``microceph.smbd``:

``microceph.smbd``
   Serves SMB clients and accesses CephFS through ``vfs_ceph_new``.

``microceph.ctdbd``
   Coordinates clustered Samba state over the MicroCluster member network.

``microceph.ctdb-nodes``
   Reconciles CTDB membership from the Ceph SMB cluster metadata.

Inspect them with:

.. code-block:: none

   snap services microceph.smbd microceph.ctdbd microceph.ctdb-nodes
   microceph.ctdb status

Network defaults
----------------

CTDB and SMB use different address roles:

* CTDB always uses the member's MicroCluster internal address. CTDB members
  must be able to reach each other on TCP port 4379. The address does not need
  to be reachable by SMB clients or respond to ICMP.
* ``smbd`` uses an explicit ``--bind-address`` or an address selected from
  ``--bind-network``. When neither is supplied, it selects a local address from
  Ceph's ``public_network``. Clients connect to that address on TCP port 445 or
  the configured ``--port``.

MicroCeph does not currently configure CTDB public or floating addresses.
Clients must reconnect to another member address after a member failure.

Placement rules
---------------

* A member can run at most one SMB cluster because there is one local Samba
  configuration, even when SMB clusters use different ports.
* A clustered SMB cluster can contain multiple MicroCeph members.
* A non-clustered SMB cluster supports one member only.
* Adding or removing members does not change the clustering mode.
* Failed placement updates preserve existing members: removals are attempted
  only after all requested placements succeed. Retry the apply after fixing
  the reported failure.
* Removed CTDB members retain commented rank slots. New members do not renumber
  survivors. Rejoining the same member identity requires its original
  MicroCluster address; in-place address migration is not supported.
* Labels, host patterns, per-host daemon names, and unknown members are not
  supported by the native MicroCeph SMB orchestrator.

Supported configuration
-----------------------

The managed CLI supports local-user authentication, direct
``samba-vfs/new`` CephFS access, an optional SMB bind address or network, and
an optional SMB port. The orchestrator supports clustered and single-member
non-clustered specifications. It rejects other SMB features.

The following are not currently supported:

* Active Directory or domain join sources;
* ``cephfs-proxy``;
* CTDB public or floating addresses;
* custom CTDB ports;
* custom DNS, remote-control TLS, metrics sidecars, unmanaged services, and
  preview-only services;
* placement network restrictions and arbitrary Ceph configuration overrides;
* additional Ceph users beyond the cluster's generated SMB identity; and
* arbitrary container arguments or Samba configuration files.

Local user identity limitations
-------------------------------

Samba uses a minimal passwd/group database plus users imported from Ceph, not
copies of the host's complete account databases. MicroCeph follows Sambacc 0.9:
without explicit upstream IDs, users receive UID and primary GID ``1000 + list
position``. Configured groups also receive positional GIDs; missing user groups
are generated by Sambacc.

Identical ordered resources produce identical current mappings on each member.
They do not preserve historical ownership across resource edits. For example,
if Alice is UID 1000 and Bob is UID 1001, deleting Alice makes Bob UID 1000 after
regeneration. Existing CephFS file ownership remains numeric: Bob may inherit
Alice's ownership interpretation and lose his previous one. Append users to
preserve earlier positions; do not reorder entries. Deletion, insertion, or
reordering requires careful consideration of existing file permissions.
Active Directory support is not yet available.

Request completion and retries
------------------------------

MicroCeph reads the current upstream placement and submits a complete updated
resource to the Ceph SMB manager. A successful manager response confirms
acceptance. It does not merge competing placement snapshots: concurrent managed
or direct Ceph updates follow upstream replacement semantics. Run membership
changes sequentially when every addition or removal must be preserved.

After a timeout or daemon crash, remote work may still continue. Do not assume
a failed command left no upstream resources. Inspect Ceph configuration, member
services, and daemon logs before retrying. Credentials are not retained in the
database: resubmit them if creation must be retried. Partial or conflicting
creation requires operator attention, not blind overwrite. No managed-request
lock or SQL lock reset is involved.

Share operations
----------------

Shares remain Ceph SMB resources and are managed with
:command:`microceph.ceph smb`. For example:

.. code-block:: none

   sudo microceph.ceph smb apply -i share.yaml
   sudo microceph.ceph smb share rm <cluster-id> <share-id>

See the :external+upstream-ceph:doc:`Ceph SMB manager documentation <mgr/smb>`
for its resource format. MicroCeph supports only the subset listed on this
page.
