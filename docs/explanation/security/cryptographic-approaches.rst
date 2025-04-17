Cryptographic approaches in MicroCeph
=======================================

MicroCeph is a Ceph cluster in a single snap package. The snap is built on top of Ubuntu Ceph packages,
and, therefore, shares some of their security features. This section makes references to `Cryptography in Ubuntu Ceph packages
<https://ubuntu.com/ceph/docs/cryptographic-technologies-in-charmed-ceph>`_.

Snap security features
----------------------

MicroCeph is distributed as a snap package. Snaps offer some inherent security features, including:

* **Sandboxing and confinement**: snaps are containerized applications that run in a sandboxed environment.
  This isolation is achieved using Linux kernel security features such as ``AppArmor``, ``Seccomp``, and ``cgroups``.
  These tools limit the system resources that snaps can access, preventing unauthorized access to the host system.

* **Confinement level**: MicroCeph uses strict confinement. This level provides the highest security by restricting
  access to system resources unless explicitly allowed through interfaces.

* **Interfaces and plugs**: snaps use interfaces to request specific permissions for accessing system resources.
  This allows for granular access to underlying hosts resources, i.e. only the resources a specific application
  requires need to be allowed. Specific resources that can be used by MicroCeph are discussed in the next section.

* **Updates**: snaps offer an easy network-based update mechanism. By default, snaps automatically upgrade;
  however a best practice for MicroCeph production deployments is to hold automatic upgrades so that operators
  can make use of zero-downtime upgrades for multi-node clusters.

* **Cryptographic signatures**: the snap store uses cryptographic signatures to verify the integrity and authenticity of snaps.
  This ensures that users download genuine software that has not been tampered with.

* **Secure distribution**: The snap store acts as a central repository where snaps are published and distributed securely.
  The store implements both automated checks and manual reviews to ensure the quality and security of the software it hosts.

Resources used by MicroCeph snaps
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Interfaces
^^^^^^^^^^

MicroCeph snaps can use the following snap interfaces:

* ``kernel-module-load``: to enable loading of Ceph-specific kernel modules, such as the ``rbd`` module.  
* ``ceph-conf``: to expose Ceph configuration to cooperating snaps.
* ``ceph-logs``: to allow access to log files.

Plugs
^^^^^

MicroCeph snaps can use the following snap plugs:

* ``block-devices``: for storage  
* ``dm-crypt``: for disk encryption  
* ``hardware-observe``: for block device detection  
* ``home``: provides access to user-supplied configuration and certificates  
* ``microceph-support``: for additional storage  
* ``mount-observe``: for storage management  
* ``network``: networking client  
* ``network-bind``: network servers
* ``process-control``:to support resource limits configuration

Microcluster security
---------------------

MicroCeph is built on the Canonical Microcluster library. Microcluster manages cluster topology and cluster membership.
Adding nodes is regulated by issuing tokens which new nodes can use to trigger a request to join the cluster.
After successfully joining the cluster, Microcluster issues TLS certificates to new nodes. ``TLS1.3``, by default, is the minimum
supported version. This can be overridden by setting an environment variable to set the minimum supported version to ``TLS1.2``.

Cluster members use the newly created certificates to authenticate for cluster API access.

Ceph authentication and authorization
-------------------------------------

Internally, MicroCeph deploys a Ceph cluster which uses the ``cephx`` protocol for authentication and
authorization. See `Cryptography in Ubuntu Ceph
<https://ubuntu.com/ceph/docs/cryptographic-technologies-in-charmed-ceph#p-151613-cryptography-in-ubuntu-ceph>`_ for more details.

Data at rest
------------

MicroCeph offers full disk encryption (FDE) for Object Storage Daemons (OSDs), activated when adding disks. Note that this disk encryption
only pertains to user data managed in Ceph, and not encryption of the cluster data (such as administrative data, 
logs, etc.).
To use FDE, users must first connect the ``dm-crypt`` plug for MicroCeph (it is not auto-connected).
When FDE is requested, MicroCeph generates a random key and stores it in the Ceph Monitor (MON). This key is then used to setup
Linux Unified Key Setup (LUKS) via ``cryptsetup``, using ``cipher AES-XTS-plain64`` and ``SHA256`` hashing, with a 256-bit keysize.

.. note::
    While the FDE approach for OSD encryption shares some of the techniques employed by the data at rest
    encryption features in Ubuntu Ceph, it's a separate implementation due to the specific sandboxing needs of the MicroCeph snap.

Storage types
-------------

Like Ceph, MicroCeph provides three types of storage, i.e. object, block and file storage, to clients. It does so
via specific components that support specific security features.

RGW object storage
~~~~~~~~~~~~~~~~~~

Accessing Ceph object storage happens via the RADOS Gateway (RGW) service. This service supports transport security
via SSL/TLS for encrypting client traffic. To do this, it will need to be configured with certificate
material for SSL/TLS.
In MicroCeph, SSL/TLS certificates can be provided when enabling the RGW service.

Server-side encryption
^^^^^^^^^^^^^^^^^^^^^^

The RGW service supports server-side encryption (SSE) according to the Amazon SSE specifications.
MicroCeph does not offer key managemenMicroCeph provides the same three types of storage to clients as Ceph. Each type of storage supports t for RGW; therefore, only the customer key mechanism is supported.
This is done via the Amazon ``SSE-C`` specification, which uses ``AES256`` symmetric encryption. RGW implements this as
``AES256-CBC``. Moreover, as per the SSE-C specification, keys may be provided as ``128-bit MD5 digest``.

CephFS file storage
~~~~~~~~~~~~~~~~~~~

CephFS provides filesystem storage to clients in MicroCeph, similarly to how Ubuntu Ceph provides filesystem storage.
Client access is regulated using the Ceph-native ``cephx`` protocol, which performs authentication and authorization for
clients. Ceph supports access control to specific filesystem and filesystem subtrees.

RBD block storage
~~~~~~~~~~~~~~~~~

Like in Ubuntu Ceph, the RADOS Block Device (RBD) component can provide block devices backed by MicroCeph storage.
Access to RBD is regulated using the native ``cephx`` protocol for authentication and authorization.

RBD encryption
^^^^^^^^^^^^^^

Users can instruct Ceph to encrypt block device images utilizing the ``rbd`` encryption format commands.
RBD supports the ``AES128`` and ``AES256`` algorithms, with ``AES256 XTS-plain64`` being the default.

Dashboard
~~~~~~~~~

The MicroCeph dashboard provides basic administrative capabilities. Access to the dashboard can be secured via SSL/TLS.
The dashboard module also exposes an API, the Ceph RESTful API. Like regular dashboard access, this can be secured
through SSL/TLS. The RESTful API can make use of JSON Web Tokens (JWTs) using the ``HMAC-SHA256`` algorithm.

Summary of cryptographic components
-----------------------------------

In summary, the cryptographic libraries and tools used in MicroCeph are:

* ``dm-crypt``
* LUKS  
* OpenSSL