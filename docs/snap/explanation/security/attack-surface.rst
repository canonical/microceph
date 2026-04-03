==============
Attack surface
==============

The attack surface encompasses all points where an unauthorized user could attempt
to enter or extract data from the system. For MicroCeph, these include:

Open ports and network interfaces
---------------------------------

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
-------------------------------

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
---------------

* Block Devices and Filesystems: OSDs interact directly with underlying storage
  (disks, partitions, or files configured via microceph disk add). The OSD processes
  require elevated privileges, managed within the snap's confinement.  
* dqlite Database Files: microcephd reads/writes configuration and state to dqlite
  database files located within the snap's data directory (e.g., /var/snap/microceph/common/state/).
  Access is controlled by filesystem permissions.  
* CephFS Mounts: Clients mounting CephFS interact via the Ceph kernel module or FUSE,
  requiring Cephx authentication.

Management infrastructure
-------------------------

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
