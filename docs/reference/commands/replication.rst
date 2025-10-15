=============================
``replication``
=============================

Manage replication to remote clusters

Usage:

.. code-block:: none

   microceph replication [command]

Available commands:

.. code-block:: none

  configure   configure replication parameters
  demote      Demote a primary cluster to non-primary status
  disable     Disable replication
  enable      Enable replication
  list        List all resources configured for replication.
  promote     Promote a non-primary cluster to primary status
  status      Show resource replication status

``replication enable``
-----------------------

Enable replication for a workload

Usage:

.. code-block:: none

  microceph replication enable rbd <resource> [flags]

Available commands:

.. code-block:: none

  cephfs      Enable replication for CephFS resource (Directory or Subvolume)
  rbd         Enable replication for RBD resource (Pool or Image)

``replication enable rbd``
------------------------------

Enable replication for RBD resource (Pool or Image)

Usage:

.. code-block:: none

  microceph replication enable rbd <resource> [flags]

Flags:

.. code-block:: none

   --remote string      remote MicroCeph cluster name
   --schedule string    snapshot schedule in days, hours, or minutes using d, h, m suffix respectively
   --skip-auto-enable   do not auto enable rbd mirroring for all images in the pool.
   --type string        'journal' or 'snapshot', defaults to journal (default "journal")

``replication enable cephfs``
------------------------------

Enable replication for CephFS resource (Directory or Subvolume)

Usage:

.. code-block:: none

  microceph replication enable cephfs <resource> [flags]

Flags:

.. code-block:: none

  --dir-path string         Directory path relative to file system
  --remote string           remote MicroCeph cluster name
  --subvolume string        CephFS Subvolume
  --subvolumegroup string   CephFS Subvolume Group
  --volume string           CephFS volume (aka file-system)

``replication status``
------------------------

Show resource replication status

Usage:

.. code-block:: none

   microceph replication status [command]

Available Commands:

.. code-block:: none

   cephfs  Show CephFS resource replication status
   rbd     Show RBD resource replication status

``replication status rbd``
---------------------------

Show RBD resource replication status

Usage:

.. code-block:: none

   microceph replication status rbd <resource> [flags]

Flags:

.. code-block:: none

   --json   output as json string

``replication status cephfs``
----------------------------------

Show CephFS resource replication status

Usage:

.. code-block:: none

  microceph replication status cephfs <resource> [flags]

Flags:

.. code-block:: none

  --json   output as json string

``replication list``
----------------------

List all configured remotes replication pairs.

Usage:

.. code-block:: none

   microceph replication list rbd [flags]

Available Commands:

.. code-block:: none

   cephfs  List all CephFS resources configured for replication
   rbd     List all RBD resources configured for replication

``replication list rbd``
---------------------------

List all RBD resources configured for replication

Usage:

.. code-block:: none

   microceph replication list rbd [flags]

.. code-block:: none

   --json          output as json string
   --pool string   RBD pool name

``replication list cephfs``
---------------------------

List all CephFS resources configured for replication

Usage:

.. code-block:: none

   microceph replication list rbd [flags]

.. code-block:: none

   --json          output as json string

``replication disable``
-----------------------------

Disable replication for a workload

Usage:

.. code-block:: none

  microceph replication disable [command]

Available Commands:

.. code-block:: none

  cephfs      Disable replication for CephFS resource (Directory or Subvolume)
  rbd         Disable replication for RBD resource (Pool or Image)

``replication disable rbd``
-----------------------------

Disable replication for RBD resource

Usage:

.. code-block:: none

  microceph replication disable rbd <resource> [flags]

Flags:

.. code-block:: none

   --force   forcefully disable replication for rbd resource

``replication disable cephfs``
------------------------------

Disable replication for CephFS resource

Usage:

.. code-block:: none

  microceph replication disable cephfs <resource> [flags]

.. code-block:: none

  --dir-path string         Directory path relative to file system
  --force                   forcefully disable replication for resource
  --subvolume string        CephFS Subvolume
  --subvolumegroup string   CephFS Subvolume Group
  --volume string           CephFS volume (aka file-system)

``replication configure``
-------------------------

Configure replication parameters

Usage:

.. code-block:: none

   microceph replication configure [command]

Available Commands:

... code-block:: none

   rbd     Configure RBD replication parameters

``replication configure rbd``
------------------------------

Configure replication parameters for RBD resource

Usage:

.. code-block:: none

   microceph replication configure rbd <resource> [flags]

Flags:

.. code-block:: none

  --schedule string   snapshot schedule in days, hours, or minutes using d, h, m suffix respectively

``replication promote``
------------------------

Promote a non-primary cluster to primary status

.. code-block:: none

   microceph replication promote [flags]

.. code-block:: none

   --remote         remote MicroCeph cluster name
   --force          forcefully promote site to primary

``replication demote``
------------------------

Demote a primary cluster to non-primary status

Usage:

.. code-block:: none

   microceph replication demote [flags]

.. code-block:: none

   --remote         remote MicroCeph cluster name

