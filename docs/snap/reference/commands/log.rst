.. meta::
   :description: Reference for the microceph log command, which gets and sets the MicroCeph daemon log level.

=======
``log``
=======

Manages the log level of the MicroCeph daemon.

See :ref:`change-log-level` for the available log levels and how to use them.

Usage:

.. code-block:: none

   microceph log [flags]
   microceph log [command]

Available commands:

.. code-block:: none

   get-level   Get the current log level, as an integer
   set-level   Set the log level for the microceph daemon

Global flags:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``get-level``
-------------

Gets the current log level of the MicroCeph daemon on the local node, as an integer. A node whose
log level has never been changed returns ``4`` (INFO).

Usage:

.. code-block:: none

   microceph log get-level [flags]

``set-level``
-------------

Sets the log level of the MicroCeph daemon on the local node. ``LEVEL`` is either the name or the
integer value of a log level. The change takes effect immediately and persists across restarts.

The mapping is as follows:

.. code-block:: none

   0 - PANIC
   1 - FATAL
   2 - ERROR
   3 - WARNING
   4 - INFO
   5 - DEBUG
   6 - TRACE

Usage:

.. code-block:: none

   microceph log set-level <LEVEL> [flags]

Example
-------

Lower the log level to WARNING, then check it:

.. code-block:: none

   sudo microceph log set-level warning
   sudo microceph log get-level
   3
