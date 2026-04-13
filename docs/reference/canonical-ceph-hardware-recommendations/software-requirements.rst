.. meta::
   :description: Software requirements for Canonical Ceph deployments, including operating system versions and software dependencies.

.. _hw-rec-software-requirements:

Software requirements
=====================

For the best experience, Canonical always recommends using the latest long-term support
(LTS) version of Ubuntu, and an LTS version of your Canonical Ceph deployment option. We
also recommend updating the software on a regular basis to benefit from new capabilities
brought by the latest kernels, features, and bug fixes. This prevents issues where, for
example, using legacy versions of the operating system (OS) or Ceph may result in certain
hardware components or features not being supported.

This table provides official release information for each component of a production
Canonical Ceph cluster deployment:

.. list-table::
   :widths: 30 70
   :header-rows: 1

   * - Component
     - Release cycle information
   * - Ubuntu (Host OS)
     - `Ubuntu releases <https://releases.ubuntu.com/>`__
   * - Linux Kernel
     - `Ubuntu Kernel release cycle <https://ubuntu.com/about/release-cycle#ubuntu-kernel-release-cycle>`__
   * - Canonical Ceph
     - `Charmed Ceph release notes <https://ubuntu.com/ceph/docs/release-notes>`__,
       `MicroCeph release notes <https://canonical-microceph.readthedocs-hosted.com/latest/reference/release-notes/>`__,
       `charm-microceph <https://charmhub.io/microceph>`__
   * - MAAS
     - `MAAS release notes and upgrade instructions <https://canonical.com/maas/docs/release-notes-and-upgrade-instructions>`__
   * - Juju
     - `Juju release notes <https://documentation.ubuntu.com/juju/3.6/releasenotes/>`__
