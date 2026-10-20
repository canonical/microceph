.. _configure-native-smb:

Configure native SMB shares
===========================

Logical clusters, shares, users, bind selectors and ports belong to the upstream
SMB resource API (``microceph.ceph smb apply``). Native ``enable smb`` only places
an existing logical cluster on a member; it does not edit that shared resource.
A member can serve one native logical cluster at a time.

Local unsigned builds
---------------------

For a locally installed unsigned snap, connect the following plugs on every
intended gateway, in addition to the normal MicroCeph storage connections:

.. code-block:: bash

   sudo snap connect microceph:network
   sudo snap connect microceph:network-bind
   sudo snap connect microceph:network-control
   sudo snap connect microceph:process-control
   sudo snap connect microceph:smb-identity
   sudo snap connect microceph:smb-mounts
   sudo snap connect microceph:ctdb-run
   sudo snap connect microceph:ctdb-socket
   sudo snap connect microceph:winbind-run

These are the declared SMB interfaces, not a request for broader permissions.
Manual local connections do not establish stable-channel snapd compatibility or
Snap Store approval/auto-connection policy.

Create resources
----------------

This example requires an existing CephFS volume ``smbfs`` with a **prepared**
``/data`` directory owned by UID 2201/GID 2200 and writable by that identity
(for example, mode 0770). Prepare and verify it using an existing administrative
CephFS client before applying the resources. Every ancestor must permit that
identity to traverse it; for subvolumes this includes the name-container parent,
not just the UUID data directory. A newly created filesystem root ``/`` is not
a writable share for the managed user merely because ``readonly`` is false.
The sample IDs below must match the prepared ownership and be available on all
gateways; use different stable IDs consistently if these are already in use.

Enable the SMB module with upstream orchestration disabled (native placement
does not use cephadm). If the module is already active with orchestration enabled,
disable/reinitialize it during maintenance before using native placement:

.. code-block:: bash

   sudo microceph.ceph config set mgr mgr/smb/update_orchestration false
   sudo microceph.ceph mgr module enable smb
   umask 077
   editor smb-resources.yaml

Save the following YAML, replacing the password and example network with your
own values. The selected network must contain an address already assigned on
each intended gateway. Resource files contain credentials: protect them and do
not paste them into status reports.

.. code-block:: yaml

   resources:
     - resource_type: ceph.smb.usersgroups
       users_groups_id: local-users
       values:
         users:
           - name: alice
             password: REPLACE-WITH-A-STRONG-PASSWORD
             uid: 2201
             gid: 2200
         groups:
           - name: data-users
             gid: 2200
     - resource_type: ceph.smb.cluster
       cluster_id: files
       auth_mode: user
       clustering: always
       user_group_settings:
         - source_type: resource
           ref: local-users
       bind_addrs:
         - network: 192.0.2.0/24
       custom_ports:
         smb: 1445
         smbmetrics: 19922
     - resource_type: ceph.smb.share
       cluster_id: files
       share_id: data
       name: Data
       cephfs:
         volume: smbfs
         path: /data
         provider: samba-vfs/new
       readonly: false
       browseable: true
       comment: Shared data
       max_connections: 32
       restrict_access: true
       login_control:
         - name: alice
           category: user
           access: read-write

Apply the resources, then place the gateway on the intended member:

.. code-block:: bash

   sudo microceph.ceph smb apply -i smb-resources.yaml
   sudo microceph enable smb --cluster-id files --target node1 --wait=true

Repeat native enable on another member to use the same clustered resource.
With ``clustering: never``, a logical cluster ID can have at most one retained
native owner. A second member is rejected even if that owner is offline or its
startup failed. Independent nonclustered services need **different cluster IDs**
on different members and disjoint data paths. CephFS sharing alone does not
coordinate Samba share modes or leases across independent services.

Use explicit ``always`` or ``never`` in operator configurations. Omitted/default
clustering keeps upstream semantics: placement count 1 disables CTDB; other counts
or omitted placement enable it. Native gateway count never selects the mode.
Active Directory requires effective clustered mode (documented configuration:
``clustering: always``), including a single gateway. The native external-AD
member path has source coverage, but installed AD authentication and group I/O
remain unqualified. Legacy domain security and AD DC modes are not supported. Effective security, realm and clustering overrides cannot contradict
the generated mode.

External AD resources
~~~~~~~~~~~~~~~~~~~~~

External-AD support means authenticating share users against an **existing**
directory and enforcing relevant domain-group access. MicroCeph is a member
server, not a domain controller or an AD user/group administration service.
Host PAM/login, host domain membership and automatic DNS/SPN management are not
provided. Source coverage and executable Robot fixtures are available; built
strict-snap AD authentication remains a separate, not-yet-completed gate.

For an existing ``EXAMPLE.TEST`` domain with short name ``EXAMPLE``, use the
upstream resource API, not local ``usersgroups``. For example:

.. code-block:: yaml

   resources:
     - resource_type: ceph.smb.join.auth
       auth_id: files-join
       auth:
         username: JOIN-AUTHORIZED-ACCOUNT
         password: REPLACE-WITH-A-PROTECTED-BOOTSTRAP-PASSWORD
     - resource_type: ceph.smb.cluster
       cluster_id: adfiles
       auth_mode: active-directory
       clustering: always
       domain_settings:
         realm: EXAMPLE.TEST
         join_sources:
           - source_type: resource
             ref: files-join
       bind_addrs:
         - network: 192.0.2.0/24
       custom_dns:
         - 192.0.2.53
     - resource_type: ceph.smb.share
       cluster_id: adfiles
       share_id: data
       cephfs:
         volume: smbfs
         path: /ad-data
         provider: samba-vfs/new
       readonly: false
       restrict_access: true
       login_control:
         - name: 'EXAMPLE\data-writers'
           category: group
           access: read-write

Protect this file with mode 0600, apply it as above, then initialize the first
native gateway completely before enabling peers with ``--cluster-id adfiles``.
Prepare ``/ad-data`` and its ancestors using the **actual retained Winbind
UID/GID mappings**, not the local-user example IDs. A new directory does not
become writable just because a share has ``readonly: false``.

``restrict_access: true`` turns positive login entries into an allowlist. Without
it, a read/write list alone is not a group-access restriction. Group names use
the directory's supported short-domain form; upstream structured login names
containing whitespace are not accepted. Share controls and CephFS Unix
ownership/ACLs must both allow the intended action. Group/cache/session changes
do not imply immediate revocation, and offline logins are not promised.

AD member runtime contract
~~~~~~~~~~~~~~~~~~~~~~~~~~

Go sequences healthy CTDB, bounded initial joining, Winbind/NSS readiness and
then smbd. These source checks are not installed AD qualification. The upstream
``addc`` instance feature denotes a **controller** and remains rejected; it is
not the external-domain member flag.

The logical cluster ID is the shared machine/NetBIOS identity, with a maximum of
15 characters in AD mode (no truncation or per-gateway accounts). The intended
service FQDN is ``<cluster_id>.<realm>``. Operators must provide working DNS/DC
resolution, synchronized time and appropriate service-name/SPN access through
each gateway. No host hostname, DNS, time or login-account changes are made.
The upstream workgroup defaults to the first uppercased realm label; a validated
short-domain override is allowed when that is not the directory's short name.
Additional service aliases require separate qualification.

The initial configuration contract uses upstream ``autorid`` with range
``2000-9999999`` and secrets-only Kerberos verification. Ordered private join
sources are bootstrap inputs, not a machine-password rotation service or SMB
user credentials. They are not imported into local SAM/passwd/group accounts.
The first credential is attempted once using the pinned upstream join operation
and password stdin. Upstream does not classify nonzero join exits as definitely
free of external effects, so native code conservatively stops instead of trying
a second credential. Correct the first source before initial enable. Once trust
exists, bootstrap credentials may be refreshed without another join or a serving
restart; Samba owns subsequent machine-password maintenance.
Inherited resolver/Kerberos settings are retained; ``custom_dns`` supplies only
an app-private resolver when needed. No mandatory keytab service is introduced.

App-local NSS uses ``files winbind`` and binds the public socket directory to
the pinned NSS client's ``/run/samba/winbindd`` path. Only smbd/Winbind receive
``winbind-run``. Its ``/run/samba`` grant is required to create the containing
directory on hosts without Samba; runtime refuses foreign parent contents,
unsafe ownership/permissions and nonempty mount targets. Only the public
``winbindd`` leaf is bound. The privileged pipe stays under the
root-only native Samba state directory. Namespace/socket source checks are not
proof of strict-snap confinement or directory authentication.

Trust (``secrets.tdb``) and allocated IDs (``autorid.tdb``, ``winbindd_idmap.tdb``)
belong to Samba/Winbind and the existing CTDB persistent context. Preparation
never creates, migrates or deletes these databases. An initialized service must
not silently rejoin or recreate mappings after state loss. Stop placement and
use explicit operator maintenance to reconcile retained trust, mappings and
filesystem ownership before restoring service. SQL retains a non-expiring
initial-attempt record tied to the immutable owner. Even the same owner cannot
retry an uncertain attempt. A timeout, cancellation, lease expiry, local marker,
PNN or CTDB recovery lock never authorizes another join. The private Winbind
control socket has fixed operations only; no domain-management CLI is provided.

Initial joining has a 60-second app execution budget; caller cancellation or
socket loss cancels its process group. A successful local exit is not an
exactly-once guarantee for AD. Failed or lost responses retain uncertainty and
private diagnostics. Initialize the first gateway before enabling peers.
Readiness checks retained trust/autorid records, live Winbind trust, domain SID,
a domain-group GID and app-private NSS. It is not proof of SMB authentication,
Kerberos, NTLMv2, all group ACLs or Windows interoperability. A short DC outage
can make readiness fail; health observation never joins, resets or restarts the
service automatically, and does not promise offline login.

Normal restart, reboot and non-purging disable/re-enable use retained CTDB
identity state. Individual retirement never leaves/deletes the AD computer,
remaps IDs or deletes peer resources. AD purge refuses the final or uncertain
identity state and, even with a retained peer, keeps local Samba/CTDB databases
and an explicit warning; it removes configuration/bootstrap credentials only.
Peer count is not a backup guarantee.

For an uncertain join or lost trust/mappings, freeze placement and stop serving
members. Preserve SQL reservations, CTDB replicas and private diagnostics. An
operator must fence any old join process and reconcile the actual AD account
and consistent trust/mapping state under maintenance; do not clear the attempt
record to retry blindly or run ``leave`` as cleanup. There is no automatic
recovery/reset API. If retained identity cannot be established, use a new logical
ID under manual maintenance and explicitly reconcile filesystem ownership/ACLs.
Recovery of an old logical identity requires separately reviewed expert
maintenance, not deletion/recreation of its trust or idmap databases.

Share login controls do not replace CephFS ownership and filesystem ACLs; ensure
the managed user's Unix identity has the intended data access.

The example explicitly selects ``samba-vfs/new`` (direct CephFS), the provider
used for the demonstrated authenticated I/O milestone. Provider expansion and
defaults remain upstream authority: in the current upstream version,
``samba-vfs`` expands to ``samba-vfs/proxied``, not the demonstrated direct path.
The custom ports and bind selectors above have source/compiler coverage;
live listening and client I/O with those endpoint settings remain unqualified.

Endpoint rules and updates
--------------------------

``bind_addrs`` is an ordered list of alternatives, **not** simultaneous listeners.
Each entry specifies exactly one ``address`` (IP literal) or ``network`` (CIDR
or host literal). IPv4 and IPv6 entries may be mixed. All entries are validated
before the first locally usable address is selected. For example, replace the
network entry above with:

.. code-block:: yaml

   bind_addrs:
     - address: 192.0.2.10
     - network: 2001:db8:1234::/64

Omitting ``bind_addrs`` selects a concrete local address on Ceph's
``public_network`` in both modes. An empty list or no local match fails
preparation. Unspecified, multicast, scoped and invalid literal addresses are
not wildcard listeners. Catch-all CIDRs remain network selectors and still
select only one suitable local address. Link-local and loopback addresses are
outside the common network helper's suitable-address policy.

SMB defaults to port 445 and metrics to 9922. ``custom_ports.smb`` and
``custom_ports.smbmetrics`` accept integers 1–65535, unique across all upstream
service defaults/custom values. CTDB remains on port 4379; CTDB and
remote-control ports must retain their default values. Metrics retains its IPv4
wildcard listener ``0.0.0.0`` (readiness uses ``127.0.0.1``), **not** the selected
SMB address.
CTDB membership, private address, PNNs and recovery identity are separate from
the SMB frontend. No address, VIP or firewall rule is created by this feature.

To change endpoints, edit and reapply the **complete cluster resource**, retaining
its user refs and mode. The existing updater re-reads upstream settings (normally
every 30 seconds), publishes configuration and restarts Samba consumers without
restarting CTDB or reallocating membership. Omitting selectors/ports restores
the defaults above. A fresh explicit enable is a placement/recovery operation,
not needed for a resource update. Application failures retain diagnostics and
are not reported as successful applied endpoints.

Credential-free observation and retirement
------------------------------------------

The ``orch`` inventory commands require the separate ``microceph`` manager
module and orchestrator backend. Enabling the upstream ``smb`` module does not
select this backend. First inspect the current selection:

.. code-block:: bash

   sudo microceph.ceph orch status

If no orchestrator is configured, enable and select the MicroCeph backend:

.. code-block:: bash

   sudo microceph.ceph mgr module enable microceph
   sudo microceph.ceph orch set backend microceph

Do not automatically replace a different backend that is already configured.
This selection supplies observed MicroCeph inventory; it does not transfer SMB
placement to upstream orchestration. Keep
``mgr/smb/update_orchestration=false``. These inventory prerequisites are separate
from native SMB serving.

``microceph.ceph orch ls --service_type smb`` reports unmanaged observed group
placement in JSON or YAML, not a resource suitable for applying back to the SMB
module. Its minimal SMB spec carries the cluster ID and the validated
``config_uri`` reference from the successfully applied upstream spec, solely to
support Ceph's inventory serialization. It does not reproduce resource settings
such as clustering mode or endpoint configuration. The reference is not source
contents or a claim about which override supplied the effective bytes.

If any member lacks a valid applied reference, or members' references disagree,
that group is omitted from ``orch ls`` with a manager warning rather than
inventing a source or breaking global inventory. Legacy rows gain the reference
only after a successful publication, not an unchanged enable/refresh. Their
daemon rows remain available through ``orch ps``.

``microceph.ceph orch ps --daemon_type smb --format json-pretty`` reports the
observed SMB address/port; its status description names the separate metrics
wildcard endpoint. Normal MicroCeph service observations also include mode,
frontend address, SMB port, metrics address, metrics port and the applied spec's
config-source reference. They exclude resource documents, credentials, keyrings
and private hashes. Legacy rows may lack endpoint facts; absence does not mean a
default was observed. These rows record successful application, not a continuous
network/listener health probe.

Stop and retire a member without deleting logical resources or CephFS data:

.. code-block:: bash

   sudo microceph disable smb --cluster-id files --target node1

For a nonclustered relocation, first stop and retire the old owner with this
command, then enable the **same logical cluster ID** on the replacement member.
An exact retained owner can retry enable idempotently. Deleting a core member or
withdrawing its successful service row does not retire an uncertain reservation;
verify stop/fencing rather than force-removing SQL rows to bypass admission.

For user-mode services, ``--purge`` erases the confirmed-stopped member's local
Samba state. For AD services it refuses final/uncertain identity loss and retains
Samba/CTDB databases even when another peer remains; see the AD maintenance
contract above. It never deletes CephFS data, logical resources or AD accounts.

Upgrade and maintenance transition
----------------------------------

Upgrade all members to compatible software with placement frozen before relying
on singleton admission. Older writers can bypass the new admission check; no
mixed-version guarantee is offered. Existing multi-owner nonclustered groups are
policy-noncompliant: additional owners are blocked, while exact-owner retries
remain allowed. No survivor is selected, service stopped or mode converted
automatically. The targeted SMB status response reports a separate
``policy_warning`` from the last reservation/refresh observation; it is not a
configuration-application error and does not change readiness. Retire surplus
owners normally after verified stop until only one remains. Offline owners remain
reserved until stop/fencing is established; do not treat forced row removal as
that evidence.

Mode, authentication mode and realm changes require maintenance migration to a
**new logical cluster ID**, preserving CephFS data while explicitly reconciling
authorization and Unix ownership/ACLs. Retained mode and rank history means that
deleting and recreating the same logical ID is not a clean conversion. There is
no automatic chown, SID remapping, forced domain leave or identity-state reset.

Support and qualification limits
--------------------------------

The compact external-directory canary uses a dedicated Samba AD DC, two native
gateways and fresh clients. Kerberos must use the intended logical FQDN with
``--use-kerberos=required`` and a corresponding CIFS service ticket; ``desired``
can fall back and is not Kerberos evidence. Domain NTLMv2 is tested separately
with Kerberos disabled. Success under that fixture's recorded client/DC policy
does not promise NTLM fallback under arbitrary enterprise policies.

The source campaign does not establish live protocol success, strict-snap NSS
permissions, CTDB trust/idmap replication, machine-password rotation or Windows
interoperability. An exact signed-source artifact must pass installed tests
before those tested claims are accepted. A named Windows Server/GPO/client
claim additionally requires that Windows fixture; its absence does not block
source implementation or the Samba-DC canary.

Cross-forest/offline join, alternate identity providers, destructive last-replica
recovery, automatic identity backup/restore, arbitrary joiner replacement,
exhaustive ACL/cache tests and long-duration rotation/fault matrices are deferred.
Do not infer automatic recovery from ordinary retained-state reboot/re-enable.
TLS, remote control, additional providers, metrics-address customization, VIPs
and firewall management remain outside this milestone. Custom resolver behavior
also needs qualification in the installed app context. No DNS, account or trust
repair should be applied silently to make a failed canary pass.
