Update management
~~~~~~~~~~~~~~~~~

Updates in Microceph are managed by the usual snap mechanisms. However, because snaps automatically deploy updates, this can be problematic in Microceph clusters that need an ordered rollout of updates.
As such, we strongly advise that automatic updates be disabled for Microceph. Documentation about snaps updates can be consulted `here <https://snapcraft.io/docs/keeping-snaps-up-to-date>`_.

Disable automatic updates
~~~~~~~~~~~~~~~~~~~~~~~~~

On each node of the cluster where microceph has been installed, we must run the following:

.. code-block:: shell

    sudo snap refresh --hold microceph

This will prevent automatic updates from happening.

Updating microceph
~~~~~~~~~~~~~~~~~~

When we want to update microceph, we need to be sure that all nodes are reachable and the cluster is healthy.

In order to do so, we run the following command on one of the nodes:

.. code-block:: shell

    microceph.ceph status

The output should be something like the following:

.. code-block:: shell

    cluster:
        id:     q84fdf22-d12e-577n-9212-p10186effdzy
        health: HEALTH_OK
    
    services:
        mon: 3 daemons, quorum microceph-1,microceph-2,microceph-3 (age 25m)
        mgr: microceph-1(active, since 35m), standbys: microceph-2, microceph-3
        osd: 3 osds: 3 up (since 2m), 3 in (since 4m)
    
    data:
        pools:   1 pools, 1 pgs
        objects: 2 objects, 577 KiB
        usage:   65 MiB used, 45 GiB / 45 GiB avail
        pgs:     1 active+clean

With a healthy cluster, we need to run the following command on each node:

.. code-block:: shell

    sudo snap refresh microceph
    sudo snap refresh --hold microceph

The order on which we run the commands is important. It should be as follows:

1. Managers
2. Monitors
3. All other entities (i.e: OSDs)

The output of the 'microceph.ceph status' command should provide us with the hostnames of the mons and managers ('microceph-1' et al in this example).

At present time, managers and monitors reside on the same nodes, but that may not always be the case.

With that done, we should verify the cluster together to make it's settled and healthy once again.

Additionally, we may consult that the new version has been refreshed correctly by running:

.. code-block:: shell

    snap list microceph

And verifying that it matches our expectation.
