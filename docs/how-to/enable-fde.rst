==========================================
Enabling full disk encryption in MicroCeph
==========================================

Overview
--------

Full disk encryption (FDE) in MicroCeph allows operating encrypted
OSDs in a MicroCeph cluster. See the :doc:`explanation
<../explanation/about-fde.rst>` section about details on FDE
protection and limitations.

Prerequisites
-------------

To use FDE, the following prerequisites must be met:

- The installed snapd daemon version must be >= 2.59.1
- The ``dm-crypt`` kernel module must be available. Note that some cloud-optimised kernels do not ship dm-crypt by default. Check by running ``sudo modinfo dm-crypt``
- The snap dm-crypt plug has to be connected, and ``microceph.daemon`` subsequently restarted:

  .. code-block:: none

     sudo snap connect microceph:dm-crypt
     sudo snap restart microceph.daemon


Enabling FDE
------------

FDE for OSDs is activated by passing the optional ``--encrypt`` flag when adding disks:

.. code-block:: shell

    sudo microceph disk add /dev/sdx --wipe --encrypt

Note there is no facility to encrypt an OSD that is already part of the cluster. To enable encryption you will have to take the OSD disk out of the cluster, ensure data is replicated and the cluster converged and is healthy, and then re-introduce the OSD with encryption.

