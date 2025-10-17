Remote replication
==================

Cloud storage services (like Ceph) are responsible for persisting information irrespective of faults and
failures in parts of the cluster. These faults could be temporary network failures, disk faults, power failures
or even failure of multiple nodes. Remote replication is a mechanism used to replicate data to a remote storage
cluster, typically located at a different geographical site to prevent complete outage in the event of a large
enough fault like natural disaster.

This section covers the essential ``remote replication concepts`` for users.

Modes of data movement
-----------------------

Replication between clusters can be implemented in two common modes, each with specific operational characteristics:

Push replication:
~~~~~~~~~~~~~~~~~

In this mode, the source cluster actively sends data updates (aka deltas or diffs) to the target cluster. The
replication process is initiated and managed by the source cluster, ensuring changes are centrally propagated.
This is easier to administer at smaller scale but can place higher resource demands on the primary cluster for
larger clusters.

Pull replication:
~~~~~~~~~~~~~~~~~

In this mode, the target cluster initiates and manages copying (or pulling) updates from the source. This model
provide target sites the control over bandwidth and timing. It scales efficiently in large environments, although
is slightly more complex to implement and administer.

Replication architectures
-------------------------

Based on cost, complexity, and recovery objectives, a choice can be made between these two architectures.

Active-Active replication
~~~~~~~~~~~~~~~~~~~~~~~~~

Both clusters handle read and write operations, synchronising changes in real time between them. This ensures high
availability because if one site goes down, users can continue operation on the other with no data loss. However, in
order to maintain simultaneous state consistency across active sites, each operation has to be acknowledged by each
active site before being considered complete. This can introduce latency, especially for geographically distant
sites. It is best for use cases requiring continuous operation and zero recovery point objective (`RPO`_). 

Active-Passive replication
~~~~~~~~~~~~~~~~~~~~~~~~~~

The active cluster is used to serve clients, while the passive cluster acts as a backup. Data is replicated asynchronously
to the passive cluster, which becomes operational only during failover. This approach is suitable for Disaster Recovery (DR)
in scenarios where the secondary site isn’t needed for real-time access, accepting minor data delays after failover.

Disaster recovery objectives
-----------------------------

It is important to set realistic objectives for recovery from service disruptions. Two key metrics are commonly used to
plan for predictable restoration of services. These are as follows:

.. _RPO:

Recovery point objective (RPO)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

RPO defines the maximum acceptable amount of data that can be lost in case of failure, typically expressed as a time interval.

* With synchronous replication, updates are mirrored instantly, resulting in zero data loss.
* With asynchronous replication, updates occur on a schedule, meaning any data written since the last successful replication may be lost during failover.

Recovery time objective (RTO)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

RTO is the maximum acceptable amount of time that a system can be down after a disruption before
significant damage or intolerable consequences occur.

