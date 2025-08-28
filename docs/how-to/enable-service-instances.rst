=====================================
Enabling additional service instances
=====================================

To ensure a base level of resiliency, MicroCeph will always try to enable a
sufficient number of instances for certain services in the cluster. This
number is set to three by default.

The services affected by this include:

* MON (`Monitor service`_)
* MDS (`Metadata service`_)
* MGR (`Manager service`_)

Cluster designs that call for extra service instances, however, can be
satisfied by manual means. In addition to the above-listed services, the
following service can be added manually to a node:

* NFS
* RGW (`RADOS Gateway service`_)

This is the purpose of the :command:`enable` command. It manually enables a
new instance of a service on a node.

The syntax is:

.. code-block:: none

   sudo microceph enable <service> --target <destination> ...

Where the service value is one of 'mon', 'mds', 'mgr', 'nfs-<cluster-id>' and
'rgw'. The destination is a node name as discerned by the output of the
:command:`status` command:

.. code-block:: none

   sudo microceph status

For a given service, the :command:`enable` command may support extra
parameters. These can be discovered by querying for help for the respective
service:

.. code-block:: none

   sudo microceph enable <service> --help

Let's take an example of enabling RGW and NFS services, viewing the possible
extra parameters for both services.

Enable an RGW service
---------------------

First check the status of the cluster to get node names and an overview of
existing services:

.. code-block:: none

   sudo microceph status

   MicroCeph deployment summary:
   - node1-2c3eb41e-14e8-465d-9877-df36f5d80922 (10.111.153.78)
     Services: mds, mgr, mon, osd
     Disks: 3
   - workbook (192.168.29.152)
     Services: mds, mgr, mon
     Disks: 0

View any possible extra parameters for the RGW service:

.. code-block:: none

   sudo microceph enable rgw --help

To enable the RGW service on node1 and specify a value for extra parameter
`port`:

.. code-block:: none

   sudo microceph enable rgw --target node1 --port 8080

Finally, view cluster status again and verify expected changes:

.. code-block:: none

   sudo microceph status

   MicroCeph deployment summary:
   - node1 (10.111.153.78)
     Services: mds, mgr, mon, rgw, osd
     Disks: 3
   - workbook (192.168.29.152)
     Services: mds, mgr, mon
     Disks: 0

Enable an NFS service
---------------------

View any possible extra parameters for the NFS service:

.. code-block:: none

   sudo microceph enable nfs --help

To enable the NFS service on ``node1``, and specify values for the extra
parameters:

.. code-block:: none

   sudo microceph enable nfs --cluster-id foo-cluster --v4-min-version 2 --target node1

View cluster status again and verify the expected changes:

.. code-block:: none

   MicroCeph deployment summary:
   - node1-2c3eb41e-14e8-465d-9877-df36f5d80922 (10.111.153.78)
     Services: mds, mgr, mon, nfs.foo-cluster osd
     Disks: 3
   - workbook (192.168.29.152)
     Services: mds, mgr, mon
     Disks: 0

.. note::

   Enabling NFS on multiple nodes with the same ``--cluster-id`` will
   effectively result in the running NFS services to be grouped in the same
   service cluster.

.. caution::

   Nodes in the same NFS service cluster **must** have matching configuration
   (``--v4-min-version``), otherwise MicroCeph will return an error when adding
   new nodes to the cluster.

.. caution::

   A node may join only one NFS service cluster. MicroCeph will return an error
   if there's already a NFS service registered on the node. If a node would
   have to join a different NFS service cluster, it would have to leave the
   original cluster first:

.. code-block:: none

   sudo microceph disable nfs --cluster-id foo-cluster --target node1

After the NFS cluster has been set up, you can create NFS shares. Next will be
a basic example in which we're creating an NFS export and mounting it.

Create a volume:

.. code-block:: none

   sudo microceph.ceph fs volume create foo-vol

Create an NFS export by running the following command:

.. code-block:: none

   sudo microceph.ceph nfs export create cephfs foo-cluster /fs-foo-dir foo-vol

   # Sample output:
   {
     "bind": "/fs-foo-dir",
     "cluster": "foo-cluster",
     "fs": "foo-vol",
     "mode": "RW",
     "path": "/"
   }

A client may now mount the NFS share. They will first need the ``nfs-common``
package:

.. code-block:: none

   sudo apt install nfs-common

Finally, the client will be able to mount the NFS share:

.. code-block:: none

   sudo mount -o rw -t nfs "nfs-bind-address:/fs-foo-dir /mnt

.. LINKS

.. _Manager service: https://docs.ceph.com/en/latest/mgr/
.. _Monitor service: https://docs.ceph.com/en/latest/man/8/ceph-mon/
.. _Metadata service: https://docs.ceph.com/en/latest/man/8/ceph-mds/
.. _RADOS Gateway service: https://docs.ceph.com/en/latest/radosgw/
