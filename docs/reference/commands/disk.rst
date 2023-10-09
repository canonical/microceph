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

``add``
-------

Adds a disk to the cluster, alongside optional devices for
write-ahead logging and database management.

Syntax:

.. code-block:: none

   microceph disk add <data-device> [options]

Options:

.. code-block:: none

   --wipe          Wipe the data device.
   --encrypt       Encrypt the data device.
   --wal-device    Path to the block device used for write-ahead logging (WAL).
   --wal-wipe      Wipe the WAL block device.
   --wal-encrypt   Encrypt the WAL block device.
   --db-device     Path to the block device to be used for database management.
   --db-wipe       Wipe the DB block device.
   --db-encrypt    Encrypt the DB block device.

.. note::

   Only the data device is mandatory. THe WAL and DB devices can improve
   performance by delegating the management of some subsystems to additional
   block devices. The WAL block device stores the internal journal whereas
   the DB one stores metadata. Using either of those should be advantageous
   as long as they are faster than the data device. WAL should take priority
   over DB if there isn't enough storage for both.

``remove``
----------

Removes a single disk from the cluster.

.. note::

   The ``remove`` command is currently only supported in channel
   ``latest/edge`` of the microstack snap.

Syntax:

.. code-block:: none

   microceph disk remove <osd-id> [options]

Options:

.. code-block:: none

   --bypass-safety-checks               Bypass safety checks
   --confirm-failure-domain-downgrade   Confirm failure domain downgrade if required
   --timeout int                        Timeout to wait for safe removal (seconds) (default: 300)
