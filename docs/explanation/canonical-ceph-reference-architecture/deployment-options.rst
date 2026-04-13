.. meta::
   :description: An overview of the deployment options available for Canonical Ceph, including Charmed Ceph, MicroCeph, charm-microceph, and Ceph Rocks, and guidance on choosing the right option for your use case.

.. _deployment-options:

Deployment options for Canonical Ceph
======================================

Canonical Ceph can be deployed using snaps or Juju charms, i.e. via MicroCeph
(or charm-microceph), Charmed Ceph and Ceph Rocks. The choice of deployment
method is informed by your storage cluster use case.

Charmed Ceph
------------

`Charmed Ceph <https://ubuntu.com/ceph/docs>`_ is the full-scale deployment
option for private cloud infrastructure. It uses Juju and machine charms to
deploy and manage Ceph clusters, providing comprehensive lifecycle management,
configuration, and integration with other Juju-managed infrastructure. This is
the appropriate choice for production private clouds where you want unified
orchestration of storage alongside compute and networking.

The diagram below depicts a typical Charmed Ceph deployment with
``ceph-radosgw`` and ``ceph-fs``. However, the Charmed Ceph ecosystem is
flexible and can be tailored to a specific use case.

.. image:: ../assets/charmed-ceph-deployment.png
   :alt: Typical Charmed Ceph deployment

Learn more about the `Charmed Ceph architecture
<https://ubuntu.com/ceph/docs/ceph-architecture>`_ in the product
documentation.

Charmed Ceph is often deployed with other products; the diagram below shows
how Charmed Ceph is integrated with OpenStack
`OpenStack <https://canonical-openstack.readthedocs-hosted.com/en/latest/>`_ charms.

.. image:: ../assets/charmed-openstack-integrations.png
   :alt: Charmed Ceph with OpenStack charms

MicroCeph
---------

`MicroCeph <https://canonical-microceph.readthedocs-hosted.com/latest/>`_ is a
lightweight deployment option, packaged as a snap. It is designed for edge
computing where minimal operational overhead matters, and for small-scale
deployments like developer workstations, Continuous Integration (CI)
environments, or training setups. The snap handles daemon lifecycle and upgrades
with minimal configuration.

See an example of a MicroCeph cluster below.. The `MicroCeph architecture
section
<https://canonical-microceph.readthedocs-hosted.com/latest/explanation/microceph-architecture/>`_
provides more details about MicroCeph components.

.. image:: ../assets/ex-microceph-cluster.png
   :alt: MicroCeph architecture overview

MicroCeph is often deployed with other products, e.g.
`MicroCloud <https://documentation.ubuntu.com/microcloud/default/microcloud/>`_,
as in this diagram:

.. image:: ../assets/microceph-microcloud.png
   :alt: MicroCeph with MicroCloud

charm-microceph
---------------

`charm-microceph <https://charmhub.io/microceph>`_ bridges MicroCeph and Juju.
It deploys MicroCeph under the hood but exposes it as a Juju-managed
application, giving you the lightweight footprint of MicroCeph while retaining
compatibility with Juju's orchestration, relations, and model-driven operations.
This fits edge deployments that are part of a larger Juju-managed estate.

Charm-microceph is often integrated with other product, e.g. the Canonical Observability Stack (COS),
as in this diagram:

.. image:: ../assets/charm-microceph-cos.png
   :alt: charm-microceph cluster

Ceph Rocks
----------

`Ceph Rocks <https://github.com/canonical/ceph-containers>`_ are OCI-compliant
container images for users who want to deploy Ceph using their own tooling
(cephadm, Rook, or other Kubernetes operators). This provides Canonical's
security maintenance and packaging quality while leaving deployment
orchestration to whatever container platform you're already using.

Here's a diagram of a cephadm cluster, using Ceph Rocks:

.. image:: ../assets/cephadm-cluster-using-ceph-rocks.png
   :alt: cephadm cluster using Ceph Rocks
