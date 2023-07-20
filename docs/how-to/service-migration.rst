========================================
Migrate essential services between nodes
========================================

MicroCeph automatically deploys essential Ceph services when needed. Essential
services include:

* MON (`monitor service`_)
* MDS (`metadata service`_)
* MGR (`manager service`_)

It can be useful however to have the ability to move (or migrate) essential
services from one node to another, such as during a maintenance window.

This is the purpose of the :command:`cluster migrate` command. It enables
essential services on a target node and disables them on the source node.

The syntax is:

.. code-block:: none

   sudo microceph cluster migrate <source> <destination>

Where the source and destination are node names. For example:

.. code-block:: none



.. note::

   * It's not possible, nor useful, to have more than one instance of
     an essential service on any given node.
   * RGW services (RADOS Gateway) are not considered essential; they are
     enabled and disabled explicitly on a node.

Use the :command:`microceph status` command to verify distribution of services
among nodes.

<!-- LINKS -->

_manager service: https://docs.ceph.com/en/latest/mgr/
_monitor service: https://docs.ceph.com/en/latest/cephadm/services/mon/
_metadata service: https://docs.ceph.com/en/latest/cephadm/services/mds/
