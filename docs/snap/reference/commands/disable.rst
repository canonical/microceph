===========
``disable``
===========

Disables a feature on the cluster

Usage:

.. code-block:: none

   microceph disable [flags]
   microceph disable [command]

Available Commands:

.. code-block:: none

   nfs         Disable the NFS Ganesha service on the --target server (default: this server)
   rgw         Disable the RGW service on this node

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number


``nfs``
-------

Disables the NFS Ganesha service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph disable nfs --cluster-id <cluster-id> [--target <server>] [flags]


Flags:

.. code-block:: none

   --cluster-id string   NFS Cluster ID (must match regex: '^[\w][\w.-]{1,61}[\w]$')
   --target string       Server hostname (default: this server)
