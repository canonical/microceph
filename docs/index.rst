=========
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

---------

In this documentation
---------------------

..  grid:: 1 2 1 2

   ..  grid-item:: :doc:`Tutorial <tutorial/get-started>`

      **A hands-on introduction** to MicroCeph for new users

   ..  grid-item:: :doc:`How-to guides <how-to/index>`

      **Step-by-step guides** covering key operations and common tasks

   .. grid-item:: :doc:`Reference <reference/index>`

      **Technical information** - specifications, APIs, architecture

   .. grid-item:: :doc:`Explanation <explanation/index>`

      **Discussion and clarification** of key topics

---------

Project and community
---------------------

MicroCeph is a member of the Ubuntu family. It's an open-source project that
warmly welcomes community projects, contributions, suggestions, fixes and
constructive feedback.

* We follow the Ubuntu community `Code of conduct`_
* Contribute to the project on `GitHub`_ (documentation contributions go under
  the :file:`docs` directory)
* GitHub is also used as our bug tracker
* To speak with us, you can find us on Matrix in `Ubuntu Ceph`
* Optionally enable `Ubuntu Pro`_ on your Ceph nodes. This is a service that
  provides the `Livepatch Service`_ and the `Expanded Security Maintenance`_
  (ESM) program.

.. toctree::
   :hidden:
   :maxdepth: 1

   tutorial/get-started

.. toctree::
   :hidden:
   :maxdepth: 2

   how-to/index
   reference/index
   explanation/index
   contributing/index

.. LINKS
.. _Code of conduct: https://ubuntu.com/community/ethos/code-of-conduct
.. _GitHub: https://github.com/canonical/microceph
.. _Ceph General: https://matrix.to/#/#ubuntu-ceph:matrix.org
.. _Ubuntu Pro: https://ubuntu.com/pro
.. _Livepatch Service: https://ubuntu.com/security/livepatch
.. _Expanded Security Maintenance: https://ubuntu.com/security/esm