Security overview
=================

.. toctree::
    :hidden:
    :maxdepth: 2

    Cryptography in MicroCeph <cryptographic-approaches>
    Full disk encryption <full-disk-encryption>

Operating a MicroCeph instance involves managing Ceph storage components
contained within a snap package, orchestrated by the microceph daemon (microcephd).
Ensuring the security of this system is necessary to protect data integrity and
confidentiality. This guide provides an overview of security aspects, potential
attack vectors, and some best practices for deploying and operating MicroCeph
in a secure manner.

Architectural overview
----------------------

Understanding the MicroCeph architecture is the first step towards securing it.
MicroCeph packages core Ceph daemons (MON, MGR, OSD, and optionally RGW, MDS)
into a single snap. These daemons are managed by the microcephd service, which
uses a distributed dqlite database for configuration and state. Management is
primarily done via the microceph command-line tool interacting with microcephd,
alongside standard snapd services.

Components
~~~~~~~~~~

* Host System: The underlying Linux operating system where the MicroCeph
  snap is installed.
* MicroCeph Snap: The package containing Ceph daemons, microcephd, and
  management logic. It runs with confinement provided by snapd. Also see
  the `Snap security documentation <https://snapcraft.io/docs/snap-explanation#p-111647-security>`_
  for details.  
* microcephd: The core service (based on Microcluster) responsible for managing the
  MicroCeph cluster state, coordinating actions across nodes (if clustered), and managing
  the Ceph daemons within the snap.  
* dqlite Database: A distributed SQLite database used by microcephd to store cluster
  configuration, node status, and other metadata.   
* microceph CLI: The primary tool used by administrators to interact with microcephd
  for managing MicroCeph instances.  
* Ceph Daemons (within the snap):  

  * ceph-mon: Ceph Monitor (MON) daemon(s).  
  * ceph-mgr: Ceph Manager (MGR) daemon(s), providing access to management
    APIs and modules like the Dashboard.  
  * ceph-osd: Ceph Object Storage Daemons (OSDs), managing data on underlying storage devices.  
  * ceph-radosgw (optional): RGW (object-storage S3/Swift gateway) service.  
  * ceph-mds (optional): Metadata Server (MDS) daemons for CephFS.  

* Client Workloads: Consume Ceph storage via RBD block devices, RGW object buckets,
  or CephFS shared filesystems.

Attack surface
--------------

The attack surface encompasses all points where an unauthorized user could attempt
to enter or extract data from the system. For MicroCeph, these include:

Open ports and network interfaces
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Ceph daemons and potentially microcephd listen on TCP ports. Use host-level firewalls
(like ufw, firewalld, or nftables) to control access.

.. list-table::
   :header-rows: 1

   * - Port
     - Component
     - Purpose
     - Security Considerations
   * - 3300, 6789
     - Ceph MON
     - Monitor daemon client communication
     - Should ideally be restricted to internal networks and specific client subnets via firewall.
   * - 6800-7300
     - Ceph OSD/MGR/MDS
     - Intra-cluster communication
     - Must be strictly firewalled from external access. Essential for cluster operation.
   * - 80
     - RGW (HTTP)
     - RADOS Gateway (Object storage access)
     - Object storage access. Only enable if needed.
   * - 443
     - RGW (HTTPS)
     - RADOS Gateway secure traffic (HTTPS)
     - Object storage access. Requires TLS certificate management (see Encryption section). Only enable if needed.
   * - 9283
     - MGR (Dashboard)
     - Ceph Dashboard HTTPS access
     - Access should be restricted via firewall. Authentication is required.
   * - 9128
     - MGR (Prometheus)
     - Prometheus metrics endpoint
     - Restrict access to monitoring servers via firewall.
   * - Internal/Local
     - microcephd
     - Local API socket for microceph CLI
     - Access controlled by filesystem permissions on the socket file within the snap's data directory.
   * - 7443
     - microcephd
     - Inter-node communication (if clustered)
     - Uses TLS. Must be firewalled from external access, allowing only cluster members.
   * - 22
     - SSH
     - Host OS access
     - Standard SSH hardening practices (key auth, restricted access, firewall).
   * - Other
     - Other Services
     - Potentially other services on host
     - Audit all open ports on the host system.


Network protocols and endpoints
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

* Ceph Protocol (Messenger v1/v2): Used for all internal Ceph communication (MON, OSD, MGR, MDS).
  Messenger v2 (default in newer Ceph versions) provides encryption capabilities for data in transit.  
* Microcluster Protocol: Used for communication between microcephd instances in a multi-node cluster.
  This communication is secured using TLS.  
* Cephx Authentication: Primary mechanism for authenticating Ceph internal and client
  communication. It provides mutual authentication.  
* HTTP/HTTPS (RGW): Used for S3/Swift access via the RADOS Gateway. HTTPS with
  strong TLS configuration is best practice.  
* SSH: Used for accessing the host system to run microceph commands and perform
  system maintenance.  
* Local Socket API (microcephd): Communication between microceph CLI and microcephd
  occurs over a Unix domain socket, protected by filesystem permissions.

Data interfaces
~~~~~~~~~~~~~~~

* Block Devices and Filesystems: OSDs interact directly with underlying storage
  (disks, partitions, or files configured via microceph disk add). The OSD processes
  require elevated privileges, managed within the snap's confinement.  
* dqlite Database Files: microcephd reads/writes configuration and state to dqlite
  database files located within the snap's data directory (e.g., /var/snap/microceph/common/state/).
  Access is controlled by filesystem permissions.  
* CephFS Mounts: Clients mounting CephFS interact via the Ceph kernel module or FUSE,
  requiring Cephx authentication.

Management infrastructure
~~~~~~~~~~~~~~~~~~~~~~~~~

The primary management attack surface is the host, snap environment, and the microcephd service:

* microceph CLI: Accessing this command usually requires sudo privileges on the host.
  Compromising a host would allow an attacker to impact the Ceph cluster.  
* microcephd: Compromising the microcephd process could allow manipulation of the cluster
  state and Ceph daemon configuration. Vulnerabilities in microcephd or the underlying
  Microcluster library are potential vectors.  
* Host OS: Compromise of the host OS grants control over MicroCeph, including access to
  microcephd and its database. Standard host hardening is advised.  
* Snap Environment (snapd): Vulnerabilities in snapd or the MicroCeph snap package itself
  could be vectors. Note that MicroCeph is running with strict snap confinement; see
  `here <https://snapcraft.io/docs/snap-confinement>`_ for details on confinement.  
* Ceph Dashboard: If enabled, secure its access via network controls and strong authentication.

Access controls
---------------

Robust access controls limit users and services to only the permissions they require.

Cephx authentication and authorization
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Cephx remains the native Ceph authentication system for Ceph client/daemon interactions.

* Key Types: Use distinct keys for different roles (admin, osd, mds, client). Manage
  these using standard Ceph commands prefixed with sudo microceph.ceph (e.g., sudo microceph.ceph auth add ...).  
* Capabilities (Caps): Assign the minimum necessary capabilities to each key 
  e.g., ``mon``, ``allow r``, ``osd``, ``allow``). Avoid using the client.admin key for applications.

User management (Ceph Dashboard / RGW)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

* Dashboard Users: Manage user accounts and roles within the Ceph Dashboard for
  accessing monitoring and limited management functions.  
* RGW Users: If using RGW, manage its separate S3/Swift users, keys (access key, secret key),
  and potentially quotas using RGW admin commands (sudo microceph.radosgw-admin ...).

Management infrastructure access
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Control access at multiple levels:

* Host OS Access (POSIX permissions): Implement standard Linux user/group permissions,
  strong SSH key management, and tightly controlled sudo rules on the host machine where
  MicroCeph runs. This governs access to the microceph CLI and the microcephd's data files/socket.  
* microcephd Access: Interaction with microcephd is primarily via the microceph CLI,
  which requires appropriate host OS permissions (sudo). Direct access to the microcephd
  API socket is protected by file permissions.  
* Snap Confinement: Rely on the isolation provided by snapd as a baseline security
  feature for the snap's processes and data.  
* Elevated Privileges: OSD processes require superuser privileges for device access,
  which is managed internally by MicroCeph and the snap environment.

Secrets
-------

Protect sensitive information:

* Cephx Keys: Stored within the snap's data directory (e.g., /var/snap/microceph/current/conf).
  Protect host access.  
* TLS Certificates & Keys: For microcephd inter-node communication (if clustered).
  These are typically managed internally by Microcluster/MicroCeph but stored within
  the snap's common directory.  
* dqlite Database Files: The database files in /var/snap/microceph/common/state/
  contain sensitive cluster configuration. Protect host access and ensure correct
  file permissions.  
* RGW User Keys: S3/Swift access and secret keys. Treat these like passwords;
  manage and distribute them securely.

Encryption
----------

Protecting data confidentiality both in transit and at rest:

In transit:
~~~~~~~~~~~

* Ceph Messenger v2: Configure Ceph internal communication (between MON, OSD, MGR, MDS)
  to use secure mode via Ceph configuration options (e.g., ms_cluster_mode \= secure).  
* microcephd Communication: Communication between microcephd nodes in a cluster is secured
  using TLS by default.  
* TLS at RGW: Essential for encrypting S3/Swift traffic. Use strong TLS protocols
  (TLS 1.2+) and ciphers. Obtain certificates from a trusted CA or manage an internal PKI.   
* Ceph Dashboard HTTPS: The dashboard uses HTTPS by default.  
* microceph CLI to microcephd: Communication occurs over a local Unix domain socket
  and is not typically encrypted itself, relying on filesystem permissions for security.

At rest:
~~~~~~~~

* OSD Encryption (via LUKS): MicroCeph supports encrypting data stored on OSDs using LUKS,
  configured during disk addition (via flags to microceph disk add). This protects data
  if physical drives are compromised. Key management is handled by MicroCeph.  
* dqlite Database: The dqlite database files themselves are not typically encrypted
  at rest by default. Protection relies on standard filesystem permissions and potentially
  Full Disk Encryption (FDE) of the host system.  
* Full Disk Encryption (FDE): Consider encrypting the entire host OS disk,
  to protect Ceph keys, the dqlite database, configs, and potentially cached data against
  physical access. Manage FDE at the OS level.

Secure deployment
-----------------

Incorporate security from the initial setup of your MicroCeph instance.

Network architecture
~~~~~~~~~~~~~~~~~~~~

* Segmentation: If the MicroCeph host has multiple network interfaces, configure Ceph's
  public_network and cluster_network settings appropriately (check MicroCeph docs for details),
  configure the microcephd listen/advertise addresses if needed for clustering, and use the firewall
  to enforce segregation between client access networks, cluster networks, and management networks.  
* As a best practice, use firewalling or VLANs to segregate into these zones:  

  * External (optional): If applicable, expose specific endpoints for external untrusted consumption, e.g. RGW.  
  * Storage Access: Client access (including RGW if no external access provided), MON access.  
  * Cluster Network: OSD replication and heartbeat traffic. Isolating this improves performance and security.  

* Firewalls: Implement strict firewall rules (e.g. using iptables, nftables) on all nodes:  

  * Deny all traffic by default.  
  * Allow only necessary ports between specific hosts/networks (refer to the port table).  
  * Restrict access to management interfaces (SSH, Juju, Dashboard) to trusted administrative networks.

Minimum privileges
~~~~~~~~~~~~~~~~~~


* Cephx Keys: Create dedicated Cephx keys for each client/application with minimal capabilities.
  Don't use client.admin routinely.  
* OS Users: Limit sudo access on the host machine. Restrict who can run microceph commands.
  Run other applications on the host as unprivileged users. Protect access to the snap's
  data directories.  
* Explicit Assignment: Ensure all access relies on explicit permissions/capabilities
  rather than default permissive settings.

Auditing and centralized logging
--------------------------------

* Enable Auditing:  

  * Configure Ceph logging levels via Ceph configuration options
  (e.g., log_to_file \= true, debug_mon, debug_osd). Check MicroCeph
  documentation for how to set these. Ceph logs are found in /var/snap/microceph/common/logs/ceph/.  
  * microcephd logs to /var/log/syslog, see the MicroCeph documentation for details on setting log levels.  

* Centralized Logging: Configure host-level standard log shipping mechanisms
  (e.g., rsyslog, journald forwarding) to send Ceph logs, microcephd logs,
  and host system logs (syslog, auth.log, kern.log, journald) to a central
  logging system (like Loki or ELK).  
* Monitor and Audit: Regularly review logs for anomalies and security events
  (e.g., repeated auth failures, crashes, microcephd errors).

Alerting
--------

* Configure Monitoring: Enable the Prometheus MGR module
  (sudo microceph.ceph mgr module enable Prometheus) and configure it if necessary
  via Ceph MGR configuration options (e.g., sudo microceph.ceph config set mgr mgr/prometheus/...).  
* Security Alerts: Configure alerts for security anomalies and health issues such as:  

  * Ceph health status changes (HEALTH_WARN, HEALTH_ERR).  
  * Ceph daemon crashes or restarts (via systemd unit status or logs).  
  * microcephd service failures or restarts.  
  * Significant performance deviations.  
  * Host system issues (CPU, RAM, Disk I/O).

Secure operation
----------------

Maintaining security is an ongoing process.

Vulnerability management
~~~~~~~~~~~~~~~~~~~~~~~~

* Monitor Advisories: Actively track CVEs and security advisories for:  

  * Ceph (via Ceph announce list, security trackers).  
  * MicroCeph snap (check snap channels/updates).  
  * Host OS (use relevant security advisories for the host OS, e.g., USNs for Ubuntu).  

* Patch Management: Implement a process for testing and applying security patches promptly
  using sudo snap refresh microceph and the host OS's package manager
  (e.g., apt update && apt upgrade for Debian/Ubuntu). Use snap channels
  (e.g., the /candidate channel) for testing before refreshing stable.

Incident response
~~~~~~~~~~~~~~~~~

* Develop a Plan: Have a documented Incident Response (IR) plan for your
  MicroCeph environment, including steps related to microcephd and the dqlite database.  
* Define Steps: Cover detection, containment (e.g., firewalling the host,
  stopping services like snap.microceph.daemon), eradication, recovery
  (potentially involving database restoration if needed), and post-mortem analysis.  
* Practice: Test the plan periodically.

Perform audits
~~~~~~~~~~~~~~

* Regular Checks: Conduct periodic security audits of the MicroCeph host, configuration,
  and data directories.  
* Validate Controls: Verify firewall rules, Ceph configuration, microcephd status and
  configuration, Cephx permissions (sudo microceph.ceph auth ls), OS access controls
  (sudo rules, SSH keys, file permissions on /var/snap/microceph/), and encryption settings.

Perform upgrades
~~~~~~~~~~~~~~~~

* Stay Current: Regularly upgrade MicroCeph (sudo snap refresh microceph), snapd
  (sudo snap refresh snapd), and the underlying OS (using the host's package manager) for
  security patches and features. Upgrading the MicroCeph snap updates Ceph, microcephd,
  dqlite, and Microcluster together.  
* Schedule Proactively: Plan and test upgrades, especially for security vulnerabilities.
  Utilize snap channels for pre-production testing.

Release notes
~~~~~~~~~~~~~

* Always read the release notes for Ceph versions included in MicroCeph snap updates,
  the MicroCeph snap itself, and the host OS before upgrading or making significant changes,
  as they contain information about security fixes, new features, and potential issues.