===============
``disk remove``
===============

The :command:`disk remove` command removes a single disk from the cluster.

.. important::

   The ``disk remove`` command is currently only available on the
   ``latest/edge`` channel.

For important background information related to disk removal, see the
:doc:`../../explanation/scaling` page.

The command's syntax is:

.. code-block:: none

   microceph disk remove <osd-id> [options]

.. note::

   The OSD ID identifies the OSD associated with the disk. It can be determined
   with the (native Ceph) :command:`ceph osd tree` command.

The command's options are:

.. list-table::
   :header-rows: 1
   :widths: 25 20 8

   * - Option
     - Meaning
     - Default

   * - ``--bypass-safety-checks``
     - bypasses safety checks
     - ``false``

   * - ``--confirm-failure-domain-downgrade``
     - confirms automatic downgrading of failure domains
     - ``false``

   * - ``--timeout``
     - specifies a timeout (seconds) for the removal to succeed
     - ``300``

.. warning::

   The ``--bypass-safety-checks`` option is intended as a last resort measure
   only. Its usage may result in data loss.
