.. meta::
   :description: Hyperconverged Ceph architecture hardware specifications, including reference configurations for performance, capacity, and general-purpose storage deployments.

.. _hw-rec-hyperconverged-architecture:

Hyperconverged architecture
============================

In the context of hyperconverged nodes, **a minimum of six nodes is required**
to be eligible for Ubuntu Pro Managed Solutions and Delivery services with
Canonical.

We'll provide three reference specifications based on the purpose of the
storage environment, i.e. performance, capacity and general-purpose storage.

Performance
-----------

In the trade-off between cost and performance, performance-oriented nodes
prioritise low latency, high throughput, and high concurrency.

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 56 cores (x86) or greater
   * - Memory
     - 256 GB RAM
   * - Network
     - 2x dual-port NICs (25 Gb+), onboard NIC for OOB
   * - Storage
     - 2x 960 GB SSD/NVMe for OS; 8x enterprise NVMe for OSDs

Capacity
--------

Available storage disk capacity (what users will see).

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 32 cores (x86)
   * - Memory
     - 256 GB RAM
   * - Network
     - 2x dual-port NICs (25 Gb+), onboard NIC for OOB
   * - Storage
     - 2x 960 GB SSD/NVMe for OS; 4x enterprise SSD/NVMe for RadosGW
       metadata OSDs (4% NL-SAS capacity) WAL/DB*; 20x NL-SAS or SSD for OSDs

.. note::

   Separate WAL/DB or bcache disks should only be purchased if there is a
   significant difference in performance between the OSD disks (slow disks,
   i.e. "Very Read Intensive SSDs") and the dedicated WAL/DB disks (fast
   disks).

General-purpose storage
-----------------------

A balanced approach between available capacity and performance.

.. list-table::
   :widths: 20 80
   :header-rows: 0

   * - Processor
     - 64 cores (x86) or greater
   * - Memory
     - 256 GB RAM
   * - Network
     - 2x dual-port NICs (25 Gb+), onboard NIC for OOB
   * - Storage
     - 2x 960 GB SSD/NVMe for OS; 20x enterprise SSD/NVMe for OSDs
