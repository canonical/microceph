.. meta::
   :description: Disaggregated Ceph architecture hardware specifications, including reference configurations for dedicated control plane: RGW, and MON/MDS nodes.

.. _hw-rec-disaggregated-architecture:

Disaggregated architecture
===========================

In the context of disaggregated nodes, **a minimum of nine nodes** is required
to be eligible for Ubuntu Pro Managed Solutions and Delivery services with
Canonical.

The initial configuration suggestion would be three nodes for the control plane:
Ceph Monitors (MONs), Ceph RADOS Gateway (RGW), and Ceph metadata servers (MDSs),
and six nodes for dedicated object storage daemons (OSDs). However, the distribution
needs to be adapted to your requirements, and allows for several combinations:

Dedicated control plane nodes
------------------------------

In some cases, customers prefer to have a set of nodes dedicated to control
plane services, with the Ceph OSDs separated. The control plane nodes would
host the Ceph MON service. It is preferred to host the RGW or MDS services
on the Ceph OSD nodes, so that the RGW and MDS services scale out with the
increase of the storage cluster itself.

Considering a generic use case, the specifications below are recommended:

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 16 cores (x86) or greater
   * - Memory
     - 32 GB RAM
   * - Network
     - 2x dual-port NICs (25 Gb+), onboard NIC for OOB
   * - Storage
     - 2x 960 GB SSD/NVMe for OS

If it is decided to host the RGW or MDS services on the control plane
nodes, Canonical recommends increasing the specifications of the control plane
nodes with the recommendations listed under the
:ref:`hw-rec-dedicated-rgw-nodes` and :ref:`hw-rec-dedicated-mds-nodes`
sections.

.. _hw-rec-dedicated-rgw-nodes:

Dedicated RGW nodes
--------------------

Having dedicated RGW nodes is relevant in a **high-performance object storage
Ceph cluster**. These nodes can also have the MON services collocated. An
object storage Ceph cluster needs to scale the RGW nodes with the load, and it
is common to see over 12 threads in use on a loaded RGW node.

In this case, the following specifications are recommended:

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 16 cores (x86) or greater
   * - Memory
     - 32 GB RAM
   * - Network
     - 2x dual-port NICs (25 Gb+), onboard NIC for OOB
   * - Storage
     - 2x 960 GB SSD/NVMe for OS

.. _hw-rec-dedicated-mds-nodes:

Dedicated Metadata nodes
-------------------------

Dedicated MDS nodes are relevant in a **high-performance CephFS cluster**.
These nodes can also have the MON services collocated. MDS nodes use a single
worker thread, so CPU speed matters significantly for performance.

For this case, we recommend these specifications:

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 12 cores (x86), highest GHz
   * - Memory
     - 128 GB RAM
   * - Network
     - 2x dual-port NICs (25 Gb+), onboard NIC for OOB
   * - Storage
     - 2x 960 GB SSD/NVMe for OS
