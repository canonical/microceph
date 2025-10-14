Remote Replication
==================

Remote replication in Ceph storage clusters is a key feature designed to enhance data protection and
enable disaster recovery for organizations of all sizes. It allows data from one Ceph cluster to be
duplicated and synchronized to another, often at a geographically distant site, ensuring that information
remains safe even in the event of serious failures, site-wide outages, or other catastrophic events.

This guide covers the essentials for new Ceph users, explaining the types and modes and objectives of
replication, and their roles in disaster recovery.

Recovery Objectives
-------------------

Recovery Point Objective (RPO)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Recovery Point Objective is the maximum acceptable amount of data loss, measured as time, that a business
can tolerate after a disaster or outage. It specifies how far back in time data can be recovered from
backup or replication to minimize the impact from the disruption.

Recovery Time Objective (RTO)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Recovery Time Objective is the maximum acceptable amount of time that a business process, application,
network, or system can be down after a disruption before significant damage or intolerable consequences occur.

Modes of data movement
-----------------------

Replication between clusters can be implemented in two common modes, each with specific operational characteristics:

Push Replication:
~~~~~~~~~~~~~~~~~

In this mode, the primary (source) cluster actively sends data updates to the secondary (target) cluster. The
replication process is initiated and managed by the source cluster, ensuring changes are quickly and centrally
propagated. This is easier to administer for simpler environments but can place higher resource demands on
the primary cluster.

Pull Replication:
~~~~~~~~~~~~~~~~~

Here, the secondary cluster initiates and manages copying updates from the primary. This model is adaptable
for distributed, decentralized management or remote sites wanting control over bandwidth and timing. It scales
efficiently in large environments, although the configuration is slightly more complex.

Replication Architectures
-------------------------

Based on cost, complexity, and recovery objectives an organisation can choose between these two architectures.

Active-Active Replication
~~~~~~~~~~~~~~~~~~~~~~~~~

Both clusters (sites) handle read and write operations, synchronizing changes in real time between them.
This ensures high availability and fault tolerance, if one site goes down, users can continue working on the
other with no data loss. It is best for use cases requiring continuous operation and zero recovery point
objective (RPO).

Active-Passive Replication
~~~~~~~~~~~~~~~~~~~~~~~~~~

Only one cluster (active) is used for operations, while the passive cluster acts as a backup. Data is
replicated asynchronously to the passive site, which becomes operational only during failover. This approach
is suitable for DR in scenarios where the secondary site isn’t needed for real-time access, accepting
minor data delays after failover.

Disaster Recovery
-----------------

Active-Active
~~~~~~~~~~~~~

This architecture offers real-time failover and is ideal for mission-critical applications, but requires careful
planning regarding network latency, consistency management, and administrative overhead.

Active-Passive
~~~~~~~~~~~~~~

Offers simpler and resource-efficient DR strategy at the cost of some data loss as agreed upon by the Recovery
point objective (RPO).

