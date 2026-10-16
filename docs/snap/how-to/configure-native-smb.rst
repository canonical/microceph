.. _configure-native-smb:

Configure native user-mode SMB
==============================

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

These are the declared SMB interfaces, not a request for broader permissions.
Manual local connections do not establish stable-channel snapd compatibility or
Snap Store approval/auto-connection policy.

Create resources
----------------

This example requires an existing CephFS volume ``smbfs`` with a **preprepared**
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

Repeat native enable on another member to use the same clustered resource. For
independent nonclustered gateways, choose ``clustering: never`` when creating the
resource instead. Changing a running gateway's mode requires explicit maintenance.
Share login controls do not replace CephFS ownership and filesystem ACLs; ensure
the managed user's Unix identity has the intended data access.

The example explicitly selects ``samba-vfs/new`` (direct CephFS), the provider
used for the demonstrated authenticated I/O milestone. Provider expansion and
defaults remain upstream authority: in the current upstream version,
``samba-vfs`` expands to ``samba-vfs/proxied``, not the demonstrated direct path.
The nondefault ports and bind selectors above have source/compiler coverage;
live listening and client I/O with those endpoint settings remain unqualified.

Endpoint rules and updates
-------------------------

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
service defaults/custom values. CTDB remains on port 4379; nondefault CTDB and
remote-control ports are rejected. Metrics retains its IPv4 wildcard listener
``0.0.0.0`` (readiness uses ``127.0.0.1``), **not** the selected SMB address.
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

``microceph.ceph orch ls --service_type smb`` reports unmanaged observed group
placement in JSON or YAML, not a resource suitable for applying back to the SMB
module. Its minimal SMB spec carries the cluster ID and the validated
``config_uri`` reference from the successfully applied upstream spec, solely to
support Ceph's inventory serialization. It does not reproduce resource settings
such as clustering mode or endpoint configuration. The reference is not source
contents or a claim about which override supplied the effective bytes.

If any member lacks a valid applied reference, or members' references disagree,
that group is omitted from ``orch ls`` with a manager warning rather than
inventing a source or breaking global inventory. Legacy rows are not backfilled
by an unchanged enable/refresh; a subsequent successful publication records the
reference. Their daemon rows remain available through ``orch ps``.

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

Add ``--purge`` only to erase that confirmed-stopped member's local Samba state.
AD, TLS, custom DNS qualification, remote control, new providers, metrics address
customization, VIPs and firewall management are outside this user-mode milestone.
