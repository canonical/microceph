=============
``waitready``
=============

Waits until the MicroCeph daemon is ready and Ceph is operational.

This command first waits for the MicroCeph daemon (microcluster) to become
available, then polls the Ceph monitor until ``ceph -s`` succeeds. It is
useful in scripting and CI environments where subsequent commands should not run
until the cluster is fully ready to accept operations.

With the ``--storage`` flag, it additionally waits until enough OSDs are up to
satisfy pool replication requirements. The required number of OSDs is determined
by the maximum ``size`` across all existing pools. If no pools exist yet, the
``osd_pool_default_size`` configuration value is used as a fallback.

Usage:

.. code-block:: none

   microceph waitready [flags]

Flags:

.. code-block:: none

       --storage   Wait until enough OSDs are up to satisfy pool replication requirements
       --timeout   Number of seconds to wait before giving up (0 = indefinitely)

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

Example
-------

Wait for daemon, Ceph, and storage readiness with a 60s timeout:

.. code-block:: bash

   sudo microceph waitready --storage --timeout 60
