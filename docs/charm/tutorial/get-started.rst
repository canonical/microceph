.. meta::
   :description: Deploy a three-node charm-microceph cluster in an LXD environment using Juju.

Deploy MicroCeph via charm-microceph
=====================================

In this tutorial we will deploy a three-node charm-microceph cluster in an LXD
environment using Juju. We will set up LXD on a single physical machine and use
simulated storage to keep hardware requirements at a minimum. This works great
for learning and testing purposes; in a production environment you would
typically deploy to several physical machines with dedicated storage media.

First, we will install and configure LXD which will provide virtual machines for
our cluster. Then we will install and bootstrap Juju, using LXD as a provider.
Subsequently we will deploy the MicroCeph cluster, and in the final step
configure simulated storage for MicroCeph.

Prerequisites
-------------

To successfully follow this tutorial, you will need:

- a Linux machine with at least 16 GB of memory and 50 GB of free disk space
- with snapd installed
- and virtualisation enabled

.. note::

   This tutorial might run into issues on a container (such as Docker) as
   containers typically lack virtualisation capabilities.

Install and configure LXD
--------------------------

Install the LXD snap and auto-configure it to give the host machine the ability
to spawn VMs, including networking and storage:

.. code-block:: text

   $ sudo snap install lxd
   $ sudo lxd init --auto

.. note::

   Depending on your system, LXD might already be installed.

Install and bootstrap Juju
---------------------------

Install and then configure Juju to make use of the LXD provider set up in the
previous step:

.. code-block:: text

   $ sudo snap install juju
   juju (3/stable) 3.6.8 from Canonical✓ installed

   $ juju bootstrap localhost lxd-controller
   Since Juju 3 is being run for the first time, it has downloaded the latest public cloud information.
   Creating Juju controller "lxd-controller" on localhost/localhost
   ...
   Now you can run
           juju add-model <model-name>
   to create a new model to deploy workloads.

You have successfully created a Juju controller named ``lxd-controller``. Models
are the logical grouping of connected applications in Juju. Create a model
called ``mymodel``:

.. code-block:: text

   $ juju add-model mymodel
   Added 'mymodel' model on localhost/localhost with credential 'localhost' for user 'admin'

Configure the model to spawn VMs with 4 GB of memory and a 16 GB disk:

.. code-block:: text

   $ juju set-model-constraints virt-type=virtual-machine mem=4G root-disk=16G

For further details, consult the
`Juju documentation on LXD <https://documentation.ubuntu.com/juju/3.6/reference/cloud/list-of-supported-clouds/the-lxd-cloud-and-juju/#the-lxd-cloud-and-juju>`_.

Deploy MicroCeph
----------------

With the Juju environment configured, deploy three MicroCeph units:

.. code-block:: text

   $ juju deploy microceph --num-units 3
   Deployed "microceph" from charm-hub charm "microceph", revision 155 in channel squid/stable on ubuntu@24.04/stable

Juju deploys the MicroCeph units in the background. This process might take a
few minutes depending on network speed and available resources. Check progress
with ``juju status``. Once the deployment is complete, all three units will
report as active:

.. code-block:: text

   $ juju status
   Model    Controller      Cloud/Region         Version  SLA          Timestamp
   mymodel  lxd-controller  localhost/localhost  3.6.8    unsupported  11:15:26Z

   App        Version  Status  Scale  Charm      Channel       Rev  Exposed  Message
   microceph           active      3  microceph  squid/stable  155  no

   Unit          Workload  Agent  Machine  Public address  Ports  Message
   microceph/0   active    idle   0        10.106.25.67
   microceph/1   active    idle   1        10.106.25.66
   microceph/2*  active    idle   2        10.106.25.144

   Machine  State    Address        Inst id        Base          AZ  Message
   0        started  10.106.25.67   juju-9fe08a-0  ubuntu@24.04      Running
   1        started  10.106.25.66   juju-9fe08a-1  ubuntu@24.04      Running
   2        started  10.106.25.144  juju-9fe08a-2  ubuntu@24.04      Running

Deploying the MicroCeph units also bootstraps a Ceph cluster. Check the cluster
status by SSHing into a unit:

.. code-block:: text

   $ juju ssh microceph/0 "sudo ceph -s"
     cluster:
       id:     e131a957-bb56-489c-bb10-1782cd29e5f2
       health: HEALTH_WARN
               OSD count 0 < osd_pool_default_size 3

     services:
       mon: 3 daemons, quorum juju-9fe08a-2,juju-9fe08a-0,juju-9fe08a-1 (age 77s)
       mgr: juju-9fe08a-2(active, since 118s), standbys: juju-9fe08a-0, juju-9fe08a-1
       osd: 0 osds: 0 up, 0 in

The cluster is running but reports a health warning because Ceph expects three
OSDs (disks) by default and none have been configured yet.

Add disks
---------

For this tutorial we will use small simulated loop disks for ease of
configuration.

.. note::

   Loop disks are only suitable for demo setups. In a production environment,
   use physical disks instead.

Add a 2 GB loop-based OSD to each unit:

.. code-block:: text

   $ juju add-storage microceph/0 osd-standalone="loop,2G,1"
   added storage osd-standalone/0 to microceph/0

   $ juju add-storage microceph/1 osd-standalone="loop,2G,1"
   added storage osd-standalone/1 to microceph/1

   $ juju add-storage microceph/2 osd-standalone="loop,2G,1"
   added storage osd-standalone/2 to microceph/2

It will take a few minutes to configure the storage. Once all units display a
status of ``active`` / ``idle``, the Ceph cluster should be healthy:

.. code-block:: text

   $ juju ssh microceph/0 "sudo ceph -s"
     cluster:
       id:     e131a957-bb56-489c-bb10-1782cd29e5f2
       health: HEALTH_OK

     services:
       mon: 3 daemons, quorum juju-9fe08a-2,juju-9fe08a-0,juju-9fe08a-1 (age 7m)
       mgr: juju-9fe08a-2(active, since 8m), standbys: juju-9fe08a-0, juju-9fe08a-1
       osd: 3 osds: 3 up (since 34s), 3 in (since 42s)

Next steps
----------

You have successfully deployed a Juju-managed MicroCeph cluster, ready to use
as a test and learning environment.

To see how to configure and integrate MicroCeph with other Juju applications,
see the `MicroCeph page on Charmhub <https://charmhub.io/microceph>`_.
