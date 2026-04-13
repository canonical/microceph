.. meta::
   :description: A comparison of Ceph's replication and erasure coding data persistence strategies, including performance considerations and Canonical's recommendations for choosing between them.

.. _replication-vs-erasure-coding:

Replication vs. erasure coding
===============================

The decision between erasure coding and replicated pools is defined by the
trade-off between usable storage and performance needed for the environment.
For replicated pools, each data object will typically be replicated twice,
which gives 33% of usable storage compared to the raw total of storage. Erasure
coding can increase storage efficiency, at the expense of increased CPU usage,
increased OSD memory usage, higher latency and increased network load.

Learn how Ceph uses these two data persistence mechanisms in the
:ref:`data-persistence-mechanisms` page.

Performance considerations and recommendation
---------------------------------------------

Canonical's testing has shown that the performance of erasure-coded pools is
much lower than that of replicated pools, for the same amount of CPUs and RAM.
Erasure coding is generally only justified for Write Once Read Many (WORM)
use-cases, or cold storage. If erasure coding is being considered to lower the
cost of storage, it is necessary to compare the cost of cores and cost of
higher clock speed, compared to the cost of storage disks. If it is less
expensive to buy more expensive CPUs than it is to purchase additional storage
devices, then using erasure coding might be justified for the use case.

.. note::

   As general guidance, replicated pools should be used in most scenarios, and
   especially for performance- and latency-sensitive workloads.
