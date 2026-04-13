.. meta::
   :description: Hardware specification recommendations for Canonical Ceph clusters, covering cluster service placement strategies, infrastructure node requirements, memory, software, CPU, and certified hardware.

.. _canonical-ceph-hardware-recommendations:

Canonical Ceph hardware recommendations
========================================

This section outlines Canonical Ceph hardware specification recommendations.
Following these configurations is highly recommended as a proven way to achieve
the best price-performance.

.. caution::

   Deviations from these specifications are possible, but they can affect both
   the overall storage performance and its total cost of ownership (TCO). We
   strongly recommend consulting Canonical before making any changes to the
   reference configurations and prior to purchasing any hardware.

Canonical provides different specifications based on cluster service placement
architecture, i.e. hyperconverged or disaggregated. For hyperconverged nodes,
we base our specifications on performance, capacity and general-purpose use
cases. For disaggregated scenarios, we recommend specifications based on
several combinations as per customer requirements, e.g. dedicated control
plane nodes, dedicated RADOS Gateway (RGW) nodes, and dedicated Metadata
server (MDS) nodes.

The Canonical Ceph hardware recommendation section provides infrastructure
node recommendations for the different ways of deploying Canonical Ceph, i.e.
via snap or charms. It also outlines memory and processor requirements, and
provides software recommendations.

.. toctree::
   :maxdepth: 2
   
   deployment-strategies
   infrastructure-node-requirements
   memory-requirements
   software-requirements
   cpu-recommendations
