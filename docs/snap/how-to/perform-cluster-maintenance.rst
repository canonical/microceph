===========================
Perform cluster maintenance
===========================

Overview
--------

MicroCeph provides a simple and consistent workflow to support maintenance activity.

Before proceeding, please refer to the :doc:`Cluster maintenance</explanation/cluster-maintenance>`
to understand its functionality and impact.

Enabling Cluster Maintenance
----------------------------

To review the action plan for enabling maintenance mode, run

.. code:: text

   microceph cluster maintenance enter <node> --dry-run

If you only want to verify if the node is ready for maintenance operations, run

.. code:: text

   microceph cluster maintenance enter <node> --check-only

By default, noout is set when entering maintenance mode. To disable noout to enable data migration
during maintenance, run

.. code:: text

   microceph cluster maintenance enter <node> --set-noout=False

By default, OSDs on the node are not stopped during maintenance mode, To stop the OSD service on
the node during maintenance, run

.. code:: text

   microceph cluster maintenance enter <node> --stop-osds

You can also forcibly bring a node into maintenance mode or ignore the safety checks if you know
what you are doing, but it's generally not recommended as it's not guaranteed the node is ready for
maintenance operations.

.. code:: text

   # Forcibly enter maintenance mode
   microceph cluster maintenance enter <node> --force

   # Ignore safety checks when entering maintenance mode
   microceph cluster maintenance enter <node> --ignore-check


Disabling Cluster Maintenance
-----------------------------

To review the action plan for disabling maintenance mode, run

.. code:: text

   microceph cluster maintenance exit <node> --dry-run

To bring a node out of maintenance, run

.. code:: text

   microceph cluster maintenance exit <node>

