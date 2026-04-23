.. meta::
   :description: CPU recommendations for Canonical Ceph services, including core count guidance for OSDs, MONs, MDS, RGW, and NVMe-oF Gateway instances.

.. _hw-rec-cpu-recommendations:

CPU recommendations
===================

:ref:`Different Ceph services have different CPU resource requirements <processor-requirements>`,
and so does the Ubuntu OS. Below is a summary table of the processor requirements per
service type running on a node.

.. list-table::
   :widths: 25 25 50
   :header-rows: 1

   * - OS/Ceph service
     - CPU requirements (cores)
     - Performance notes
   * - Ubuntu Operating System
     - 4
     -
   * - Ceph MON
     - 4
     - Fast disk (SSD or better)
   * - Ceph OSD
     - | 1 per HDD
       | 2–4 per SATA SSD
       | 4–8 per NVMe
     - | 1 core per 1,000–3,000 IOPS
       | 1 core per 200–500 MB/s
   * - MDS
     - 4
     - Single-threaded service. High clock rate (GHz) preferred.
   * - RadosGW
     - 4–16
     - Can consume up to 16 cores for higher performance (>25 Gbit/s).
   * - NVMe-oF GW
     - 4–30
     - For a PoC with <10,000 IOPS, 4 cores are the minimum requirement. Values between
       200,000 and 300,000 IOPS require 25–30+ cores.
