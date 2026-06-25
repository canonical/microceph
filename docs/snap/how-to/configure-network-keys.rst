.. meta::
   :description: How to configure the MicroCeph cluster network for OSD replication and recovery traffic.

.. _configure-network-keys:

Configuring cluster network
============================

If you configure a cluster network, OSDs will route heartbeat, object replication and recovery traffic over the cluster network. This may improve performance compared to using a single network. 

The MicroCeph cluster configuration CLI supports setting, getting, resetting and listing supported config keys mentioned below.

.. list-table:: Supported config keys
   :widths: 30 70
   :header-rows: 1

   * - Key
     - Description
   * - cluster_network
     - Set this key to desired CIDR to configure cluster network

.. note::

   ``cluster_network`` accepts a comma-delimited list of CIDRs, matching
   Ceph's native ``ceph.conf`` syntax. The same applies to
   ``--public-network`` and ``--cluster-network`` at
   :command:`microceph cluster bootstrap` and
   :command:`microceph cluster adopt`. Each host in the cluster must
   have an interface address on at least one of the listed subnets.

   ``public_network`` is set at :command:`microceph cluster bootstrap` (or
   :command:`microceph cluster adopt`) time and is read-only afterwards: it
   cannot be changed with :command:`microceph cluster config set`.

   For example, to span two cluster networks:

   .. code-block:: none

      $ sudo microceph cluster config set cluster_network 10.5.0.0/16,10.6.0.0/16

1. Supported config keys can be configured using the 'set' command:

  .. code-block:: shell

    $ sudo microceph cluster config set cluster_network 10.5.0.0/16

2. Config value for a particular key could be queried using the 'get' command:

  .. code-block:: shell

    $ sudo microceph cluster config get cluster_network
    +---+-----------------+-------------+
    | # |       KEY       |     VALUE   |
    +---+-----------------+-------------+
    | 0 | cluster_network | 10.5.0.0/16 |
    +---+-----------------+-------------+

3. A list of all the configured keys can be fetched using the 'list' command:

  .. code-block:: shell

    $ sudo microceph cluster config list
    +---+-----------------+-------------+
    | # |       KEY       |     VALUE   |
    +---+-----------------+-------------+
    | 0 | cluster_network | 10.5.0.0/16 |
    +---+-----------------+-------------+

4. Resetting a config key (i.e. setting the key to its default value) can performed using the 'reset' command:

  .. code-block:: shell

   $ sudo microceph cluster config reset cluster_network
   $ sudo microceph cluster config list
   +---+-----+-------+
   | # | KEY | VALUE |
   +---+-----+-------+

For more explanations and implementation details refer to :ref:`explanation-cluster-configurations`.

