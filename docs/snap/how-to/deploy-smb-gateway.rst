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
* network access from SMB clients to TCP port 445 on every SMB member; and
* Snapd with support for the ``microceph-support`` identity-switching policy.

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
   sudo microceph.ceph fs subvolume create smbfs shared --mode 0770

Enable the three SMB instances
------------------------------

The first command creates the managed SMB cluster and its initial local user.
Replace the example password before running it. Be aware that a password
provided on a command line may be retained in shell history.

.. code-block:: none

   sudo microceph enable smb \
      --cluster-id files \
      --target node1 \
      --define-user-pass 'smbuser%REPLACE_WITH_A_SECRET'

Add the other two members to the same SMB cluster:

.. code-block:: none

   sudo microceph enable smb --cluster-id files --target node2
   sudo microceph enable smb --cluster-id files --target node3

MicroCeph submits the complete three-member placement to the Ceph SMB manager.
Each member runs ``microceph.smbd``, ``microceph.ctdbd``, and
``microceph.ctdb-nodes``.

Create the SMB share
--------------------

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

From an SMB client, create a test file and connect to each member address:

.. code-block:: none

   printf 'MicroCeph SMB test\n' > test-file
   smbclient //node1-address/shared -U 'smbuser%REPLACE_WITH_A_SECRET' -c 'put test-file'
   smbclient //node2-address/shared -U 'smbuser%REPLACE_WITH_A_SECRET' -c 'get test-file result-node2'
   smbclient //node3-address/shared -U 'smbuser%REPLACE_WITH_A_SECRET' -c 'get test-file result-node3'

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
