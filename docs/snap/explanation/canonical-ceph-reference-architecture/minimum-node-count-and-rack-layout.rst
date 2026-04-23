.. meta::
   :description: Minimum node count and rack layout requirements for Canonical Ceph deployments, including infrastructure nodes, Ceph nodes, availability zones, and network design guidance.

.. _minimum-node-count-and-rack-layout:

Minimum node count and rack layout
===================================

To qualify for Canonical Ceph design and delivery services, Ubuntu Pro and
Managed Ceph, the cloud has to consist of the following components at minimum:

* **Three infrastructure nodes** hosting the automation infrastructure,
  including Metal as a Service (MAAS), Juju and the observability stack, for
  the Juju-based options, i.e., Charmed Ceph and charm-microceph.
* **Six Ceph nodes** for a hyperconverged architecture,
  or **nine nodes** for a disaggregated architecture.
* Required rack, power and network infrastructure.

In order to ensure sufficient power supply, non-oversubscribed network and
resilience against failures, we highly recommend designing the hardware layer
in the following way:

* Using at least three availability zones (AZs) so that one unit of each
  automation service and cloud control plane service would be running in a
  separate AZ.
* Using at least three racks so that services from different AZs would be
  running physically separated in different racks.
* Distributing hyperconverged nodes equally across racks to ensure sufficient
  power supply and cooling inside a single rack.
* Using at least two leaf switches per AZ to ensure high availability on the
  network layer inside the AZ.
* Using a sufficient number of spine switches (usually one per two racks) to
  ensure network non-oversubscription.
* Using at least two managed switches for management and provisioning.

Canonical provides capacity estimates to ensure that the cloud being built has
enough resources to run customer workloads. The actual number of hyperconverged
node racks and switches may vary depending on the capacity requirements.
