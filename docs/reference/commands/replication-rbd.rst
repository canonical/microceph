=============================
``replication``
=============================

Usage:

.. code-block:: none

   microceph replication [command]

Available commands:

.. code-block:: none

   configure   Configure replication parameters for RBD resource (Pool or Image)
   disable     Disable replication for RBD resource (Pool or Image)
   enable      Enable replication for RBD resource (Pool or Image)
   list        List all configured replications.
   status      Show RBD resource (Pool or Image) replication status

Global options:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``enable``
----------

Enable replication for RBD resource (Pool or Image)

Usage:

.. code-block:: none

   microceph replication enable rbd <resource> [flags]

Flags:

.. code-block:: none

   --remote string      remote MicroCeph cluster name
   --schedule string    snapshot schedule in days, hours, or minutes using d, h, m suffix respectively
   --skip-auto-enable   do not auto enable rbd mirroring for all images in the pool.
   --type string        'journal' or 'snapshot', defaults to journal (default "journal")

``status``
----------

Show RBD resource (Pool or Image) replication status

Usage:

.. code-block:: none

   microceph replication status rbd <resource> [flags]

Flags:

.. code-block:: none

   --json   output as json string

``list``
----------

List all configured remotes replication pairs.

Usage:

.. code-block:: none

   microceph replication list rbd [flags]

.. code-block:: none

   --json          output as json string
   --pool string   RBD pool name

``disable``
------------

Disable replication for RBD resource (Pool or Image)

Usage:

.. code-block:: none

   microceph replication disable rbd <resource> [flags]

.. code-block:: none

   --force   forcefully disable replication for rbd resource

``promote``
------------

Promote local cluster to primary

.. code-block:: none

   microceph replication promote [flags]

.. code-block:: none

   --remote         remote MicroCeph cluster name
   --force          forcefully promote site to primary

``demote``
------------

Demote local cluster to secondary

Usage:

.. code-block:: none

   microceph replication demote [flags]

.. code-block:: none

   --remote         remote MicroCeph cluster name

