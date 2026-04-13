.. meta::
   :description: Key architectural considerations for building a Canonical Ceph storage cluster, covering infrastructure node requirements, cluster service placement strategies, and the purpose of the storage environment.

.. _architectural-considerations:

Architectural considerations
============================

There are several factors to consider when selecting hardware for building a
Ceph storage cluster. Canonical Ceph cluster design choices are influenced by
infrastructure node requirements (which are influenced by method of
deployment), cluster service placement approach, the purpose of the cluster,
and various hardware and software specifications, e.g. Random Access Memory
(RAM), operating system (OS), network, disks, server requirements, etc.

Infrastructure node requirements
---------------------------------

Management infrastructure requirements vary by `deployment option
<deployment-options>`_. The `Juju <https://canonical.com/juju/docs>`_-based
options, i.e., Charmed Ceph and charm-microceph, require a Juju controller
environment, typically deployed alongside `Metal as a Service (MAAS)
<https://canonical.com/maas>`_ for bare-metal provisioning. Canonical
recommends dedicating three infrastructure nodes to host Juju and MAAS in a
High Availability (HA) configuration. Ceph Rocks and stand-alone MicroCeph
options have no such dependency; you bring your own orchestration or manage
nodes directly.

Regardless of deployment option, production environments benefit from
additional management infrastructure for observability, and fleet
management/patching. For observability, we recommend the `Canonical
Observability Stack (COS)
<https://documentation.ubuntu.com/observability/track-2/>`_, and for
management/patching, we recommend using `Landscape
<https://ubuntu.com/landscape>`_. These typically require another three-node
cluster, which can be colocated with the Juju/MAAS infrastructure nodes where
applicable, reducing the total infrastructure footprint.

Cluster service placement
--------------------------

Ceph is composed of several components, both for the control plane and the
data plane. Control plane services include Ceph Monitors (MONs), Ceph Managers
(MGRs), Ceph RADOS Gateways (RGWs), and Ceph metadata servers (MDSs), whereas
the data-plane services include Ceph object storage daemons (OSDs).

.. image:: ../assets/ceph-internals.png
   :alt: Ceph internal components

Canonical recommends the hyperconverged and disaggregated approaches to
service placement in the Ceph cluster, depending on your use case. In general,
we recommend minimising the number of hardware configurations to simplify
capacity planning and replacement strategy.

Hyperconverged architecture
~~~~~~~~~~~~~~~~~~~~~~~~~~~

The hyperconverged approach to cluster placement is characterised by a setup
where every node in the cloud is hosting the control plane services.

The hyperconverged architecture enables standardisation on a single hardware
configuration, ensures maximum resource utilisation and minimises the overall
number of nodes. Therefore, it has proven to provide the lowest private
infrastructure total cost of ownership (TCO).

The hyperconverged architecture is suitable for general-purpose storage, but
it may not be a suitable option for specific workloads and certain storage use
cases.

The diagram below is a representation of a hyperconverged architecture setup
in the context of an OpenStack deployment.

.. image:: ../assets/hyperconverged-ceph.png
   :alt: Hyperconverged architecture in an OpenStack deployment

Disaggregated architecture
~~~~~~~~~~~~~~~~~~~~~~~~~~

The disaggregated approach, on the other hand, features dedicated nodes for
each type of service, e.g. OSDs, RGW and MDS (control plane services).

This architecture is suitable in specific scenarios, for example, where
applications require high performance object or file system storage, because
dedicated RGWs or MDSs can provide lower latency and dedicated resources to
those services.

.. image:: ../assets/disaggregated-architecture.png
   :alt: Disaggregated Ceph architecture

Purpose of the storage cluster
--------------------------------

The next aspect to consider when selecting hardware for a Ceph cluster is the
purpose of the storage environment, which may be one of the following:

* Performance
* Capacity
* General-purpose storage

A high performance cluster will look differently from a general-purpose or
large capacity cluster.

The type of storage needed will also impact the overall design. For example, a
high-performance object storage cluster will perform better if it is not mixed
with RBD (block storage) and CephFS (file system storage) storage pools. Empty
storage pools can unbalance the distribution of the storage on the cluster and
lead to performance issues. For this reason, we will consider three scenarios
throughout the reference architecture:

1. Balanced use case; with block, object and file system storage available
2. Object storage only (RGW)
3. File system storage only (CephFS)
