.. meta::
   :description: How-to guides for deploying and operating MicroCeph with Juju

.. _charm-how-to:

How-to guides
=============

Our how-to guides walk you through deploying and operating MicroCeph with Juju; from first deployment
to day-to-day management.

Installation
------------

Deploy MicroCeph with Juju on your machine cloud - MAAS, LXD, AWS, or others.

.. toctree::
   :maxdepth: 1

   Install MicroCeph from a charm <install-charmed-microceph>

Add storage
-----------

Your cluster needs storage to be useful. Add physical disks for production, or virtual ones for testing.

.. toctree::
   :maxdepth: 1

   Add physical disk OSDs <adding-osds>
   Add virtual OSDs for test clusters <adding-file-osds>

Enable services
---------------

Once your cluster has storage, turn on the services your workloads need - like S3-compatible object
storage via RGW.

.. toctree::
   :maxdepth: 1

   Enable RGW <enable-rgw>

Cluster maintenance
-------------------

Need to reboot a node or replace hardware? Maintenance mode lets you take a node offline safely,
without triggering unnecessary data movement or risking availability.

.. toctree::
   :maxdepth: 1

   Perform cluster maintenance <performing-cluster-maintenance>
