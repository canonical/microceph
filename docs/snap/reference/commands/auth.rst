.. _auth-command:

=========
``auth``
=========

Manage MicroCeph CephX authentication keys

Usage:

.. code-block:: none

   microceph auth [flags]
   microceph auth [command]

Available commands:

.. code-block:: none

   rotate      Rotate CephX authentication keys to a selected key type
   status      Display CephX key rotation status and blockers

Global options:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number


``auth rotate``
---------------

The command rotates the keys of all entities the cluster manages (monitors,
managers, OSDs, metadata servers, gateways and managed client credentials) to
the target key type, which defaults to the cluster's
``auth_preferred_cipher`` setting. The rotation runs in the background on the
daemon and the command tracks its progress and reports the final outcome.

The rotation will be paused when a blocker is encountered, for example by
credentials unknown to the snap, incompatible client sessions or a pending
``client.admin`` rotation. Re-running the command with the same key type
resumes the paused rotation.

``client.admin`` is not rotated automatically. Its key is shared beyond this
cluster (it is distributed through the ceph-conf content interface, e.g. to
MicroCloud's LXD, and often copied to other nodes), and holders that already
loaded it into memory cannot re-authenticate once their ticket expires. This
key must be rotated explicitly with ``--client client.admin`` when the user
deems appropriate.

The ``--abort`` flag is used to clear an incomplete rotation (blocked or failed)
so new rotations are accepted again. A rotation that is currently running cannot
be aborted, and aborting does not undo keys that were already rotated.

Usage:

.. code-block:: none

   microceph auth rotate [--key-type TYPE] [--client NAME] [flags]

Flags:

.. code-block:: none

   --abort             Clear an incomplete rotation record so new rotations are accepted again (does not undo keys that were already rotated)
   --client string     Rotate and distribute only the specified client key (--client client.admin runs the protected admin rotation, which the full run does not do automatically)
   --key-type string   Key type/cipher to rotate keys to (defaults to auth_preferred_cipher)


``auth status``
---------------

Reports the state of the key rotation: ``idle`` (no rotation started or the
last one was aborted), ``in_progress``, ``blocked``, ``failed`` or
``completed``. The output also includes the state of a running rotation,
the target key type, details of the failure (if any) and the key types currently
in use by service and client keys, so a mixed cluster is easy to spot. The
``--json`` flag can be used for machine-readable output.

Usage:

.. code-block:: none

   microceph auth status [--json]

Flags:

.. code-block:: none

   --json   Output status as JSON
