Configure a local-user SMB gateway
==================================

MicroCeph can serve CephFS through one native Samba gateway per logical SMB
cluster. The gateway supports local users and direct CephFS access only.
Clustered SMB, Active Directory, metrics, CephFS proxy and floating-IP failover
are not supported. Ceph itself may be a multi-node cluster.

Configuration is maintained by Ceph's SMB manager module. MicroCeph explicitly
places and stops the gateway; the manager must not schedule containers. No
membership updater runs in this version. An SMB-specific configuration updater
propagates supported changes to existing placements; it never places gateways.

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
-------------------------------

Create the CephFS directory first and grant the selected numeric UID/GID access
through its existing ownership/ACLs. MicroCeph does not change data permissions.
Local user IDs below are examples; choose IDs suitable for your filesystem.

Create a root-private resource file (``umask 077`` before creating it). Replace
``CHANGE-ME``, ``192.0.2.10``, ``cephfs`` and ``/team`` before applying:

.. code-block:: yaml

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

Configuration must be direct and explicit. Samba ``include``, ``copy`` and
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

Automatic configuration updates
-------------------------------

Apply supported share, local-user/password/group and endpoint changes through
Ceph's SMB manager as above. While the MicroCeph daemon is running, its
SMB-specific updater checks existing local placements every 30 seconds,
independently of monitor-address changes. A queued placement can delay a check;
once the placement lock is acquired, each update has a two-minute budget.
Allow for manager propagation, validation and restart time as well as this
interval. This is not zero-downtime configuration: changed inputs stop ``smbd``,
publish validated files, import users into its retained SAM and restart it.
Existing sessions may disconnect. Unchanged effective source documents do not
import accounts, restart or re-enable the service. Ordinary unhealthy-process
recovery remains snapd's responsibility, not permission for configuration repair.

The updater reads manager policy, the managed RADOS config/user sources, the
local endpoint and Ceph configuration. It does not provision identities/keys,
create placements, enable the manager module or reconstruct missing SAM/SID
state. It refuses unsupported authentication/mode/runtime-role changes and
mutable configuration indirection before publication. AD, clustered mode,
proxy and ownership/relocation changes remain unsupported; use explicit
maintenance for placement changes. CephFS permissions still require separate
administration.

Explicit ``microceph disable smb`` retirement/stop intent and snapd disabled
startup intent are preserved. Changes are not published to a disabled gateway
and the updater never uses ``--enable``. Explicit re-enable consumes supported
current inputs while retaining the original gateway identity. Confirmed logical
cluster deletion stops serving and withdraws its success row, but retains owner
uncertainty until explicit retirement; unknown source/transport errors do not
authorize that containment.

Status reports a pending or failed local update without publishing source
fingerprints, raw configuration or passwords. Validation/source-recheck failure
leaves the live configuration unchanged. Once publication starts, it can be
partial: preparation is invalidated before candidate/files advance and the
service cannot independently start unprepared state. Failed/interrupted work
is retried on a later check, including when upstream inputs revert. Successful
account-import/restart controls are recorded separately from health readiness;
waiting for health does not repeat those controls. A crash before that completion
record is durable can require another controlled import/restart. There is no
atomic rollback guarantee. Persistent failure, lost identity or uncertain first
initialization requires explicit maintenance/recovery, never automatic identity
reconstruction.

An upgrade from the local-user parent adopts the actual already-published
candidate only with an exact SQL owner/success row, known SAM-started history,
prepared context and retained identity files. It does not mark newly fetched
upstream changes as previously applied. Missing/incomplete parent state is not
permission to bootstrap. Installed parent-artifact upgrade qualification remains
a release gate.

Maintenance, restart and disable
--------------------------------

To avoid automatic propagation during planned maintenance, stop the gateway
before changing and applying resources, then explicitly enable it again::

    sudo microceph disable smb --cluster-id files
    sudo microceph.ceph smb apply -i /root/smb-resources.yaml
    sudo microceph enable smb --cluster-id files --wait=true

Ordinary daemon restart does not itself reconfigure or enable SMB; subsequent
updater checks propagate changed inputs on existing enabled placements. snapd independently
supervises the prepared native ``smbd`` service; a same-gateway service restart or
host reboot consumes retained configuration and SAM/SID state without the Go
daemon running. A partially published or unprepared projection cannot start.
With the daemon stopped, configuration changes wait until its updater resumes.

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

The local-user parent has no automatic updater; rolling back to it stops automatic
propagation and requires its explicit stop/apply/enable maintenance procedure.
Do not roll back a pending/failed publication: first complete it or restore a
coherent backup during maintenance. A version predating local-user SMB cannot
manage the deployment; explicitly disable SMB before that rollback and preserve
retained state. Never purge or reconstruct identity as part of package rollback.
Installed upgrade/rollback and exact-package qualification remain release gates.
