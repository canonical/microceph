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

Set or rotate SSL certificates for the RGW service. Provide a base64-encoded PEM
certificate and private key. If the certificate includes a chain, place the
server's leaf certificate first.

Use ``--restart`` to activate the certificate immediately. Without it, a new
certificate is stored for later activation. An earlier pending update may still
cause RGW to restart, even after a manual restart, interrupting client connections.

See :ref:`rotate-rgw-tls-certificates` for examples and activation instructions.

Usage:

.. code-block:: none

   microceph certificate set rgw --ssl-certificate <base64> --ssl-private-key <base64> [--target <server>] [--restart] [flags]

Flags:

.. code-block:: none

   --ssl-certificate string   base64 encoded SSL certificate (required)
   --ssl-private-key string   base64 encoded SSL private key (required)
   --target string            Server hostname (default: this server)
   --restart                  Restart the RGW service for immediate certificate pickup
