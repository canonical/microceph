.. meta::
   :description: Reference for the microceph disable command, which disables a feature or service on a MicroCeph node.

===========
``disable``
===========

Disables a feature or service on the cluster.

Usage:

.. code-block:: none

   microceph disable [flags]
   microceph disable [command]

Available Commands:

.. code-block:: none

   mds         Disable the MDS service on the --target server (default: this server)
   mgr         Disable the MGR service on the --target server (default: this server)
   mon         Disable the MON service on the --target server (default: this server)
   nfs         Disable the NFS Ganesha service on the --target server (default: this server)
   rbd-mirror  Disable the RBD Mirror service on the --target server (default: this server)
   rgw         Disable the RGW service on this node

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number


``mds``
-------

Disables the MDS service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph disable mds [--target <server>] [flags]


Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)


``mgr``
-------

Disables the MGR service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph disable mgr [--target <server>] [flags]


Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)


``mon``
-------

Disables the MON service on the --target server (default: this server).

MicroCeph refuses to remove a monitor when the monitors left in quorum would
not be enough for the resulting monitor map.

Usage:

.. code-block:: none

   microceph disable mon [--target <server>] [flags]


Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)


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


``rbd-mirror``
--------------

Disables the RBD Mirror service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph disable rbd-mirror [--target <server>] [flags]


Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)
