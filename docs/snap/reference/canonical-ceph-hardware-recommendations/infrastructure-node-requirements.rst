.. meta::
   :description: Infrastructure node requirements for Canonical Ceph deployments, covering MAAS, Juju, and observability stack node needs based on deployment method.

.. _hw-rec-infrastructure-node-requirements:

Infrastructure node requirements
=================================

For either hyperconverged or disaggregated scenarios, refer to the
:ref:`cluster service placement strategies <cluster-service-placement>` outlined in the
explanation section of our reference architecture. The nodes should have the following
specifications:

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 24 cores
   * - Memory
     - 128 GB RAM
   * - Network
     - 1x 4-port 1 Gb onboard NIC for OOB
   * - Storage
     - 2x 6 TB SAS 3.5" SSD in RAID-1 mode, hardware RAID controller
