.. meta::
   :description: MicroCeph pool commands, including replication factor settings and how to create erasure-coded pools with Ceph.

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
This command changes existing replicated pools or the default for future
replicated pools. It does not create pools or configure erasure coding.

The pool specification can take one of three forms: Either a list of pools,
separated by a space, in which case the replication factor is applied only to
those pools (provided they exist). It can also be an asterisk ('*') in which
case the process is applied to all existing pools; or an empty string (''),
which sets the default pool size, but doesn't change any existing pools.

Usage:

.. code-block:: none

   microceph pool set-rf --size <replication-factor> <pool-spec>... [flags]

Flags:

.. code-block:: none

   --size int   Pool size (required)

Erasure-coded pools
-------------------

MicroCeph supports erasure-coded pools through the standard Ceph administration
commands. Check the default erasure-code profile, then create a pool with it:

.. code-block:: none

   sudo ceph osd erasure-code-profile get default
   sudo ceph osd pool create my_ec_pool erasure

If you need a different profile, create it with
``ceph osd erasure-code-profile set`` and pass its name to
``ceph osd pool create``. See the `Ceph erasure-code documentation
<https://docs.ceph.com/en/latest/rados/operations/erasure-code/>`_ for profile
options and pool requirements. ``microceph pool set-rf`` does not apply to
erasure-coded pools.
