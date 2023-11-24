========
``disk``
========

Manages disks in MicroCeph.

Usage:

.. code-block:: none

   microceph disk [flags]
   microceph disk [command]

Available commands:

.. code-block:: none

   add         Add a Ceph disk (OSD)
   list        List servers in the cluster
   remove      Remove a Ceph disk (OSD)

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number


``add``
-------

Adds a new Ceph disk (OSD) to the cluster, alongside optional devices for write-ahead logging and database management.

The command takes a parameter SPEC which is either a path to a block device such as /dev/sdb or a specification for one or more loop files.

The specification for loop files is of the form "loop,<size>,<nr>"

- size is a number with M, G, or T suffixes for megabytes, gigabytes, or terabytes.
- nr is the number of file-backed loop OSDs to create.

For instance, a spec of `loop,8G,3` will create 3 file-backed loop OSDs of 8GB each.

While loop files provide a convenient way to quickly set up small test and development clusters, for
production deployments it is **strongly** recommended to use dedicated block devices for safety and
performance reasons.


Usage:

.. code-block:: none

   microceph disk add <SPEC> [flags]

Flags:

.. code-block:: none

   --db-device string    The device used for the DB
   --db-encrypt          Encrypt the DB device prior to use
   --db-wipe             Wipe the DB device prior to use
   --encrypt             Encrypt the disk prior to use (only block devices)
   --wal-device string   The device used for WAL
   --wal-encrypt         Encrypt the WAL device prior to use
   --wal-wipe            Wipe the WAL device prior to use
   --wipe                Wipe the disk prior to use


.. note::

   Only the data device is mandatory. The WAL and DB devices can improve
   performance by delegating the management of some subsystems to additional
   block devices. The WAL block device stores the internal journal whereas
   the DB one stores metadata. Using either of those should be advantageous
   as long as they are faster than the data device. WAL should take priority
   over DB if there isn't enough storage for both.

   WAL and DB devices can only be used with data devices that reside on a
   block device, not with loop files. Loop files do not support encryption.


``list``
--------

List servers in the cluster

Usage:

.. code-block:: none

   microceph disk list [flags]


``remove``
----------

Removes a single disk from the cluster.

Usage:

.. code-block:: none

   microceph disk remove <osd-id> [flags]

Flags:

.. code-block:: none

   --bypass-safety-checks               Bypass safety checks
   --confirm-failure-domain-downgrade   Confirm failure domain downgrade if required
   --timeout int                        Timeout to wait for safe removal (seconds) (default: 300)
