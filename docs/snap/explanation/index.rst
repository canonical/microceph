.. meta::
   :description: Explanations of MicroCeph concepts and topics like, the MicroCeph charm, MicroCeph architecture, security, network configuration, remote replication, and cluster scaling.

.. _snap-explanation:

Explanation
===========

Our explanation section will help you gain a conceptual understanding of MicroCeph;
how it works and why it works the way it does.

How MicroCeph works
-------------------

Before you change how your cluster runs, it helps to understand the MicroCeph architecture, i.e. the components that make up MicroCeph and how they fit together, 
and what MicroCeph is doing for you behind the scenes, e.g. how it configures the cluster, keeps your data safe, and scales as you grow.

.. toctree::
   :maxdepth: 1

   microceph-architecture
   cluster-configurations
   cluster-maintenance
   cluster-scaling
   taking-snapshots
   replication

The Snap content interface
--------------------------

Access MicroCeph's configuration and credentials.

.. toctree::
   :maxdepth: 1

   snap-content-interface

The MicroCeph charm
--------------------

The MicroCeph charm helps you deploy and manage your MicroCeph deployment with `Juju`_.

.. LINKS
.. _Juju: https://juju.is/

.. toctree::
   :maxdepth: 1

   microceph-charm

Security in MicroCeph
---------------------

Learn about security approaches in MicroCeph, e.g. enabling support for MicroCeph's automatic full disk encryption on OSDs and
cryptographic technology used in MicroCeph.

.. toctree::
   :maxdepth: 2

   MicroCeph architecture <microceph-architecture>
   Security in MicroCeph <security/security-overview>

Canonical Ceph reference architecture
--------------------------------------

Find detailed guidelines for optimising your Ceph architecture. Learn about the
different deployment options; what to consider when designing a Canonical Ceph
cluster; hardware and software specifications and recommendations, and
information on Ubuntu certified hardware.

.. toctree::
   :maxdepth: 2

   Canonical Ceph reference architecture <canonical-ceph-reference-architecture/index>

If you have a specific goal, and are already familiar with MicroCeph, our
:ref:`how-to guides <snap-how-to>` have more in-depth detail and instructions.

Take a look at our :ref:`reference section <snap-reference>` to find our
release notes and recommended hardware specifications, or when you need
to know which MicroCeph commands to use.

