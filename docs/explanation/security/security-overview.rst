..meta::
  :description: Security in MicroCeph: encryption, secrets, auditing, access controls, and secure operation.

Security overview
=================

.. toctree::
    :hidden:
    :maxdepth: 2

    Attack surface <attack-surface>
    Best practices for secure deployment <secure-deployment>
    Best practices for secure operation <secure-operation>
    Cryptography in MicroCeph <cryptographic-approaches>
    Full disk encryption <about-fde>

Operating a MicroCeph instance involves managing Ceph storage components
contained within a snap package, orchestrated by the microceph daemon (microcephd).
Ensuring the security of this system is necessary to protect data integrity and
confidentiality. This guide provides an overview of security aspects, potential
attack vectors, and some best practices for deploying and operating MicroCeph
in a secure manner.

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


