.. meta::
   :description: An overview of Ceph data persistence mechanisms, including replication and erasure coding, and how failure domains are used to ensure resilience in a Canonical Ceph cluster.

.. _data-persistence-mechanisms:

Data persistence mechanisms
===========================

Ceph uses intelligent data replication to ensure resiliency. Ceph models its
underlying infrastructure with the concept of failure domains; replicated data
is saved across different failure domains. Each failure domain should be
separated in such a way that losing one domain won't impact the remaining
failure domains. Examples include, but are not limited to, separated power
supplies and cooling systems, networking equipment, etc.

Ceph also offers erasure coding for improved storage efficiency, where data is
split into chunks that are distributed across failure domains.

In the unlikely event of a complete domain failure or broken networking between
failure domains, Ceph will try to avoid a split-brain scenario. A single
failure domain will never form a quorum, so no write operations will happen
there. Only if a majority of the failure domains are able to communicate will
Ceph permit quorum formation and allow full service.

.. admonition:: Info
   :class: info

   For replicated pools, the number of replicas for objects in the pool is
   configured to 3 (``size``). The minimum number of written replicas for
   objects in the pool in order to acknowledge an I/O operation to the client
   is by Ceph's default configured to 2 (``min_size``). See the
   :external+upstream-ceph:confval:`pool, placement group (PG), and CRUSH config reference <osd_pool_default_min_size>`
   in the upstream documentation.


Replication
-----------

By default, data in Ceph is stored in three copies over different hosts.
Canonical's recommendation is to configure the failure domain to reflect the
deployment's physical architecture. This can mean configuring the failure
domain to the availability zone (AZ) level, assuming the physical deployment
has at least three separate AZs, preferably four. The three copies are then
spread across AZs. This ensures that each AZ can be lost without short-term
impact to the Ceph service. In the event of a host failure, Ceph will
replicate the objects stored in that host to another host in the same AZ. If
a complete AZ is down, Ceph will replicate to another AZ, only if an unused AZ
is available.

Erasure coding
--------------

Erasure coding allows dividing objects into K data chunks and M calculated
coding chunks that allow data to be recovered in the event of a missing chunk.
The K+M chunks are spread across failure domains, one chunk per failure domain.
This requires the Ceph cluster to have K+M failure domains, with K+M+1 or
greater failure domains recommended.

.. toctree::
   :maxdepth: 1

   replication-vs-erasure-coding
