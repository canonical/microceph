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
   export      Generates cluster token for given Remote cluster
   join        Joins an existing cluster
   list        List servers in the cluster
   maintenance Enter or exit the maintenance mode.
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

   --microceph-ip    string Network address microceph daemon binds to.
   --mon-ip          string Public address for bootstrapping ceph mon service.
   --public-network  string Public network Ceph daemons bind to.
   --cluster-network string Cluster network Ceph daemons bind to.

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

   --wait           Wait for required ceph services to restart post config reset.
   --skip-restart   Don't perform the daemon restart for current config.


``config set``
--------------

Sets specified Ceph Cluster config.

Usage:

.. code-block:: none

   microceph cluster config set <Key> <Value> [flags]


Flags:

.. code-block:: none

   --wait           Wait for required ceph services to restart post config set.
   --skip-restart   Don't perform the daemon restart for current config.


``export``
----------

Generates cluster token for Remote cluster with given name.

Usage:

.. code-block:: none

   microceph cluster export <remote-name> [flags]

Flags:

.. code-block:: none

   --json   output as json string

``join``
--------

Joins an existing cluster.

Usage:

.. code-block:: none

   microceph cluster join <TOKEN> [flags]

Flags:

.. code-block:: none

   --microceph-ip    string Network address microceph daemon binds to.


``list``
--------

Lists servers in the cluster.

Usage:

.. code-block:: none

   microceph cluster list [flags]


``maintenance``
---------------

Enter or exit the maintenance mode.

Usage:

.. code-block:: none

   microceph cluster maintenance [flags]
   microceph cluster maintenance [command]

Available Commands:

.. code-block:: none

   enter       Enter maintenance mode.
   exit        Exit maintenance mode.


``maintenance enter``
---------------------

Enter maintenance mode.

Usage:

.. code-block:: none

   microceph cluster maintenance enter <NODE_NAME> [flags]

Flags:

.. code-block:: none

   --check-only     Only run the preflight checks (mutually exclusive with --ignore-check).
   --dry-run        Dry run the command.
   --force          Force to enter maintenance mode.
   --ignore-check   Ignore the the preflight checks (mutually exclusive with --check-only).
   --set-noout      Stop CRUSH from rebalancing the cluster. (default true)
   --stop-osds      Stop the OSDS when entering maintenance mode.


``maintenance exit``
--------------------

Exit maintenance mode.

Usage:

.. code-block:: none

   microceph cluster maintenance exit <NODE_NAME> [flags]

Flags:

.. code-block:: none

   --check-only     Only run the preflight checks (mutually exclusive with --ignore-check).
   --dry-run        Dry run the command.
   --ignore-check   Ignore the the preflight checks (mutually exclusive with --check-only).


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
