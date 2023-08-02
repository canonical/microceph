==========================================
Migrate automatically-provisioned services
==========================================

MicroCeph deploys automatically-provisioned Ceph services when needed. These
services include:

* MON - `Monitor service`_
* MDS - `Metadata service`_
* MGR - `Manager service`_

It can however be useful to have the ability to move (or migrate) these
services from one node to another. This may be desirable during a maintenance
window for instance where these services must remain available.

This is the purpose of the :command:`cluster migrate` command. It enables
automatically-provisioned services on a target node and disables them on the
source node.

The syntax is:

.. code-block:: none

   sudo microceph cluster migrate <source> <destination>

Where the source and destination are node names that are available via the
:command:`status` command:

.. code-block:: none

   sudo microceph status

Post-migration, the :command:`status` command can also be used to verify the
distribution of services among nodes.

**Notes:**

* It's not possible, nor useful, to have more than one instance of an
  automatically-provisioned service on any given node.

* RADOS Gateway services are not considered to be of the
  automatically-provisioned type; they are enabled and disabled explicitly on a
  node.

.. LINKS

.. _Manager service: https://docs.ceph.com/en/latest/mgr/
.. _Monitor service: https://docs.ceph.com/en/latest/man/8/ceph-mon/
.. _Metadata service: https://docs.ceph.com/en/latest/man/8/ceph-mds/
