Remote Replication
==================

Cloud storage services (like Ceph) are responsible for persisting information irrespective of faults and
failures in parts of the cluster. These faults could be temporary network failures, disk faults, power failures
or even failure of multiple nodes. Remote replication is a mechanism used to replicate data to a remote storage
cluster, typically located at a different geographical site to prevent complete outage in the event of a large
enough fault like natural disaster.

This guide covers the essential replication concepts for new Ceph users.

Modes of data movement
-----------------------

Replication between clusters can be implemented in two common modes, each with specific operational characteristics:

Push Replication:
~~~~~~~~~~~~~~~~~

In this mode, the source cluster actively sends data updates (aka deltas or diffs) to the target cluster. The
replication process is initiated and managed by the source cluster, ensuring changes are centrally propagated.
This is easier to administer for simpler environments but can place higher resource demands on the primary
cluster.

Pull Replication:
~~~~~~~~~~~~~~~~~

In this mode, the target cluster initiates and manages copying (or pulling) updates from the source. This model
provide target sites the control over bandwidth and timing. It scales efficiently in large environments, although
is slightly more complex.

Replication Architectures
-------------------------

Based on cost, complexity, and recovery objectives a choice can be made between these two architectures.

Active-Active Replication
~~~~~~~~~~~~~~~~~~~~~~~~~

Both clusters handle read and write operations, synchronising changes in real time between them. This ensures high
availability as if one site goes down, users can continue operation on the other with no data loss. It is best for
use cases requiring continuous operation and zero recovery point objective (RPO).

Active-Passive Replication
~~~~~~~~~~~~~~~~~~~~~~~~~~

The active cluster is used to serve clients, while the passive cluster acts as a backup. Data is replicated asynchronously
to the passive cluster, which becomes operational only during failover. This approach is suitable for DR in scenarios
where the secondary site isn’t needed for real-time access, accepting minor data delays after failover.

Disaster Recovery Objectives
-----------------------------

Recovery Point Objective (RPO)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

In a synchronous system updates are replicated to the target cluster in real time, thus even in case of a disaster
the remote cluster is immediately available to replace the cluster experience outage. However, in case of asyncronous
replication these updates are replicated in a schedule manner. Thus in case of disaster, the updates received since the
last successful replication are lost during failover. This capacity (maximum) to loose updates to data defined in units
of time (say 12H worth of data updates) is defined as RPO or recovery point objective.

Recovery Time Objective (RTO)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Recovery Time Objective is the maximum acceptable amount of time that a system can be down after a disruption before
significant damage or intolerable consequences occur.

