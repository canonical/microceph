.. meta::
   :description: CPU requirements and recommendations for Canonical Ceph services, including OSDs, MONs, MDS, RGW, and the NVMe-oF Gateway, to help plan and size hardware for a Ceph storage cluster.

.. _processor-requirements:

Processor requirements
======================

Choosing a processor with sufficient capacity is very important. In most
cases, the processor is the only component that cannot be replaced once
purchased. Its capacity (measured by the number of cores or threads) cannot be
scaled up as with the memory or storage. Moreover, the number of central
processing unit (CPU) sockets inside a server is always limited. So, in order
to get more CPU resources, it is required to either buy higher-end processors,
or buy more servers. More servers generate additional costs on their own,
require additional space in the data centre and network fabric, and consume
more power, so it is usually more economical to choose higher-end processors
unless the cost per thread becomes unreasonably high.

Some CPU resources are required for operating system (OS) and control plane
services. Different Ceph services have different CPU resource requirements, and
so does the Ubuntu OS. For both OS and control plane services, the performance
governor should be used, as Ceph benefits from consistently fast CPU
performance.

This section outlines CPU requirements for Ceph Object Storage Daemons (OSDs),
Ceph Monitors (MONs), the CephFS Metadata Server (MDS), RADOS Gateway (RGW),
and the NVMe-oF Gateway service. Read the `upstream Ceph documentation
<https://docs.ceph.com/en/latest/start/hardware-recommendations/#cpu>`_ to
learn more about CPU recommendations.

OSDs
----

In general, reserving 2 cores per OSD is sufficient. The type of storage
device also impacts the number of cores utilised by the OSD. A non-volatile
memory express (NVMe) OSD drive can leverage up to 6 cores, which will provide
higher input/output operations per second (IOPS) than if fewer cores are
available. It is therefore more reliable to consider the desired IOPS and
calculate the number of cores required from there. 1 core can provide
1000–3000 IOPS and 200–500 MB/s throughput.

MONs
----

Ceph MONs are control plane services that are not very CPU-intensive. Resource
requirements spike when the cluster is undergoing changes; the Ceph MON
service requires 2 to 4 cores to function adequately. `For quorum, Ceph MONs
should be an odd number
<https://docs.ceph.com/en/latest/rados/operations/add-or-rm-mons/>`_.

MDS
---

Metadata servers are CPU-intensive. They benefit from high clock rate (GHz),
and typically only need 2 to 4 cores per MDS.

RGW
---

RGW requires a minimum of 2 cores. In high-performance scenarios, providing a
dedicated server to RADOSGW with 16+ cores will provide better performance.

NVMe-oF Gateway
---------------

The Ceph NVMe-oF Gateway service is a CPU-intensive service, and CPU usage
grows together with IOPS. In general, separate hosts are strongly preferred
for NVMe-oF gateway daemons (or at least isolating them). Provision at least
two NVMe-oF gateways in a gateway group, on separate Ceph cluster nodes, for a
highly-available Ceph NVMe/TCP solution.

At least 4 CPU cores dedicated to each NVMe-oF gateway instance are highly
recommended.

See CPU requirements for a summary of the processor requirements per type of Ceph service.
