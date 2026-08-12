.. meta::
   :description: Reference information for the MicroCeph declarative placement API, including the policy schema, capability markers, and the supported ownership model.

.. _declarative-placement:

Declarative placement
=====================

Declarative placement lets an external orchestrator, such as the MicroCeph
charm, describe which cluster members should run which services. The
orchestrator submits a desired-state policy and MicroCeph reconciles the
cluster to match it, owning all destructive-operation safety itself.

Placement is an API-only surface. There is no ``microceph`` subcommand for it;
it is intended to be driven by an orchestrator rather than by hand.

Capability markers
------------------

Before using placement, a consumer should confirm the running snap revision
supports it by querying ``GET /1.0/cluster/capabilities``. The markers relevant
to placement are:

.. list-table::
   :header-rows: 1

   * - Marker
     - Meaning
   * - ``declarative-placement``
     - The ``/1.0/placement`` endpoints exist and control services (MON, MGR,
       MDS) are reconciled.
   * - ``placement-rgw``
     - The ``rgw`` field accepts an object carrying frontend configuration, and
       ``GET`` reports an observed ``rgw_frontend``.

A consumer that needs RGW placement must gate on ``placement-rgw``. A snap
revision without it rejects the object-shaped ``rgw`` field with HTTP 400.

Endpoints
---------

.. list-table::
   :header-rows: 1

   * - Method
     - Path
     - Effect
   * - ``PUT``
     - ``/1.0/placement``
     - Apply a policy, then store it as the declared intent.
   * - ``GET``
     - ``/1.0/placement``
     - Return the declared policy, the observed placement, and lifecycle state.
   * - ``DELETE``
     - ``/1.0/placement``
     - Clear the declared policy. Services are not added or removed.

Policy schema
-------------

The ``PUT`` body is a policy document. ``mode`` is required and must be
``reconcile``; an omitted or unknown mode is rejected, so a policy written for
a future mode fails loudly against an older snap rather than being silently
applied.

.. code-block:: json

   {
     "mode": "reconcile",
     "members": {
       "node-a": { "control": true },
       "node-b": { "control": false },
       "node-c": { "rgw": { "enabled": true, "port": 80 } }
     }
   }

Members absent from ``members`` are never touched. For members that are
present, an omitted field leaves that service untouched on that member, which
is distinct from an explicit ``false`` requesting removal.

.. list-table::
   :header-rows: 1

   * - Field
     - Type
     - Effect
   * - ``control``
     - boolean
     - ``true`` places MON, MGR and MDS on the member; ``false`` removes them,
       subject to the keep-one invariant below.
   * - ``rgw``
     - object
     - ``{"enabled": true}`` places RGW; ``{"enabled": false}`` removes it. See
       :ref:`declarative-placement-rgw` below.
   * - ``nfs``
     - array
     - Accepted and reported, but not yet reconciled by the placement engine.
   * - ``storage_eligible``
     - boolean
     - Accepted and reported, but not yet enforced by the disk APIs.

Safety rules
------------

The engine applies additions before removals, so a migration brings the
replacement up before tearing the previous instance down.

Control services are further protected by a **keep-one invariant**: the engine
refuses to remove the last viable MON, MGR or MDS. Viability is checked against
Ceph itself (MON quorum, MGR active or standby, MDS up) rather than against
database records, so a service that exists but is not healthy is still a
removal target while never counting as the last retainer.

RGW has no keep-one invariant. It is a stateless gateway that may be scaled to
zero, so a policy that disables RGW on every member is honoured.

If a removal is refused, the requested additions remain in effect, the policy
is still stored as the declared intent, and the reason is reported in the
``placement_refusal`` field of ``GET /1.0/placement``.

.. _declarative-placement-rgw:

RGW placement
-------------

The ``rgw`` field carries both placement intent and beast frontend
configuration, so that presence and frontend settings are applied together.

.. list-table::
   :header-rows: 1

   * - Field
     - Type
     - Notes
   * - ``enabled``
     - boolean
     - Whether the member should run RGW.
   * - ``port``
     - integer
     - Unencrypted listener port. Defaults to 80 when no TLS material is given.
   * - ``ssl_port``
     - integer
     - TLS listener port. Ignored unless both certificate and key are given.
   * - ``ssl_certificate``
     - string
     - base64-encoded PEM certificate.
   * - ``ssl_private_key``
     - string
     - base64-encoded PEM private key.

Re-applying the same configuration is a no-op: the member compares the desired
frontend against what is on disk and restarts RGW only when something actually
changed. Rotating a certificate is therefore an ordinary ``PUT`` with the new
material.

TLS key material is write-only. It travels over the authenticated API to the
member that needs it and is written to disk there, but it is stripped before
the policy is stored and is never returned by ``GET``. The observed
``rgw_frontend`` reports ports and a TLS on/off flag only. A consumer that
manages TLS must therefore hold its own copy of the material; it cannot read it
back from MicroCeph.

Reading placement state
-----------------------

``GET /1.0/placement`` returns the declared policy alongside an ``observed``
list describing what each member is actually running. Comparing the two is the
supported way to determine whether the cluster has converged.

Response codes
--------------

.. list-table::
   :header-rows: 1

   * - Code
     - Meaning
   * - 400
     - The request cannot be satisfied: Ceph is not bootstrapped, the policy
       names an unknown member, the RGW frontend configuration is malformed, or
       a removal was refused by the keep-one invariant.
   * - 409
     - Another placement apply, or a Ceph bootstrap, is in progress. The
       request can be retried.

.. _declarative-placement-ownership:

Ownership model
---------------

Placement applies are serialised cluster-wide, so concurrent policy
submissions cannot interleave. That serialisation covers the placement API
only.

.. important::

   Managing the same services through both the placement API and the
   ``microceph enable`` / ``microceph disable`` commands is **not a supported
   configuration**. Pick one owner for a given cluster.

The two mechanisms are independent write paths. The service commands do not
participate in the placement lock, do not consult the declared policy, and are
not subject to the keep-one invariant. Consequently, on a cluster driven by an
orchestrator:

- Enabling or disabling a service by hand puts the cluster out of step with the
  declared policy. Nothing corrects this until the orchestrator submits its
  next policy, at which point the manual change is reverted without warning.
- A service command issued while an apply is in flight can interleave with it.
  Observed state may briefly disagree with what is running until the next
  policy is applied.
- The keep-one invariant does not protect manual removals. ``microceph disable
  mon`` will remove a MON that the placement engine would have refused to
  remove.

Divergence is visible: ``GET /1.0/placement`` reports observed placement
alongside the declared policy, so the gap can be inspected at any time.

This restriction applies only to services that placement manages. On a cluster
that does not use placement, the service commands remain the normal way to
manage services and are fully supported.
