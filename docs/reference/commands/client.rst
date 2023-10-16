===========
``client``
===========

Manages MicroCeph clients

Usage:

.. code-block:: none

   microceph client [flags]
   microceph client [command]

Available commands:

.. code-block:: none

   config      Manage Ceph Client configs

Global options:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``config``
----------

Manages Ceph Cluster configs.

Usage:

.. code-block:: none

   microceph cluster config [flags]
   microceph cluster config [command]

Available Commands:

.. code-block:: none

   get         Fetches specified Ceph Client config
   list        Lists all configured Ceph Client configs
   reset       Removes specified Ceph Client configs
   set         Sets specified Ceph Client config

``config set``
--------------

Sets specified Ceph Client config

Usage:

.. code-block:: none

   microceph client config set <Key> <Value> [flags]

Flags:

.. code-block:: none

   --target string   Specify a microceph node the provided config should be applied to. (default "*")
   --wait            Wait for configs to propagate across the cluster. (default true)

``config get``
--------------

Fetches specified Ceph Client config

Usage:

.. code-block:: none

   microceph client config get <key> [flags]

Flags:

.. code-block:: none

   --target string   Specify a microceph node the provided config should be applied to. (default "*")

``config list``
---------------

Lists all configured Ceph Client configs

Usage:

.. code-block:: none

   microceph client config list [flags]

Flags:

.. code-block:: none

   --target string   Specify a microceph node the provided config should be applied to. (default "*")

``config reset``
----------------

Removes specified Ceph Client configs

Usage:

.. code-block:: none

   microceph client config reset <key> [flags]

Flags:

.. code-block:: none

   --target string          Specify a microceph node the provided config should be applied to. (default "*")
   --wait                   Wait for required ceph services to restart post config reset. (default true)
   --yes-i-really-mean-it   Force microceph to reset all client config records for given key.

