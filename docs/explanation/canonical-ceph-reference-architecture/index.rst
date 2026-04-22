.. _canonical-ceph-reference-architecture:

Canonical Ceph reference architecture
======================================

Canonical Ceph can be deployed as a stand-alone cluster, or integrated with
OpenStack, VMware, or Kubernetes. Depending on your cluster use case, Canonical
Ceph provides various `deployment options <https://ubuntu.com/ceph/install>`_:
single-node and multi-node deployments, through MicroCeph; large-scale
deployments through Charmed Ceph, and containerized deployments with Ceph
Rocks. A single cluster can provide block, file, and object storage, and can be
customized to meet price-performance requirements.

Our reference architecture provides detailed guidelines for optimizing your Ceph
architecture. It starts by detailing the different deployment options and their
architectures, then highlights the factors to consider when designing a
Canonical Ceph cluster, and then outlines hardware specifications, including
compute, storage, memory and networking requirements. The :ref:`reference section of the reference architecture <ref-arch-reference-section>`
includes Canonical's recommendations regarding hardware and software
configurations, to further inform your architectural choices.

.. warning::

   This reference architecture is a starting point, not a prescription. Before making any
   purchasing decisions, we recommend reviewing your plans with Canonical to ensure they
   align with your specific needs. Hardware requirements can vary significantly depending
   on your workload and goals.

   Canonical is not liable or responsible for any equipment purchases made as a result
   of this reference architecture. Following these recommendations does not guarantee
   that the proposed hardware will meet the requirements of your project or use case.

   To discuss your specific requirements, `contact Canonical <https://ubuntu.com/ceph#get-in-touch>`_.

.. toctree::
    :maxdepth: 2
    
    deployment-options
    architectural-considerations
    processor-requirements
    data-persistence-mechanisms
    server-recommendations
    disk-recommendations
    network-recommendations
    minimum-node-count-and-rack-layout
    data-encryption
    certified-hardware
