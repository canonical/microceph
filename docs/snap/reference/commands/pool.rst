.. meta::
   :description: Reference for the microceph pool command, which sets the replication factor of existing pools and the default size of pools created later.

========
``pool``
========

Manages pools in MicroCeph.

Usage:

.. code-block:: none

   microceph pool [command]

Available commands:

.. code-block:: none

   set-rf      Set the replication factor for pools

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number


``set-rf``
----------

Sets the replication factor for one or more pools in the cluster.
The replication factor (an integer) is set with the required ``--size`` flag.
The command takes the pool specification (a string) as one or more arguments.

The pool specification can take one of three forms: Either a list of pools,
separated by a space, in which case the replication factor is applied only to
those pools (provided they exist). It can also be an asterisk ('*') in which
case the process is applied to all existing pools; or an empty string (''),
which sets the default pool size, but doesn't change any existing pools.

Changing the replication factor of existing pools leaves the default pool size,
used for pools created later, unchanged. To set the default pool size as well,
add the ``--set-default`` flag. With this flag, the pool specification can be
omitted to set only the default pool size.

Usage:

.. code-block:: none

   microceph pool set-rf --size <replication-factor> [--set-default] [<pool-spec>...] [flags]

Flags:

.. code-block:: none

   --set-default   Also set the default size of pools created later
   --size int      Pool size (required)
