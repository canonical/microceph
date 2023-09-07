=============
Remove an OSD
=============

Overview
--------

There are valid reasons for wanting to remove an OSD disk from a Ceph cluster.
Examples include the need to prevent imminent disk failure or the desire to
scale down the cluster through node removal.

The `disk remove` command
-------------------------

Disk removals are managed with the :command:`disk remove` command. Its syntax
is:

.. code-block:: none

   microceph disk remove <osd-id> [options]

The disk is specified via its OSD id, which can be found with the
:command:`blah` command.

The command's options are:

* ``--bypass-safety-checks``: bypass safety checks
* ``--confirm-failure-domain-downgrade``: confirm automatic downgrading of
  failure domains
* ``--timeout``: specify a timeout (seconds) for the removal to succeed
  (default: 300)

Notes
~~~~~

Safety checks consist of ????????????? and they are all performed by default.
The ``--bypass-safety-checks`` option disables these checks.

The operation will abort if the removal would trigger a downgrade in failure
domain (see upstream documentation on `CRUSH map`_). The
``--confirm-failure-domain-downgrade`` option overrides this behaviour and
allows the downgrade to proceed.

Example
-------

Here, we will remove OSD with an id of ``osd.1``:

, and a timeout of
5min is in effect after which the operation would abort.

.. code-block:: none

   sudo microceph disk remove osd.1
    Removing osd.1, timeout 300s

Cluster health and safety checks
--------------------------------

By default, MicroCeph waits for data to be cleanly redistributed before
evicting an OSD. There may be cases, such as when a cluster is not healthy to
begin with, where redistribution of data is not feasible. In such situations,
the operator can surpass these checks by passing in the
``--bypass-safety-checks`` flag.

**Warning: Possible Data Loss**

Using this flag is meant to be used as a last resort only. Be aware that
removing an OSD with this flag might well result in data loss.

Automatic failure domain
------------------------

If removing an OSD would result in less than three OSDs on three different
hosts, the automatic failure rules will have to be downgraded. MicroCeph
prompts the user for confirmation before executing this downgrade. For more
information about automatic failure domains, refer to :ref:`Scaling` for
details.

.. LInks
.. _CRUSH map: https://docs.ceph.com/en/latest/rados/operations/crush-map/
