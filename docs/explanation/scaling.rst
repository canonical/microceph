Scaling MicroCeph
=================


Overview
--------

MicroCeph's scalability is courtesy of its foundation on Ceph, which has excellent scaling capabilities. To scale out, either add machines to the existing cluster nodes or introduce additional disks (OSDs) on the nodes.

Note it is strongly recommended to use uniformly-sized machines, particularly with smaller clusters, to ensure Ceph fully utilizes all available disk space.

Failure Domains
---------------

In the realm of Ceph, the concept of `failure domains`_ comes into play in order to provide data safety. A failure domain is an entity or a category across which object replicas are spread. This could be OSDs, hosts, racks, or even larger aggregates like rooms or data centers. The key purpose of failure domains is to mitigate the risk of extensive data loss that could occur if a larger aggregate (e.g. machine or rack) crashes or becomes otherwise unavailable.

This spreading of data or objects across various failure domains is managed through the Ceph's Controlled Replication Under Scalable Hashing (CRUSH_) rules. The CRUSH algorithm enables Ceph to distribute data replicas over various failure domains efficiently and without any central directory, thus providing consistent performance as you scale. 

In simple terms, if one component within a failure domain fails, Ceph's built-in redundancy means your data is still accessible from an alternate location. For instance, with a host-level failure domain, Ceph will ensure that no two replicas are placed on the same host. This prevents loss of more than one replica should a host crash or get disconnected. This extends to higher-level aggregates like racks and rooms as well.

Furthermore, the CRUSH rules ensure that data is automatically re-distributed if parts of the system fail, assuring the resiliency and high availability of your data.

The flipside is that for a given replication factor and failure domain you will need the appropriate number of aggregates. So for the default replication factor of 3 and failure domain at host level you'll need at least 3 hosts (of comparable size); for failure domain rack you'll need at least 3 racks, etc.

Failure Domain Management
-------------------------

MicroCeph implements automatic failure domain management at the OSD and host levels. At the start, CRUSH rules are set for OSD-level failure domain. This makes single-node clusters viable, provided they have at least 3 OSDs.

Scaling Up
++++++++++

As you scale up, the failure domain automatically will be upgraded by MicroCeph. Once the cluster size is increased to 3 nodes having at least one OSD each, the automatic failure domain shifts to the host level to safeguard data even if an entire host fails. This upgrade typically will need some data redistribution which is automatically performed by Ceph.

Scaling Down
++++++++++++

Similarly, when scaling down the cluster by removing OSDs or nodes, the automatic failure domain rules will be downgraded, from the host level to the osd level. This is done once a cluster has less than 3 nodes with at least one OSD each. MicroCeph will ask for confirmation if such a downgrade is necessary.

Disk removal
~~~~~~~~~~~~

The :doc:`../reference/command-disk-remove` command is used to remove OSDs.

Automatic failure domain downgrades
^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

The removal operation will abort if it would lead to a downgrade in failure
domain. In such a case, the command's ``--confirm-failure-domain-downgrade``
option overrides this behaviour and allows the downgrade to proceed.

Cluster health and safety checks
^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

The removal operation will wait for data to be cleanly redistributed before
evicting the OSD. There may be cases however, such as when a cluster is not
healthy to begin with, where the redistribution of data is not feasible. In
such situations, the command's ``--bypass-safety-checks`` option disable these
safety checks.

Custom Crush Rules
++++++++++++++++++
Please note, users can freely set custom CRUSH rules anytime. MicroCeph will respect custom rules and not perform any automatic updates for these. Custom CRUSH rules can be useful to implement larger failure domains such as rack- or room-level. At the other end of the spectrum, custom CRUSH rules could be used to enforce OSD-level failure domains for clusters larger than 3 nodes.


Machine Sizing
--------------

Maintaining uniformly sized machines is an important aspect of scaling up MicroCeph. This means machines should ideally have a similar number of OSDs and similar disk sizes. This uniformity in machine sizing offers several advantages:

1. Balanced Cluster: Having nodes with a similar configuration drives a balanced distribution of data and load in the cluster. It ensures all nodes are optimally performing and no single node is overstrained, enhancing the cluster's overall efficiency.

2. Space Utilization: With similar sized machines, Ceph can optimally use all available disk space rather than having some remain underutilized and hence wasted.

3. Easy Management: Uniform machines are simpler to manage as each has similar capabilities and resource needs.

As an example, consider a cluster with 3 nodes with host-level failure domain and replication factor 3, where one of the nodes has significant lower disk space available. That node would effectively bottleneck available disk space, as Ceph needs to ensure one replica of each object is placed on each machine (due to the host-level failure domain).



.. _`failure domains`: https://en.wikipedia.org/wiki/Failure_domain
.. _CRUSH: https://docs.ceph.com/en/latest/rados/operations/crush-map/
