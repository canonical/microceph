Configure a local-user SMB gateway
==================================

MicroCeph can serve CephFS through one native Samba gateway per logical SMB
cluster. The gateway supports local users and direct CephFS access only.
Clustered SMB, Active Directory, metrics, CephFS proxy and floating-IP failover
are not supported. Ceph itself may be a multi-node cluster.

Configuration is maintained by Ceph's SMB manager module. MicroCeph explicitly
places and stops the gateway; the manager must not schedule containers. No
configuration polling or membership updater runs in this version.

Prerequisites
-------------

Use an operational MicroCeph cluster with a CephFS volume and a gateway address
reachable by clients. The snap requires snapd 2.78 or later. For an unsigned local
artifact, install with ``sudo snap install --dangerous microceph_*.snap`` and
connect the normal MicroCeph interfaces, plus these gateway interfaces::

    sudo snap connect microceph:network
    sudo snap connect microceph:network-control
    sudo snap connect microceph:process-control
    sudo snap connect microceph:smb-identity
    sudo snap connect microceph:smb-mounts
    sudo snap connect microceph:smb-control-mounts
    sudo snap restart microceph.daemon

``smb-identity`` permits Samba to switch to authenticated users. The mount grants
permit only private passwd, group and nsswitch files in child namespaces; they
do not replace the host's identity files. No additional SMB runtime directory or
resolver access is needed. The ``smbd`` service is disabled at installation.
Open your chosen SMB TCP port in any external firewall; MicroCeph does not add
firewall rules.

If the SMB module is already active with orchestration enabled or unknown,
stop existing SMB deployments and enter maintenance before changing it. Never
silently take over a cephadm-managed deployment. On a new deployment::

    sudo microceph.ceph config set mgr mgr/smb/update_orchestration false
    sudo microceph.ceph mgr module enable smb

Define users and a direct share
------------------------------

Create the CephFS directory first and grant the selected numeric UID/GID access
through its existing ownership/ACLs. MicroCeph does not change data permissions.
Local user IDs below are examples; choose IDs suitable for your filesystem.

Create a root-private resource file (``umask 077`` before creating it). Replace
``CHANGE-ME``, ``192.0.2.10``, ``cephfs`` and ``/team`` before applying::

    resource_type: ceph.smb.usersgroups
    users_groups_id: team-users
    values:
      users:
        - name: alice
          password: CHANGE-ME
          uid: 2201
          gid: 2200
      groups:
        - name: team
          gid: 2200
    ---
    resource_type: ceph.smb.cluster
    cluster_id: files
    auth_mode: user
    clustering: never
    user_group_settings:
      - source_type: resource
        ref: team-users
    bind_addrs:
      - address: 192.0.2.10
    custom_ports:
      smb: 445
    ---
    resource_type: ceph.smb.share
    cluster_id: files
    share_id: data
    cephfs:
      volume: cephfs
      path: /team
      provider: samba-vfs/new

Apply the file, then enable on the selected gateway::

    sudo microceph.ceph smb apply -i /root/smb-resources.yaml
    sudo microceph enable smb --cluster-id files --wait=true
    sudo microceph status smb

``microceph status smb`` prints credential-free JSON for the local member,
including failed/uncertain owners with no successful service row. Use
``microceph status smb --target <member>`` to query one explicit remote member;
it does not aggregate or repair health. Bare ``microceph status`` retains the
normal cluster deployment summary. A SQL reservation without a published local
owner file is reported as uncertain, never as ready. Ambiguous reservations or a
failed ownership query produce an error, not an empty healthy result. Status
never repairs or retires ownership.

Use ``provider: samba-vfs/new`` explicitly for direct CephFS. The proxied provider
is rejected. Omitted bind addresses select a local address on Ceph's
``public_network``; explicit address/network selectors must match a local
interface. Only the SMB service port is configurable here.

Configuration must be direct and inspectable. Samba ``include``, ``copy`` and
``config file`` indirection, registry configuration backends and registry shares
are unsupported in global options, share defaults and individual shares. These
could change effective policy after admission. Put supported options directly in
the managed resources instead; a later syntax-only ``testparm`` success does not
make external configuration safe.

Verify authenticated access with a client such as ``smbclient``. Use a protected
client authentication file rather than placing passwords in argv::

    smbclient //192.0.2.10/data -p 445 -A /path/to/private.auth -c 'ls'

Verify a real upload/download and wrong-password refusal before relying on the
share. Do not expose the private resource file or gateway state in support logs.
Service inventory reports only applied source references and endpoints.

Maintenance, restart and disable
-------------------------------

To change shares, users or endpoints, explicitly stop the gateway, modify and
apply the Ceph SMB resources, then enable it again::

    sudo microceph disable smb --cluster-id files
    sudo microceph.ceph smb apply -i /root/smb-resources.yaml
    sudo microceph enable smb --cluster-id files --wait=true

Ordinary daemon restart does not reconfigure or enable SMB. snapd independently
supervises the prepared native ``smbd`` service; a same-gateway service restart or
host reboot consumes retained configuration and SAM/SID state without the Go
daemon running. A partially published or unprepared projection cannot start.
Configuration changes do not propagate automatically in this version.

Disable retains the gateway's local SAM/SID and configuration for re-enable.
These identities are gateway-local, not portable across moves to another gateway.
A stop and relocation may place a new local SAM against the same CephFS data;
it is not an identity migration. Returning to retained original gateway state
preserves its identity. Back up data and identity state through your operational
backup procedure before maintenance.

``sudo microceph disable smb --cluster-id files --purge`` removes only that
confirmed-stopped gateway's local directory. It deliberately destroys its local
SAM/SID. Only after successful directory destruction does it clear that exact
member's SQL SAM history. It does not delete CephFS data, shared Ceph keys,
logical SMB resources or another gateway's state. A purge can also be retried
after normal retirement or a failed purge: exact SQL history authorizes local
cleanup only if the shared service is confirmed inactive and no conflicting
local owner exists.

Recovery and rollback limits
----------------------------

An uncertain start/stop retains ownership. Retry explicit disable on the exact
owner and confirm it stopped; never remove SQL ownership or an owner file as a
substitute for stopping/fencing an offline gateway. Reusing a member name does
not authorize taking over its previous database identity. Forced member removal
cannot certify that SMB stopped.

Inspect ``microceph status smb``, ``snap services microceph`` and service journals
first. Detailed failures live beneath the root-private
``/var/snap/microceph/common/smb/<cluster-id>/diagnostics`` directory. Do not share
configuration, keyring, SAM or native-error contents without secret review.
A changed data key is refused and requires explicit recovery, not a fresh SAM.

Before the first SAM binding, MicroCeph records write-ahead initialization
uncertainty in SQL, bound to the member's database identity, and writes a private
``identity/local-sam`` fence. This is not proof of successful initialization.
Normal disable and offline member removal do not clear this history. Preparation
and independent snap startup require the fence and both retained
``samba/private/passdb.tdb`` and ``secrets.tdb``. They refuse missing, empty or
unsafe files before account import or SID queries, which can themselves recreate
Samba databases. Even an empty-user/shareless gateway has a local SAM.

After an interrupted first initialization, lost databases, missing fence or
contradictory retained state, do not retry by deleting files or SQL fields.
Restore a coherent backup of the original gateway's identity and associated
state through an explicit administrative recovery procedure. There is no
automatic reconstruction or recovery/rejoin operation. If intentionally replacing
rather than recovering the old identity, use explicit ``disable smb --purge``
after verified stop, then enable to create a new SAM/SID. If directory removal or
the SQL history-clear transaction fails, purge remains failed and conservative
history is retained; retry the exact purge before enabling. Never purge when the
original identity must be preserved.

Missing historical SQL fields in an older development snapshot mean unknown
history, not permission to bootstrap a new SID. Such state requires explicit
administrative recovery or a new logical cluster; no automatic migration or
identity-reset compatibility is promised.

There is no SMB deployment state in the parent MicroCeph version. Before rolling
back to it, explicitly disable SMB and preserve a backup of retained gateway
state. Parent binaries cannot manage this feature. Do not purge or reconstruct
identity state as part of a package rollback. Installed upgrade/rollback and
exact-package qualification remain required release gates.
