=============
``waitready``
=============

Waits until the MicroCeph daemon is ready and Ceph is operational.

This command first waits for the MicroCeph daemon (microcluster) to become
available, then polls the Ceph monitor until ``ceph -s`` succeeds. It is
useful in scripting and CI environments where subsequent commands should not run
until the cluster is fully ready to accept operations.

Usage:

.. code-block:: none

   microceph waitready [flags]

Flags:

.. code-block:: none

       --timeout   Number of seconds to wait before giving up (0 = indefinitely)

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number
