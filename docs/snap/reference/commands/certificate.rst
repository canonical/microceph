===============
``certificate``
===============

Manage SSL certificates for MicroCeph services.

Usage:

.. code-block:: none

   microceph certificate [command]

Available commands:

.. code-block:: none

   set         Set SSL certificates for services

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``set``
-------

Set SSL certificates for services.

Usage:

.. code-block:: none

   microceph certificate set [command]

Available commands:

.. code-block:: none

   rgw         Set the SSL certificate for the RGW service

``set rgw``
-----------

Set or rotate SSL certificates for the RGW service. The new certificate and
key are written to disk. Use ``--restart`` to restart the RGW service and pick
up the new certificate immediately. Without ``--restart``, the certificate is
stored but the service must be restarted manually for the change to take effect.

Usage:

.. code-block:: none

   microceph certificate set rgw --ssl-certificate <base64> --ssl-private-key <base64> [--target <server>] [--restart] [flags]

Flags:

.. code-block:: none

   --ssl-certificate string   base64 encoded SSL certificate (required)
   --ssl-private-key string   base64 encoded SSL private key (required)
   --target string            Server hostname (default: this server)
   --restart                  Restart the RGW service for immediate certificate pickup
