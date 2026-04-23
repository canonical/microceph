.. meta::
   :description: Memory requirements for Canonical Ceph deployments, including RAM recommendations for OSD, MON, MDS, RGW, and other Ceph services.

.. _hw-rec-memory-requirements:

Memory requirements
===================

The overall amount of random access memory (RAM) is dictated by the number of disks and
overall size of the cluster. Each service type has its minimum memory amount. When building
a server, the total memory must be calculated based on the number of services collocated on
it (e.g. 12 disks equals 12 OSDs, each requiring a minimum RAM amount), and additionally
the operating system itself also needs to be considered.

The table below specifies memory requirements for different service types, and the Ubuntu OS:

.. list-table::
   :widths: 25 25 50
   :header-rows: 1

   * - OS/Ceph service
     - Memory requirements (GB)
     - Performance notes
   * - Ubuntu
     - 16
     -
   * - MON
     - 8
     - RAM consumption increases with cluster instability.
   * - OSD
     - 8
     - HDD requires a minimum of 4 GB, but will perform better at 8 GB. SSD and NVMe
       require more memory, consuming up to 16–24 GB per disk depending on tuning.
   * - MDS
     - 64
     - Up to 96 GB can improve performance; after which horizontal scaling is recommended.
   * - RadosGW
     - 4
     - Up to 32 GB can improve performance; after that, horizontal scaling is recommended.
   * - NVMe-oF GW
     - 8
     - 8 GB minimum per daemon. Requires more memory proportional to the number of RBD
       images mapped and IOPS. 16+ GB is required for 200,000+ IOPS scenarios.

.. note::
   Recommendations will change with the size and workload of the cluster.
