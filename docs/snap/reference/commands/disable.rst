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
   smb         Remove a managed SMB member, deployment, or logical cluster
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


``smb``
-------

Removes a managed SMB member, deployment, or logical cluster.

Usage:

.. code-block:: none

   microceph disable smb --cluster-id <cluster-id> [--target <server>] [--force]

Flags:

.. code-block:: none

   --cluster-id string   SMB Cluster ID (must match regex: '^[A-Za-z0-9]([A-Za-z0-9-]{0,16}[A-Za-z0-9])?$')
   --target string       Permanently remove this server from the SMB deployment
   --force               Delete the logical SMB cluster after upstream share validation

Without ``--target`` or ``--force``, the command removes the whole gateway
deployment but retains the logical SMB cluster and its assets. ``--force``
without ``--target`` requests logical-cluster deletion before cleanup; it does
not delete shares, the shared ``.smb`` pool, or CephFS data. See
:ref:`smb-disable-scopes` for scope details.
