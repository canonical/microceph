==========
``enable``
==========

Enables a feature or service on the cluster.

Usage:

.. code-block:: none

   microceph enable [flags]
   microceph enable [command]

Available commands:

.. code-block:: none

   mds         Enable the MDS service on the --target server (default: this server)
   mgr         Enable the MGR service on the --target server (default: this server)
   mon         Enable the MON service on the --target server (default: this server)
   rgw         Enable the RGW service on the --target server (default: this server)

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``mds``
-------

Enables the MDS service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph enable mds [--target <server>] [--wait <bool>] [flags]
   

Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)
   --wait            Wait for mds service to be up. (default true)
   

``mgr``
-------

Enables the MGR service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph enable mgr [--target <server>] [--wait <bool>] [flags]
   

Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)
   --wait            Wait for mgr service to be up. (default true)
   

``mon``
-------

Enables the MON service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph enable mon [--target <server>] [--wait <bool>] [flags]
   

Flags:

.. code-block:: none

   --target string   Server hostname (default: this server)
   --wait            Wait for mon service to be up. (default true)
   

``rgw``
-------

Enables the RGW service on the --target server (default: this server).

Usage:

.. code-block:: none

   microceph enable rgw [--port <port>] [--ssl-port <port>] [--ssl-certificate <certificate material>] [--ssl-private-key <private key material>] [--target <server>] [--wait <bool>] [flags]
   

Flags:

.. code-block:: none

   --port int                Service non-SSL port (default: 80) (default 80)
   --ssl-port int            Service SSL port (default: 443) (default 443)
   --ssl-certificate string  base64 encoded SSL certificate
   --ssl-private-key string  base64 encoded SSL private key
   --target string           Server hostname (default: this server)
   --wait                    Wait for rgw service to be up. (default true)
