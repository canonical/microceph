Removing OSDs
=============


Overview
--------

The process of removing OSDs (Object Storage Daemons) from MicroCeph may become necessary in certain situations like when disks are broken, or when there's a need to scale down a Ceph cluster which might involve removing a complete cluster node or machine.

Invocation
----------

Invoking the removal of OSDs is executed with the :command:`disk remove` command. It takes one mandatory parameter, the OSD to remove, ``<osd-id>``

.. code-block:: none

    Usage:
      microceph disk remove <osd-id> [flags]

The :command:`disk remove` command takes the following options:

- ``--bypass-safety-checks``: bypass safety checks, see below for details
- ``--confirm-failure-domain-downgrade``: confirm downgrading automatic failure domains. See below for details.
- ``--timeout``: by default, MicroCeph will abort a removal operation after 5min. Pass in a timeout (in seconds) to control this duration



Examples
--------

The following invocation would remove the OSD ``osd.1``. If removing the OSD would need a failure domain downgrade, the operation would abort. All safety checks would be performed, and a timeout of 5min is in effect after which the operation would abort.

.. code-block:: none

    $ sudo microceph disk remove osd.1 
    Removing osd.1, timeout 300s


Cluster Health and Safety Checks
--------------------------------

By default, MicroCeph waits for data to be cleanly redistributed before evicting an OSD. There may be cases, such as when a cluster is not healthy to begin with, where redistribution of data is not feasible. In such situations, the operator can surpass these checks by passing in the ``--bypass-safety-checks`` flag.

**Warning: Possible Data Loss**

Using this flag is meant to be used as a last resort only. Be aware that removing an OSD with this flag might well result in data loss.


Automatic Failure Domain
------------------------

If removing an OSD would result in less than three OSDs on three different hosts, the automatic failure rules will have to be downgraded. MicroCeph prompts the user for confirmation before executing this downgrade. For more information about automatic failure domains, refer to :ref:`Scaling` for details.
