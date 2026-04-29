.. meta::
   :description: MicroCeph is a lightweight way of deploying and managing a Ceph cluster. Ceph is a highly scalable, open-source distributed storage system designed to provide excellent performance, reliability, and flexibility for object, block, and file-level storage. This is the homepage of MicroCeph's documentation.

.. _microceph-homepage:

MicroCeph
=========

MicroCeph is the easiest way to get up and running with Ceph.

MicroCeph is a lightweight way of deploying and managing a Ceph cluster. Ceph
is a highly scalable, open-source distributed storage system designed to
provide excellent performance, reliability, and flexibility for object, block,
and file-level storage.

Ceph cluster management is streamlined by simplifying key distribution, service
placement, and disk administration for quick, effortless deployment and
operations. This applies to clusters that span private clouds, edge clouds, as
well as home labs and single workstations.

MicroCeph is focused on providing a modern deployment and management experience
to Ceph administrators and storage software developers.

In this documentation
---------------------

MicroCeph can be deployed and managed as a standalone snap or as a charm as
part of a Juju model.

.. grid:: 1 1 2 2

   .. grid-item-card:: :ref:`MicroCeph snap › <snap-get-started>`

      The ``microceph`` snap is a self-contained, secure and dependency-free
      Linux app package used to deploy and manage a Ceph cluster. If you are
      new to MicroCeph, start here.

   .. grid-item-card:: :ref:`MicroCeph charm › <charm-get-started>`

      The ``microceph`` charm takes care of installing, configuring and
      managing MicroCeph on cloud instances managed by Juju.

Project and community
---------------------

MicroCeph is a member of the Ubuntu family. It's an open-source project that
warmly welcomes community projects, contributions, suggestions, fixes and
constructive feedback.

Get involved
~~~~~~~~~~~~

* Contribute to the project on the `MicroCeph`_ or `charm-microceph`_ GitHub repositories _ (documentation contributions go under
  the :file:`docs` directory)
* GitHub is also used as our bug tracker
* To speak with us, you can find us on Matrix in `Ceph General`_ or `Ceph Devel`_
* :ref:`Contribute to our documentation <contributing>`

Governance and policies
~~~~~~~~~~~~~~~~~~~~~~~

* We follow the Ubuntu community `Code of conduct`_

Commercial support
~~~~~~~~~~~~~~~~~~

* Optionally enable `Ubuntu Pro`_ on your Ceph nodes. This is a service that
  provides the `Livepatch Service`_ and the `Expanded Security Maintenance`_
  (ESM) program.

.. toctree::
   :hidden:
   :titlesonly:
   :caption: Deploy from Snap package

   Tutorial <snap/tutorial/get-started>
   How-to guides <snap/how-to/index>
   Reference <snap/reference/index>
   Explanation <snap/explanation/index>

.. toctree::
   :hidden:
   :titlesonly:
   :caption: Deploy with Juju

   Tutorial <charm/tutorial/get-started>
   How-to guides <charm/how-to/index>

.. toctree::
   :hidden:
   :titlesonly:
   :caption: Contributing

   Contribute to our documentation <contributing/index>

.. LINKS
.. _Code of conduct: https://ubuntu.com/community/ethos/code-of-conduct
.. _MicroCeph: https://github.com/canonical/microceph
.. _charm-microceph: https://github.com/canonical/charm-microceph
.. _Ceph General: https://matrix.to/#/#ubuntu-ceph:matrix.org
.. _Ceph Devel: https://matrix.to/#/#ceph-devel:ubuntu.com
.. _Ubuntu Pro: https://ubuntu.com/pro
.. _Livepatch Service: https://ubuntu.com/security/livepatch
.. _Expanded Security Maintenance: https://ubuntu.com/security/esm