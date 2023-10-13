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

* RGW (`RADOS Gateway service`_)

This is the purpose of the :command:`enable` command. It manually enables a
new instance of a service on a node.

The syntax is:

.. code-block:: none

   sudo microceph enable <service> --target <destination> ...

Where the service value is one of 'mon', 'mds', 'mgr', and 'rgw'. The
destination is a node name as discerned by the output of the :command:`status`
command:

.. code-block:: none

   sudo microceph status

For a given service, the :command:`enable` command may support extra
parameters. These can be discovered by querying for help for the respective
service:

.. code-block:: none

   sudo microceph enable <service> --help

Example: enable an RGW service
------------------------------

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

.. LINKS

.. _Manager service: https://docs.ceph.com/en/latest/mgr/
.. _Monitor service: https://docs.ceph.com/en/latest/man/8/ceph-mon/
.. _Metadata service: https://docs.ceph.com/en/latest/man/8/ceph-mds/
.. _RADOS Gateway service: https://docs.ceph.com/en/latest/radosgw/
