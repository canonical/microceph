.. meta::
   :description: How to get and set the MicroCeph daemon log level, and how the integer levels map to level names.

.. _change-log-level:

Changing the log level
======================

By default, the MicroCeph daemon runs with the log level set to INFO. When troubleshooting, a
more verbose level such as DEBUG can help; on the other hand, a less verbose level such as WARNING
keeps the logs small, for example on embedded devices where storage is much more limited. For these
reasons, the MicroCeph daemon exposes a way to both get and set the log level.

The log level only applies to the MicroCeph daemon (``microcephd``). It does not change the log
levels of the Ceph daemons (MON, MGR, OSD, MDS and RGW), which are set with Ceph's own ``debug_*``
configuration options.

Log levels
----------

Each log level has both a name and an integer value. Lower values log fewer messages, and each
level also logs the messages of all lower levels:

.. list-table:: MicroCeph daemon log levels
   :widths: 30 70
   :header-rows: 1

   * - Integer
     - Name
   * - 0
     - ``panic``
   * - 1
     - ``fatal``
   * - 2
     - ``error``
   * - 3
     - ``warning`` (or ``warn``)
   * - 4
     - ``info`` (default)
   * - 5
     - ``debug``
   * - 6
     - ``trace``

Configuring the log level
-------------------------

MicroCeph includes the command ``log``, with the sub-commands ``set-level`` and ``get-level``. When
setting, we support both the name and the integer value of a log level from the table above. For
example:

.. code-block:: none

   sudo microceph log set-level warning
   sudo microceph log set-level 3

Both commands are equivalent. Any change made to the log level takes effect immediately and needs
no restart. The new level is also saved, so it persists across restarts of the MicroCeph daemon and
reboots of the host.

The log level is set on the MicroCeph daemon of the node where the command runs. In a multi-node
cluster, run the command on each node whose log level you want to change.

On the other hand, the ``get-level`` sub-command takes no arguments and returns the integer value
of the current log level only. Any value returned by ``get-level`` can be used for ``set-level``.

For example, after setting the level as shown in the example, we can verify in this way:

.. code-block:: none

   sudo microceph log get-level
   3

On a node where the log level has never been changed, ``get-level`` returns ``4`` (INFO).
