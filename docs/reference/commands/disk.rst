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

Adds one or more new Ceph disks (OSDs) to the cluster, alongside optional
devices for write-ahead logging and database management.
The command takes arguments which is either one or more paths to block
devices such as /dev/sdb, or a specification for loop files.

For block devices, add a space separated list of absolute paths, e.g.
"/dev/sda /dev/sdb ...". You may also specify WAL and DB devices referred
to by absolute paths. However when specifying WAL and DB devices you
may only add a single OSD block device at a time.

The specification for loop files is of the form loop,<size>,<nr>

size is an integer with M, G, or T suffixes for megabytes, gigabytes,
or terabytes.
nr is the number of file-backed loop OSDs to create.
For instance, a spec of loop,8G,3 will create 3 file-backed OSDs, 8GB each.

Note that loop files can't be used with encryption nor WAL/DB devices.


Usage:

.. code-block:: none

   microceph disk add <spec> [flags]

Flags:

.. code-block:: none

   --all-available         add all available devices as OSDs
   --db-device string      The device used for the DB
   --db-encrypt            Encrypt the DB device prior to use
   --db-wipe               Wipe the DB device prior to use
   --dry-run               Show matched devices without adding them (requires --osd-match)
   --encrypt               Encrypt the disk prior to use (only block devices)
   --osd-match string      DSL expression to match devices for OSD creation
   --wal-device string     The device used for WAL
   --wal-encrypt           Encrypt the WAL device prior to use
   --wal-wipe              Wipe the WAL device prior to use
   --wipe                  Wipe the disk prior to use

.. note::

   Only the data device is mandatory. The WAL and DB devices can improve
   performance by delegating the management of some subsystems to additional
   block devices. The WAL block device stores the internal journal whereas
   the DB one stores metadata. Using either of those should be advantageous
   as long as they are faster than the data device. WAL should take priority
   over DB if there isn't enough storage for both.

   WAL and DB devices can only be used with data devices that reside on a
   block device, not with loop files. Loop files do not support encryption.


DSL-based device selection
~~~~~~~~~~~~~~~~~~~~~~~~~~

The ``--osd-match`` flag allows selecting devices using a DSL expression based
on device attributes. This is useful for automation scenarios where device
names may vary between nodes but functionally similar devices are present.

Example expressions:

.. code-block:: bash

   # Select all NVMe devices
   microceph disk add --osd-match "eq(@type, 'nvme')"

   # Select devices larger than 100GiB
   microceph disk add --osd-match "gt(@size, 100GiB)"

   # Complex selection: NVMe devices from Samsung
   microceph disk add --osd-match "and(eq(@type, 'nvme'), re('samsung', @vendor))"

   # Preview matches without adding
   microceph disk add --osd-match "eq(@type, 'sata')" --dry-run

Available predicates:

- ``and(a, b, ...)`` - Logical AND (variadic)
- ``or(a, b, ...)`` - Logical OR (variadic)
- ``not(a)`` - Logical NOT
- ``in(x, a, b, ...)`` - True if x equals any of the other arguments
- ``re(pattern, value)`` - Regex match (Go RE2 syntax, case-insensitive)
- ``eq(a, b)`` - Equality
- ``ne(a, b)`` - Not equal
- ``gt(a, b)``, ``ge(a, b)``, ``lt(a, b)``, ``le(a, b)`` - Comparisons

Available variables:

- ``@type`` - Device type (sata, nvme, virtio, etc.)
- ``@vendor`` - Vendor name extracted from model (lowercased)
- ``@model`` - Full model string (lowercased)
- ``@size`` - Device size in bytes (compare with units like 100GiB, 500MB)
- ``@devnode`` - Device path (e.g., /dev/disk/by-id/...)
- ``@host`` - Short hostname

Size units: B, KiB, MiB, GiB, TiB, PiB (1024-based) or KB, MB, GB, TB, PB (1000-based).
Numbers and units must be written without any space between them (e.g., ``100GiB``, not ``100 GiB``)

Limitations:

- ``--osd-match`` cannot be used together with ``--wal-device`` or ``--db-device``.


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
