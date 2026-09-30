.. meta::
   :description: Deploy a three-instance SMB cluster on MicroCeph and verify CTDB member failover and recovery.

.. _deploy-smb-gateway:

Deploy a highly available SMB gateway
=====================================

This guide deploys one managed SMB cluster with an SMB and CTDB instance on
all three members of a MicroCeph cluster. It then verifies access through every
instance, continued service after one member fails, and recovery of that member.

The deployed service exports a CephFS subvolume through Samba's direct
``ceph_new`` VFS provider. MicroCeph does not provide a floating SMB address;
clients must reconnect to another member address after a failure.

Prerequisites
-------------

You need:

* a healthy three-member MicroCeph cluster; see :ref:`multi-node-install`;
* the member names reported by :command:`microceph status`;
* network access from SMB clients to TCP port 445 (or the configured custom
  port) on every SMB member, and inter-member access to TCP port 4379 for CTDB;
  and
* snapd 2.78 or later for the ``microceph-support`` identity-switching policy.

This guide uses ``node1``, ``node2``, and ``node3`` as the MicroCeph member
names.

Connect the required snap interfaces on every member:

.. code-block:: none

   sudo snap connect microceph:smb-identity
   sudo snap connect microceph:ctdb-run

The ``smb-identity`` interface permits Samba to assume the authenticated local
user's UID and groups. The ``ctdb-run`` interface provides the shared CTDB
runtime socket directory.

Create CephFS storage
---------------------

Create a CephFS volume and a dedicated subvolume for the share:

.. code-block:: none

   sudo microceph.ceph fs volume create smbfs
   sudo microceph.ceph fs subvolume create smbfs shared --uid 1000 --gid 1000 --mode 0770

Enable the first SMB instance
-----------------------------

The first command configures the cluster and its initial user. Deployment starts
when the first share is applied. The default mode uses CTDB from the first
instance.

Create :file:`smb-users.json` with mode ``0600`` using your secret-management
workflow. Do not put the password in command arguments or shell history:

.. code-block:: json

   {"users": [{"name": "smbuser", "password": "REPLACE_WITH_A_SECRET"}]}

Then submit the file:

.. code-block:: none

   sudo microceph enable smb \
      --cluster-id files \
      --target node1 \
      --credentials-file smb-users.json \
      --port 1445

Alternatively, pipe the JSON from a secret manager and use
``--credentials-file -``. Keep the source available until creation is confirmed;
MicroCeph does not persist credentials for retry. The first user receives UID/GID
1000, matching the subvolume owner above. See :ref:`smb-reference` before editing
or reordering local users.

This example uses TCP port 1445 instead of the default 445. The port applies
to every member of the cluster; configure client access accordingly. To bind
to a specific client-facing subnet as well, add ``--bind-network <CIDR>`` on
the first command. Each member must have an address on that subnet. Alternatively,
use repeatable ``--bind-address`` options for individual member addresses.

Create the first SMB share
--------------------------

The Ceph SMB manager does not submit initial placement until a CephFS-backed
share exists. Create the first share before adding another member.

Create a file named :file:`share.yaml`:

.. code-block:: yaml

   resource_type: ceph.smb.share
   cluster_id: files
   share_id: shared
   cephfs:
     volume: smbfs
     subvolume: shared
     provider: samba-vfs/new

Apply the resource:

.. code-block:: none

   sudo microceph.ceph smb apply -i share.yaml

Add the other two members
-------------------------

.. code-block:: none

   sudo microceph enable smb --cluster-id files --target node2
   sudo microceph enable smb --cluster-id files --target node3

MicroCeph submits the updated placement to the Ceph SMB manager. Each member
runs ``microceph.smbd``, ``microceph.ctdbd``, and ``microceph.ctdb-nodes``.

Verify the cluster
------------------

Verify the managed placement:

.. code-block:: none

   sudo microceph status
   sudo microceph.ceph orch ls --service_type smb

On each member, verify the snap services and CTDB membership:

.. code-block:: none

   snap services microceph.smbd microceph.ctdbd microceph.ctdb-nodes
   microceph.ctdb status

All three services should be ``enabled`` and ``active``. CTDB should report
three nodes in the ``OK`` state.

On the SMB client, prepare a mode-``0600`` Samba authentication file named
:file:`smb-auth` with ``username = smbuser`` and ``password = <your secret>``.
Create a test file and connect without placing the password in argv:

.. code-block:: none

   printf 'MicroCeph SMB test\n' > test-file
   smbclient -p 1445 //node1-address/shared -A smb-auth -c 'put test-file'
   smbclient -p 1445 //node2-address/shared -A smb-auth -c 'get test-file result-node2'
   smbclient -p 1445 //node3-address/shared -A smb-auth -c 'get test-file result-node3'

Verify member failure and recovery
----------------------------------

Stop or isolate one member using the controls provided by your machine or VM
platform. On either surviving member, poll:

.. code-block:: none

   microceph.ctdb status

The status should continue to list three nodes, with two nodes in the ``OK``
state and the failed member unavailable.

Connect a new SMB client session to either surviving member and verify that the
existing file can be read and a new file can be written. Existing connections
to the failed member address are not moved automatically.

Restore the failed member. Verify that its three snap services return to the
``enabled`` and ``active`` state and that ``microceph.ctdb status`` reports all
three nodes as ``OK``. Data written during the outage should be accessible
through the recovered member.

Remove the SMB cluster
----------------------

Remove the share before removing the final SMB instance:

.. code-block:: none

   sudo microceph.ceph smb share rm files shared

Remove the instances one target at a time:

.. code-block:: none

   sudo microceph disable smb --cluster-id files --target node3
   sudo microceph disable smb --cluster-id files --target node2
   sudo microceph disable smb --cluster-id files --target node1

Removing a non-final member updates CTDB placement while keeping the remaining
SMB instances available. Removing the final member removes the managed SMB
cluster.

See :ref:`smb-reference` for command options and constraints, and
:ref:`smb-concepts` for the relevant architecture and network model.
