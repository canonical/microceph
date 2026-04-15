.. meta::
   :description: How to install MicroCeph using Juju.

Install MicroCeph with Juju
===========================

This guide shows how to perform a general install of MicroCeph.

What you will need
------------------

- A snapd-compatible host to run the `Juju client <https://documentation.ubuntu.com/juju/3.6/howto/manage-juju/?utm_source%3Atakeover=#install-juju>`_
- A `MAAS cluster <https://maas.io/install>`_ (with a user account at your
  disposal), or a `manual cloud cluster <https://discourse.charmhub.io/t/juju-101-configure-3-12/4484>`_
- Disks on each node to add as OSD to the Ceph cluster

Deploy MicroCeph
----------------

The actual deployment of MicroCeph is straightforward:

.. code-block:: text

   juju deploy -n 3 microceph --channel latest/edge --to 0,1,2

The output to the ``juju status`` command should look similar to this:

.. code-block:: text

   Model      Controller       Cloud/Region     Version    SLA          Timestamp
   microceph  sunbeam-default  sunbeam/default  3.2-beta3  unsupported  03:40:02Z
   App        Version  Status  Scale  Charm      Channel      Rev  Exposed  Message
   microceph           active      3  microceph  latest/edge    3  no
   Unit          Workload  Agent  Machine  Public address  Ports  Message
   microceph/0*  active    idle   0        10.5.0.106
   microceph/1   active    idle   1        10.5.1.191
   microceph/2   active    idle   2        10.5.1.81
   Machine  State    Address     Inst id            Base          AZ  Message
   0        started  10.5.0.106  manual:10.5.0.106  ubuntu@22.04      Manually provisioned machine
   1        started  10.5.1.191  manual:10.5.1.191  ubuntu@22.04      Manually provisioned machine
   2        started  10.5.1.81   manual:10.5.1.81   ubuntu@22.04      Manually provisioned machine

Verification
------------

Verify the state of the Ceph cluster by running the ``ceph status`` command on
one of the nodes:

.. code-block:: text

   juju ssh microceph/leader sudo microceph.ceph status

Sample output:

.. code-block:: text

     cluster:
       id:     edd914f5-fdf8-4b56-bdd7-95d6c5e10d81
       health: HEALTH_WARN
               OSD count 0 < osd_pool_default_size 3

     services:
       mon: 3 daemons, quorum microceph2,microceph3,microceph4 (age 57s)
       mgr: microceph2(active, since 74s), standbys: microceph3, microceph4
       osd: 0 osds: 0 up, 0 in

     data:
       pools:   0 pools, 0 pgs
       objects: 0 objects, 0 B
       usage:   0 B used, 0 B / 0 B avail
       pgs:

The next step is to add disks to the microceph nodes.
