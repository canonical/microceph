.. _enable-smb:

Serve SMB shares from MicroCeph
===============================

MicroCeph can serve CephFS subvolumes over SMB using Samba, clustered
with CTDB for high availability. The feature is driven entirely through
the upstream ``ceph smb`` manager module: MicroCeph acts as its
orchestrator backend and deploys ``smbd``/``ctdbd`` on the placed nodes.

.. note::

   SMB support currently requires the snap to be installed in devmode:
   strictly confined ``smbd`` needs ``setgroups`` and the ``setuid``/
   ``setgid`` capabilities, which no existing snapd interface grants. A
   dedicated ``smb-support`` interface is being proposed to snapd; until
   it lands, install with ``--devmode``.

Prerequisites
-------------

- A bootstrapped MicroCeph cluster with OSDs and a CephFS filesystem.
- One unused IP address per placed node, in the nodes' subnet, to serve
  as CTDB public addresses (VIPs). Clients connect to these.

Enable the orchestrator backend
-------------------------------

The ``smb`` manager module submits deployment specs to an orchestrator.
Point it at MicroCeph's:

.. code-block:: none

    $ sudo microceph.ceph mgr module enable smb
    $ sudo microceph.ceph mgr module enable microceph
    $ sudo microceph.ceph orch set backend microceph
    $ sudo microceph.ceph orch status
    Backend: microceph
    Available: Yes

Prepare the share path and users
--------------------------------

Create a subvolume to back the share. Setting the mode at creation
avoids having to mount the filesystem just to fix permissions:

.. code-block:: none

    $ sudo microceph.ceph fs subvolume create newfs s1 --mode 0777

SMB users authenticate against Samba's clustered password database,
which MicroCeph seeds automatically, but each one must map to a system
user present on every placed node:

.. code-block:: none

    $ sudo useradd -M -s /usr/sbin/nologin smbuser

Create the SMB cluster
----------------------

Create a CTDB-clustered SMB cluster with user authentication, placed on
three nodes, listing one public address per node:

.. code-block:: none

    $ sudo microceph.ceph smb cluster create dev user \
        --define-user-pass=smbuser%s3cr3t \
        --placement=count:3 --clustering=always \
        --public-addrs=10.0.0.200/24 \
        --public-addrs=10.0.0.201/24 \
        --public-addrs=10.0.0.202/24

Create the share
----------------

Shares are declared against the cluster. Use the declarative interface
with the ``samba-vfs/new`` provider; the imperative
``ceph smb share create`` defaults to a proxied provider that MicroCeph
does not deploy:

.. code-block:: none

    $ cat share1.yaml
    resource_type: ceph.smb.share
    cluster_id: dev
    share_id: share1
    cephfs:
      volume: newfs
      subvolume: s1
      path: /
      provider: samba-vfs/new

    $ sudo microceph.ceph smb apply -i share1.yaml

Inspect the deployment:

.. code-block:: none

    $ sudo microceph.ceph smb show
    $ sudo microceph.ceph orch ls
    NAME     PORTS  RUNNING  PLACEMENT
    smb.dev             3/3  count:3

Connect from a client
---------------------

Any SMB client can connect through a public address:

.. code-block:: none

    $ smbclient //10.0.0.200/share1 -U smbuser%s3cr3t
    smb: \> put file.txt

Failover semantics
------------------

When a node fails, CTDB moves its public addresses to a surviving node.
This is reconnect-based failover, not transparent state migration: open
sessions against a failed node drop, and clients re-establish them
against the same address once it is re-hosted (typically well under two
minutes with default timers). Applications should treat an SMB session
drop as retryable.

Remove the cluster
------------------

Removal is also driven through the manager module:

.. code-block:: none

    $ sudo microceph.ceph smb share rm dev share1
    $ sudo microceph.ceph smb cluster rm dev

This stops and removes ``smbd``/``ctdbd`` from all placed nodes and
deletes the per-cluster service state. The CephFS data backing the
share is left untouched.
