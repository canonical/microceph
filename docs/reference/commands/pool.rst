========
``pool``
========

Manages pools in MicroCeph.

Usage:

.. code-block:: none

   microceph disk [command]

Available commands:

.. code-block:: none

   set-tf      Set the replication factor for pools

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
The command takes two arguments: The pool specification (a string) and the
replication factor (an integer).

The pool specification can take one of three forms: Either a list of pools,
separated by a space, in which case the replication factor is applied only to
those pools (provided they exist). It can also be an asterisk ('*') in which
case the process is applied to all existing pools; or an empty string (''),
which sets the default pool size, but doesn't change any existing pools.

Usage:

.. code-block:: none

   microceph pool set-rf <pool-spec> <replication-factor>
