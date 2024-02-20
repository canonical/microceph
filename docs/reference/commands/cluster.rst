===========
``cluster``
===========

Manages the MicroCeph cluster.

Usage:

.. code-block:: none

   microceph cluster [flags]
   microceph cluster [command]

Available commands:

.. code-block:: none

   add         Generates a token for a new server
   bootstrap   Sets up a new cluster
   config      Manage Ceph Cluster configs
   join        Joins an existing cluster
   list        List servers in the cluster
   migrate     Migrate automatic services from one node to another
   remove      Removes a server from the cluster
   sql         Runs a SQL query against the cluster database


Global options:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``add``
-------

Generates a token for a new server

Usage:

.. code-block:: none

   microceph cluster add <NAME> [flags]


``bootstrap``
-------------

Sets up a new cluster

Usage:

.. code-block:: none

   microceph cluster bootstrap [flags]

Flags:

.. code-block:: none

   --microceph-ip    string Public address for microcephd daemon.
   --mon-ip          string Public address for bootstrapping ceph mon service.
   --public-network  string Public Network for Ceph daemons to bind to.
   --cluster-network string Cluster Network for Ceph daemons to bind to.

``config``
----------

Manages Ceph Cluster configs.

Usage:

.. code-block:: none

   microceph cluster config [flags]
   microceph cluster config [command]

Available Commands:

.. code-block:: none

   get         Get specified Ceph Cluster config
   list        List all set Ceph level configs
   reset       Clear specified Ceph Cluster config
   set         Set specified Ceph Cluster config


``config get``
--------------

Gets specified Ceph Cluster config.

Usage:

.. code-block:: none

   microceph cluster config get <key> [flags]


``config list``
---------------

Lists all set Ceph level configs.

Usage:

.. code-block:: none

   microceph cluster config list [flags]


``config reset``
----------------

Clears specified Ceph Cluster config.

Usage:

.. code-block:: none

   microceph cluster config reset <key> [flags]

Flags:

.. code-block:: none

   --wait   Wait for required ceph services to restart post config reset.


``config set``
--------------

Sets specified Ceph Cluster config.

Usage:

.. code-block:: none

   microceph cluster config set <Key> <Value> [flags]


Flags:

.. code-block:: none

   --wait   Wait for required ceph services to restart post config set.


``join``
--------

Joins an existing cluster.

Usage:

.. code-block:: none

   microceph cluster join <TOKEN> [flags]


``list``
--------

Lists servers in the cluster.

Usage:

.. code-block:: none

   microceph cluster list [flags]


``migrate``
-----------

Migrates automatic services from one node to another.

Usage:

.. code-block:: none

   microceph cluster migrate <SRC> <DST [flags]


``remove``
----------

Removes a server from the cluster.

Syntax:

.. code-block:: none

   microceph cluster remove <NAME> [flags]


Flags:

.. code-block:: none

   -f, --force   Forcibly remove the cluster member


``sql``
-------

Runs a SQL query against the cluster database.

Usage:

.. code-block:: none

   microceph cluster sql <query> [flags]
