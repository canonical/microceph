.. meta::
   :description: Network recommendations for Canonical Ceph deployments, covering NIC speed, Ceph traffic categories, network topology options, and spine-leaf architecture guidance.

.. _network-recommendations:

Network recommendations
=======================

Network usually tends to be a bottleneck in the traffic between cloud
instances and nodes. Therefore, network topology should be designed carefully
based on the individual requirements and the characteristics of the workloads.
We will recommend network speed based on the different categories of Ceph
traffic and the choice of network topology.

Network type
------------

Canonical recommends using at least 10 Gbps Network Interface Cards (NICs)
for general-purpose traffic. Since 100 Gbps NICs currently provide the best
value for money, measured as the cost per 1 Gbps, they are the preferred
option. However, not all types of workloads and environments require such a
fast networking configuration.

More precisely, Ceph traffic is divided into three categories:

* **Management traffic** between management tools such as Metal as a Service
  (MAAS), Juju Controller, and Canonical Observability Stack (COS) on the one
  hand, and the Ceph cluster on the other. This traffic can live on a 1 Gbps
  bond at a minimum, but 10 Gbps is recommended.
* The **Ceph Access network**, defined for client access to the Ceph cluster.
  All Ceph nodes should be on a shared Layer 2 (L2) domain, and ideally the
  clients should be on the L2 domain of the access network.
* The **Ceph Replication network**, which should be on a separate bond, and
  ideally at least 25 Gbps to allow for sufficient bandwidth in a recovery
  situation. When one node fails, replication traffic to recover
  replicas/parity consumes significant network resources.

To ensure resilience against failures on the network level and eliminate all
single points of failure, Canonical recommends using dual-port Network
Interface Cards (NICs) for all traffic. Those NICs plug into the network
fabric which should also be designed in a highly available fashion.

Network topology
----------------

The preferred network fabric topology depends on the size of the deployment.
Small-scale deployments that are not immediately expected to grow should use a
simple topology with two network switches for high availability. In turn,
small-scale deployments that are expected to grow rapidly and large-scale
deployments should use a Clos network topology with Layer 3 (L3) leaf and
spine switches.

The following types of switches are required for the spine-leaf architecture:

* **Leaf switches** — connect servers inside of a single availability zone
  (AZ) and act as a gateway to other AZs for general-purpose traffic. Each AZ,
  consisting of two racks, contains a pair of leaf switches and every server in
  the AZ is connected to both switches using link aggregation control protocol
  (LACP) technology. Leaf switches terminate L2 inside of the AZ and can only
  route connections to other AZs via spine switches.
* **Spine switches** — connect leaf switches inside different AZs using L3
  routing protocols. They are connected to every leaf switch, but not to each
  other. At least two spine switches are required for the recommended cloud
  deployment.
* **Management switches** — connect all servers to enable operations,
  administration and management (OAM) traffic and automated server
  provisioning. Segregating these types of traffic to management switches
  guarantees reachability even in the face of heavy tenant and storage load and
  flooding.

In order to prevent network oversubscription, the number of ports in leaf
switches should match the number of ports in spine switches.
