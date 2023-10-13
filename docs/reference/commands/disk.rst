========
``disk``
========

Manages disks in MicroCeph.

Usage:

.. code-block:: none

   microceph disk [options]
   microceph disk [command]

Available commands:

.. code-block:: none

   add         Add a Ceph disk (OSD)
   list        List servers in the cluster
   remove      Remove a Ceph disk (OSD)

Global options:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``remove``
----------

Removes a single disk from the cluster.

.. note::

   The ``remove`` command is currently only supported in channel
   ``latest/edge`` of the **microstack** snap.

Syntax:

.. code-block:: none

   microceph disk remove <osd-id> [options]

Options:

.. code-block:: none

   --bypass-safety-checks               Bypass safety checks
   --confirm-failure-domain-downgrade   Confirm failure domain downgrade if required
   --timeout int                        Timeout to wait for safe removal (seconds) (default: 300)
