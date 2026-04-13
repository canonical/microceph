.. meta::
   :description: Disk and storage device recommendations for Canonical Ceph deployments, including guidance on device types, interfaces, performance optimisation, and HDD write-ahead-log configuration.

.. _disk-recommendations:

Disk recommendations
====================

When it comes to storage configuration, the more important factor to consider
is the disk and interface type, rather than the disk capacity. There are
various types of storage devices: hard disk drives (HDDs); solid-state drives
(SSDs), and various types of storage interfaces: serial-attached SCSI (SAS);
Serial Advanced Technology Attachment (SATA), and Non-Volatile Memory Express
(NVMe), available on the market. Their cost per GB varies from around $0.03 to
$3. Moreover, there are various types of storage available in the cloud:
ephemeral, block, and object storage. Therefore, designing storage
price-performance requires some more attention and further optimisations.

Optimisation is required for persistent storage (block, file, and object).
Canonical Ceph uses replication as default, where data is stored in three
copies distributed over three failure domains. This ensures that each failure
domain can be lost without short-term impact to services. This also means that
the overall amount of raw persistent storage required is usually significant.

Enterprise NVMe or SAS/SATA SSD devices with power loss protection should be
purchased. Consumer-grade devices without this feature have very unpredictable
performance characteristics, and in the worst-case scenario, can cause
data-loss.

NVMe and SSD devices have a durability characteristic, Drive Writes per Day
(DWPD); for Ceph, we recommend a value of 1 or above.

Since Ceph is designed to work with more nodes, each with less storage, it is
recommended to limit the Ceph raw storage to the allowance per object storage
daemon (OSD) node as defined in the `Ubuntu Pro service description
<https://ubuntu.com/legal/ubuntu-advantage-service-description#uasd-storage-support>`_.
OSDs should always be distributed equally across all available OSD nodes in
the cluster for predictable performance, higher resilience and sustainable
maintenance.

Improving HDD performance
--------------------------

A recommended approach to increase the performance of an OSD is to use a
faster storage device for the write-ahead-log (WAL) and database (DB). For
example, an OSD on a SAS-HDD could have its WAL and DB located on a faster
NVMe device. One faster device can be shared between multiple OSDs by locating
the WAL and DB on partitions of the faster device.

Care needs to be taken to ensure that the NVMe device is not used for too many
OSDs, otherwise it can become a performance bottleneck and create operational
risk when shared amongst a large number of OSDs.

This approach accelerates effective write performance with the WAL and metadata
performance with the DB. As metadata performance is an important part of OSD
operations, Canonical recommends providing fast storage for both the WAL and
DB.
